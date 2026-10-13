package oauth

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const oauthProxyAuth = "fixture-proxy-user:fixture-proxy-password"

type oauthProxyFixture struct {
	svc                      *service
	transport                *http.Transport
	proxyURL                 *url.URL
	base, target, originHost string
	requests, calls, direct  atomic.Int32
	active                   atomic.Int32
}

// All sockets are real loopback sockets. The resolver adapter makes the original
// hostname observable without relying on DNS or reaching any external service.
func newOAuthProxyFixture(t *testing.T, originTLS, proxyTLS, numeric bool, hook func(http.ResponseWriter, *http.Request, int32) bool, hostname ...string) *oauthProxyFixture {
	t.Helper()
	f := &oauthProxyFixture{}
	origin := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.requests.Add(1)
		if r.Host != f.originHost || r.Header.Get("Proxy-Authorization") != "" || r.Header.Get("X-Proxy-Session") != "" || r.Header.Get("X-Static-Only") != "" {
			t.Error("origin Host changed or proxy credentials reached origin")
		}
		if originTLS && !numeric {
			host, _, _ := net.SplitHostPort(f.originHost)
			if r.TLS.ServerName != host {
				t.Error("original TLS SNI changed")
			}
		}
		switch r.URL.Path {
		case "/gitea/login/oauth/access_token":
			if r.ParseForm() != nil || r.Form.Get("client_secret") != "fixture-client-secret" || r.Form.Get("code") != "fixture-code" || r.Header.Get("Authorization") != "" {
				t.Error("token authentication changed")
			}
			io.WriteString(w, `{"access_token":"fixture-access-token"}`)
		case "/gitea/api/v1/user":
			if r.Header.Get("Authorization") != "Bearer fixture-access-token" {
				t.Error("user authentication changed")
			}
			io.WriteString(w, `{"login":"alice"}`)
		default:
			t.Error("unexpected OAuth endpoint")
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	if originTLS {
		if len(hostname) > 0 {
			origin.TLS = &tls.Config{Certificates: []tls.Certificate{oauthHostCertificate(t, hostname[1])}, MinVersion: tls.VersionTLS12}
		}
		origin.StartTLS()
	} else {
		origin.Start()
	}
	t.Cleanup(origin.Close)
	originURL, _ := url.Parse(origin.URL)
	f.target = originURL.Host
	f.originHost = originURL.Host
	if !numeric {
		f.originHost = net.JoinHostPort("example.com", originURL.Port())
	}
	f.base = originURL.Scheme + "://" + f.originHost + "/gitea"
	if len(hostname) > 0 {
		f.originHost = net.JoinHostPort(hostname[1], originURL.Port())
		f.base = originURL.Scheme + "://" + net.JoinHostPort(hostname[0], originURL.Port()) + "/gitea"
	}
	forward := numeric && !originTLS
	proxy := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.active.Add(1)
		defer f.active.Add(-1)
		n := f.calls.Add(1)
		if len(hostname) >= 4 && (r.TLS == nil || r.TLS.ServerName != hostname[3]) {
			t.Error("Unicode HTTPS proxy SNI was not canonicalized")
		}
		if r.Header.Get("Proxy-Authorization") != "Basic "+base64.StdEncoding.EncodeToString([]byte(oauthProxyAuth)) {
			t.Error("proxy authentication changed")
		}
		if !forward && (r.Method != http.MethodConnect || r.Host != f.target || r.Header.Get("Authorization") != "" || r.ContentLength > 0) {
			t.Error("CONNECT was not pinned or leaked origin authentication")
			w.WriteHeader(http.StatusForbidden)
			return
		}
		if hook != nil && hook(w, r, n) {
			return
		}
		if forward {
			if r.Method == http.MethodConnect || !r.URL.IsAbs() || r.URL.Host != f.target {
				t.Error("numeric HTTP forwarding changed")
				w.WriteHeader(http.StatusForbidden)
				return
			}
			request := r.Clone(r.Context())
			request.RequestURI = ""
			request.Header.Del("Proxy-Authorization")
			transport := &http.Transport{Proxy: nil}
			defer transport.CloseIdleConnections()
			response, err := transport.RoundTrip(request)
			if err != nil {
				t.Error("fixture forwarding failed")
				w.WriteHeader(http.StatusBadGateway)
				return
			}
			defer response.Body.Close()
			w.WriteHeader(response.StatusCode)
			io.Copy(w, response.Body)
			return
		}
		upstream, err := net.DialTimeout("tcp", f.target, time.Second)
		if err != nil {
			t.Error("fixture upstream unavailable")
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		client, buffered, err := w.(http.Hijacker).Hijack()
		if err != nil {
			upstream.Close()
			t.Error("fixture hijack failed")
			return
		}
		defer client.Close()
		defer upstream.Close()
		fmt.Fprint(buffered, "HTTP/1.1 200 Connection Established\r\n\r\n")
		buffered.Flush()
		done := make(chan struct{})
		go func() {
			io.Copy(upstream, buffered)
			upstream.Close()
			close(done)
		}()
		io.Copy(client, upstream)
		client.Close()
		<-done
	}))
	if proxyTLS {
		if len(hostname) >= 4 {
			proxy.TLS = &tls.Config{Certificates: []tls.Certificate{oauthHostCertificate(t, hostname[3])}, MinVersion: tls.VersionTLS12}
		}
		proxy.StartTLS()
	} else {
		proxy.Start()
	}
	t.Cleanup(proxy.Close)
	f.proxyURL, _ = url.Parse(proxy.URL)
	actualProxy := f.proxyURL.Host
	proxyAddress := actualProxy
	if len(hostname) >= 4 {
		proxyAddress = net.JoinHostPort(hostname[3], f.proxyURL.Port())
		f.proxyURL.Host = net.JoinHostPort(hostname[2], f.proxyURL.Port())
	}
	f.proxyURL.User = url.UserPassword("fixture-proxy-user", "fixture-proxy-password")
	roots := x509.NewCertPool()
	if originTLS {
		roots.AddCert(origin.Certificate())
	}
	if proxyTLS {
		roots.AddCert(proxy.Certificate())
	}
	f.transport = &http.Transport{Proxy: http.ProxyURL(f.proxyURL), TLSClientConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}}
	svc, _, _, _ := newService(t, &http.Client{Transport: f.transport, Timeout: time.Second})
	f.svc = svc.(*service)
	f.svc.lookupIP = func(_ context.Context, host string) ([]net.IPAddr, error) {
		want := "example.com"
		if len(hostname) > 0 {
			want = hostname[1]
		}
		if host != want {
			t.Error("unexpected DNS target")
		}
		return []net.IPAddr{{IP: net.ParseIP("127.0.0.1")}}, nil
	}
	f.svc.dialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		if address == f.target {
			f.direct.Add(1)
		} else if address != proxyAddress {
			t.Error("unexpected or unpinned socket destination")
			return nil, ErrExchangeFailed
		} else {
			address = actualProxy
		}
		return (&net.Dialer{}).DialContext(ctx, network, address)
	}
	if _, err := f.svc.SaveApp(ctx(), SaveAppInput{Provider: ProviderGitea, ClientID: "fixture-id", ClientSecret: strp("fixture-client-secret"), BaseURL: f.base, Enabled: true}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		deadline := time.Now().Add(time.Second)
		for f.active.Load() != 0 && time.Now().Before(deadline) {
			time.Sleep(time.Millisecond)
		}
		if f.active.Load() != 0 {
			t.Error("proxy connection handler leaked")
		}
	})
	return f
}

