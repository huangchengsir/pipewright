package gitauth

import (
	"context"
	"errors"
	"fmt"
	"mime"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/go-git/go-git/v5/plumbing/protocol/packp"
	"github.com/go-git/go-git/v5/plumbing/transport"
	gitclient "github.com/go-git/go-git/v5/plumbing/transport/client"
	githttp "github.com/go-git/go-git/v5/plumbing/transport/http"
)

const localGiteaScheme = "pipewright-local-gitea"

var (
	// ErrRepoAddressDenied indicates that a Git address failed repository policy.
	ErrRepoAddressDenied     = errors.New("git: repository address denied by policy")
	errLocalGiteaUnavailable = errors.New("git: local Gitea read failed")

	localGitea        atomic.Pointer[localGiteaConfig]
	localGiteaSegment = regexp.MustCompile("^[A-Za-z0-9_.-]+$")
)

type localGiteaConfig struct {
	origin       string
	authority    string
	endpoint     transport.Endpoint
	repositories map[string]struct{}
	direct       *http.Transport
}

func init() {
	// go-git's registry is a map. Install the private scheme before Git calls
	// can run, and publish the immutable deployment configuration separately.
	gitclient.InstallProtocol(localGiteaScheme, localGiteaTransport{})
}

// ConfigureLocalGitea is called once at startup. Empty origin and repositories
// leave the connector disabled. Repositories are comma-separated owner/repo.git
// paths for a single HTTP(S) loopback origin.
func ConfigureLocalGitea(origin, repositories string) error {
	cfg, err := parseLocalGiteaConfig(strings.TrimSpace(origin), strings.TrimSpace(repositories))
	if err != nil {
		return err
	}
	if !localGitea.CompareAndSwap(nil, cfg) {
		return errors.New("git: local Gitea is already configured")
	}
	return nil
}

// LocalGiteaEnabled reports whether the administrator configured local Git access.
func LocalGiteaEnabled() bool {
	cfg := localGitea.Load()
	return cfg != nil && cfg.origin != ""
}

// ResolveRepoURL validates an external repository URL. A registered local Gitea
// URL resolves to the dedicated Git transport. All other addresses use the
// shared repository URL policy.
func ResolveRepoURL(repoURL string) (string, error) {
	repoURL = strings.TrimSpace(repoURL)
	if cfg := localGitea.Load(); cfg != nil && cfg.origin != "" {
		if path, ok := strings.CutPrefix(repoURL, cfg.origin); ok {
			if _, registered := cfg.repositories[path]; registered {
				return localGiteaScheme + "://" + cfg.authority + "/" + cfg.endpoint.Protocol + path, nil
			}
		}
	}
	if IsLocalGitAddress(repoURL) {
		return "", ErrRepoAddressDenied
	}
	if !AllowedRepoURL(repoURL) {
		return "", ErrRepoAddressDenied
	}
	return repoURL, nil
}

// IsLocalGitAddress identifies literal loopback and the private transport
// without resolving DNS. Local addresses must not use the shared mirror.
func IsLocalGitAddress(repoURL string) bool {
	endpoint, err := transport.NewEndpoint(strings.TrimSpace(repoURL))
	if err != nil {
		return false
	}
	if endpoint.Protocol == localGiteaScheme {
		return true
	}
	host := strings.Trim(endpoint.Host, "[]")
	if strings.EqualFold(strings.TrimSuffix(host, "."), "localhost") {
		return true
	}
	ip, err := netip.ParseAddr(host)
	return err == nil && (ip.Unmap().IsLoopback() || ip.IsUnspecified())
}

