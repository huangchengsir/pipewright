package httpapi

import (
	"context"
	"io"
	"testing"
	"time"

	"github.com/huangchengsir/pipewright/internal/previewenv"
	"github.com/huangchengsir/pipewright/internal/proxy"
	"github.com/huangchengsir/pipewright/internal/run"
	"github.com/huangchengsir/pipewright/internal/store"
	"github.com/huangchengsir/pipewright/internal/target"
	"github.com/huangchengsir/pipewright/internal/vault"
)

// --- parsePRNumber ---------------------------------------------------------

func TestParsePRNumber(t *testing.T) {
	cases := []struct {
		branch string
		want   int
	}{
		{"pr-12", 12},
		{"PR/7", 7},
		{"pull/34", 34},
		{"pull-99", 99},
		{"mr/5", 5},
		{"merge-requests/8", 8},
		{"123", 123},
		{"main", 0},
		{"feature/login", 0},
		{"", 0},
		{"pr-0", 0},      // 非正
		{"pr-abc", 0},    // 非数字
		{"release-1", 0}, // 非约定前缀
	}
	for _, c := range cases {
		if got := parsePRNumber(c.branch); got != c.want {
			t.Errorf("parsePRNumber(%q) = %d, want %d", c.branch, got, c.want)
		}
	}
}

// --- 钩子全链路(real run + 真路由/服务器 + stub provisioner)----------------

// stubProvisioner 记录 Provision 调用,GetConfig 据 enabled 返回配置。
type stubProvisioner struct {
	enabled    bool
	calls      []previewenv.ProvisionInput
	forceError bool
}

func (s *stubProvisioner) GetConfig(_ context.Context, projectID string) (*previewenv.Config, error) {
	return &previewenv.Config{ProjectID: projectID, Enabled: s.enabled, DNSProviderID: "prov-1", BaseDomain: "preview.example.com"}, nil
}

func (s *stubProvisioner) Provision(_ context.Context, in previewenv.ProvisionInput) (*previewenv.PreviewEnv, error) {
	s.calls = append(s.calls, in)
	if s.forceError {
		return nil, context.DeadlineExceeded
	}
	return &previewenv.PreviewEnv{ID: "env-1", Subdomain: "pr-1-x.preview.example.com"}, nil
}

