package gitauth

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-git/go-billy/v5/memfs"
	git "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing/format/pktline"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/plumbing/protocol/packp"
	"github.com/go-git/go-git/v5/plumbing/transport"
	gitclient "github.com/go-git/go-git/v5/plumbing/transport/client"
	githttp "github.com/go-git/go-git/v5/plumbing/transport/http"
	"github.com/go-git/go-git/v5/plumbing/transport/server"
	"github.com/go-git/go-git/v5/storage/memory"
)

func TestParseLocalGiteaConfig(t *testing.T) {
	for _, origin := range []string{"http://localhost:3000/", "http://127.0.0.1", "https://[::1]:443"} {
		cfg, err := parseLocalGiteaConfig(origin, "org/my-project.git, org/another.repo.git")
		if err != nil {
			t.Fatalf("parse local origin %q: %v", origin, err)
		}
		if cfg.origin != strings.TrimSuffix(origin, "/") || len(cfg.repositories) != 2 || cfg.direct.Proxy != nil {
			t.Errorf("unexpected configuration for %q", origin)
		}
	}
	cfg, err := parseLocalGiteaConfig("", "")
	if err != nil || cfg.origin != "" {
		t.Fatalf("empty configuration should disable the connector: %v", err)
	}

	const validOrigin = "http://localhost:3000"
	const validRepository = "org/project.git"
	bad := []struct{ origin, repositories string }{
		{"", validRepository},
		{validOrigin, ""},
		{"http://192.168.1.2:3000", validRepository},
		{"http://git.example.test:3000", validRepository},
		{"http://0.0.0.0:3000", validRepository},
		{"http://localhost.:3000", validRepository},
		{"http://localhost:65536", validRepository},
		{"http://localhost:0", validRepository},
		{"http://localhost:", validRepository},
		{"http://[::1%25test]:3000", validRepository},
		{"ssh://localhost:3000", validRepository},
		{"http://fixture:synthetic-secret@localhost:3000", validRepository},
		{validOrigin + "?", validRepository},
		{validOrigin + "#", validRepository},
		{validOrigin + "/gitea", validRepository},
		{validOrigin, "*"},
		{validOrigin, "org/project"},
		{validOrigin, "org/project.git,"},
		{validOrigin, "/org/project.git"},
		{validOrigin, "org/sub/project.git"},
		{validOrigin, "org/../project.git"},
		{validOrigin, "org/project.git?x=1"},
		{validOrigin, "org/project.git#fragment"},
		{validOrigin, "org/project%2egit"},
		{validOrigin, "org/project..git"},
	}
	for _, tc := range bad {
		if _, err := parseLocalGiteaConfig(tc.origin, tc.repositories); err == nil {
			t.Errorf("accepted invalid config for %q and %q", tc.origin, tc.repositories)
		} else if strings.Contains(err.Error(), "synthetic-secret") {
			t.Error("configuration error exposed input credentials")
		}
	}
}