func parseLocalGiteaConfig(origin, repositories string) (*localGiteaConfig, error) {
	if origin == "" && repositories == "" {
		return &localGiteaConfig{}, nil
	}
	if origin == "" || repositories == "" {
		return nil, errors.New("git: local Gitea origin and repository list must both be set")
	}

	u, err := url.Parse(origin)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") ||
		(u.Path != "" && u.Path != "/") || strings.TrimSuffix(origin, "/") != u.Scheme+"://"+u.Host {
		return nil, errors.New("git: local Gitea needs an HTTP(S) origin without credentials, path, or query")
	}

	host := u.Hostname()
	dialHost := "127.0.0.1"
	endpointHost := host
	if host != "localhost" {
		ip, err := netip.ParseAddr(host)
		if err != nil || !ip.IsLoopback() || ip.String() != host {
			return nil, errors.New("git: local Gitea origin must use localhost or a loopback IP")
		}
		dialHost = host
		if ip.Is6() {
			endpointHost = "[" + host + "]"
		}
	}

	port := u.Port()
	endpointPort := 0
	if port != "" {
		endpointPort, err = strconv.Atoi(port)
		if err != nil || endpointPort < 1 || endpointPort > 65535 || strconv.Itoa(endpointPort) != port ||
			u.Host != net.JoinHostPort(host, port) {
			return nil, errors.New("git: local Gitea origin has an invalid port")
		}
	} else {
		if u.Host != endpointHost {
			return nil, errors.New("git: local Gitea origin has an invalid authority")
		}
		port = "80"
		if u.Scheme == "https" {
			port = "443"
		}
	}

	registered := make(map[string]struct{})
	for index, entry := range strings.Split(repositories, ",") {
		entry = strings.TrimSpace(entry)
		owner, repo, ok := strings.Cut(entry, "/")
		name, gitSuffix := strings.CutSuffix(repo, ".git")
		if !ok || !gitSuffix || !localGiteaSegment.MatchString(owner) || !localGiteaSegment.MatchString(name) ||
			strings.HasSuffix(owner, ".") || strings.HasSuffix(name, ".") {
			return nil, fmt.Errorf("git: local Gitea repository entry %d must be an owner/repo.git path", index+1)
		}
		registered["/"+entry] = struct{}{}
	}

	// A localhost origin pins the TCP connection to IPv4 loopback. Use an
	// explicit [::1] origin for an IPv6-only listener. Keep the configured
	// authority for the HTTP Host header and normal TLS certificate checks.
	dialer := &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}
	address := net.JoinHostPort(dialHost, port)
	direct := &http.Transport{
		DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
			return dialer.DialContext(ctx, network, address)
		},
		ForceAttemptHTTP2:     true,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 30 * time.Second,
		IdleConnTimeout:       90 * time.Second,
		MaxIdleConnsPerHost:   4,
	}
	return &localGiteaConfig{
		origin:       u.Scheme + "://" + u.Host,
		authority:    u.Host,
		endpoint:     transport.Endpoint{Protocol: u.Scheme, Host: endpointHost, Port: endpointPort},
		repositories: registered,
		direct:       direct,
	}, nil
}

type localGiteaTransport struct{}

func (localGiteaTransport) NewUploadPackSession(ep *transport.Endpoint, auth transport.AuthMethod) (transport.UploadPackSession, error) {
	cfg := localGitea.Load()
	if cfg == nil || cfg.origin == "" || ep == nil || ep.Protocol != localGiteaScheme ||
		ep.User != "" || ep.Password != "" || ep.InsecureSkipTLS ||
		len(ep.ClientCert) != 0 || len(ep.ClientKey) != 0 || len(ep.CaBundle) != 0 ||
		ep.Proxy != (transport.ProxyOptions{}) {
		return nil, ErrRepoAddressDenied
	}
	path, ok := strings.CutPrefix(ep.Path, "/"+cfg.endpoint.Protocol)
	if _, registered := cfg.repositories[path]; !ok || !registered {
		return nil, ErrRepoAddressDenied
	}

	// Build the HTTP endpoint from the deployment configuration after
	// verifying that the internal URL refers to the configured origin.
	if ep.Host != cfg.endpoint.Host || ep.Port != cfg.endpoint.Port {
		return nil, ErrRepoAddressDenied
	}
	if _, ok := auth.(*githttp.BasicAuth); auth != nil && !ok {
		return nil, transport.ErrInvalidAuthMethod
	}

	repo := cfg.endpoint
	repo.Path = path
	httpURL, err := url.Parse(repo.String())
	if err != nil {
		return nil, errLocalGiteaUnavailable
	}
	client := githttp.NewClient(&http.Client{
		Transport: localGiteaRoundTripper{
			direct: cfg.direct, scheme: httpURL.Scheme, host: httpURL.Host, authority: cfg.authority, path: path,
		},
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return errLocalGiteaUnavailable
		},
	})
	session, err := client.NewUploadPackSession(&repo, auth)
	if err != nil {
		return nil, localGiteaError(context.Background(), err)
	}
	return localGiteaSession{session}, nil
}

