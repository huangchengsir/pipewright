package version

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestCompareVersions(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"v1.2.3", "v1.2.3", 0},
		{"1.2.3", "v1.2.3", 0}, // 前导 v 可有可无
		{"v1.2.4", "v1.2.3", 1},
		{"v1.2.3", "v1.2.4", -1},
		{"v1.3.0", "v1.2.9", 1},
		{"v2.0.0", "v1.9.9", 1},
		{"v1.2", "v1.2.0", 0},        // 缺位补 0
		{"v1.0.0", "v1.0.0-rc.1", 1}, // 正式版高于预发布
		{"v1.0.0-rc.1", "v1.0.0", -1},
		{"v1.0.0-rc.2", "v1.0.0-rc.1", 1},
		{"v1.0.0-rc.1", "v1.0.0-rc.1", 0},
		{"v1.0.0-beta", "v1.0.0-rc", -1}, // 字典序 beta < rc
		{"v1.0.0+build.5", "v1.0.0", 0},  // build 元数据不参与比较
	}
	for _, c := range cases {
		if got := CompareVersions(c.a, c.b); got != c.want {
			t.Errorf("CompareVersions(%q,%q)=%d want %d", c.a, c.b, got, c.want)
		}
	}
}

func TestIsComparable(t *testing.T) {
	for v, want := range map[string]bool{
		"v1.2.3":      true,
		"1.2.3":       true,
		"v1.0.0-rc.1": true,
		"dev":         false,
		"none":        false,
		"":            false,
		"latest":      false,
	} {
		if got := isComparable(v); got != want {
			t.Errorf("isComparable(%q)=%v want %v", v, got, want)
		}
	}
}

// newStubChecker 指向本地 stub server,并钉死当前版本与时钟。
func newStubChecker(t *testing.T, current string, h http.HandlerFunc) (*Checker, func()) {
	t.Helper()
	srv := httptest.NewServer(h)
	prevBase, prevHTML, prevVer := apiBase, htmlBase, Version
	apiBase = srv.URL
	htmlBase = srv.URL // 同一 stub 兼当网页站,避免重定向兜底打到真实 github.com
	Version = current
	cleanup := func() {
		apiBase = prevBase
		htmlBase = prevHTML
		Version = prevVer
		srv.Close()
	}
	c := &Checker{repo: "owner/repo", client: srv.Client(), now: time.Now}
	return c, cleanup
}

func TestCheck_UpdateAvailable(t *testing.T) {
	c, done := newStubChecker(t, "v1.0.0", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"tag_name":"v1.2.0","html_url":"https://example/releases/v1.2.0","published_at":"2026-06-06T00:00:00Z","body":"notes"}`))
	})
	defer done()

	info := c.Check(context.Background())
	if info.CheckError != "" {
		t.Fatalf("unexpected checkError: %s", info.CheckError)
	}
	if !info.UpdateAvailable {
		t.Errorf("expected updateAvailable=true (v1.0.0 → v1.2.0)")
	}
	if info.Current != "v1.0.0" || info.Latest != "v1.2.0" {
		t.Errorf("current=%q latest=%q", info.Current, info.Latest)
	}
	if info.ReleaseURL == "" || info.Notes != "notes" {
		t.Errorf("missing release fields: %+v", info)
	}
}

func TestCheck_AlreadyLatest(t *testing.T) {
	c, done := newStubChecker(t, "v1.2.0", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"tag_name":"v1.2.0"}`))
	})
	defer done()
	if info := c.Check(context.Background()); info.UpdateAvailable {
		t.Errorf("expected no update when current==latest")
	}
}

func TestCheck_DevVersionNeverPrompts(t *testing.T) {
	c, done := newStubChecker(t, "dev", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"tag_name":"v9.9.9"}`))
	})
	defer done()
	info := c.Check(context.Background())
	if info.UpdateAvailable {
		t.Errorf("dev build must not report an update")
	}
	if info.Latest != "v9.9.9" {
		t.Errorf("latest should still be reported: %q", info.Latest)
	}
}

func TestCheck_NoReleasesSoftFails(t *testing.T) {
	c, done := newStubChecker(t, "v1.0.0", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})
	defer done()
	info := c.Check(context.Background())
	if info.CheckError != "" {
		t.Errorf("404 (no releases) should be soft, got error %q", info.CheckError)
	}
	if info.UpdateAvailable || info.Latest != "" {
		t.Errorf("no releases → no update, no latest")
	}
}

func TestCheck_RateLimitedReportsError(t *testing.T) {
	c, done := newStubChecker(t, "v1.0.0", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	})
	defer done()
	if info := c.Check(context.Background()); info.CheckError == "" {
		t.Errorf("403 should surface a checkError")
	}
}

// API 限流(403)时,退回网页站 /releases/latest 的 302 重定向解析出最新 tag。
func TestCheck_RateLimitFallsBackToRedirect(t *testing.T) {
	c, done := newStubChecker(t, "v1.0.0", func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/repos/"):
			w.WriteHeader(http.StatusForbidden) // API 限流
		case strings.HasSuffix(r.URL.Path, "/releases/latest"):
			w.Header().Set("Location", "/owner/repo/releases/tag/v1.2.0")
			w.WriteHeader(http.StatusFound) // 网页站 302 → tag
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})
	defer done()

	info := c.Check(context.Background())
	if info.CheckError != "" {
		t.Fatalf("重定向兜底应清空 checkError,实得 %q", info.CheckError)
	}
	if info.Latest != "v1.2.0" || !info.UpdateAvailable {
		t.Errorf("应经重定向得到 latest=v1.2.0 updateAvailable=true,实得 %+v", info)
	}
}

func TestCheck_CachesSuccess(t *testing.T) {
	var hits int
	c, done := newStubChecker(t, "v1.0.0", func(w http.ResponseWriter, _ *http.Request) {
		hits++
		_, _ = w.Write([]byte(`{"tag_name":"v1.1.0"}`))
	})
	defer done()
	_ = c.Check(context.Background())
	_ = c.Check(context.Background())
	if hits != 1 {
		t.Errorf("expected 1 upstream hit (second served from cache), got %d", hits)
	}
}
