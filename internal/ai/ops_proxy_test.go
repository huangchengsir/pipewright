package ai

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
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

func opsLoopbackProxy(t *testing.T, secure bool, target, upstream, auth string, calls *atomic.Int32, forward ...bool) *httptest.Server {
	t.Helper()
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if len(forward) > 0 && forward[0] {
			if r.Method == http.MethodConnect || !r.URL.IsAbs() || r.URL.Host != target || r.Header.Get("Proxy-Authorization") != auth {
				t.Error("unexpected forward proxy method, pinned destination or auth")
				w.WriteHeader(http.StatusForbidden)
				return
			}
			request := r.Clone(r.Context())
			request.URL.Host = upstream
			request.URL.Scheme = "http"
			request.RequestURI = ""
			request.Header.Del("Proxy-Authorization")
			transport := http.DefaultTransport.(*http.Transport).Clone()
			transport.Proxy = nil
			defer transport.CloseIdleConnections()
			response, err := transport.RoundTrip(request)
			if err != nil {
				t.Error("fixture forward failed")
				w.WriteHeader(http.StatusBadGateway)
				return
			}
			defer response.Body.Close()
			for key, values := range response.Header {
				for _, value := range values {
					w.Header().Add(key, value)
				}
			}
			w.WriteHeader(response.StatusCode)
			io.Copy(w, response.Body)
			return
		}
		if r.Method != http.MethodConnect || r.Host != target || r.Header.Get("Proxy-Authorization") != auth {
			t.Error("unexpected proxy method, pinned target or authentication")
			w.WriteHeader(http.StatusForbidden)
			return
		}
		conn, err := net.DialTimeout("tcp", upstream, time.Second)
		if err != nil {
			t.Error("loopback upstream unavailable")
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		client, buffered, err := w.(http.Hijacker).Hijack()
		if err != nil {
			conn.Close()
			t.Error("proxy hijack failed")
			return
		}
		fmt.Fprint(buffered, "HTTP/1.1 200 Connection Established\r\n\r\n")
		buffered.Flush()
		go func() {
			io.Copy(conn, buffered)
			conn.Close()
		}()
		io.Copy(client, conn)
		client.Close()
		conn.Close()
	}))
	if secure {
		server.StartTLS()
	} else {
		server.Start()
	}
	t.Cleanup(server.Close)
	return server
}

func opsProxyFixture(t *testing.T, originTLS, proxyTLS bool, originHost string) (*httptest.Server, *http.Transport, *atomic.Int32, *atomic.Int32) {
	t.Helper()
	requests, tunnels := &atomic.Int32{}, &atomic.Int32{}
	origin := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.Header.Get("Proxy-Authorization") != "" {
			t.Error("proxy credentials reached the provider")
		}
		if originHost != "" && r.Host != originHost {
			t.Error("original Host was not preserved")
		}
		if r.Header.Get("Authorization") != "Bearer test-api-key" {
			t.Error("provider authentication changed")
		}
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/v1/models" {
			io.WriteString(w, `{"data":[{"id":"test-model"}]}`)
			return
		}
		if r.URL.Path != "/v1/chat/completions" {
			t.Error("unexpected inference path")
		}
		json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]string{"content": `{"stages":[],"rationale":"proxy reply"}`}}}})
	}))
	if originTLS {
		origin.StartTLS()
	} else {
		origin.Start()
	}
	t.Cleanup(origin.Close)
	originURL, _ := url.Parse(origin.URL)
	transport := http.DefaultTransport.(*http.Transport).Clone()
	roots := x509.NewCertPool()
	if originTLS {
		roots.AddCert(origin.Certificate())
	}
	transport.TLSClientConfig = &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}
	proxy := opsLoopbackProxy(t, proxyTLS, originURL.Host, originURL.Host, "Basic "+base64.StdEncoding.EncodeToString([]byte("fixture-user:fixture-password")), tunnels, !originTLS)
	if proxyTLS {
		roots.AddCert(proxy.Certificate())
	}
	proxyURL, _ := url.Parse(proxy.URL)
	proxyURL.User = url.UserPassword("fixture-user", "fixture-password")
	transport.Proxy = http.ProxyURL(proxyURL)
	return origin, transport, requests, tunnels
}

