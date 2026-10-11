package ai

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

	"github.com/huangchengsir/pipewright/internal/opschat"
)

// Numeric HTTP destinations can use ordinary forwarding without proxy DNS.
// Hostname destinations use CONNECT so pinning preserves their virtual host.
type opsForwardProxy struct {
	transport   *http.Transport
	destination opsDestination
}

func (p *opsForwardProxy) RoundTrip(request *http.Request) (*http.Response, error) {
	u := request.URL
	port := u.Port()
	if port == "" {
		port = "80"
	}
	if u.Scheme != "http" || u.User != nil || !strings.EqualFold(u.Hostname(), p.destination.host) || port != p.destination.port {
		return nil, opschat.ErrUnavailable
	}
	r := request.Clone(request.Context())
	r.Host = u.Host
	r.Header.Del("Proxy-Authorization")
	return p.transport.RoundTrip(r)
}

func (d opsDestination) forwardProxy(base string, original, pinned *http.Transport, dial func(context.Context, string, string) (net.Conn, error)) (http.RoundTripper, error) {
	u, err := url.Parse(base)
	if err != nil {
		return nil, opschat.ErrUnavailable
	}
	if u.Scheme != "http" || net.ParseIP(u.Hostname()) == nil || original.Proxy == nil {
		return nil, nil
	}
	proxy, err := original.Proxy(&http.Request{URL: u})
	if err != nil {
		return nil, opschat.ErrUnavailable
	}
	if proxy == nil {
		return nil, nil
	}
	if (proxy.Scheme != "http" && proxy.Scheme != "https") || proxy.Hostname() == "" {
		return nil, opschat.ErrUnavailable
	}
	copy := *proxy
	pinned.Proxy = http.ProxyURL(&copy)
	if dial == nil {
		dial = (&net.Dialer{Timeout: 30 * time.Second}).DialContext
	}
	pinned.DialContext = dial
	if copy.Scheme == "https" {
		pinned.TLSClientConfig.ServerName = copy.Hostname()
	}
	return &opsForwardProxy{transport: pinned, destination: d}, nil
}

// Select the proxy for the original destination (including NO_PROXY), but CONNECT
// only to the validated IP supplied by destination.dial, never a hostname.
func (d opsDestination) proxyDial(base string, transport *http.Transport, dial func(context.Context, string, string) (net.Conn, error)) (func(context.Context, string, string) (net.Conn, error), error) {
	if dial == nil {
		dial = (&net.Dialer{Timeout: 30 * time.Second}).DialContext
	}
	if transport.Proxy == nil {
		return dial, nil
	}
	u, err := url.Parse(base)
	if err != nil {
		return nil, opschat.ErrUnavailable
	}
	proxy, err := transport.Proxy(&http.Request{URL: u})
	if err != nil {
		return nil, opschat.ErrUnavailable
	}
	if proxy == nil {
		return dial, nil
	}
	if (proxy.Scheme != "http" && proxy.Scheme != "https") || proxy.Hostname() == "" {
		return nil, opschat.ErrUnavailable
	}
	proxyCopy := *proxy
	return func(ctx context.Context, network, address string) (net.Conn, error) {
		return connectOpsProxy(ctx, &proxyCopy, address, transport.TLSClientConfig, dial)
	}, nil
}

type opsProxyConn struct {
	net.Conn
	reader *bufio.Reader
}

func (c *opsProxyConn) Read(p []byte) (int, error) { return c.reader.Read(p) }

func dialOpsProxy(ctx context.Context, proxy *url.URL, tlsConfig *tls.Config, dial func(context.Context, string, string) (net.Conn, error)) (net.Conn, error) {
	port := proxy.Port()
	if port == "" {
		port = "80"
		if proxy.Scheme == "https" {
			port = "443"
		}
	}
	conn, err := dial(ctx, "tcp", net.JoinHostPort(proxy.Hostname(), port))
	if err != nil {
		return nil, err
	}
	rawConn := conn
	connected := false
	defer func() {
		if !connected {
			rawConn.Close()
		}
	}()
	// Also interrupt a stalled CONNECT when cancellation has no deadline.
	stop := context.AfterFunc(ctx, func() { rawConn.Close() })
	defer stop()
	if deadline, ok := ctx.Deadline(); ok {
		if err = conn.SetDeadline(deadline); err != nil {
			return nil, err
		}
	}
	if proxy.Scheme == "https" {
		config := &tls.Config{MinVersion: tls.VersionTLS12}
		if tlsConfig != nil {
			config = tlsConfig.Clone()
		}
		config.ServerName = proxy.Hostname()
		config.NextProtos = []string{"http/1.1"}
		secure := tls.Client(conn, config)
		if err = secure.HandshakeContext(ctx); err != nil {
			return nil, err
		}
		conn = secure
	}
	if !stop() || ctx.Err() != nil {
		return nil, ctx.Err()
	}
	connected = true
	return conn, nil
}

func connectOpsProxy(ctx context.Context, proxy *url.URL, target string, tlsConfig *tls.Config, dial func(context.Context, string, string) (net.Conn, error)) (net.Conn, error) {
	conn, err := dialOpsProxy(ctx, proxy, tlsConfig, dial)
	if err != nil {
		return nil, err
	}
	connected := false
	defer func() {
		if !connected {
			conn.Close()
		}
	}()
	stop := context.AfterFunc(ctx, func() { conn.Close() })
	defer stop()
	request := &http.Request{Method: http.MethodConnect, URL: &url.URL{Opaque: target}, Host: target, Header: make(http.Header)}
	if proxy.User != nil {
		password, _ := proxy.User.Password()
		value := proxy.User.Username() + ":" + password
		request.Header.Set("Proxy-Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(value)))
	}
	if err = request.Write(conn); err != nil {
		return nil, err
	}
	limited := &io.LimitedReader{R: conn, N: 64 << 10}
	reader := bufio.NewReader(limited)
	response, err := http.ReadResponse(reader, request)
	if err != nil || response.StatusCode != http.StatusOK {
		// Never return a proxy's status text, body, URL or credentials.
		return nil, opschat.ErrUnavailable
	}
	limited.N = math.MaxInt64
	if !stop() || ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if err = conn.SetDeadline(time.Time{}); err != nil {
		return nil, err
	}
	connected = true
	return &opsProxyConn{Conn: conn, reader: reader}, nil
}
