package releasesource

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"github.com/huangchengsir/pipewright/internal/store"
	"github.com/huangchengsir/pipewright/internal/storetest"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
)

func validMarker(tag string) (Marker, []byte) {
	m := Marker{SchemaVersion: 1, Tag: tag, Commit: strings.Repeat("a", 40), Assets: map[string]string{}}
	var sums strings.Builder
	for _, name := range AssetNames(tag) {
		digest := sha256.Sum256([]byte(name))
		m.Assets[name] = hex.EncodeToString(digest[:])
		fmt.Fprintf(&sums, "%x  %s\n", digest, name)
	}
	data := []byte(sums.String())
	sum := sha256.Sum256(data)
	m.ChecksumsSHA256 = hex.EncodeToString(sum[:])
	return m, data
}
func TestMarkerRequiresCompleteExactRelease(t *testing.T) {
	m, sums := validMarker("v2.2.0")
	raw, _ := json.Marshal(m)
	if parsed, e := ParseMarker(raw, m.Tag, m.Commit); e != nil || parsed.VerifyChecksums(sums) != nil {
		t.Fatal(e)
	}
	cases := []struct {
		name   string
		mutate func(*Marker)
	}{
		{"schema", func(m *Marker) { m.SchemaVersion = 2 }}, {"wrong-tag", func(m *Marker) { m.Tag = "v2.3.0" }},
		{"short-commit", func(m *Marker) { m.Commit = "a123" }}, {"missing-platform", func(m *Marker) { delete(m.Assets, AssetNames(m.Tag)[0]) }},
		{"extra-platform", func(m *Marker) { m.Assets["extra"] = strings.Repeat("a", 64) }}, {"wrong-digest", func(m *Marker) { m.Assets[AssetNames(m.Tag)[0]] = "xx" }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m, _ := validMarker("v2.2.0")
			tc.mutate(&m)
			raw, _ := json.Marshal(m)
			if _, e := ParseMarker(raw, "v2.2.0", strings.Repeat("a", 40)); e == nil {
				t.Fatal("invalid marker accepted")
			}
		})
	}
	if m.VerifyChecksums(append(sums, 'x')) == nil {
		t.Fatal("tampered manifest accepted")
	}
	if _, e := ParseMarker(append(raw, []byte(` {}`)...), m.Tag, m.Commit); e == nil {
		t.Fatal("trailing JSON accepted")
	}
	if _, e := ParseMarker([]byte(`{"schemaVersion":1,"tag":"v2.2.0-rc.1"}`), "v2.2.0-rc.1", m.Commit); e == nil {
		t.Fatal("prerelease accepted")
	}
}
func TestGiteeVerificationUsesFixedRoutesAndRequiresCompletedAssets(t *testing.T) {
	for _, mode := range []string{"valid", "missing", "prerelease", "draft", "commit", "marker-missing", "duplicate", "checksums"} {
		t.Run(mode, func(t *testing.T) {
			m, sums := validMarker("v2.2.0")
			names := append(AssetNames(m.Tag), "checksums.txt", MarkerName)
			var calls atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if r.URL.Path == "/api/v5/repos/test/mirror/releases" {
					assets := []map[string]string{}
					for _, name := range names {
						if mode == "missing" && name == names[0] || mode == "marker-missing" && name == MarkerName {
							continue
						}
						assets = append(assets, map[string]string{"name": name, "browser_download_url": "https://untrusted.invalid/ignored"})
					}
					if mode == "duplicate" {
						assets = append(assets, assets[0])
					}
					commit := m.Commit
					if mode == "commit" {
						commit = "main"
					}
					json.NewEncoder(w).Encode([]any{map[string]any{"tag_name": m.Tag, "target_commitish": commit, "prerelease": mode == "prerelease", "draft": mode == "draft", "assets": assets}})
					return
				}
				if r.URL.Path == "/test/mirror/releases/download/"+m.Tag+"/"+MarkerName {
					json.NewEncoder(w).Encode(m)
					return
				}
				if r.URL.Path == "/test/mirror/releases/download/"+m.Tag+"/checksums.txt" {
					if mode == "checksums" {
						w.Write([]byte("tampered"))
					} else {
						w.Write(sums)
					}
					return
				}
				t.Error("unexpected route", r.URL.Path)
				w.WriteHeader(404)
			}))
			defer srv.Close()
			catalog := NewCatalog(Config{GitHubRepository: "test/upstream", GiteeRepository: "test/mirror", TestBaseURL: srv.URL})
			_, e := catalog.VerifyGitee(context.Background(), srv.Client(), "")
			if mode == "valid" && e != nil || mode != "valid" && e == nil {
				t.Fatal(mode, e)
			}
			if calls.Load() > 3 {
				t.Fatal("unexpected fallback/retry")
			}
		})
	}
}
func TestSourceSettingsDefaultRevisionAndPersistence(t *testing.T) {
	storetest.ForEachDialect(t, func(t *testing.T, st *store.Store) {
		catalog := NewCatalog(Config{GitHubRepository: "test/upstream", GiteeRepository: "test/mirror"})
		s := NewSettings(st.DB, catalog)
		initial, e := s.Get(context.Background())
		if e != nil || initial.Source != GitHub || initial.Revision != 1 || len(initial.Sources) != 2 {
			t.Fatal(initial, e)
		}
		saved, e := s.Save(context.Background(), SettingsInput{Source: Gitee, ExpectedRevision: 1})
		if e != nil || saved.Source != Gitee || saved.Revision != 2 {
			t.Fatal(saved, e)
		}
		if _, e = s.Save(context.Background(), SettingsInput{Source: GitHub, ExpectedRevision: 1}); e != ErrConflict {
			t.Fatal(e)
		}
		reopened := NewSettings(st.DB, catalog)
		if got, e := reopened.Preferred(context.Background()); e != nil || got != Gitee {
			t.Fatal(got, e)
		}
		if _, e = s.Save(context.Background(), SettingsInput{Source: "https://evil.invalid", ExpectedRevision: 2}); e != ErrInvalid {
			t.Fatal(e)
		}
		unconfigured := NewSettings(st.DB, NewCatalog(Config{GitHubRepository: "test/upstream"}))
		if _, e = unconfigured.Save(context.Background(), SettingsInput{Source: Gitee, ExpectedRevision: 2}); e != ErrUnavailable {
			t.Fatal(e)
		}
		if got, e := s.Get(context.Background()); e != nil || got.Source != Gitee || got.Revision != 2 {
			t.Fatal(got, e)
		}
	})
}