func oauthHostCertificate(t *testing.T, host string) tls.Certificate {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1), DNSNames: []string{host},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true, IsCA: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
}

func TestOAuthIDNAHostnameExchange(t *testing.T) {
	const unicodeHost, asciiHost = "b\u00fccher.example", "xn--bcher-kva.example"
	for _, secure := range []bool{false, true} {
		for _, proxied := range []bool{false, true} {
			t.Run(fmt.Sprintf("TLS=%v/proxy=%v", secure, proxied), func(t *testing.T) {
				f := newOAuthProxyFixture(t, secure, false, false, nil, unicodeHost, asciiHost)
				f.transport.Proxy = func(r *http.Request) (*url.URL, error) {
					if r.URL.Hostname() != unicodeHost {
						t.Error("proxy did not receive the original Unicode request")
					}
					if proxied {
						return f.proxyURL, nil
					}
					return nil, nil
				}
				result, err := f.exchange(ctx())
				if err != nil || result.Login != "alice" || f.requests.Load() != 2 {
					t.Fatal("IDNA token/user exchange failed", err)
				}
				if proxied && (f.calls.Load() != 2 || f.direct.Load() != 0) || !proxied && (f.calls.Load() != 0 || f.direct.Load() != 2) {
					t.Fatal("IDNA exchange bypassed configured route")
				}
			})
		}
	}
}