func TestLocalGiteaReadOnly(t *testing.T) {
	const path = "/org/project.git"
	const username = "fixture-user"
	const token = "synthetic-fixture-token"
	const mediaType = "application/x-git-upload-pack"

	storage := memory.NewStorage()
	repo, err := git.Init(storage, memfs.New())
	if err != nil {
		t.Fatal(err)
	}
	wt, err := repo.Worktree()
	if err != nil {
		t.Fatal(err)
	}
	commit, err := wt.Commit("fixture", &git.CommitOptions{
		AllowEmptyCommits: true,
		Author:            &object.Signature{Name: "Fixture", Email: "fixture@example.test", When: time.Unix(1, 0)},
	})
	if err != nil {
		t.Fatal(err)
	}
	gitServer := server.NewServer(server.MapLoader{path: storage})
	var requests, reads, fetches atomic.Int32
	var status atomic.Int32
	var postStatus atomic.Int32
	fixture := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if !strings.HasPrefix(r.Host, "localhost:") {
			t.Errorf("unexpected fixture authority %q", r.Host)
		}
		user, password, _ := r.BasicAuth()
		if user != username || password != token {
			t.Error("Git Basic authentication did not reach the fixture")
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if code := int(status.Load()); code != 0 {
			if code == http.StatusFound {
				// A same-origin redirect is also forbidden.
				w.Header().Set("Location", path+"/info/refs?service=git-upload-pack&other="+token)
			}
			w.WriteHeader(code)
			_, _ = io.WriteString(w, token)
			return
		}

		session, err := gitServer.NewUploadPackSession(&transport.Endpoint{Path: path}, nil)
		if err != nil {
			t.Errorf("fixture session: %v", err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		defer session.Close()

		switch {
		case r.Method == http.MethodGet && r.URL.Path == path+"/info/refs" && r.URL.RawQuery == "service=git-upload-pack":
			reads.Add(1)
			w.Header().Set("Content-Type", mediaType+"-advertisement")
			refs, err := session.AdvertisedReferencesContext(r.Context())
			if err != nil {
				t.Errorf("fixture refs: %v", err)
				return
			}
			refs.Prefix = [][]byte{[]byte("# service=git-upload-pack"), pktline.Flush}
			if err := refs.Encode(w); err != nil {
				t.Errorf("fixture refs encoding: %v", err)
			}
		case r.Method == http.MethodPost && r.URL.Path == path+"/git-upload-pack" && r.URL.RawQuery == "" &&
			r.Header.Get("Content-Type") == mediaType+"-request":
			fetches.Add(1)
			if code := int(postStatus.Load()); code != 0 {
				w.WriteHeader(code)
				_, _ = io.WriteString(w, token)
				return
			}
			request := packp.NewUploadPackRequest()
			if err := request.UploadRequest.Decode(r.Body); err != nil {
				t.Errorf("fixture upload request: %v", err)
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			response, err := session.UploadPack(r.Context(), request)
			if err != nil {
				t.Errorf("fixture upload response: %v", err)
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			w.Header().Set("Content-Type", mediaType+"-result")
			if err := response.Encode(w); err != nil {
				t.Errorf("fixture pack encoding: %v", err)
			}
		default:
			t.Errorf("unexpected fixture request %s %q", r.Method, r.URL)
			w.WriteHeader(http.StatusBadRequest)
		}
	}))
	defer fixture.Close()

	origin := strings.Replace(fixture.URL, "127.0.0.1", "localhost", 1)
	raw := origin + path
	if _, err := ResolveRepoURL(raw); !errors.Is(err, ErrRepoAddressDenied) {
		t.Fatalf("disabled connector resolved local URL: %v", err)
	}
	if err := ConfigureLocalGitea(origin, "org/project.git,org/another.git"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cfg := localGitea.Swap(nil)
		cfg.direct.CloseIdleConnections()
	})
	if err := ConfigureLocalGitea("", ""); err == nil {
		t.Fatal("deployment configuration was changed after startup")
	}

	resolved, err := ResolveRepoURL(" " + raw + " ")
	if err != nil || !strings.HasPrefix(resolved, localGiteaScheme+"://") {
		t.Fatalf("registered address resolved to %q: %v", resolved, err)
	}
	if AllowedRepoURL(raw) {
		t.Fatal("shared Git URL policy allowed a loopback address")
	}
	for _, ordinary := range []string{"https://192.168.1.2/org/project.git", "git@192.168.1.2:org/project.git"} {
		if got, err := ResolveRepoURL(ordinary); err != nil || got != ordinary {
			t.Errorf("ordinary repository %q changed to %q: %v", ordinary, got, err)
		}
	}
	for _, denied := range []string{
		resolved,
		fixture.URL + path,
		origin + "/org/arbitrary.git",
		origin + "/org/Project.git",
		raw + "/",
		raw + "?",
		raw + "#",
		raw + "?service=git-upload-pack",
		origin + "/org/%70roject.git",
		"ssh://git@127.0.0.1/org/project.git",
	} {
		if _, err := ResolveRepoURL(denied); !errors.Is(err, ErrRepoAddressDenied) {
			t.Errorf("resolved out-of-policy URL %q: %v", denied, err)
		}
	}

	ep, err := transport.NewEndpoint(resolved)
	if err != nil {
		t.Fatal(err)
	}
	client, err := gitclient.NewClient(ep)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.NewReceivePackSession(ep, nil); !errors.Is(err, ErrRepoAddressDenied) {
		t.Errorf("local transport allowed receive-pack: %v", err)
	}
	mutated := *ep
	mutated.Path = "/http/org/arbitrary.git"
	if _, err := client.NewUploadPackSession(&mutated, nil); !errors.Is(err, ErrRepoAddressDenied) {
		t.Errorf("local transport allowed direct access to another repository: %v", err)
	}
	mutated = *ep
	mutated.InsecureSkipTLS = true
	if _, err := client.NewUploadPackSession(&mutated, nil); !errors.Is(err, ErrRepoAddressDenied) {
		t.Errorf("local transport allowed a caller TLS override: %v", err)
	}
	// Each HTTP session is tied to one registered repository and Git service.
	authority := strings.TrimPrefix(origin, "http://")
	guard := localGiteaRoundTripper{
		direct: localGitea.Load().direct, scheme: "http", host: authority, authority: authority, path: path,
	}
	for _, tc := range []struct{ method, route string }{
		{http.MethodGet, path + "/info/refs?service=git-receive-pack"},
		{http.MethodGet, "/org/another.git/info/refs?service=git-upload-pack"},
		{http.MethodGet, path + "/info/refs?service=git-upload-pack&extra=1"},
		{http.MethodPost, path + "/git-upload-pack"},
		{http.MethodGet, path + "/HEAD"},
	} {
		request, err := http.NewRequest(tc.method, origin+tc.route, nil)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := guard.RoundTrip(request); !errors.Is(err, ErrRepoAddressDenied) {
			t.Errorf("HTTP guard accepted %s %s: %v", tc.method, tc.route, err)
		}
	}
	if got := requests.Load(); got != 0 {
		t.Errorf("out-of-policy Git inputs caused %d requests", got)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	auth, err := AuthMethod(raw, username, token)
	if err != nil {
		t.Fatal(err)
	}
	remote := git.NewRemote(memory.NewStorage(), &config.RemoteConfig{Name: "origin", URLs: []string{resolved}})
	if refs, err := remote.ListContext(ctx, &git.ListOptions{Auth: auth}); err != nil || len(refs) == 0 {
		t.Fatalf("list registered repository: refs=%v, error=%v", refs, err)
	}
	cloned, err := git.CloneContext(ctx, memory.NewStorage(), memfs.New(), &git.CloneOptions{URL: resolved, Auth: auth})
	if err != nil {
		t.Fatal(err)
	}
	head, err := cloned.Head()
	if err != nil || head.Hash() != commit {
		t.Fatalf("cloned head=%v, wanted commit %s, error=%v", head, commit, err)
	}
	if got := reads.Load(); got != 2 {
		t.Errorf("advertisements = %d, want 2", got)
	}
	if got := fetches.Load(); got != 1 {
		t.Errorf("upload-pack requests = %d, want 1", got)
	}

	for _, tc := range []struct {
		code int
		want error
	}{
		{http.StatusUnauthorized, transport.ErrAuthenticationRequired},
		{http.StatusForbidden, transport.ErrAuthorizationFailed},
		{http.StatusNotFound, transport.ErrRepositoryNotFound},
		{http.StatusFound, errLocalGiteaUnavailable},
		{http.StatusBadGateway, errLocalGiteaUnavailable},
		{http.StatusOK, errLocalGiteaUnavailable},
	} {
		t.Run(fmt.Sprint(tc.code), func(t *testing.T) {
			status.Store(int32(tc.code))
			before := requests.Load()
			_, err := remote.ListContext(ctx, &git.ListOptions{Auth: auth})
			if !errors.Is(err, tc.want) || strings.Contains(err.Error(), token) {
				t.Errorf("list error = %v, want sanitized %v", err, tc.want)
			}
			if got := requests.Load() - before; got != 1 {
				t.Errorf("sent %d requests for response status %d, want 1", got, tc.code)
			}
		})
	}

	status.Store(0)
	postStatus.Store(http.StatusUnauthorized)
	_, err = git.CloneContext(ctx, memory.NewStorage(), memfs.New(), &git.CloneOptions{
		URL: resolved, Auth: &githttp.BasicAuth{Username: username, Password: token},
	})
	if !errors.Is(err, transport.ErrAuthenticationRequired) || strings.Contains(err.Error(), token) {
		t.Errorf("upload-pack authentication error = %v", err)
	}

	fixture.Close()
	_, err = remote.ListContext(ctx, &git.ListOptions{Auth: auth})
	if !errors.Is(err, errLocalGiteaUnavailable) {
		t.Errorf("unavailable listener error = %v", err)
	}
}

func TestLocalGiteaHTTPSAndIPv6(t *testing.T) {
	for _, name := range []string{"https", "ipv6"} {
		t.Run(name, func(t *testing.T) {
			fixture := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				w.WriteHeader(http.StatusUnauthorized)
			}))
			if name == "ipv6" {
				_ = fixture.Listener.Close()
				listener, err := net.Listen("tcp6", "[::1]:0")
				if err != nil {
					t.Skipf("IPv6 loopback unavailable: %v", err)
				}
				fixture.Listener = listener
				fixture.Start()
			} else {
				fixture.StartTLS()
			}
			defer fixture.Close()

			const repo = "org/project.git"
			if err := ConfigureLocalGitea(fixture.URL, repo); err != nil {
				t.Fatal(err)
			}
			cfg := localGitea.Load()
			defer func() {
				cfg.direct.CloseIdleConnections()
				localGitea.Store(nil)
			}()
			address, err := ResolveRepoURL(fixture.URL + "/" + repo)
			if err != nil {
				t.Fatal(err)
			}
			remote := git.NewRemote(memory.NewStorage(), &config.RemoteConfig{Name: "origin", URLs: []string{address}})
			probe := func() error {
				_, err := remote.ListContext(context.Background(), &git.ListOptions{})
				return err
			}
			if name == "https" {
				if err := probe(); !errors.Is(err, errLocalGiteaUnavailable) {
					t.Fatalf("untrusted TLS error = %v", err)
				}
				roots := x509.NewCertPool()
				roots.AddCert(fixture.Certificate())
				cfg.direct.TLSClientConfig = &tls.Config{RootCAs: roots}
			}
			if err := probe(); !errors.Is(err, transport.ErrAuthenticationRequired) {
				t.Fatalf("Git fixture error = %v, want authentication required", err)
			}
		})
	}
}