func TestTrustedURLsAndRedirects(t *testing.T) {
	for _, repo := range []string{"../bad", "a/b/c", "a b/c", "https://gitee.com/a/b"} {
		if _, e := AssetURL(Gitee, repo, "v1.2.3", MarkerName, ""); e == nil {
			t.Fatal(repo)
		}
	}
	if _, e := NewCatalog(Config{}).ForTestBase("https://gitee.com"); e == nil {
		t.Fatal("production URL accepted as test override")
	}
	next := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { t.Fatal("untrusted destination reached") }))
	defer next.Close()
	// A non-loopback redirect is rejected before a network request, including URL credentials.
	for _, dest := range []string{"https://example.invalid/file", "http://127.0.0.1:1/file", "http://user@localhost/file"} {
		srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, dest, 302) }))
		req, _ := http.NewRequest("GET", srv.URL, nil)
		if _, e := Do(srv.Client(), req, srv.URL, Gitee); e == nil {
			t.Fatal(dest)
		}
		srv.Close()
	}
}

func TestInstallationPreferenceInitializesOnceAndUserChoiceWins(t *testing.T) {
	st := storetest.Open(t)
	cat := NewCatalog(Config{GitHubRepository: "test/upstream", GiteeRepository: "test/mirror"})
	s := NewSettings(st.DB, cat)
	t.Setenv("PIPEWRIGHT_UPDATE_SOURCE", "gitee")
	if e := s.InitializeFromEnvironment(context.Background()); e != nil {
		t.Fatal(e)
	}
	first, e := s.Get(context.Background())
	if e != nil || first.Source != Gitee || first.Revision != 2 {
		t.Fatal(first, e)
	}
	if e = s.InitializeFromEnvironment(context.Background()); e != nil {
		t.Fatal(e)
	}
	again, _ := s.Get(context.Background())
	if again.Revision != 2 {
		t.Fatal(again)
	}
	if _, e = s.Save(context.Background(), SettingsInput{Source: GitHub, ExpectedRevision: 2}); e != nil {
		t.Fatal(e)
	}
	if e = NewSettings(st.DB, cat).InitializeFromEnvironment(context.Background()); e != nil {
		t.Fatal(e)
	}
	got, _ := s.Get(context.Background())
	if got.Source != GitHub || got.Revision != 3 {
		t.Fatal(got)
	}
}
func TestInstallationPreferenceDoesNotSelectUnconfiguredSource(t *testing.T) {
	st := storetest.Open(t)
	s := NewSettings(st.DB, NewCatalog(Config{GitHubRepository: "test/upstream"}))
	t.Setenv("PIPEWRIGHT_UPDATE_SOURCE", "gitee")
	if e := s.InitializeFromEnvironment(context.Background()); e != ErrUnavailable {
		t.Fatal(e)
	}
	got, _ := s.Get(context.Background())
	if got.Source != GitHub || got.Revision != 1 {
		t.Fatal(got)
	}
}