func TestOpsProxyServiceCompatibility(t *testing.T) {
	for _, originTLS := range []bool{false, true} {
		for _, proxyTLS := range []bool{false, true} {
			t.Run(fmt.Sprintf("originTLS=%v/proxyTLS=%v", originTLS, proxyTLS), func(t *testing.T) {
				origin, transport, requests, tunnels := opsProxyFixture(t, originTLS, proxyTLS, "")
				svc, _, _ := newService(t, &http.Client{Transport: transport, Timeout: time.Second})
				if _, err := svc.Save(ctx(), SaveInput{Provider: ProviderOpenAI, BaseURL: origin.URL, Model: "test-model", APIKey: strp("test-api-key"), Enabled: true}); err != nil {
					t.Fatal(err)
				}
				if result, err := svc.Test(ctx(), TestInput{}); err != nil || !result.OK {
					t.Fatal("proxy-backed Test failed", err)
				}
				if result, err := svc.(ModelLister).Models(ctx(), TestInput{}); err != nil || !result.OK || len(result.Items) != 1 {
					t.Fatal("proxy-backed Models failed", err)
				}
				if result, err := svc.Generate(ctx(), GenerateInput{NL: "loopback fixture"}); err != nil || result.Rationale != "proxy reply" {
					t.Fatal("proxy-backed Generate failed", err)
				}
				if requests.Load() != 3 || tunnels.Load() != 3 {
					t.Fatal("proxy bypassed or requests retried")
				}
			})
		}
	}
}

func TestOpsProxyDNSPinningAndForbiddenAddresses(t *testing.T) {
	var tunnels atomic.Int32
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Host != "provider.invalid" {
			t.Error("original provider Host changed")
		}
		io.WriteString(w, "pinned")
	}))
	defer origin.Close()
	u, _ := url.Parse(origin.URL)
	proxy := opsLoopbackProxy(t, false, "127.0.0.1:80", u.Host, "", &tunnels)
	proxyURL, _ := url.Parse(proxy.URL)
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = http.ProxyURL(proxyURL)
	svc, _, _ := newService(t, &http.Client{Transport: transport})
	m := &opsModel{service: svc.(*service)}
	lookups := 0
	m.lookupIP = func(context.Context, string) ([]net.IPAddr, error) {
		lookups++
		if lookups != 1 {
			t.Error("destination resolved again after pinning")
		}
		return []net.IPAddr{{IP: net.ParseIP("127.0.0.1")}}, nil
	}
	client, cleanup, err := m.pinnedClient(ctx(), "http://provider.invalid")
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	// A rebound DNS answer cannot affect the already-created client or proxy target.
	m.lookupIP = func(context.Context, string) ([]net.IPAddr, error) {
		return []net.IPAddr{{IP: net.ParseIP("169.254.169.254")}}, nil
	}
	response, err := client.Get("http://provider.invalid/v1/models")
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if lookups != 1 || tunnels.Load() != 1 {
		t.Fatal("destination was not pinned")
	}
	for _, ip := range []string{"169.254.169.254", "0.0.0.0", "fe80::1"} {
		m.lookupIP = func(context.Context, string) ([]net.IPAddr, error) {
			return []net.IPAddr{{IP: net.ParseIP(ip)}}, nil
		}
		if _, _, err := m.pinnedClient(ctx(), "http://provider.invalid"); err == nil {
			t.Fatal("forbidden destination accepted")
		}
	}
	if tunnels.Load() != 1 {
		t.Fatal("forbidden destination reached the proxy")
	}
}

