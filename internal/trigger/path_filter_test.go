package trigger

import (
	"context"
	"testing"

	"github.com/huangchengsir/pipewright/internal/vault"
)

// ─── pathGlobMatch 单元 ─────────────────────────────────────────────────────

func TestPathGlobMatch(t *testing.T) {
	cases := []struct {
		pat, name string
		want      bool
	}{
		{"backend/**", "backend/app/main.go", true},
		{"backend/**", "backend/main.go", true},
		{"backend/**", "frontend/index.ts", false},
		{"**/*.go", "backend/app/main.go", true},
		{"**/*.go", "backend/app/main.ts", false},
		{"*.md", "README.md", true},
		{"*.md", "docs/README.md", false}, // `*` 不跨 /
		{"docs/*.md", "docs/guide.md", true},
		{"docs/*.md", "docs/sub/guide.md", false},
		{"backend/**/*.go", "backend/a/b/c.go", true},
		{"backend/**/*.go", "backend/a/b/c.txt", false},
		{"Makefile", "Makefile", true},
		{"Makefile", "src/Makefile", false},
		{"**", "anything/at/all", true},
	}
	for _, c := range cases {
		if got := pathGlobMatch(c.pat, c.name); got != c.want {
			t.Errorf("pathGlobMatch(%q, %q) = %v, want %v", c.pat, c.name, got, c.want)
		}
	}
}

func TestMatchPathFiltersAnyHit(t *testing.T) {
	filters := []string{"backend/**", "shared/**"}
	// 一个文件命中即放行。
	if !matchPathFilters(filters, []string{"frontend/x.ts", "backend/y.go"}) {
		t.Error("expected match when one file hits backend/**")
	}
	if matchPathFilters(filters, []string{"frontend/x.ts", "docs/y.md"}) {
		t.Error("expected no match when no file hits any filter")
	}
}

// ─── changedFiles 解析 ──────────────────────────────────────────────────────

func TestChangedFilesUnionAndDedup(t *testing.T) {
	commits := []pushCommit{
		{Added: []string{"a.go"}, Modified: []string{"b.go"}, Removed: []string{"c.go"}},
		{Modified: []string{"b.go", " "}, Added: []string{"d.go"}}, // b.go 去重、空白剔除
	}
	got := changedFiles(commits)
	want := map[string]bool{"a.go": true, "b.go": true, "c.go": true, "d.go": true}
	if len(got) != len(want) {
		t.Fatalf("changed files = %v, want 4 unique", got)
	}
	for _, f := range got {
		if !want[f] {
			t.Errorf("unexpected changed file %q", f)
		}
	}
	if changedFiles(nil) != nil {
		t.Error("nil commits should yield nil changed files (degrade to allow)")
	}
}

// ─── 端到端:配置 round-trip + 接收器行为 ──────────────────────────────────

// newReceiverWithPaths 装配一个配了 push 事件 + main→prod 映射 + 给定路径过滤的接收器。
func newReceiverWithPaths(t *testing.T, pathFilters []string) (*Receiver, *fakeRunCreator, string, string) {
	t.Helper()
	db, _ := testDB(t)
	v := vault.New(db, testMasterKey())
	svc := New(db, v)
	projID := seedProject(t, db)

	ctx := context.Background()
	cfg, err := svc.Get(ctx, projID)
	if err != nil {
		t.Fatalf("get config: %v", err)
	}
	token := cfg.WebhookToken
	reset, err := svc.ResetSecret(ctx, projID)
	if err != nil {
		t.Fatalf("reset secret: %v", err)
	}

	saved, err := svc.Save(ctx, projID, SaveInput{
		Events:          Events{Push: true},
		BranchMappings:  []BranchMapping{{BranchPattern: "main", Environment: "prod"}},
		UnmatchedPolicy: PolicyIgnore,
		PathFilters:     pathFilters,
	})
	if err != nil {
		t.Fatalf("save config: %v", err)
	}
	// 配置 round-trip:存读一致。
	if len(saved.PathFilters) != len(pathFilters) {
		t.Fatalf("path filters round-trip mismatch: got %v want %v", saved.PathFilters, pathFilters)
	}

	fake := &fakeRunCreator{}
	return NewReceiver(db, v, fake), fake, token, reset.Secret
}

// pushBodyWithFiles 构造带 commits 改动文件的 push payload。
func pushBodyWithFiles(branch, commit string, files ...string) []byte {
	body := `{"ref":"refs/heads/` + branch + `","after":"` + commit + `","commits":[{"added":[`
	for i, f := range files {
		if i > 0 {
			body += ","
		}
		body += `"` + f + `"`
	}
	body += `],"modified":[],"removed":[]}]}`
	return []byte(body)
}

func TestWebhookPathFilterHitTriggers(t *testing.T) {
	rc, fake, token, secret := newReceiverWithPaths(t, []string{"backend/**"})
	res, err := rc.Handle(context.Background(), Delivery{
		Token: token, Event: eventPush, TokenHdr: secret, DeliveryID: "d-hit",
		RawBody: pushBodyWithFiles("main", "abc", "backend/app/main.go", "README.md"),
	})
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if !res.Accepted || fake.count() != 1 {
		t.Fatalf("path filter hit should create run, got %+v / count=%d", res, fake.count())
	}
}

func TestWebhookPathFilterMissIgnored(t *testing.T) {
	rc, fake, token, secret := newReceiverWithPaths(t, []string{"backend/**"})
	res, err := rc.Handle(context.Background(), Delivery{
		Token: token, Event: eventPush, TokenHdr: secret, DeliveryID: "d-miss",
		RawBody: pushBodyWithFiles("main", "abc", "frontend/index.ts", "docs/guide.md"),
	})
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if res.Accepted || res.Ignored != IgnoredPathNoMatch {
		t.Fatalf("path filter miss should ignore (path_no_match), got %+v", res)
	}
	if fake.count() != 0 {
		t.Fatalf("no run should be created on path miss, count=%d", fake.count())
	}
}

func TestWebhookPathFilterNoFilesAllows(t *testing.T) {
	// 配了过滤但 payload 拿不到改动文件(无 commits)→ 诚实降级放行。
	rc, fake, token, secret := newReceiverWithPaths(t, []string{"backend/**"})
	res, err := rc.Handle(context.Background(), Delivery{
		Token: token, Event: eventPush, TokenHdr: secret, DeliveryID: "d-nofiles",
		RawBody: pushBody("main", "abc"), // 无 commits 字段
	})
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if !res.Accepted || fake.count() != 1 {
		t.Fatalf("missing changed-files should degrade to allow, got %+v / count=%d", res, fake.count())
	}
}
