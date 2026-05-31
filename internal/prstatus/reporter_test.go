package prstatus

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDetect(t *testing.T) {
	cases := []struct {
		url      string
		wantPlat Platform
		wantOwn  string
		wantRepo string
		ok       bool
	}{
		{"https://github.com/octo/app.git", GitHub, "octo", "app", true},
		{"https://github.com/octo/app", GitHub, "octo", "app", true},
		{"https://gitee.com/cool-jiawei/aireboot.git", Gitee, "cool-jiawei", "aireboot", true},
		{"https://gitlab.com/x/y.git", "", "", "", false},   // 不支持
		{"https://github.com/onlyowner", "", "", "", false}, // 缺 repo
		{"not a url at all ::::", "", "", "", false},
	}
	for _, c := range cases {
		got, ok := Detect(c.url)
		if ok != c.ok {
			t.Errorf("Detect(%q) ok=%v want %v", c.url, ok, c.ok)
			continue
		}
		if ok && (got.Platform != c.wantPlat || got.Owner != c.wantOwn || got.Repo != c.wantRepo) {
			t.Errorf("Detect(%q) = %+v, want %s %s/%s", c.url, got, c.wantPlat, c.wantOwn, c.wantRepo)
		}
	}
}

func TestStateForRunStatus(t *testing.T) {
	if StateForRunStatus("success") != StateSuccess {
		t.Error("success → success")
	}
	for _, s := range []string{"failed", "partial_failed", "rolled_back"} {
		if StateForRunStatus(s) != StateFailure {
			t.Errorf("%s → failure", s)
		}
	}
}

// 用 stub server 校验真实请求形状(诚实机制验证;真 PR 回写需用户 token+repo)。
func TestReportGitHubRequestShape(t *testing.T) {
	var (
		gotPath, gotMethod, gotAuth, gotAccept string
		gotBody                                map[string]any
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotMethod = r.URL.Path, r.Method
		gotAuth, gotAccept = r.Header.Get("Authorization"), r.Header.Get("Accept")
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &gotBody)
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()

	rep := NewReporter(srv.Client()).WithBaseURLs(srv.URL, "")
	target := Target{Platform: GitHub, Owner: "octo", Repo: "app"}
	err := rep.Report(context.Background(), target, "abc123", StateSuccess, "构建成功", "http://pw/runs/r1", "ghp_secret")
	if err != nil {
		t.Fatalf("Report: %v", err)
	}
	if gotMethod != http.MethodPost {
		t.Errorf("method = %s", gotMethod)
	}
	if gotPath != "/repos/octo/app/statuses/abc123" {
		t.Errorf("path = %s", gotPath)
	}
	if gotAuth != "token ghp_secret" {
		t.Errorf("auth header = %q", gotAuth)
	}
	if !strings.Contains(gotAccept, "github") {
		t.Errorf("accept = %q", gotAccept)
	}
	if gotBody["state"] != "success" || gotBody["context"] != "pipewright" || gotBody["target_url"] != "http://pw/runs/r1" {
		t.Errorf("body = %+v", gotBody)
	}
	// GitHub body 不应含 access_token(token 只在头)。
	if _, ok := gotBody["access_token"]; ok {
		t.Error("GitHub body 不应含 access_token")
	}
}

func TestReportGiteeIncludesAccessToken(t *testing.T) {
	var gotBody map[string]any
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &gotBody)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	rep := NewReporter(srv.Client()).WithBaseURLs("", srv.URL)
	target := Target{Platform: Gitee, Owner: "cool-jiawei", Repo: "aireboot"}
	if err := rep.Report(context.Background(), target, "deadbeef", StateFailure, "失败", "http://pw/runs/r2", "gitee_tok"); err != nil {
		t.Fatalf("Report: %v", err)
	}
	if gotPath != "/repos/cool-jiawei/aireboot/statuses/deadbeef" {
		t.Errorf("path = %s", gotPath)
	}
	if gotBody["access_token"] != "gitee_tok" || gotBody["state"] != "failure" {
		t.Errorf("gitee body = %+v", gotBody)
	}
}

func TestReportNon2xxErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()
	rep := NewReporter(srv.Client()).WithBaseURLs(srv.URL, "")
	err := rep.Report(context.Background(), Target{Platform: GitHub, Owner: "o", Repo: "r"}, "sha", StateSuccess, "", "", "bad")
	if err == nil {
		t.Error("非 2xx 应返回错误")
	}
	if strings.Contains(err.Error(), "bad") {
		t.Error("错误信息不应含 token")
	}
}

func TestReportEmptyShaErrors(t *testing.T) {
	rep := NewReporter(nil)
	if err := rep.Report(context.Background(), Target{Platform: GitHub, Owner: "o", Repo: "r"}, "  ", StateSuccess, "", "", "t"); err == nil {
		t.Error("空 sha 应报错(不发请求)")
	}
}