func TestMarkerRejectsDuplicateKeysAndCaseAliases(t *testing.T) {
	m, _ := validMarker("v2.2.0")
	encoded, _ := json.Marshal(m)
	valid := string(encoded)
	first := AssetNames(m.Tag)[0]
	cases := []string{
		strings.Replace(valid, `"tag":`, `"tag":"v2.2.0","tag":`, 1),
		strings.Replace(valid, `"assets":{`, `"assets":{"`+first+`":"`+m.Assets[first]+`",`, 1),
		strings.Replace(valid, `"schemaVersion":`, `"SchemaVersion":`, 1),
	}
	for i, raw := range cases {
		if _, e := ParseMarker([]byte(raw), m.Tag, m.Commit); e == nil {
			t.Fatal("ambiguous marker accepted", i)
		}
	}
}
func TestGiteeMetadataRejectsAmbiguousDeepAndOversizeJSON(t *testing.T) {
	cases := map[string]string{
		"duplicate":        `{"tag_name":"v2.2.0","tag_name":"v2.2.0","target_commitish":"` + strings.Repeat("a", 40) + `"}`,
		"nested-duplicate": `{"assets":[{"name":"file","name":"file"}]}`,
		"deep":             `{"ignored":` + strings.Repeat("[", 20) + `0` + strings.Repeat("]", 20) + `}`,
		"oversize":         `{"body":"` + strings.Repeat("x", int(MaxMetadata)) + `"}`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			var calls atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); w.Write([]byte(body)) }))
			defer srv.Close()
			cat := NewCatalog(Config{GitHubRepository: "test/upstream", GiteeRepository: "test/mirror", TestBaseURL: srv.URL})
			if _, e := cat.VerifyGitee(context.Background(), srv.Client(), ""); e == nil {
				t.Fatal("invalid metadata accepted")
			}
			if calls.Load() != 1 {
				t.Fatal("invalid metadata proceeded to download", calls.Load())
			}
		})
	}
}

func TestRepositoryKeepsValidForkNamesAndRejectsDotSegments(t *testing.T) {
	for _, repo := range []string{"owner/.pipewright", "owner/-pipewright", "owner/pipewright..mirror", "_owner/repo", "owner.../repo"} {
		if !ValidRepository(repo) {
			t.Fatal("valid fork rejected", repo)
		}
		if _, e := AssetURL(GitHub, repo, "v1.2.3", "checksums.txt", ""); e != nil {
			t.Fatal(repo, e)
		}
	}
	for _, repo := range []string{"./repo", "../repo", "owner/.", "owner/..", "owner/repo/extra", "owner/%2e%2e", "https://example.invalid/owner/repo"} {
		if ValidRepository(repo) {
			t.Fatal("unsafe path accepted", repo)
		}
	}
}
func TestStableTagSelectionHandlesLargeNumbersAndDeterministicTies(t *testing.T) {
	for _, pair := range [][2]string{{"v2.0.0", "v1.99.99"}, {"v1.100.0", "v1.99.99"}, {"v999999999999999999999999.0.0", "v99999999999999999999999.0.0"}, {"v1.2.3", "1.2.3"}, {"v01.2.3", "v001.2.3"}} {
		if compareStableTags(pair[0], pair[1]) <= 0 || compareStableTags(pair[1], pair[0]) >= 0 {
			t.Fatal(pair)
		}
	}
}
func TestGiteeHighestStableRequiresCompletePaginationAndNeverDowngrades(t *testing.T) {
	for _, mode := range []string{"old-updated-last", "multi-page", "partial-failure", "highest-incomplete", "skip-preview", "page-limit"} {
		t.Run(mode, func(t *testing.T) {
			marker, sums := validMarker("v3.0.0")
			release := func(tag string, complete bool) map[string]any {
				assets := []map[string]string{}
				if complete {
					for _, name := range append(AssetNames(tag), "checksums.txt", MarkerName) {
						assets = append(assets, map[string]string{"name": name})
					}
				}
				return map[string]any{"tag_name": tag, "target_commitish": marker.Commit, "assets": assets}
			}
			var pages, downloads atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/api/v5/repos/test/mirror/releases" {
					pages.Add(1)
					page, e := strconv.Atoi(r.URL.Query().Get("page"))
					if e != nil || r.URL.Query().Get("per_page") != "20" {
						t.Error("bad pagination", r.URL.RawQuery)
						w.WriteHeader(400)
						return
					}
					items := []map[string]any{}
					if mode == "page-limit" {
						for i := 0; i < 20; i++ {
							items = append(items, release("v1.0.0", false))
						}
					} else if (mode == "multi-page" || mode == "partial-failure") && page == 1 {
						for i := 0; i < 20; i++ {
							items = append(items, release(fmt.Sprintf("v1.0.%d", i), true))
						}
					} else if mode == "partial-failure" && page == 2 {
						w.WriteHeader(503)
						return
					} else {
						older := release("v2.0.0", true)
						older["created_at"] = "2099-01-01T00:00:00Z"
						items = append(items, older, release(marker.Tag, mode != "highest-incomplete"))
						if mode == "skip-preview" {
							pre := release("v9.0.0", true)
							pre["prerelease"] = true
							draft := release("v8.0.0", true)
							draft["draft"] = true
							items = append(items, pre, draft)
						}
					}
					json.NewEncoder(w).Encode(items)
					return
				}
				downloads.Add(1)
				if r.URL.Path == "/test/mirror/releases/download/"+marker.Tag+"/"+MarkerName {
					json.NewEncoder(w).Encode(marker)
					return
				}
				if r.URL.Path == "/test/mirror/releases/download/"+marker.Tag+"/checksums.txt" {
					w.Write(sums)
					return
				}
				t.Error("selected older/unexpected asset", r.URL.Path)
				w.WriteHeader(404)
			}))
			defer srv.Close()
			cat := NewCatalog(Config{GitHubRepository: "test/upstream", GiteeRepository: "test/mirror", TestBaseURL: srv.URL})
			selected, e := cat.VerifyGitee(context.Background(), srv.Client(), "")
			if mode == "partial-failure" || mode == "highest-incomplete" || mode == "page-limit" {
				if e == nil || downloads.Load() != 0 {
					t.Fatal("partial list or older release used", selected, e, downloads.Load())
				}
			} else if e != nil || selected.Release.Tag != marker.Tag {
				t.Fatal(selected, e)
			}
			want := int32(1)
			if mode == "multi-page" || mode == "partial-failure" {
				want = 2
			}
			if mode == "page-limit" {
				want = 100
			}
			if pages.Load() != want {
				t.Fatal(pages.Load(), want)
			}
		})
	}
}