// hookTestEnv 搭建:DB + 种子项目/服务器/反代路由 + real run + real proxy(fake target)+ real target。
func hookTestEnv(t *testing.T) (run.Service, proxy.Service, target.Service, string) {
	t.Helper()
	st, err := store.Open(t.TempDir() + "/preview_hook.db")
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	now := time.Now().UTC().Format(time.RFC3339)

	// 真凭据(满足 projects/servers 的 credential_id 外键)。
	v := vault.New(st.DB, testMasterKey())
	cred, err := v.Create(vault.CreateInput{Name: "c", Type: vault.TypeGitToken, Secret: "ghp_x"})
	if err != nil {
		t.Fatalf("vault.Create: %v", err)
	}

	projID := "proj-preview"
	if _, err := st.DB.Exec(
		`INSERT INTO projects (id, name, repo_url, default_branch, credential_id, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		projID, "demo", "https://github.com/acme/demo.git", "main", cred.ID, now, now,
	); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	// 种子服务器(Host 为 IPv4 → resolveHostIPv4 直接可解析,不触网)。
	srvID := "srv-1"
	if _, err := st.DB.Exec(
		`INSERT INTO servers (id, name, host, port, user, credential_id, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		srvID, "host1", "10.0.0.5", 22, "root", cred.ID, now, now,
	); err != nil {
		t.Fatalf("seed server: %v", err)
	}
	// 种子一条 enabled 反代路由(预览反代复用其上游容器/端口)。
	if _, err := st.DB.Exec(
		`INSERT INTO proxy_routes (id, server_id, domain, upstream_container, upstream_port, tls_mode, enabled, cert_status, cert_detail, config, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, 'auto', 1, 'issued', '', '', ?, ?)`,
		"route-app", srvID, "app.example.com", "appweb", 8080, now, now,
	); err != nil {
		t.Fatalf("seed proxy route: %v", err)
	}

	runSvc := run.New(st.DB)
	proxySvc := proxy.New(st.DB, fakeHookTarget{})
	targetSvc := target.New(st.DB, nil, nil)

	r, err := runSvc.Create(context.Background(), projID, run.Trigger{
		Type: run.TriggerWebhook, Branch: "pr-1", Commit: "deadbeef",
		ResolvedTargetServerIDs: []string{srvID},
	})
	if err != nil {
		t.Fatalf("run.Create: %v", err)
	}
	return runSvc, proxySvc, targetSvc, r.ID
}

func TestPreviewHookProvisionsOnPRSuccess(t *testing.T) {
	runSvc, proxySvc, targetSvc, runID := hookTestEnv(t)
	prov := &stubProvisioner{enabled: true}
	hook := NewPreviewProvisionHook(runSvc, prov, proxySvc, targetSvc)

	hook(context.Background(), runID, run.StatusSuccess)

	if len(prov.calls) != 1 {
		t.Fatalf("PR 成功部署应触发一次 provision, got %d", len(prov.calls))
	}
	c := prov.calls[0]
	if c.PRNumber != 1 || c.ServerID != "srv-1" || c.UpstreamContainer != "appweb" || c.UpstreamPort != 8080 {
		t.Fatalf("provision 入参不符: %+v", c)
	}
	if c.HostIP != "10.0.0.5" {
		t.Fatalf("应据 server.Host 解析宿主机 IP, got %q", c.HostIP)
	}
}

func TestPreviewHookDisabledNoProvision(t *testing.T) {
	runSvc, proxySvc, targetSvc, runID := hookTestEnv(t)
	prov := &stubProvisioner{enabled: false} // 项目未开启预览
	hook := NewPreviewProvisionHook(runSvc, prov, proxySvc, targetSvc)

	// 不应 panic、不应 provision、不应报错(钩子无返回值,单纯验证不触发分配)。
	hook(context.Background(), runID, run.StatusSuccess)
	if len(prov.calls) != 0 {
		t.Fatalf("未开启预览不应 provision, got %d", len(prov.calls))
	}
}

func TestPreviewHookNonSuccessNoProvision(t *testing.T) {
	runSvc, proxySvc, targetSvc, runID := hookTestEnv(t)
	prov := &stubProvisioner{enabled: true}
	hook := NewPreviewProvisionHook(runSvc, prov, proxySvc, targetSvc)
	hook(context.Background(), runID, run.StatusFailed) // 失败终态不分配预览
	if len(prov.calls) != 0 {
		t.Fatalf("非成功终态不应 provision, got %d", len(prov.calls))
	}
}

func TestPreviewHookProvisionErrorSwallowed(t *testing.T) {
	runSvc, proxySvc, targetSvc, runID := hookTestEnv(t)
	prov := &stubProvisioner{enabled: true, forceError: true}
	hook := NewPreviewProvisionHook(runSvc, prov, proxySvc, targetSvc)
	// 分配失败必须被钩子吞掉(绝不冒泡影响 run 终态)——不 panic 即通过。
	hook(context.Background(), runID, run.StatusSuccess)
}

func TestPreviewHookNilDepsNoOp(t *testing.T) {
	// 任一依赖 nil → 静默 no-op 钩子(不 panic)。
	hook := NewPreviewProvisionHook(nil, nil, nil, nil)
	hook(context.Background(), "any", run.StatusSuccess)
}

// fakeHookTarget 是 proxy.New 需要的 target.Service 占位(本钩子测试只调 proxySvc.List,不触网)。
type fakeHookTarget struct{}

func (fakeHookTarget) Get(context.Context, string) (*target.Server, error) { return nil, nil }
func (fakeHookTarget) List(context.Context) ([]*target.Server, error)      { return nil, nil }
func (fakeHookTarget) Create(context.Context, target.CreateInput) (*target.Server, error) {
	return nil, nil
}
func (fakeHookTarget) Update(context.Context, string, target.UpdateInput) (*target.Server, error) {
	return nil, nil
}
func (fakeHookTarget) Delete(context.Context, string) error { return nil }
func (fakeHookTarget) Test(context.Context, string) (*target.TestResult, error) {
	return nil, nil
}
func (fakeHookTarget) Exec(context.Context, string, []string) (*target.ExecResult, error) {
	return &target.ExecResult{}, nil
}
func (fakeHookTarget) ExecStream(context.Context, string, []string) (io.ReadCloser, error) {
	return nil, nil
}
func (fakeHookTarget) ExecInteractive(context.Context, string, []string) (target.Session, error) {
	return nil, nil
}
func (fakeHookTarget) Upload(context.Context, string, io.Reader, string) error { return nil }
