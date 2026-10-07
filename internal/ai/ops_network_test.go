package ai

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/huangchengsir/pipewright/internal/mask"
	"github.com/huangchengsir/pipewright/internal/opschat"
)

func TestOpsDNSPinnedDialProxyAndTLSHostname(t *testing.T) {
	for _, secure := range []bool{false, true} {
		t.Run(map[bool]string{false: "http", true: "https"}[secure], func(t *testing.T) {
			host := "provider.invalid"
			var requests atomic.Int32
			server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				if !strings.HasPrefix(r.Host, host+":") || (secure && r.TLS.ServerName != host) {
					t.Error("original HTTP/TLS identity lost", r.Host)
				}
				response := opsResponse(`{"text":"ok","actions":[]}`)
				defer response.Body.Close()
				_, _ = io.Copy(w, response.Body)
			}))
			if secure {
				server.StartTLS()
				host = server.Certificate().DNSNames[0]
			} else {
				server.Start()
			}
			defer server.Close()
			_, port, err := net.SplitHostPort(server.Listener.Addr().String())
			if err != nil {
				t.Fatal(err)
			}
			base, _ := url.Parse(server.URL)
			base.Host = net.JoinHostPort(host, port)
			client := server.Client()
			original := http.DefaultTransport.(*http.Transport).Clone()
			if secure {
				original = client.Transport.(*http.Transport).Clone()
				original.TLSClientConfig.ServerName = "wrong.invalid"
			}
			original.Proxy = func(*http.Request) (*url.URL, error) { t.Error("implicit proxy used"); return nil, errors.New("proxy") }
			original.DialContext = func(context.Context, string, string) (net.Conn, error) {
				t.Error("unpinned dial used")
				return nil, errors.New("dial")
			}
			original.DialTLSContext = original.DialContext
			client.Transport = original
			t.Setenv("HTTP_PROXY", "http://127.0.0.1:1")
			t.Setenv("HTTPS_PROXY", "http://127.0.0.1:1")
			svc, _, _ := newService(t, client)
			key := "provider-pinned-secret"
			if _, err := svc.Save(ctx(), SaveInput{Provider: ProviderOpenAI, BaseURL: base.String(), Model: "m", APIKey: &key, Enabled: true}); err != nil {
				t.Fatal(err)
			}
			m := NewOpsModel(svc, mask.NewMasker()).(*opsModel)
			lookups, dials := 0, 0
			addresses := []net.IPAddr{{IP: net.ParseIP("127.0.0.1")}}
			m.lookupIP = func(_ context.Context, name string) ([]net.IPAddr, error) {
				lookups++
				if name != host {
					t.Fatal(name)
				}
				return addresses, nil
			}
			m.dialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
				dials++
				// DNS now advertises metadata. No second lookup or aliased IP may reach it.
				addresses[0].IP = net.ParseIP("169.254.169.254")
				if address != net.JoinHostPort("127.0.0.1", port) {
					t.Fatal("rebound address dialed", address)
				}
				return (&net.Dialer{}).DialContext(ctx, network, address)
			}
			if _, err := m.Plan(ctx(), opsRequest()); err != nil {
				t.Fatal(err)
			}
			if lookups != 1 || dials != 1 || requests.Load() != 1 || client.Transport != original || original.Proxy == nil || (secure && original.TLSClientConfig.ServerName != "wrong.invalid") {
				t.Fatal("pinning changed legacy client or re-resolved", lookups, dials, requests.Load())
			}
			if _, err := m.Plan(ctx(), opsRequest()); err != opschat.ErrUnavailable || dials != 1 {
				t.Fatal("prohibited DNS answer connected", err, dials)
			}
		})
	}
}

func TestOpsResolverRejectsMixedAndEmptyAddresses(t *testing.T) {
	for _, addresses := range [][]net.IPAddr{
		nil, {{IP: net.ParseIP("127.0.0.1")}, {IP: net.ParseIP("169.254.169.254")}},
		{{IP: net.ParseIP("::")}}, {{IP: net.ParseIP("fe80::1")}}, {{IP: nil}},
	} {
		_, err := opsResolve(ctx(), "http://provider.invalid", func(context.Context, string) ([]net.IPAddr, error) { return addresses, nil })
		if err != opschat.ErrUnavailable {
			t.Fatal(addresses, err)
		}
	}
	d, err := opsResolve(ctx(), "https://provider.invalid", func(context.Context, string) ([]net.IPAddr, error) {
		return []net.IPAddr{{IP: net.ParseIP("::1")}, {IP: net.ParseIP("127.0.0.1")}}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	var called []string
	dial := d.dial(func(_ context.Context, _, address string) (net.Conn, error) {
		called = append(called, address)
		return nil, errors.New("deterministic dial failure")
	})
	_, _ = dial(ctx(), "tcp", "provider.invalid:443")
	if strings.Join(called, ",") != "[::1]:443,127.0.0.1:443" {
		t.Fatal(called)
	}
	_, _ = dial(ctx(), "tcp", "other.invalid:443")
	_, _ = dial(ctx(), "tcp", "provider.invalid:80")
	if len(called) != 2 {
		t.Fatal("unexpected destination bypassed pin", called)
	}
}
