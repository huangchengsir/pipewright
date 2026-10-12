package oauth

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestIssue188ExplicitGiteaAndLegacyGitLab(t *testing.T) {
	for _, prefix := range []string{"", "/gitea", "/nested/gitea"} {
		gitea, err := resolveProvider(ProviderGitea, "https://git.example"+prefix+"/")
		if err != nil || gitea.AuthorizeURL != "https://git.example"+prefix+"/login/oauth/authorize" || gitea.TokenURL != "https://git.example"+prefix+"/login/oauth/access_token" || gitea.UserURL != "https://git.example"+prefix+"/api/v1/user" || gitea.scope() != "read:user read:repository" {
			t.Fatalf("Gitea: %+v %v", gitea, err)
		}
		gitlab, err := resolveProvider(ProviderCustom, "https://git.example"+prefix+"/")
		if err != nil || gitlab.AuthorizeURL != "https://git.example"+prefix+"/oauth/authorize" || gitlab.UserURL != "https://git.example"+prefix+"/api/v4/user" {
			t.Fatalf("legacy: %+v %v", gitlab, err)
		}
	}
}

func TestIssue188InvalidProviderDestinations(t *testing.T) {
	for _, base := range []string{"", "/gitea", "file:///etc/passwd", "javascript:alert(1)", "https://user:secret@git.example", "https://git.example?token=secret", "https://git.example#secret", "https://git.example?", "http://[fe80::1%25eth0]"} {
		for _, provider := range []string{ProviderCustom, ProviderGitea} {
			if _, err := resolveProvider(provider, base); !errors.Is(err, ErrBaseURLRequired) {
				t.Fatalf("accepted %q: %v", base, err)
			}
		}
	}
	for _, host := range []string{"169.254.169.254", "0.0.0.0", "::", "fe80::1", "224.0.0.1", "ff02::1"} {
		lookup := func(context.Context, string) ([]net.IPAddr, error) { return []net.IPAddr{{IP: net.ParseIP(host)}}, nil }
		if _, err := resolveDestination(context.Background(), "https://git.example", lookup); !errors.Is(err, ErrExchangeFailed) {
			t.Fatalf("accepted DNS address %s", host)
		}
	}
}

func TestIssue188DNSPinningRejectsMixedAnswers(t *testing.T) {
	lookupCalls := 0
	lookup := func(context.Context, string) ([]net.IPAddr, error) {
		lookupCalls++
		return []net.IPAddr{{IP: net.ParseIP("192.168.1.10")}}, nil
	}
	d, err := resolveDestination(context.Background(), "https://git.example/gitea", lookup)
	if err != nil {
		t.Fatal(err)
	}
	var dialed string
	dial := d.dial(func(_ context.Context, _, address string) (net.Conn, error) {
		dialed = address
		return nil, errors.New("fixture failure")
	})
	dial(context.Background(), "tcp", "git.example:443")
	if lookupCalls != 1 || dialed != "192.168.1.10:443" {
		t.Fatalf("unpinned destination: calls=%d dial=%s", lookupCalls, dialed)
	}
	dialed = ""
	dial(context.Background(), "tcp", "other.example:443")
	if dialed != "" {
		t.Fatal("dialed different host")
	}
	_, err = resolveDestination(context.Background(), "https://git.example", func(context.Context, string) ([]net.IPAddr, error) {
		return []net.IPAddr{{IP: net.ParseIP("192.168.1.10")}, {IP: net.ParseIP("169.254.169.254")}}, nil
	})
	if !errors.Is(err, ErrExchangeFailed) {
		t.Fatal("accepted mixed safe/blocked answers")
	}
}

func TestIssue188NeverFollowsTokenOrUserRedirects(t *testing.T) {
	for _, stage := range []string{"token", "user"} {
		t.Run(stage, func(t *testing.T) {
			leaks := 0
			sink := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				leaks++
				io.WriteString(w, `{"access_token":"leaked","login":"leaked"}`)
			}))
			defer sink.Close()
			provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if stage == "token" || r.URL.Path == "/api/v1/user" {
					http.Redirect(w, r, sink.URL, http.StatusTemporaryRedirect)
					return
				}
				io.WriteString(w, `{"access_token":"synthetic-token"}`)
			}))
			defer provider.Close()
			svc, _, _, _ := newService(t, provider.Client())
			_, err := svc.SaveApp(ctx(), SaveAppInput{Provider: ProviderGitea, ClientID: "client", ClientSecret: strp("secret"), BaseURL: provider.URL, Enabled: true})
			if err != nil {
				t.Fatal(err)
			}
			_, err = svc.Exchange(ctx(), ProviderGitea, "code", "https://app.example/callback")
			if !errors.Is(err, ErrExchangeFailed) || leaks != 0 {
				t.Fatalf("redirect followed: leaks=%d err=%v", leaks, err)
			}
		})
	}
}

