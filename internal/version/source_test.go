package version

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"github.com/huangchengsir/pipewright/internal/releasesource"
	"github.com/huangchengsir/pipewright/internal/storetest"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
)

func TestCheckSourcesUseIndependentCacheAndNoImplicitFallback(t *testing.T) {
	tag := "v2.2.0"
	names := releasesource.AssetNames(tag)
	marker := releasesource.Marker{SchemaVersion: 1, Tag: tag, Commit: strings.Repeat("b", 40), Assets: map[string]string{}}
	var sums strings.Builder
	for _, name := range names {
		sum := sha256.Sum256([]byte(name))
		marker.Assets[name] = hex.EncodeToString(sum[:])
		fmt.Fprintf(&sums, "%x  %s\n", sum, name)
	}
	sum := sha256.Sum256([]byte(sums.String()))
	marker.ChecksumsSHA256 = hex.EncodeToString(sum[:])
	var github, gitee atomic.Int32
	broken := false
	c, done := newStubChecker(t, "v1.0.0", func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/repos/owner/repo/releases/latest":
			github.Add(1)
			json.NewEncoder(w).Encode(map[string]any{"tag_name": "v2.1.0"})
		case r.URL.Path == "/api/v5/repos/owner/mirror/releases":
			gitee.Add(1)
			if broken {
				w.WriteHeader(503)
				return
			}
			assets := []map[string]string{}
			for _, name := range append(names, "checksums.txt", releasesource.MarkerName) {
				assets = append(assets, map[string]string{"name": name})
			}
			json.NewEncoder(w).Encode([]any{map[string]any{"tag_name": tag, "target_commitish": marker.Commit, "assets": assets}})
		case strings.HasSuffix(r.URL.Path, "/"+releasesource.MarkerName):
			json.NewEncoder(w).Encode(marker)
		case strings.HasSuffix(r.URL.Path, "/checksums.txt"):
			w.Write([]byte(sums.String()))
		default:
			t.Error(r.URL.Path)
			w.WriteHeader(404)
		}
	})
	defer done()
	c.catalog = releasesource.NewCatalog(releasesource.Config{GitHubRepository: "owner/repo", GiteeRepository: "owner/mirror", TestBaseURL: apiBase})
	for _, source := range []releasesource.ID{releasesource.GitHub, releasesource.Gitee, releasesource.GitHub, releasesource.Gitee} {
		info := c.CheckSource(context.Background(), source)
		want := "v2.1.0"
		if source == releasesource.Gitee {
			want = tag
		}
		if info.Latest != want || info.Source != source || !info.UpdateAvailable || info.CheckError != "" {
			t.Fatal(info)
		}
	}
	if github.Load() != 1 || gitee.Load() != 1 {
		t.Fatal(github.Load(), gitee.Load())
	}
	c.sourceCache = nil
	broken = true
	info := c.CheckSource(context.Background(), releasesource.Gitee)
	if info.CheckErrorCode != "source_unavailable" || info.UpdateAvailable || github.Load() != 1 {
		t.Fatal(info, github.Load())
	}
	c.CheckSource(context.Background(), releasesource.Gitee)
	if gitee.Load() != 3 {
		t.Fatal("failed check was cached")
	}
}
func TestCheckUnconfiguredAndSavedPreference(t *testing.T) {
	st := storetest.Open(t)
	catalog := releasesource.NewCatalog(releasesource.Config{GitHubRepository: "owner/repo"})
	settings := releasesource.NewSettings(st.DB, catalog)
	c := NewCheckerWithSources(catalog, settings)
	if info := c.CheckSource(context.Background(), releasesource.Gitee); info.Source != releasesource.Gitee || info.CheckErrorCode != "source_not_configured" || info.UpdateAvailable {
		t.Fatal(info)
	}
	if _, e := st.DB.Exec("UPDATE release_source_settings SET source='gitee',revision=2 WHERE id=1"); e != nil {
		t.Fatal(e)
	}
	if info := c.Check(context.Background()); info.Source != releasesource.Gitee || info.CheckErrorCode != "source_not_configured" {
		t.Fatal(info)
	}
}
