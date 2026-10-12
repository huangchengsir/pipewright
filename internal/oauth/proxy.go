package oauth

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/base64"
	"io"
	"math"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"golang.org/x/net/http/httpguts"
	xproxy "golang.org/x/net/proxy"
)

// Selection uses the original request, not its resolved IP, so NO_PROXY and
// explicit proxy selectors keep their hostname semantics.
func selectedProxy(transport *http.Transport, req *http.Request) (*url.URL, error) {
	if transport.Proxy == nil {
		return nil, nil
	}
	proxy, err := transport.Proxy(req)
	if err != nil {
		return nil, ErrExchangeFailed
	}
	if proxy == nil {
		return nil, nil
	}
	if (proxy.Scheme != "http" && proxy.Scheme != "https" && proxy.Scheme != "socks5" && proxy.Scheme != "socks5h") || proxy.Hostname() == "" || proxy.RawQuery != "" || proxy.Fragment != "" {
		return nil, ErrExchangeFailed
	}
	if _, _, err := proxyEndpoint(proxy); err != nil {
		return nil, ErrExchangeFailed
	}
	copy := *proxy
	return &copy, nil
}

func proxyEndpoint(proxy *url.URL) (string, string, error) {
	host, err := canonicalHost(proxy.Hostname())
	if err != nil {
		return "", "", ErrExchangeFailed
	}
	port := proxy.Port()
	if port == "" {
		port = "80"
		switch proxy.Scheme {
		case "https":
			port = "443"
		case "socks5", "socks5h":
			port = "1080"
		}
	}
	return host, net.JoinHostPort(host, port), nil
}

func proxyConnectHeaders(ctx context.Context, transport *http.Transport, proxy *url.URL, target string) (http.Header, error) {
	headers := transport.ProxyConnectHeader
	if transport.GetProxyConnectHeader != nil {
		copy := *proxy
		var err error
		headers, err = transport.GetProxyConnectHeader(ctx, &copy, target)
		if err != nil {
			return nil, ErrExchangeFailed
		}
	}
	cloned := make(http.Header, len(headers))
	size := 0
	for key, values := range headers {
		if !httpguts.ValidHeaderFieldName(key) {
			return nil, ErrExchangeFailed
		}
		key = http.CanonicalHeaderKey(key)
		switch key {
		case "Host", "Content-Length", "Transfer-Encoding", "Trailer":
			return nil, ErrExchangeFailed
		}
		for _, value := range values {
			size += len(key) + len(value) + 4
			if !httpguts.ValidHeaderFieldValue(value) || size > 64<<10 {
				return nil, ErrExchangeFailed
			}
			cloned[key] = append(cloned[key], value)
		}
	}
	return cloned, nil
}

type contextProxyDialer func(context.Context, string, string) (net.Conn, error)

func (d contextProxyDialer) Dial(network, address string) (net.Conn, error) {
	return d(context.Background(), network, address)
}

func (d contextProxyDialer) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	return d(ctx, network, address)
}

func connectSOCKS(ctx context.Context, proxy *url.URL, target string, dial contextProxyDialer) (net.Conn, error) {
	host, _, err := net.SplitHostPort(target)
	if err != nil || blockedIP(net.ParseIP(host)) {
		return nil, ErrExchangeFailed
	}
	_, address, err := proxyEndpoint(proxy)
	if err != nil {
		return nil, ErrExchangeFailed
	}
	var auth *xproxy.Auth
	if proxy.User != nil {
		password, _ := proxy.User.Password()
		auth = &xproxy.Auth{User: proxy.User.Username(), Password: password}
	}
	socks, err := xproxy.SOCKS5("tcp", address, auth, dial)
	if err != nil {
		return nil, ErrExchangeFailed
	}
	contextDialer, ok := socks.(xproxy.ContextDialer)
	if !ok {
		return nil, ErrExchangeFailed
	}
	conn, err := contextDialer.DialContext(ctx, "tcp", target)
	if err != nil {
		return nil, ErrExchangeFailed
	}
	if ctx.Err() != nil {
		conn.Close()
		return nil, ErrExchangeFailed
	}
	return conn, nil
}

type originTransport struct{ transport *http.Transport }