func TestOAuthIDNARejectsInvalidAndUnexpectedHost(t *testing.T) {
	lookupCalls := 0
	lookup := func(context.Context, string) ([]net.IPAddr, error) {
		lookupCalls++
		return []net.IPAddr{{IP: net.ParseIP("127.0.0.1")}}, nil
	}
	if _, err := resolveDestination(ctx(), "http://bad\u200d.example", lookup); !errors.Is(err, ErrExchangeFailed) || lookupCalls != 0 {
		t.Fatal("invalid IDNA reached the resolver")
	}
	d, err := resolveDestination(ctx(), "http://b\u00fccher.example", lookup)
	if err != nil || d.host != "xn--bcher-kva.example" {
		t.Fatal("resolver hostname was not canonicalized")
	}
	dials := 0
	dial := d.dial(func(context.Context, string, string) (net.Conn, error) { dials++; return nil, ErrExchangeFailed })
	dial(ctx(), "tcp", "other.example:80")
	if dials != 0 {
		t.Fatal("different canonical host was dialed")
	}
	dial(ctx(), "tcp", "xn--bcher-kva.example:80")
	dial(ctx(), "tcp", "b\u00fccher.example:80")
	if dials != 2 {
		t.Fatal("Unicode and punycode dial forms did not match the validated host")
	}
}

func (f *oauthProxyFixture) exchange(ctx context.Context) (*TokenResult, error) {
	return f.svc.Exchange(ctx, ProviderGitea, "fixture-code", "https://app.example/callback")
}

func assertOAuthProxyError(t *testing.T, err error) {
	t.Helper()
	if !errors.Is(err, ErrExchangeFailed) || err.Error() != ErrExchangeFailed.Error() {
		t.Fatal("proxy error was not safely reduced to the public domain error")
	}
}

func TestOAuthExplicitProxyExchange(t *testing.T) {
	for _, originTLS := range []bool{false, true} {
		for _, proxyTLS := range []bool{false, true} {
			for _, numeric := range []bool{false, true} {
				t.Run(fmt.Sprintf("originTLS=%v/proxyTLS=%v/numeric=%v", originTLS, proxyTLS, numeric), func(t *testing.T) {
					f := newOAuthProxyFixture(t, originTLS, proxyTLS, numeric, nil)
					var selections atomic.Int32
					f.transport.Proxy = func(r *http.Request) (*url.URL, error) {
						selections.Add(1)
						if r.URL.Host != f.originHost {
							t.Error("proxy selected after hostname replacement")
						}
						return f.proxyURL, nil
					}
					result, err := f.exchange(ctx())
					if err != nil || result.Login != "alice" || result.AccessToken != "fixture-access-token" {
						t.Fatal("proxy-backed token/user exchange failed", err)
					}
					if f.requests.Load() != 2 || f.calls.Load() != 2 || f.direct.Load() != 0 || selections.Load() != 2 {
						t.Fatal("proxy bypass, retry or lost selection")
					}
				})
			}
		}
	}
}

// Go caches ProxyFromEnvironment's first environment snapshot process-wide.
// Fresh subprocesses prove the real default client path, not a proxy mock.
func TestOAuthProxyEnvironment(t *testing.T) {
	const marker = "PIPEWRIGHT_OAUTH_PROXY_TEST"
	if mode := os.Getenv(marker); mode != "" {
		secure := strings.HasPrefix(mode, "https")
		bypass := strings.Contains(mode, "no-proxy")
		var f *oauthProxyFixture
		if strings.HasPrefix(mode, "socks5") {
			scheme := "socks5"
			if strings.HasPrefix(mode, "socks5h") {
				scheme = "socks5h"
			}
			f = newOAuthSOCKSFixture(t, false, false, scheme, true, nil)
		} else {
			f = newOAuthProxyFixture(t, secure, strings.Contains(mode, "tls-proxy"), false, nil)
		}
		if secure {
			t.Setenv("HTTPS_PROXY", f.proxyURL.String())
		} else {
			t.Setenv("HTTP_PROXY", f.proxyURL.String())
		}
		if bypass {
			t.Setenv("NO_PROXY", "example.com")
		}
		f.transport.Proxy = http.ProxyFromEnvironment
		if mode == "http-default" {
			// Exercise application construction: nil uses DefaultTransport.
			f.svc.client.Transport = nil
		}
		result, err := f.exchange(ctx())
		if err != nil || result.Login != "alice" {
			t.Fatal("environment proxy exchange failed", err)
		}
		want := int32(2)
		if bypass {
			if f.calls.Load() != 0 || f.direct.Load() != want {
				t.Fatal("NO_PROXY did not match the original hostname")
			}
		} else if f.calls.Load() != want || f.direct.Load() != 0 {
			t.Fatal("environment proxy bypassed or retried")
		}
		return
	}
	for _, mode := range []string{"http", "https", "http-no-proxy", "https-no-proxy", "http-default", "http-tls-proxy", "https-tls-proxy", "socks5", "socks5h", "socks5-no-proxy", "socks5h-no-proxy"} {
		t.Run(mode, func(t *testing.T) {
			command := exec.Command(os.Args[0], "-test.run=^TestOAuthProxyEnvironment$")
			for _, entry := range os.Environ() {
				key, _, _ := strings.Cut(entry, "=")
				switch strings.ToUpper(key) {
				case "HTTP_PROXY", "HTTPS_PROXY", "NO_PROXY", "ALL_PROXY", "REQUEST_METHOD", marker:
					continue
				}
				command.Env = append(command.Env, entry)
			}
			command.Env = append(command.Env, marker+"="+mode)
			if output, err := command.CombinedOutput(); err != nil {
				t.Fatalf("environment fixture failed: %v\n%s", err, output)
			}
		})
	}
}

