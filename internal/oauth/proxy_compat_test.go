package oauth

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type socksBehavior struct {
	stage           int32
	mode            string
	entered, closed chan struct{}
}

// A minimal RFC 1928/1929 peer used only to inspect wire traffic. Production
// negotiation and cancellation are provided by x/net/proxy, not this fixture.
func newOAuthSOCKSFixture(t *testing.T, secure, numeric bool, scheme string, auth bool, behavior *socksBehavior) *oauthProxyFixture {
	t.Helper()
	f := newOAuthProxyFixture(t, secure, false, numeric, nil)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	connections := make(map[net.Conn]bool)
	var workers sync.WaitGroup
	acceptDone := make(chan struct{})
	go func() {
		defer close(acceptDone)
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			mu.Lock()
			connections[conn] = true
			mu.Unlock()
			workers.Add(1)
			go func() {
				defer workers.Done()
				defer conn.Close()
				defer func() { mu.Lock(); delete(connections, conn); mu.Unlock() }()
				f.active.Add(1)
				defer f.active.Add(-1)
				n := f.calls.Add(1)
				var greeting [2]byte
				if _, err := io.ReadFull(conn, greeting[:]); err != nil {
					return
				}
				methods := make([]byte, int(greeting[1]))
				if _, err := io.ReadFull(conn, methods); err != nil {
					return
				}
				method := byte(0)
				if auth {
					method = 2
				}
				if greeting[0] != 5 || !bytes.Contains(methods, []byte{method}) {
					t.Error("SOCKS greeting or authentication method changed")
					return
				}
				conn.Write([]byte{5, method})
				if auth {
					var prefix [2]byte
					if _, err := io.ReadFull(conn, prefix[:]); err != nil {
						return
					}
					user := make([]byte, int(prefix[1]))
					if _, err := io.ReadFull(conn, user); err != nil {
						return
					}
					var length [1]byte
					if _, err := io.ReadFull(conn, length[:]); err != nil {
						return
					}
					password := make([]byte, int(length[0]))
					if _, err := io.ReadFull(conn, password); err != nil {
						return
					}
					if prefix[0] != 1 || string(user) != "fixture-proxy-user" || string(password) != "fixture-proxy-password" {
						t.Error("SOCKS authentication changed")
						return
					}
					if behavior != nil && behavior.stage == n && behavior.mode == "auth-reject" {
						conn.Write([]byte{1, 1})
						return
					}
					conn.Write([]byte{1, 0})
				}
				var command [4]byte
				if _, err := io.ReadFull(conn, command[:]); err != nil {
					return
				}
				length := 0
				switch command[3] {
				case 1:
					length = net.IPv4len
				case 4:
					length = net.IPv6len
				default:
					t.Error("SOCKS target was not a validated numeric IP")
					return
				}
				address := make([]byte, length+2)
				if _, err := io.ReadFull(conn, address); err != nil {
					return
				}
				target := net.JoinHostPort(net.IP(address[:length]).String(), strconv.Itoa(int(binary.BigEndian.Uint16(address[length:]))))
				if command[0] != 5 || command[1] != 1 || command[2] != 0 || target != f.target {
					t.Error("SOCKS command target changed")
					return
				}
				if behavior != nil && behavior.stage == n {
					if behavior.mode == "stall" {
						conn.SetReadDeadline(time.Now().Add(2 * time.Second))
						close(behavior.entered)
						var b [1]byte
						if _, err := conn.Read(b[:]); err != io.EOF {
							t.Error("cancelled SOCKS socket did not close")
						}
						close(behavior.closed)
						return
					}
					conn.Write([]byte{5, 2, 0, 1, 0, 0, 0, 0, 0, 0})
					return
				}
				upstream, err := net.DialTimeout("tcp", f.target, time.Second)
				if err != nil {
					t.Error("SOCKS fixture upstream unavailable")
					return
				}
				defer upstream.Close()
				conn.Write([]byte{5, 0, 0, 1, 127, 0, 0, 1, 0, 0})
				done := make(chan struct{})
				go func() { io.Copy(upstream, conn); upstream.Close(); close(done) }()
				io.Copy(conn, upstream)
				conn.Close()
				<-done
			}()
		}
	}()
	t.Cleanup(func() {
		listener.Close()
		<-acceptDone
		mu.Lock()
		for conn := range connections {
			conn.Close()
		}
		mu.Unlock()
		workers.Wait()
	})
	f.proxyURL = &url.URL{Scheme: scheme, Host: listener.Addr().String()}
	if auth {
		f.proxyURL.User = url.UserPassword("fixture-proxy-user", "fixture-proxy-password")
	}
	f.transport.Proxy = http.ProxyURL(f.proxyURL)
	f.svc.dialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		if address == f.target {
			f.direct.Add(1)
		} else if address != f.proxyURL.Host {
			t.Error("SOCKS attempted an unexpected socket destination")
			return nil, ErrExchangeFailed
		}
		return (&net.Dialer{}).DialContext(ctx, network, address)
	}
	return f
}