func (p *originTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	r := req.Clone(req.Context())
	r.Header.Del("Proxy-Authorization")
	return p.transport.RoundTrip(r)
}

// Numeric HTTP URLs are already pinned; ordinary forwarding remains compatible
// with proxies that do not permit CONNECT to port 80.
type forwardProxy struct {
	transport   *http.Transport
	destination destination
}

func (p *forwardProxy) RoundTrip(req *http.Request) (*http.Response, error) {
	u := req.URL
	port := u.Port()
	if port == "" {
		port = "80"
	}
	if u.Scheme != "http" || u.User != nil || !strings.EqualFold(u.Hostname(), p.destination.host) || port != p.destination.port {
		return nil, ErrExchangeFailed
	}
	r := req.Clone(req.Context())
	r.Host = u.Host
	r.Header.Del("Proxy-Authorization")
	return p.transport.RoundTrip(r)
}

type proxyConn struct {
	net.Conn
	reader *bufio.Reader
}

func (c *proxyConn) Read(p []byte) (int, error) { return c.reader.Read(p) }

func dialProxy(ctx context.Context, proxy *url.URL, tlsConfig *tls.Config, dial func(context.Context, string, string) (net.Conn, error)) (net.Conn, error) {
	host, address, err := proxyEndpoint(proxy)
	if err != nil {
		return nil, ErrExchangeFailed
	}
	conn, err := dial(ctx, "tcp", address)
	if err != nil {
		return nil, ErrExchangeFailed
	}
	raw := conn
	connected := false
	defer func() {
		if !connected {
			raw.Close()
		}
	}()
	stop := context.AfterFunc(ctx, func() { raw.Close() })
	defer stop()
	if deadline, ok := ctx.Deadline(); ok {
		if err := conn.SetDeadline(deadline); err != nil {
			return nil, ErrExchangeFailed
		}
	}
	if proxy.Scheme == "https" {
		config := &tls.Config{MinVersion: tls.VersionTLS12}
		if tlsConfig != nil {
			config = tlsConfig.Clone()
		}
		config.ServerName = host
		config.NextProtos = []string{"http/1.1"}
		secure := tls.Client(conn, config)
		if err := secure.HandshakeContext(ctx); err != nil {
			return nil, ErrExchangeFailed
		}
		conn = secure
	}
	if !stop() || ctx.Err() != nil {
		return nil, ErrExchangeFailed
	}
	connected = true
	return conn, nil
}

// The caller supplies only a validated IP:port. Never CONNECT to a hostname or
// retry a rejected tunnel via direct networking.
func connectProxy(ctx context.Context, proxy *url.URL, target string, headers http.Header, tlsConfig *tls.Config, dial func(context.Context, string, string) (net.Conn, error)) (net.Conn, error) {
	host, _, err := net.SplitHostPort(target)
	if err != nil || blockedIP(net.ParseIP(host)) {
		return nil, ErrExchangeFailed
	}
	conn, err := dialProxy(ctx, proxy, tlsConfig, dial)
	if err != nil {
		return nil, ErrExchangeFailed
	}
	connected := false
	defer func() {
		if !connected {
			conn.Close()
		}
	}()
	stop := context.AfterFunc(ctx, func() { conn.Close() })
	defer stop()
	req := &http.Request{Method: http.MethodConnect, URL: &url.URL{Opaque: target}, Host: target, Header: headers}
	if proxy.User != nil {
		password, _ := proxy.User.Password()
		req.Header.Set("Proxy-Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(proxy.User.Username()+":"+password)))
	}
	if err := req.Write(conn); err != nil {
		return nil, ErrExchangeFailed
	}
	limited := &io.LimitedReader{R: conn, N: 64 << 10}
	reader := bufio.NewReader(limited)
	resp, err := http.ReadResponse(reader, req)
	if err != nil || resp.StatusCode != http.StatusOK {
		return nil, ErrExchangeFailed
	}
	limited.N = math.MaxInt64
	if !stop() || ctx.Err() != nil {
		return nil, ErrExchangeFailed
	}
	if err := conn.SetDeadline(time.Time{}); err != nil {
		return nil, ErrExchangeFailed
	}
	connected = true
	return &proxyConn{Conn: conn, reader: reader}, nil
}