func TestOAuthProxyTLSIdentityFailureDoesNotFallback(t *testing.T) {
	for _, mode := range []string{"origin", "proxy-connect", "proxy-forward"} {
		t.Run(mode, func(t *testing.T) {
			f := newOAuthProxyFixture(t, mode == "origin", mode != "origin", mode == "proxy-forward", nil)
			if mode == "origin" {
				f.base = strings.Replace(f.base, "example.com", "wrong.invalid", 1)
				f.originHost = strings.Replace(f.originHost, "example.com", "wrong.invalid", 1)
				f.svc.lookupIP = func(context.Context, string) ([]net.IPAddr, error) {
					return []net.IPAddr{{IP: net.ParseIP("127.0.0.1")}}, nil
				}
				if _, err := f.svc.SaveApp(ctx(), SaveAppInput{Provider: ProviderGitea, ClientID: "fixture-id", ClientSecret: strp("fixture-client-secret"), BaseURL: f.base, Enabled: true}); err != nil {
					t.Fatal(err)
				}
			} else {
				actual := f.proxyURL.Host
				f.proxyURL.Host = net.JoinHostPort("wrong.invalid", f.proxyURL.Port())
				f.svc.dialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
					if address != f.proxyURL.Host {
						f.direct.Add(1)
						return nil, ErrExchangeFailed
					}
					return (&net.Dialer{}).DialContext(ctx, network, actual)
				}
			}
			_, err := f.exchange(ctx())
			assertOAuthProxyError(t, err)
			want := int32(0)
			if mode == "origin" {
				want = 1
			}
			if f.calls.Load() != want || f.requests.Load() != 0 || f.direct.Load() != 0 {
				t.Fatal("TLS identity failure bypassed validation or retried directly")
			}
		})
	}
}

func TestOAuthProxyRejectsWithoutFallback(t *testing.T) {
	for _, numeric := range []bool{false, true} {
		for _, proxyTLS := range []bool{false, true} {
			for _, stage := range []int32{1, 2} {
				for _, status := range []int{http.StatusProxyAuthRequired, http.StatusTemporaryRedirect} {
					t.Run(fmt.Sprintf("numeric=%v/proxyTLS=%v/stage=%d/status=%d", numeric, proxyTLS, stage, status), func(t *testing.T) {
						var leaks atomic.Int32
						sink := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { leaks.Add(1) }))
						defer sink.Close()
						f := newOAuthProxyFixture(t, false, proxyTLS, numeric, func(w http.ResponseWriter, r *http.Request, n int32) bool {
							if n != stage {
								return false
							}
							w.Header().Set("Location", sink.URL+"/fixture-access-token")
							w.WriteHeader(status)
							io.WriteString(w, "fixture-client-secret fixture-proxy-password")
							return true
						})
						_, err := f.exchange(ctx())
						assertOAuthProxyError(t, err)
						if f.requests.Load() != stage-1 || f.calls.Load() != stage || f.direct.Load() != 0 || leaks.Load() != 0 {
							t.Fatal("proxy rejection retried, followed redirect or leaked to origin")
						}
					})
				}
			}
		}
	}
}

