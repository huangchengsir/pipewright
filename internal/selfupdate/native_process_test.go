package selfupdate

import (
	"context"
	"encoding/json"
	"github.com/huangchengsir/pipewright/internal/store"
	"github.com/huangchengsir/pipewright/internal/version"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

// This opt-in child process uses the production installer, current build metadata,
// actual DB close and production Exec. A parent supplies a loopback asset fixture.
func TestNativeUpgradeProcess(t *testing.T) {
	if os.Getenv("PW_UPDATE_NATIVE_CHILD") != "1" {
		t.Skip("isolated native child not requested")
	}
	if runtime.GOOS != "linux" {
		t.Skip("native Linux acceptance only")
	}
	base := os.Getenv("PW_UPDATE_TEST_BASE")
	u, e := url.Parse(base)
	if e != nil || u.Scheme != "http" || u.Hostname() != "127.0.0.1" {
		t.Fatal("loopback fixture required")
	}
	dir := os.Getenv("PW_UPDATE_TEST_DIR")
	if dir == "" || !filepath.IsAbs(dir) {
		t.Fatal("absolute isolated directory required")
	}
	target := os.Getenv("PW_UPDATE_TEST_TARGET")
	if !tagPattern.MatchString(target) {
		t.Fatal("target required")
	}
	db, e := store.Open(filepath.Join(dir, "native.db"))
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	restart := make(chan struct{}, 1)
	svc, e := New(db.DB, Options{Directory: filepath.Join(dir, "native-update"), BaseURL: base, Client: &http.Client{}, RequestRestart: func() error { restart <- struct{}{}; return nil }})
	if e != nil {
		t.Fatal(e)
	}
	defer svc.Close()
	svc.Start(context.Background())
	if version.Get().Version == target {
		result, e := svc.Current(context.Background())
		if e != nil || result.State != "completed" {
			t.Fatalf("startup identity not confirmed: %+v %v", result, e)
		}
		proof, _ := json.Marshal(struct {
			Task    *Task        `json:"task"`
			Running version.Info `json:"running"`
			Scope   string       `json:"scope"`
		}{result, version.Get(), "native_engine_install_db_close_and_reexec"})
		if e = os.WriteFile(filepath.Join(dir, "native-proof.json"), proof, 0600); e != nil {
			t.Fatal(e)
		}
		return
	}
	if _, e = svc.Submit(context.Background(), Request{RequestID: "native-reexec-once", Target: target}); e != nil {
		t.Fatal(e)
	}
	select {
	case <-restart:
	case <-time.After(11 * time.Minute):
		t.Fatal("native upgrade did not reach restart")
	}
	svc.Close()
	if e = db.Close(); e != nil {
		t.Fatal(e)
	}
	if e = version.Reexec(); e != nil {
		t.Fatal(e)
	}
}