func TestOpsProxyEnvironment(t *testing.T) {
	if mode := os.Getenv("PIPEWRIGHT_AI_PROXY_TEST"); mode != "" {
		var tunnels atomic.Int32
		origin := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Host != "example.com" {
				t.Error("original Host changed")
			}
			io.WriteString(w, `{"data":[{"id":"fixture"}]}`)
		}))
		base, target := "http://example.com", "127.0.0.1:80"
		transport := http.DefaultTransport.(*http.Transport).Clone()
		if mode == "https" {
			origin.StartTLS()
			base, target = "https://example.com", "127.0.0.1:443"
			transport.TLSClientConfig = origin.Client().Transport.(*http.Transport).TLSClientConfig.Clone()
		} else {
			origin.Start()
		}
		defer origin.Close()
		u, _ := url.Parse(origin.URL)
		proxy := opsLoopbackProxy(t, false, target, u.Host, "", &tunnels)
		t.Setenv("HTTP_PROXY", proxy.URL)
		t.Setenv("HTTPS_PROXY", proxy.URL)
		t.Setenv("NO_PROXY", "")
		if mode == "bypass" {
			t.Setenv("NO_PROXY", "example.com")
		}
		svc, _, _ := newService(t, &http.Client{Transport: transport, Timeout: time.Second})
		m := &opsModel{service: svc.(*service), lookupIP: func(context.Context, string) ([]net.IPAddr, error) {
			return []net.IPAddr{{IP: net.ParseIP("127.0.0.1")}}, nil
		}, dialContext: func(c context.Context, network, address string) (net.Conn, error) {
			proxyURL, _ := url.Parse(proxy.URL)
			if address != proxyURL.Host && !(mode == "bypass" && address == target) {
				return nil, fmt.Errorf("unexpected non-loopback fixture route")
			}
			if mode == "bypass" {
				address = u.Host
			}
			return (&net.Dialer{}).DialContext(c, network, address)
		}}
		if result := m.probe(ctx(), ProviderOpenAI, base, "test-api-key"); !result.OK {
			t.Fatal("environment proxy probe failed")
		}
		want := int32(1)
		if mode == "bypass" {
			want = 0
		}
		if tunnels.Load() != want {
			t.Fatal("environment proxy selection or NO_PROXY changed")
		}
		return
	}
	// ProxyFromEnvironment caches configuration; exercise each mode in a fresh process.
	for _, mode := range []string{"proxy", "https", "bypass"} {
		t.Run(mode, func(t *testing.T) {
			command := exec.Command(os.Args[0], "-test.run=^TestOpsProxyEnvironment$")
			for _, entry := range os.Environ() {
				name, _, _ := strings.Cut(entry, "=")
				switch strings.ToUpper(name) {
				case "HTTP_PROXY", "HTTPS_PROXY", "NO_PROXY", "REQUEST_METHOD", "PIPEWRIGHT_AI_PROXY_TEST":
					continue
				}
				command.Env = append(command.Env, entry)
			}
			command.Env = append(command.Env, "PIPEWRIGHT_AI_PROXY_TEST="+mode)
			if output, err := command.CombinedOutput(); err != nil {
				t.Fatalf("environment regression failed: %v\n%s", err, output)
			}
		})
	}
}