func TestOAuthProxyAuthorizationHeaderIsIsolated(t *testing.T) {
	for _, numeric := range []bool{false, true} {
		t.Run(fmt.Sprintf("numeric=%v", numeric), func(t *testing.T) {
			f := newOAuthProxyFixture(t, false, false, numeric, nil)
			req, err := http.NewRequestWithContext(ctx(), http.MethodPost, f.base+"/login/oauth/access_token", strings.NewReader("client_secret=fixture-client-secret&code=fixture-code"))
			if err != nil {
				t.Fatal(err)
			}
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			req.Header.Set("Proxy-Authorization", "Bearer fixture-access-token")
			response, err := f.svc.request(req)
			if err != nil {
				t.Fatal("credential isolation request failed")
			}
			io.Copy(io.Discard, response.Body)
			response.Body.Close()
			if f.calls.Load() != 1 || f.requests.Load() != 1 || f.direct.Load() != 0 {
				t.Fatal("isolation request bypassed configured proxy")
			}
			if req.Header.Get("Proxy-Authorization") != "Bearer fixture-access-token" {
				t.Fatal("shared caller headers were mutated")
			}
		})
	}
}

func TestOAuthProxyCancellationClosesSocket(t *testing.T) {
	for _, stage := range []int32{1, 2} {
		for _, timeout := range []bool{false, true} {
			t.Run(fmt.Sprintf("stage=%d/timeout=%v", stage, timeout), func(t *testing.T) {
				entered, closed := make(chan struct{}), make(chan struct{})
				f := newOAuthProxyFixture(t, false, false, false, func(w http.ResponseWriter, r *http.Request, n int32) bool {
					if n != stage {
						return false
					}
					conn, _, err := w.(http.Hijacker).Hijack()
					if err != nil {
						t.Error("fixture hijack failed")
						return true
					}
					defer conn.Close()
					conn.SetReadDeadline(time.Now().Add(2 * time.Second))
					close(entered)
					var b [1]byte
					_, err = conn.Read(b[:])
					if err != io.EOF {
						t.Error("cancelled CONNECT socket did not close")
					}
					close(closed)
					return true
				})
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				if timeout {
					f.svc.client.Timeout = 150 * time.Millisecond
				}
				result := make(chan error, 1)
				go func() { _, err := f.exchange(ctx); result <- err }()
				select {
				case <-entered:
				case <-time.After(time.Second):
					t.Fatal("CONNECT did not start")
				}
				if !timeout {
					cancel()
				}
				select {
				case err := <-result:
					assertOAuthProxyError(t, err)
				case <-time.After(time.Second):
					t.Fatal("proxy cancellation did not return within bound")
				}
				select {
				case <-closed:
				case <-time.After(time.Second):
					t.Fatal("proxy connection leaked after cancellation")
				}
				if f.calls.Load() != stage || f.requests.Load() != stage-1 || f.direct.Load() != 0 {
					t.Fatal("cancelled proxy attempt retried or bypassed")
				}
			})
		}
	}
}

func TestOAuthProxySelectorAndDNSFailuresAreClosed(t *testing.T) {
	for _, mode := range []string{"selector-error", "invalid-proxy", "dns-error", "metadata-answer", "dial-error"} {
		t.Run(mode, func(t *testing.T) {
			f := newOAuthProxyFixture(t, false, false, false, nil)
			switch mode {
			case "selector-error":
				f.transport.Proxy = func(*http.Request) (*url.URL, error) { return nil, errors.New("fixture-proxy-password") }
			case "invalid-proxy":
				f.transport.Proxy = http.ProxyURL(&url.URL{Scheme: "ftp", Host: "127.0.0.1:1080", User: url.UserPassword("user", "fixture-proxy-password")})
			case "dns-error":
				f.svc.lookupIP = func(context.Context, string) ([]net.IPAddr, error) { return nil, errors.New("fixture-client-secret") }
			case "metadata-answer":
				f.svc.lookupIP = func(context.Context, string) ([]net.IPAddr, error) {
					return []net.IPAddr{{IP: net.ParseIP("169.254.169.254")}}, nil
				}
			case "dial-error":
				f.svc.dialContext = func(context.Context, string, string) (net.Conn, error) {
					return nil, errors.New("fixture-proxy-password")
				}
			}
			_, err := f.exchange(ctx())
			assertOAuthProxyError(t, err)
			if f.calls.Load() != 0 || f.requests.Load() != 0 || f.direct.Load() != 0 {
				t.Fatal("invalid selection or DNS attempted networking")
			}
		})
	}
}
