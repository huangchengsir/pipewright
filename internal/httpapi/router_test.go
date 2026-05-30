package httpapi

import (
	"encoding/json"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/fstest"
)

func testWebFS() fs.FS {
	return fstest.MapFS{
		"index.html":    &fstest.MapFile{Data: []byte("<!doctype html><title>devopstool</title>")},
		"assets/app.js": &fstest.MapFile{Data: []byte("console.log('hi')")},
	}
}

// TestHealthz 验证 GET /healthz 返回 200 与 {"status":"ok"}。
func TestHealthz(t *testing.T) {
	srv := httptest.NewServer(New(testWebFS(), nil))
	t.Cleanup(srv.Close)

	resp, err := http.Get(srv.URL + "/healthz")
	if err != nil {
		t.Fatalf("GET /healthz: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var body map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body["status"] != "ok" {
		t.Fatalf("body = %v, want status=ok", body)
	}
}

// TestServesStaticAsset 验证存在的静态文件被直接返回。
func TestServesStaticAsset(t *testing.T) {
	srv := httptest.NewServer(New(testWebFS(), nil))
	t.Cleanup(srv.Close)

	resp, err := http.Get(srv.URL + "/assets/app.js")
	if err != nil {
		t.Fatalf("GET asset: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("asset status = %d, want 200", resp.StatusCode)
	}
}

// TestMissingAssetReturns404 验证缺失的带扩展名资源返回 404(而非 SPA 的 200 HTML)。
func TestMissingAssetReturns404(t *testing.T) {
	srv := httptest.NewServer(New(testWebFS(), nil))
	t.Cleanup(srv.Close)

	resp, err := http.Get(srv.URL + "/assets/missing-deadbeef.js")
	if err != nil {
		t.Fatalf("GET missing asset: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("missing asset status = %d, want 404", resp.StatusCode)
	}
}

// TestSPAFallback 验证不存在的前端路由回退到 index.html(SPA)。
func TestSPAFallback(t *testing.T) {
	srv := httptest.NewServer(New(testWebFS(), nil))
	t.Cleanup(srv.Close)

	resp, err := http.Get(srv.URL + "/projects/42/settings")
	if err != nil {
		t.Fatalf("GET spa route: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("spa fallback status = %d, want 200", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct == "" {
		t.Fatalf("expected content-type on fallback")
	}
}