type oauthRoundTrip func(*http.Request) (*http.Response, error)

func (f oauthRoundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type oauthFailedBody struct{}

func (oauthFailedBody) Read([]byte) (int, error) { return 0, errors.New("secret-token-provider-error") }
func (oauthFailedBody) Close() error             { return nil }

func TestIssue188BoundedSanitizedProviderFailures(t *testing.T) {
	for _, body := range []io.ReadCloser{
		io.NopCloser(strings.NewReader(`{"access_token":"token","error":"secret-token-provider-error"}`)),
		io.NopCloser(strings.NewReader(strings.Repeat(" ", (1<<16)+1))),
		oauthFailedBody{},
	} {
		client := &http.Client{Transport: oauthRoundTrip(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: 200, Header: make(http.Header), Body: body}, nil
		})}
		svc, _, _, _ := newService(t, client)
		_, err := svc.SaveApp(ctx(), SaveAppInput{Provider: ProviderGitea, ClientID: "id", ClientSecret: strp("secret"), BaseURL: "http://192.168.1.10", Enabled: true})
		if err != nil {
			t.Fatal(err)
		}
		_, err = svc.Exchange(ctx(), ProviderGitea, "code", "https://app.example/callback")
		if !errors.Is(err, ErrExchangeFailed) || strings.Contains(err.Error(), "secret-token") {
			t.Fatalf("unsafe failure: %v", err)
		}
	}
}

func TestIssue188SecretRetentionAndDisabledExchange(t *testing.T) {
	calls := 0
	client := &http.Client{Transport: oauthRoundTrip(func(*http.Request) (*http.Response, error) {
		calls++
		return nil, errors.New("must not be called")
	})}
	svc, _, _, _ := newService(t, client)
	in := SaveAppInput{Provider: ProviderGitea, ClientID: "id", ClientSecret: strp("synthetic-secret"), BaseURL: "https://192.168.1.10/gitea", Enabled: true}
	if _, err := svc.SaveApp(ctx(), in); err != nil {
		t.Fatal(err)
	}
	in.ClientSecret = nil
	in.BaseURL += "/"
	if _, err := svc.SaveApp(ctx(), in); err != nil {
		t.Fatalf("same destination should retain secret: %v", err)
	}
	in.BaseURL = "https://192.168.1.11/gitea"
	if _, err := svc.SaveApp(ctx(), in); !errors.Is(err, ErrClientSecretRequired) {
		t.Fatalf("carried secret to new destination: %v", err)
	}
	in.BaseURL = "https://192.168.1.10/gitea"
	in.Enabled = false
	if _, err := svc.SaveApp(ctx(), in); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Exchange(ctx(), ProviderGitea, "code", "https://app.example/callback"); !errors.Is(err, ErrAppNotConfigured) || calls != 0 {
		t.Fatalf("disabled app exchanged: calls=%d err=%v", calls, err)
	}
}

func TestIssue188TLSSubpathExchange(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/gitea/login/oauth/access_token":
			io.WriteString(w, `{"access_token":"synthetic-token"}`)
		case "/gitea/api/v1/user":
			io.WriteString(w, `{"login":"alice"}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	client := server.Client()
	client.CheckRedirect = func(*http.Request, []*http.Request) error {
		t.Error("injected redirect hook should never be used")
		return nil
	}
	svc, _, _, _ := newService(t, client)
	_, err := svc.SaveApp(ctx(), SaveAppInput{Provider: ProviderGitea, ClientID: "id", ClientSecret: strp("secret"), BaseURL: server.URL + "/gitea", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	result, err := svc.Exchange(ctx(), ProviderGitea, "code", "https://app.example/callback")
	if err != nil || result.Login != "alice" {
		t.Fatalf("TLS exchange: %v", err)
	}
}
