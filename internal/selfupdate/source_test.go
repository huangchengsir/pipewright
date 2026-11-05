package selfupdate

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"github.com/huangchengsir/pipewright/internal/releasesource"
	"net/http"
	"os"
	"strings"
	"sync/atomic"
	"testing"
)

func TestTaskSourceIdentityAndLegacyDefault(t *testing.T) {
	h := fixture(t, func(h *harness, w http.ResponseWriter, r *http.Request) { w.WriteHeader(503) })
	h.s.opts.Sources = releasesource.NewCatalog(releasesource.Config{GitHubRepository: "huangchengsir/pipewright", GiteeRepository: "test/mirror"})
	task := submit(t, h)
	wait(t, h.s, task.ID, "interrupted")
	if task.Source != releasesource.GitHub {
		t.Fatal(task.Source)
	}
	if _, e := h.s.Submit(context.Background(), Request{RequestID: task.RequestID, Target: task.Target, Source: releasesource.Gitee}); e != ErrConflict {
		t.Fatal(e)
	}
	raw, e := encoded(task)
	if e != nil {
		t.Fatal(e)
	}
	var fields map[string]any
	json.Unmarshal([]byte(raw), &fields)
	delete(fields, "source")
	legacy, _ := json.Marshal(fields)
	if _, e = h.db.Exec("UPDATE selfupdate_tasks SET record_json=? WHERE id=?", string(legacy), task.ID); e != nil {
		t.Fatal(e)
	}
	got, e := h.s.Get(context.Background(), task.ID)
	if e != nil || got.Source != releasesource.GitHub {
		t.Fatal(got, e)
	}
	if _, e = h.s.Submit(context.Background(), Request{RequestID: task.RequestID, Target: task.Target}); e != nil {
		t.Fatal(e)
	}
}
func TestGiteeTaskChecksCompleteMarkerAndPreservesSourceAfterRestart(t *testing.T) {
	var packageRequests atomic.Int32
	allowPackage := false
	var marker releasesource.Marker
	var sums string
	h := fixture(t, func(h *harness, w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/api/v5/repos/test/mirror/releases/tags/2.0.0") {
			names := append(releasesource.AssetNames("2.0.0"), "checksums.txt", releasesource.MarkerName)
			assets := []map[string]string{}
			for _, name := range names {
				assets = append(assets, map[string]string{"name": name})
			}
			json.NewEncoder(w).Encode(map[string]any{"tag_name": "2.0.0", "target_commitish": marker.Commit, "assets": assets})
			return
		}
		if strings.HasSuffix(r.URL.Path, "/"+releasesource.MarkerName) {
			json.NewEncoder(w).Encode(marker)
			return
		}
		if strings.HasSuffix(r.URL.Path, "/checksums.txt") {
			fmt.Fprint(w, sums)
			return
		}
		if !strings.HasPrefix(r.URL.Path, "/test/mirror/releases/download/2.0.0/") {
			t.Error("wrong source route", r.URL.Path)
			w.WriteHeader(404)
			return
		}
		packageRequests.Add(1)
		if !allowPackage {
			w.WriteHeader(503)
			return
		}
		w.Write(h.pkg)
	})
	marker = releasesource.Marker{SchemaVersion: 1, Tag: "2.0.0", Commit: strings.Repeat("a", 40), Assets: map[string]string{}}
	sum := sha256.Sum256(h.pkg)
	for _, name := range releasesource.AssetNames("2.0.0") {
		marker.Assets[name] = hex.EncodeToString(sum[:])
		sums += fmt.Sprintf("%x  %s\n", sum, name)
	}
	manifest := sha256.Sum256([]byte(sums))
	marker.ChecksumsSHA256 = hex.EncodeToString(manifest[:])
	h.opts.Sources = releasesource.NewCatalog(releasesource.Config{GitHubRepository: "huangchengsir/pipewright", GiteeRepository: "test/mirror"})
	h.s.opts.Sources = h.opts.Sources
	task, e := h.s.Submit(context.Background(), Request{RequestID: "gitee-task", Target: "2.0.0", Source: releasesource.Gitee})
	if e != nil {
		t.Fatal(e)
	}
	first := wait(t, h.s, task.ID, "interrupted")
	if first.Source != releasesource.Gitee || first.Repository != "test/mirror" || first.SHA256 != hex.EncodeToString(sum[:]) || first.ReleaseCommit != marker.Commit || first.ChecksumsSHA256 != marker.ChecksumsSHA256 {
		t.Fatal(first)
	}
	h.s.Close()
	allowPackage = true
	h.opts.PreferredSource = func(context.Context) (releasesource.ID, error) { return releasesource.GitHub, nil }
	h.s, e = New(h.db, h.opts)
	if e != nil {
		t.Fatal(e)
	}
	h.s.Start(context.Background())
	if _, e = h.s.Resume(context.Background(), task.ID, "continue-gitee"); e != nil {
		t.Fatal(e)
	}
	done := wait(t, h.s, task.ID, "restarting")
	if done.Source != releasesource.Gitee || h.installs.Load() != 1 || packageRequests.Load() != 2 {
		t.Fatal(done, h.installs.Load(), packageRequests.Load())
	}
}
func TestIncompleteGiteeMirrorNeverDownloadsOrInstalls(t *testing.T) {
	h := fixture(t, func(h *harness, w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"tag_name": "2.0.0", "target_commitish": strings.Repeat("a", 40), "assets": []any{}})
	})
	h.s.opts.Sources = releasesource.NewCatalog(releasesource.Config{GitHubRepository: "huangchengsir/pipewright", GiteeRepository: "test/mirror"})
	task, e := h.s.Submit(context.Background(), Request{RequestID: "incomplete-mirror", Target: "2.0.0", Source: releasesource.Gitee})
	if e != nil {
		t.Fatal(e)
	}
	got := wait(t, h.s, task.ID, "interrupted")
	if got.SHA256 != "" || h.installs.Load() != 0 {
		t.Fatal(got)
	}
	old, _ := os.ReadFile(h.opts.Executable)
	if string(old) != "old" {
		t.Fatal("executable changed")
	}
}