// Release bodies are normal API metadata: 100 ordinary 11 KiB notes must fit
// across bounded pages while every individual response remains under 1 MiB.
func TestGiteeNotesAcrossPagesKeepMetadataBudget(t *testing.T) {
	marker, sums := validMarker("v3.0.0")
	var pages atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v5/repos/test/mirror/releases" {
			pages.Add(1)
			page, e := strconv.Atoi(r.URL.Query().Get("page"))
			if e != nil || page < 1 {
				t.Error("invalid page")
				w.WriteHeader(400)
				return
			}
			size, e := strconv.Atoi(r.URL.Query().Get("per_page"))
			if e != nil || size < 1 || size > 100 {
				t.Error("invalid page size")
				w.WriteHeader(400)
				return
			}
			releases := []map[string]any{}
			for i := (page - 1) * size; i < page*size && i < 100; i++ {
				tag := fmt.Sprintf("v2.0.%d", i)
				if i == 99 {
					tag = marker.Tag
				}
				assets := []map[string]string{}
				for _, name := range append(AssetNames(tag), "checksums.txt", MarkerName) {
					assets = append(assets, map[string]string{"name": name})
				}
				releases = append(releases, map[string]any{"tag_name": tag, "target_commitish": marker.Commit, "assets": assets, "body": strings.Repeat("n", 11<<10)})
			}
			json.NewEncoder(w).Encode(releases)
			return
		}
		if r.URL.Path == "/test/mirror/releases/download/"+marker.Tag+"/"+MarkerName {
			json.NewEncoder(w).Encode(marker)
			return
		}
		if r.URL.Path == "/test/mirror/releases/download/"+marker.Tag+"/checksums.txt" {
			w.Write(sums)
			return
		}
		t.Error("unexpected download", r.URL.Path)
		w.WriteHeader(404)
	}))
	defer srv.Close()
	cat := NewCatalog(Config{GitHubRepository: "test/upstream", GiteeRepository: "test/mirror", TestBaseURL: srv.URL})
	selected, e := cat.VerifyGitee(context.Background(), srv.Client(), "")
	if e != nil || selected.Release.Tag != marker.Tag || pages.Load() != 6 {
		t.Fatal(selected, e, pages.Load())
	}
}

func TestUnavailableSourceDoesNotEchoInvalidRepositoryURL(t *testing.T) {
	catalog := NewCatalog(Config{GitHubRepository: "https://example-user:example-password@gitee.com/owner/repo", GiteeRepository: "https://example-user:example-password@gitee.com/owner/repo"})
	sources := catalog.Sources()
	if len(sources) != 2 {
		t.Fatal(sources)
	}
	for _, source := range sources {
		if source.Available || source.Repository != "" || source.Reason != "not_configured" {
			t.Fatal("invalid repository was exposed")
		}
	}
	encoded, e := json.Marshal(sources)
	if e != nil || strings.Contains(string(encoded), "example-password") || strings.Contains(string(encoded), "example-user") {
		t.Fatal("synthetic URL userinfo leaked")
	}
}