func (localGiteaTransport) NewReceivePackSession(*transport.Endpoint, transport.AuthMethod) (transport.ReceivePackSession, error) {
	return nil, ErrRepoAddressDenied
}

type localGiteaRoundTripper struct {
	direct    *http.Transport
	scheme    string
	host      string
	authority string
	path      string
}

func (t localGiteaRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	const service = transport.UploadPackServiceName
	const media = "application/x-" + service
	u := req.URL
	var expectedMedia string
	switch {
	case req.Method == http.MethodGet && u.Path == t.path+"/info/refs" &&
		u.RawQuery == "service="+service && req.Body == nil:
		expectedMedia = media + "-advertisement"
	case req.Method == http.MethodPost && u.Path == t.path+"/"+service && u.RawQuery == "" &&
		req.Body != nil && req.Header.Get("Content-Type") == media+"-request":
		expectedMedia = media + "-result"
	}
	if expectedMedia == "" || u.Scheme != t.scheme || u.Host != t.host || req.Host != t.host ||
		u.User != nil || u.RawPath != "" || u.Fragment != "" || u.ForceQuery || u.Opaque != "" {
		if req.Body != nil {
			_ = req.Body.Close()
		}
		return nil, ErrRepoAddressDenied
	}

	wire := *req
	wire.Host = t.authority
	res, err := t.direct.RoundTrip(&wire)
	if err != nil {
		return nil, localGiteaError(req.Context(), err)
	}
	mediaType, _, _ := mime.ParseMediaType(res.Header.Get("Content-Type"))
	if res.StatusCode == http.StatusOK && mediaType == expectedMedia {
		return res, nil
	}
	_ = res.Body.Close()
	switch res.StatusCode {
	case http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound:
		// Preserve go-git's status classification without giving it an
		// untrusted error body or response headers to relay to callers.
		return &http.Response{StatusCode: res.StatusCode, Body: http.NoBody, Request: req}, nil
	default:
		return nil, errLocalGiteaUnavailable
	}
}

// go-git can include server-supplied packet data in Git protocol errors. Return
// only the errors callers need to classify, with context cancellation intact.
type localGiteaSession struct{ transport.UploadPackSession }

func (s localGiteaSession) AdvertisedReferences() (*packp.AdvRefs, error) {
	return s.AdvertisedReferencesContext(context.Background())
}

func (s localGiteaSession) AdvertisedReferencesContext(ctx context.Context) (*packp.AdvRefs, error) {
	refs, err := s.UploadPackSession.AdvertisedReferencesContext(ctx)
	return refs, localGiteaError(ctx, err)
}

func (s localGiteaSession) UploadPack(ctx context.Context, req *packp.UploadPackRequest) (*packp.UploadPackResponse, error) {
	res, err := s.UploadPackSession.UploadPack(ctx, req)
	return res, localGiteaError(ctx, err)
}

func localGiteaError(ctx context.Context, err error) error {
	switch {
	case err == nil:
		return nil
	case ctx.Err() != nil:
		return ctx.Err()
	case errors.Is(err, transport.ErrAuthenticationRequired):
		return transport.ErrAuthenticationRequired
	case errors.Is(err, transport.ErrAuthorizationFailed):
		return transport.ErrAuthorizationFailed
	case errors.Is(err, transport.ErrRepositoryNotFound):
		return transport.ErrRepositoryNotFound
	case errors.Is(err, transport.ErrEmptyRemoteRepository):
		return transport.ErrEmptyRemoteRepository
	case errors.Is(err, transport.ErrEmptyUploadPackRequest):
		return transport.ErrEmptyUploadPackRequest
	case errors.Is(err, transport.ErrInvalidAuthMethod):
		return transport.ErrInvalidAuthMethod
	default:
		return errLocalGiteaUnavailable
	}
}