func TestOpsProxyTLSIdentityAndRedirects(t *testing.T) {
	for _, secureProxy := range []bool{false, true} {
		t.Run(fmt.Sprintf("proxyTLS=%v", secureProxy), func(t *testing.T) {
			var tunnels, requests atomic.Int32
			origin := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				if r.Host != "example.com" || r.TLS.ServerName != "example.com" {
					t.Error("original TLS identity or HTTP Host changed")
				}
				if r.URL.Path == "/redirect" {
					http.Redirect(w, r, "http://169.254.169.254", http.StatusTemporaryRedirect)
					return
				}
				io.WriteString(w, "verified")
			}))
			defer origin.Close()
			u, _ := url.Parse(origin.URL)
			proxy := opsLoopbackProxy(t, secureProxy, "127.0.0.1:443", u.Host, "", &tunnels)
			proxyURL, _ := url.Parse(proxy.URL)
			transport := origin.Client().Transport.(*http.Transport).Clone()
			if secureProxy {
				transport.TLSClientConfig.RootCAs.AddCert(proxy.Certificate())
			}
			transport.Proxy = http.ProxyURL(proxyURL)
			svc, _, _ := newService(t, &http.Client{Transport: transport, Timeout: time.Second})
			m := &opsModel{service: svc.(*service), lookupIP: func(context.Context, string) ([]net.IPAddr, error) {
				return []net.IPAddr{{IP: net.ParseIP("127.0.0.1")}}, nil
			}}
			client, cleanup, err := m.pinnedClient(ctx(), "https://example.com")
			if err != nil {
				t.Fatal(err)
			}
			defer cleanup()
			for _, path := range []string{"/v1/models", "/redirect"} {
				response, err := client.Get("https://example.com" + path)
				if err != nil {
					t.Fatal("verified origin unavailable", err)
				}
				response.Body.Close()
				if path == "/redirect" && response.StatusCode != http.StatusTemporaryRedirect {
					t.Fatal("provider redirect followed")
				}
			}
			if requests.Load() != 2 || tunnels.Load() != 2 {
				t.Fatal("redirect reached another destination")
			}
			bad, cleanupBad, err := m.pinnedClient(ctx(), "https://wrong.invalid")
			if err != nil {
				t.Fatal(err)
			}
			defer cleanupBad()
			if response, err := bad.Get("https://wrong.invalid/v1/models"); err == nil {
				response.Body.Close()
				t.Fatal("origin certificate mismatch accepted")
			}
			if secureProxy {
				badProxy := *proxyURL
				badProxy.Host = net.JoinHostPort("proxy.invalid", proxyURL.Port())
				transport.Proxy = http.ProxyURL(&badProxy)
				m.dialContext = func(c context.Context, network, address string) (net.Conn, error) {
					if address != badProxy.Host {
						return nil, fmt.Errorf("unexpected fixture route")
					}
					return (&net.Dialer{}).DialContext(c, network, proxyURL.Host)
				}
				bad, cleanupBad, err := m.pinnedClient(ctx(), "https://example.com")
				if err != nil {
					t.Fatal(err)
				}
				defer cleanupBad()
				if response, err := bad.Get("https://example.com/v1/models"); err == nil {
					response.Body.Close()
					t.Fatal("proxy certificate mismatch accepted")
				}
			}
		})
	}
}

func TestOpsProxyTimeoutCancellationAndSafeFailure(t *testing.T) {
	for _, mode := range []string{"timeout", "cancel", "reject", "redirect"} {
		t.Run(mode, func(t *testing.T) {
			entered := make(chan struct{})
			closed := make(chan struct{})
			proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				close(entered)
				if mode == "reject" || mode == "redirect" {
					w.Header().Set("Location", "http://169.254.169.254")
					status := "407 fixture-password"
					if mode == "redirect" {
						status = "307 fixture-password"
					}
					conn, _, _ := w.(http.Hijacker).Hijack()
					fmt.Fprintf(conn, "HTTP/1.1 %s\r\nContent-Length: 0\r\n\r\n", status)
					conn.Close()
					return
				}
				<-r.Context().Done()
				close(closed)
			}))
			defer proxy.Close()
			u, _ := url.Parse(proxy.URL)
			transport := http.DefaultTransport.(*http.Transport).Clone()
			transport.Proxy = http.ProxyURL(u)
			svc, _, _ := newService(t, &http.Client{Transport: transport, Timeout: 40 * time.Millisecond})
			c, cancel := context.WithCancel(ctx())
			defer cancel()
			if mode == "cancel" {
				go func() { <-entered; cancel() }()
			}
			start := time.Now()
			result, err := svc.Test(c, TestInput{Provider: strp(ProviderOpenAI), BaseURL: strp("http://127.0.0.1:1"), APIKey: strp("test-api-key")})
			if err != nil || result.OK || strings.Contains(result.Error, "fixture-password") || time.Since(start) > time.Second {
				t.Fatal("proxy failure was unbounded, successful or unsanitized")
			}
			if mode == "timeout" || mode == "cancel" {
				select {
				case <-closed:
				case <-time.After(time.Second):
					t.Fatal("stalled proxy connection was not closed")
				}
			}
		})
	}
}