func TestOAuthSOCKSProxyExchange(t *testing.T) {
	for _, scheme := range []string{"socks5", "socks5h"} {
		for _, secure := range []bool{false, true} {
			for _, numeric := range []bool{false, true} {
				for _, auth := range []bool{false, true} {
					t.Run(fmt.Sprintf("%s/TLS=%v/numeric=%v/auth=%v", scheme, secure, numeric, auth), func(t *testing.T) {
						f := newOAuthSOCKSFixture(t, secure, numeric, scheme, auth, nil)
						f.transport.GetProxyConnectHeader = func(context.Context, *url.URL, string) (http.Header, error) {
							t.Error("SOCKS must not invoke HTTP CONNECT headers")
							return nil, ErrExchangeFailed
						}
						result, err := f.exchange(ctx())
						if err != nil || result.Login != "alice" || f.requests.Load() != 2 || f.calls.Load() != 2 || f.direct.Load() != 0 {
							t.Fatal("SOCKS token/user exchange failed or bypassed proxy", err)
						}
					})
				}
			}
		}
	}
}

func TestOAuthSOCKSProxyFailureAndCancellation(t *testing.T) {
	for _, scheme := range []string{"socks5", "socks5h"} {
		for _, stage := range []int32{1, 2} {
			for _, mode := range []string{"auth-reject", "command-reject", "cancel", "timeout"} {
				t.Run(fmt.Sprintf("%s/stage=%d/%s", scheme, stage, mode), func(t *testing.T) {
					behavior := &socksBehavior{stage: stage, mode: mode, entered: make(chan struct{}), closed: make(chan struct{})}
					if mode == "cancel" || mode == "timeout" {
						behavior.mode = "stall"
					}
					f := newOAuthSOCKSFixture(t, false, false, scheme, true, behavior)
					if behavior.mode != "stall" {
						_, err := f.exchange(ctx())
						assertOAuthProxyError(t, err)
					} else {
						ctx, cancel := context.WithCancel(context.Background())
						defer cancel()
						if mode == "timeout" {
							f.svc.client.Timeout = 150 * time.Millisecond
						}
						result := make(chan error, 1)
						go func() { _, err := f.exchange(ctx); result <- err }()
						select {
						case <-behavior.entered:
						case <-time.After(time.Second):
							t.Fatal("SOCKS handshake did not start")
						}
						if mode == "cancel" {
							cancel()
						}
						select {
						case err := <-result:
							assertOAuthProxyError(t, err)
						case <-time.After(time.Second):
							t.Fatal("SOCKS cancellation exceeded bound")
						}
						select {
						case <-behavior.closed:
						case <-time.After(time.Second):
							t.Fatal("SOCKS cancellation leaked connection")
						}
					}
					if f.calls.Load() != stage || f.requests.Load() != stage-1 || f.direct.Load() != 0 {
						t.Fatal("SOCKS failure retried or bypassed proxy")
					}
				})
			}
		}
	}
}

