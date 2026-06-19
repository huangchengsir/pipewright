package previewenv

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// stubResolver 是注入用的假项目解析器:固定返回仓库地址 + token,或返回错误。
type stubResolver struct {
	repoURL string
	token   string
	err     error
}

func (r *stubResolver) Resolve(_ context.Context, _ string) (string, string, error) {
	return r.repoURL, r.token, r.err
}

// newCheckerWithServer 起一个 stub HTTP server,构造指向它的 checker(GitHub + Gitee base 都指 stub)。
func newCheckerWithServer(t *testing.T, resolver ProjectRepoResolver, handler http.HandlerFunc) *httpChecker {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return NewPRStateChecker(resolver, srv.Client()).WithBaseURLs(srv.URL, srv.URL)
}

func TestPRStateChecker_GitHub(t *testing.T) {
	cases := []struct {
		name string
		body string
		want PRState
	}{
		{"open", `{"state":"open","merged":false}`, PRStateOpen},
		{"closed", `{"state":"closed","merged":false}`, PRStateClosed},
		{"merged", `{"state":"closed","merged":true}`, PRStateMerged},
		{"weird-state", `{"state":"weird","merged":false}`, PRStateUnknown},
		{"bad-json", `not json`, PRStateUnknown},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ck := newCheckerWithServer(t,
				&stubResolver{repoURL: "https://github.com/acme/widget", token: "tok"},
				func(w http.ResponseWriter, r *http.Request) {
					// token 绝不进 URL。
					if r.URL.RawQuery != "" {
						t.Errorf("token must not be in URL query: %q", r.URL.RawQuery)
					}
					if got := r.Header.Get("Authorization"); got != "token tok" {
						t.Errorf("Authorization = %q", got)
					}
					_, _ = w.Write([]byte(tc.body))
				})
			got, err := ck.State(context.Background(), "proj-1", 7)
			if err != nil {
				t.Fatalf("State err: %v", err)
			}
			if got != tc.want {
				t.Fatalf("State = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestPRStateChecker_Gitee(t *testing.T) {
	cases := []struct {
		name string
		body string
		want PRState
	}{
		{"open", `{"state":"open"}`, PRStateOpen},
		{"closed", `{"state":"closed"}`, PRStateClosed},
		{"merged", `{"state":"merged"}`, PRStateMerged},
		{"weird", `{"state":"draft"}`, PRStateUnknown},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ck := newCheckerWithServer(t,
				&stubResolver{repoURL: "https://gitee.com/acme/widget", token: "tok"},
				func(w http.ResponseWriter, r *http.Request) {
					if r.URL.RawQuery != "" {
						t.Errorf("token must not be in URL query: %q", r.URL.RawQuery)
					}
					_, _ = w.Write([]byte(tc.body))
				})
			got, err := ck.State(context.Background(), "proj-1", 9)
			if err != nil {
				t.Fatalf("State err: %v", err)
			}
			if got != tc.want {
				t.Fatalf("State = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestPRStateChecker_AuthFailIsUnknown(t *testing.T) {
	// 401/403/404/5xx 一律视为不确定 → unknown(绝不回收)。
	for _, code := range []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound, http.StatusInternalServerError} {
		ck := newCheckerWithServer(t,
			&stubResolver{repoURL: "https://github.com/acme/widget", token: "tok"},
			func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(code)
				_, _ = w.Write([]byte(`{"state":"closed","merged":true}`))
			})
		got, err := ck.State(context.Background(), "proj-1", 7)
		if err != nil {
			t.Fatalf("State err: %v", err)
		}
		if got != PRStateUnknown {
			t.Fatalf("HTTP %d: State = %q, want unknown", code, got)
		}
	}
}

func TestPRStateChecker_GuardsAreUnknown(t *testing.T) {
	ctx := context.Background()

	// 无 token → unknown(不发请求)。
	ck := NewPRStateChecker(&stubResolver{repoURL: "https://github.com/acme/widget", token: ""}, http.DefaultClient)
	if got, _ := ck.State(ctx, "p", 1); got != PRStateUnknown {
		t.Fatalf("no token: want unknown, got %q", got)
	}

	// resolver 报错 → unknown(不发请求)。
	ck = NewPRStateChecker(&stubResolver{err: context.Canceled}, http.DefaultClient)
	if got, _ := ck.State(ctx, "p", 1); got != PRStateUnknown {
		t.Fatalf("resolver err: want unknown, got %q", got)
	}

	// 未知平台(非 github/gitee)→ unknown。
	ck = NewPRStateChecker(&stubResolver{repoURL: "https://example.com/acme/widget", token: "tok"}, http.DefaultClient)
	if got, _ := ck.State(ctx, "p", 1); got != PRStateUnknown {
		t.Fatalf("unknown host: want unknown, got %q", got)
	}

	// 非法 PR 号 → unknown。
	ck = NewPRStateChecker(&stubResolver{repoURL: "https://github.com/acme/widget", token: "tok"}, http.DefaultClient)
	if got, _ := ck.State(ctx, "p", 0); got != PRStateUnknown {
		t.Fatalf("bad pr number: want unknown, got %q", got)
	}

	// nil resolver → unknown。
	ck = NewPRStateChecker(nil, http.DefaultClient)
	if got, _ := ck.State(ctx, "p", 1); got != PRStateUnknown {
		t.Fatalf("nil resolver: want unknown, got %q", got)
	}
}
