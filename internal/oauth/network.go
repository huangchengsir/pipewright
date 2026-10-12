package oauth

import (
	"context"
	"crypto/tls"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"golang.org/x/net/idna"
)

type destination struct {
	host, port string
	ips        []net.IPAddr
}

func blockedIP(ip net.IP) bool {
	// Private and loopback Git hosts remain supported for administrator-configured
	// self-hosted instances; metadata, unspecified and multicast addresses do not.
	return ip == nil || ip.IsUnspecified() || ip.IsLinkLocalUnicast() || ip.IsMulticast()
}

func canonicalHost(host string) (string, error) {
	if net.ParseIP(host) != nil {
		return host, nil
	}
	ascii, err := idna.Lookup.ToASCII(host)
	if err != nil || ascii == "" {
		return "", ErrExchangeFailed
	}
	return ascii, nil
}

func resolveDestination(ctx context.Context, raw string, lookup func(context.Context, string) ([]net.IPAddr, error)) (destination, error) {
	var d destination
	if _, err := providerBase(raw); err != nil {
		return d, ErrExchangeFailed
	}
	u, _ := url.Parse(raw)
	var err error
	d.host, err = canonicalHost(u.Hostname())
	if err != nil {
		return destination{}, ErrExchangeFailed
	}
	d.port = u.Port()
	if d.port == "" {
		d.port = "80"
		if u.Scheme == "https" {
			d.port = "443"
		}
	}
	if ip := net.ParseIP(d.host); ip != nil {
		d.ips = []net.IPAddr{{IP: ip}}
	} else {
		ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
		defer cancel()
		var err error
		d.ips, err = lookup(ctx, d.host)
		if err != nil {
			return destination{}, ErrExchangeFailed
		}
	}
	if len(d.ips) == 0 {
		return destination{}, ErrExchangeFailed
	}
	validated := make([]net.IPAddr, len(d.ips))
	for i, address := range d.ips {
		if blockedIP(address.IP) || address.Zone != "" {
			return destination{}, ErrExchangeFailed
		}
		validated[i].IP = append(net.IP(nil), address.IP...)
	}
	d.ips = validated
	return d, nil
}

func (d destination) dial(dial func(context.Context, string, string) (net.Conn, error)) func(context.Context, string, string) (net.Conn, error) {
	return func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil {
			return nil, ErrExchangeFailed
		}
		host, err = canonicalHost(host)
		if err != nil || !strings.EqualFold(host, d.host) || port != d.port {
			return nil, ErrExchangeFailed
		}
		for _, address := range d.ips {
			conn, err := dial(ctx, network, net.JoinHostPort(address.IP.String(), port))
			if err == nil {
				return conn, nil
			}
			if ctx.Err() != nil {
				break
			}
		}
		return nil, ErrExchangeFailed
	}
}

func (s *service) request(req *http.Request) (*http.Response, error) {
	lookup := s.lookupIP
	if lookup == nil {
		lookup = net.DefaultResolver.LookupIPAddr
	}
	d, err := resolveDestination(req.Context(), req.URL.String(), lookup)
	if err != nil {
		return nil, ErrExchangeFailed
	}
	client := *s.client
	transport := client.Transport
	if transport == nil {
		transport = http.DefaultTransport
	}
	// Custom transports are trusted in-process adapters. Network transports are
	// cloned and pinned so redirects and proxy-side DNS cannot reroute secrets.
	if original, ok := transport.(*http.Transport); ok {
		pinned := original.Clone()
		pinned.Proxy = nil
		pinned.DialTLS, pinned.DialTLSContext = nil, nil
		pinned.DisableKeepAlives = true
		if pinned.TLSClientConfig == nil {
			pinned.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12}
		}
		pinned.TLSClientConfig.ServerName = d.host
		dial := s.dialContext
		if dial == nil {
			dial = (&net.Dialer{Timeout: 10 * time.Second}).DialContext
		}
		proxy, err := selectedProxy(original, req)
		if err != nil {
			return nil, ErrExchangeFailed
		}
		httpProxy := proxy != nil && (proxy.Scheme == "http" || proxy.Scheme == "https")
		if httpProxy && req.URL.Scheme == "http" && net.ParseIP(d.host) != nil {
			host, address, err := proxyEndpoint(proxy)
			if err != nil {
				return nil, ErrExchangeFailed
			}
			copy := *proxy
			copy.Host = address
			pinned.Proxy = http.ProxyURL(&copy)
			pinned.DialContext = dial
			if proxy.Scheme == "https" {
				pinned.TLSClientConfig.ServerName = host
			}
			client.Transport = &forwardProxy{transport: pinned, destination: d}
		} else {
			if proxy != nil {
				direct := dial
				dial = func(ctx context.Context, network, address string) (net.Conn, error) {
					if !httpProxy {
						return connectSOCKS(ctx, proxy, address, direct)
					}
					headers, err := proxyConnectHeaders(ctx, original, proxy, address)
					if err != nil {
						return nil, ErrExchangeFailed
					}
					return connectProxy(ctx, proxy, address, headers, original.TLSClientConfig, direct)
				}
			}
			pinned.DialContext = d.dial(dial)
			client.Transport = &originTransport{transport: pinned}
		}
		defer pinned.CloseIdleConnections()
	}
	return client.Do(req)
}