func TestOAuthUnicodeHTTPSProxyIdentity(t *testing.T) {
	const unicodeHost, asciiHost = "pr\u00fcxy.example", "xn--prxy-1ra.example"
	for _, numeric := range []bool{false, true} {
		for _, mismatch := range []bool{false, true} {
			t.Run(fmt.Sprintf("forward=%v/mismatch=%v", numeric, mismatch), func(t *testing.T) {
				f := newOAuthProxyFixture(t, false, true, numeric, nil, "example.com", "example.com", unicodeHost, asciiHost)
				if numeric {
					// Retain numeric origin for the standard HTTP forward branch.
					f.base = "http://" + f.target + "/gitea"
					f.originHost = f.target
					if _, err := f.svc.SaveApp(ctx(), SaveAppInput{Provider: ProviderGitea, ClientID: "fixture-id", ClientSecret: strp("fixture-client-secret"), BaseURL: f.base, Enabled: true}); err != nil {
						t.Fatal(err)
					}
				}
				if mismatch {
					actualDial := f.svc.dialContext
					port := f.proxyURL.Port()
					f.proxyURL.Host = net.JoinHostPort("wrong.invalid", port)
					f.svc.dialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
						if address != f.proxyURL.Host {
							f.direct.Add(1)
							return nil, ErrExchangeFailed
						}
						return actualDial(ctx, network, net.JoinHostPort(asciiHost, port))
					}
				}
				f.transport.Proxy = func(r *http.Request) (*url.URL, error) {
					if r.URL.Host != f.originHost {
						t.Error("Unicode proxy selection changed the original target")
					}
					return f.proxyURL, nil
				}
				result, err := f.exchange(ctx())
				if mismatch {
					assertOAuthProxyError(t, err)
					if f.calls.Load() != 0 || f.requests.Load() != 0 || f.direct.Load() != 0 {
						t.Fatal("Unicode proxy TLS failure did not fail closed")
					}
				} else if err != nil || result.Login != "alice" || f.requests.Load() != 2 || f.calls.Load() != 2 || f.direct.Load() != 0 {
					t.Fatal("Unicode HTTPS proxy exchange failed", err)
				}
			})
		}
	}
}

func TestOAuthCONNECTHeadersAreClonedAndIsolated(t *testing.T) {
	for _, dynamic := range []bool{false, true} {
		f := newOAuthProxyFixture(t, true, false, false, func(w http.ResponseWriter, r *http.Request, n int32) bool {
			if r.Header.Get("X-Proxy-Session") != "fixture-connect-secret" || dynamic && r.Header.Get("X-Static-Only") != "" {
				t.Error("CONNECT headers lost or dynamic priority changed")
			}
			return false
		})
		static := http.Header{"X-Proxy-Session": {"fixture-connect-secret"}, "X-Static-Only": {"static"}, "Proxy-Authorization": {"fixture-original-header"}}
		returned := http.Header{"X-Proxy-Session": {"fixture-connect-secret"}, "Proxy-Authorization": {"fixture-original-header"}}
		f.transport.ProxyConnectHeader = static
		var hooks atomic.Int32
		if dynamic {
			f.transport.GetProxyConnectHeader = func(ctx context.Context, proxyURL *url.URL, target string) (http.Header, error) {
				hooks.Add(1)
				if ctx.Err() != nil || target != f.target || proxyURL.String() != f.proxyURL.String() {
					t.Error("CONNECT hook received unexpected proxy or unpinned target")
				}
				proxyURL.Host = "mutated.invalid"
				return returned, nil
			}
		}
		result, err := f.exchange(ctx())
		if err != nil || result.Login != "alice" || f.calls.Load() != 2 || f.requests.Load() != 2 || f.direct.Load() != 0 {
			t.Fatal("CONNECT header exchange failed", err)
		}
		if static.Get("Proxy-Authorization") != "fixture-original-header" || returned.Get("Proxy-Authorization") != "fixture-original-header" || dynamic && hooks.Load() != 2 {
			t.Fatal("shared CONNECT headers were mutated or hook not called")
		}
	}
}

func TestOAuthCONNECTHeaderFailuresAreClosed(t *testing.T) {
	for _, mode := range []string{"hook-error", "invalid-name", "newline", "oversize", "host", "body-framing"} {
		t.Run(mode, func(t *testing.T) {
			f := newOAuthProxyFixture(t, false, false, false, nil)
			header := make(http.Header)
			switch mode {
			case "hook-error":
				f.transport.GetProxyConnectHeader = func(context.Context, *url.URL, string) (http.Header, error) {
					return nil, errors.New("fixture-connect-secret")
				}
			case "invalid-name":
				header["Invalid Header"] = []string{"secret"}
			case "newline":
				header.Set("X-Proxy-Session", "secret\r\nAuthorization: fixture-access-token")
			case "oversize":
				header.Set("X-Proxy-Session", string(bytes.Repeat([]byte{'s'}, 64<<10)))
			case "host":
				header.Set("Host", "other.invalid")
			case "body-framing":
				header.Set("Content-Length", "123")
			}
			f.transport.ProxyConnectHeader = header
			_, err := f.exchange(ctx())
			assertOAuthProxyError(t, err)
			if f.calls.Load() != 0 || f.requests.Load() != 0 || f.direct.Load() != 0 {
				t.Fatal("CONNECT header failure attempted networking")
			}
		})
	}
}
