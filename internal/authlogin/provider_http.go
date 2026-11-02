package authlogin

import (
	"context"
	"crypto/tls"
	"io"
	"net/http"
	"time"
)

const (
	providerTimeout = 15 * time.Second
	maxProviderBody = 1 << 20
)

// providerHTTP restricts all requests, including requests made by OAuth libraries.
// Redirects, token POST replay, implicit cookie sharing and unbounded bodies are
// disabled. A caller-supplied transport is a trusted in-process test/integration seam.
func providerHTTP(base *http.Client) *http.Client {
	client := http.Client{}
	if base != nil {
		client = *base
	}
	if client.Timeout <= 0 || client.Timeout > providerTimeout {
		client.Timeout = providerTimeout
	}
	client.Jar = nil
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	transport := client.Transport
	if transport == nil {
		transport = http.DefaultTransport
	}
	if t, ok := transport.(*http.Transport); ok {
		copy := t.Clone()
		if copy.TLSClientConfig == nil {
			copy.TLSClientConfig = &tls.Config{}
		} else {
			copy.TLSClientConfig = copy.TLSClientConfig.Clone()
		}
		copy.TLSClientConfig.InsecureSkipVerify = false
		if copy.TLSClientConfig.MinVersion < tls.VersionTLS12 {
			copy.TLSClientConfig.MinVersion = tls.VersionTLS12
		}
		transport = copy
	}
	client.Transport = &providerTransport{base: transport}
	return &client
}

type providerTransport struct{ base http.RoundTripper }

func (t *providerTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if !trustedProviderURL(req) {
		return nil, ErrProviderUnavailable
	}
	copy := req.Clone(req.Context())
	copy.Header = req.Header.Clone()
	// net/http can replay a request on a stale reused connection when GetBody
	// exists, even before it knows whether the peer consumed a code.
	if copy.Method == http.MethodPost {
		copy.GetBody = nil
		copy.Close = true
	}
	resp, err := t.base.RoundTrip(copy)
	if err != nil {
		return nil, ErrResultUncertain
	}
	if resp == nil || resp.Body == nil {
		return nil, ErrResultUncertain
	}
	if resp.ContentLength > maxProviderBody {
		resp.Body.Close()
		return nil, ErrProviderRejected
	}
	resp.Body = &boundedProviderBody{ReadCloser: resp.Body, remaining: maxProviderBody}
	return resp, nil
}

func trustedProviderURL(req *http.Request) bool {
	u := req.URL
	if u == nil || u.Scheme != "https" || u.User != nil || u.Port() != "" ||
		u.RawQuery != "" || u.Fragment != "" || u.RawPath != "" {
		return false
	}
	if req.Method == http.MethodPost {
		switch u.Host {
		case "github.com":
			return u.Path == "/login/oauth/access_token"
		case "gitlab.com", "gitee.com":
			return u.Path == "/oauth/token"
		case "oauth2.googleapis.com":
			return u.Path == "/token"
		case "login.microsoftonline.com":
			return validMicrosoftTokenPath(u.Path)
		}
	}
	if req.Method == http.MethodGet {
		switch u.Host {
		case "api.github.com":
			return u.Path == "/user"
		case "gitlab.com":
			return u.Path == "/api/v4/user"
		case "gitee.com":
			return u.Path == "/api/v5/user"
		case "www.googleapis.com":
			return u.Path == "/oauth2/v3/certs"
		case "login.microsoftonline.com":
			return u.Path == "/common/discovery/v2.0/keys"
		}
	}
	return false
}

type boundedProviderBody struct {
	io.ReadCloser
	remaining int64
}

func (b *boundedProviderBody) Read(p []byte) (int, error) {
	if b.remaining < 0 {
		return 0, ErrProviderRejected
	}
	if int64(len(p)) > b.remaining+1 {
		p = p[:b.remaining+1]
	}
	n, err := b.ReadCloser.Read(p)
	b.remaining -= int64(n)
	if b.remaining < 0 {
		return n, ErrProviderRejected
	}
	return n, err
}

func requestContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, providerTimeout)
}
