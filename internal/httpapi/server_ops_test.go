package httpapi

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/huangchengsir/pipewright/internal/audit"
	"github.com/huangchengsir/pipewright/internal/auth"
	"github.com/huangchengsir/pipewright/internal/mask"
	"github.com/huangchengsir/pipewright/internal/target"
	"github.com/huangchengsir/pipewright/internal/vault"
)

// --- 纯函数:参数校验(AC-SEC-02 要害) ---

func TestValidateServiceParams(t *testing.T) {
	cases := []struct {
		name    string
		typ     string
		target  string
		action  string
		wantErr bool
	}{
		{"systemd restart 合法", "systemd", "nginx", "restart", false},
		{"systemd 带 .service", "systemd", "nginx.service", "restart", false},
		{"systemd @ 实例 unit", "systemd", "ssh@1.service", "start", false},
		{"docker stop 合法", "docker", "my-app_1", "stop", false},
		{"docker start 合法", "docker", "web.1", "start", false},
		// 注入 / flag 注入(AC-SEC-02):一律拒。
		{"分号命令注入拒", "systemd", "nginx;rm -rf /", "restart", true},
		{"管道注入拒", "docker", "app|sh", "restart", true},
		{"命令替换拒", "systemd", "$(reboot)", "restart", true},
		{"反引号拒", "systemd", "`id`", "restart", true},
		{"首字符 - flag 注入拒", "systemd", "--now", "stop", true},
		{"docker 首字符 - 拒", "docker", "-f", "stop", true},
		{"含空格拒", "systemd", "nginx svc", "restart", true},
		{"含斜杠拒", "docker", "a/b", "restart", true},
		{"含换行拒", "systemd", "nginx\nrm", "restart", true},
		{"target 空拒", "systemd", "", "restart", true},
		// action 枚举:非法拒。
		{"非法 action 拒", "systemd", "nginx", "reload", true},
		{"action 注入拒", "systemd", "nginx", "restart;rm", true},
		{"空 action 拒", "systemd", "nginx", "", true},
		// type 白名单:非法拒。
		{"非法 type 拒", "k8s", "nginx", "restart", true},
		{"空 type 拒", "", "nginx", "restart", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := validateServiceParams(c.typ, c.target, c.action)
			if (err != nil) != c.wantErr {
				t.Fatalf("validateServiceParams(%q,%q,%q) err=%v, wantErr=%v",
					c.typ, c.target, c.action, err, c.wantErr)
			}
		})
	}
}

// --- 纯函数:命令 array 构造(不拼 shell) ---

func TestBuildServiceCmd(t *testing.T) {
	got := buildServiceCmd("systemd", "restart", "nginx")
	want := []string{"systemctl", "restart", "nginx"}
	if !equalStrSlice(got, want) {
		t.Fatalf("systemd cmd = %v, want %v", got, want)
	}
	got = buildServiceCmd("docker", "stop", "my-app")
	want = []string{"docker", "stop", "my-app"}
	if !equalStrSlice(got, want) {
		t.Fatalf("docker cmd = %v, want %v", got, want)
	}
}

func equalStrSlice(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// --- HTTP 集成:setup(servers + vault + audit) ---

// capturingDialer 记录最后一次执行的 cmd,供断言命令 array 化未拼 shell。
type capturingDialer struct {
	res     *target.ExecResult
	err     error
	lastCmd []string
}

func (d *capturingDialer) Run(_ context.Context, _ string, _ target.SSHConfig, cmd []string) (*target.ExecResult, error) {
	d.lastCmd = cmd
	return d.res, d.err
}

func (d *capturingDialer) RunWithStdin(_ context.Context, _ string, _ target.SSHConfig, cmd []string, _ io.Reader) (*target.ExecResult, error) {
	d.lastCmd = cmd
	return d.res, d.err
}

func (d *capturingDialer) RunStream(_ context.Context, _ string, _ target.SSHConfig, _ []string) (io.ReadCloser, error) {
	return nil, d.err
}

func (d *capturingDialer) RunInteractive(_ context.Context, _ string, _ target.SSHConfig, cmd []string) (target.Session, error) {
	d.lastCmd = cmd
	return nil, d.err
}

func setupServiceOpsAPI(t *testing.T, dialer target.SSHDialer) (*httptest.Server, *http.Client, string, audit.Recorder) {
	t.Helper()
	st := testStoreAuth(t)
	svc := auth.NewService(st.DB, nil)
	if err := svc.Bootstrap("admin", "testpass"); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	v := vault.New(st.DB, testMasterKey())
	rec := audit.New(st.DB, mask.NewMasker(), nil)
	tsvc := target.New(st.DB, v, dialer)
	srv := httptest.NewServer(New(testWebFSAuth(), svc,
		WithVault(v), WithServers(tsvc), WithAudit(rec)))
	t.Cleanup(srv.Close)

	client := newTestClient(t)
	csrf := loginWithClient(t, client, srv.URL)
	return srv, client, csrf, rec
}

// newServerAPI 经 API 建一条 server,返回 id。
func newServerAPI(t *testing.T, client *http.Client, srvURL, csrf string) string {
	t.Helper()
	credID := newSSHCredAPI(t, client, srvURL, csrf, "LEAKMARKER_priv_key")
	body := `{"name":"web-1","host":"127.0.0.1","port":22,"user":"deploy","credentialId":"` + credID + `"}`
	resp := doJSON(t, client, http.MethodPost, srvURL+"/api/servers", csrf, body)
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var s map[string]any
	_ = json.Unmarshal(raw, &s)
	id, _ := s["id"].(string)
	if id == "" {
		t.Fatalf("create server failed: %s", raw)
	}
	return id
}

func postServiceAction(t *testing.T, client *http.Client, srvURL, csrf, id, body string) *http.Response {
	t.Helper()
	return doJSON(t, client, http.MethodPost, srvURL+"/api/servers/"+id+"/service/action", csrf, body)
}

// --- AC-SEC-02:白名单拒注入 → 400 invalid_service_target ---

func TestServiceActionRejectsInjection(t *testing.T) {
	dialer := &capturingDialer{res: &target.ExecResult{ExitCode: 0}}
	srv, client, csrf, _ := setupServiceOpsAPI(t, dialer)
	id := newServerAPI(t, client, srv.URL, csrf)

	bad := []string{
		`{"type":"systemd","target":"nginx;rm -rf /","action":"restart"}`,
		`{"type":"systemd","target":"--now","action":"stop"}`,
		`{"type":"systemd","target":"$(reboot)","action":"restart"}`,
		`{"type":"docker","target":"-f","action":"stop"}`,
		`{"type":"systemd","target":"nginx","action":"reload"}`,
		`{"type":"k8s","target":"nginx","action":"restart"}`,
	}
	for _, b := range bad {
		resp := postServiceAction(t, client, srv.URL, csrf, id, b)
		raw, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("注入/非法入参应 400, got %d body=%s req=%s", resp.StatusCode, raw, b)
		}
		var e errBody
		_ = json.Unmarshal(raw, &e)
		if e.Error.Code != "invalid_service_target" {
			t.Fatalf("错误码应 invalid_service_target, got %q req=%s", e.Error.Code, b)
		}
	}
	// 拒绝路径不应触达 SSH(命令不构造、不执行)。
	if dialer.lastCmd != nil {
		t.Fatalf("非法入参不应执行任何命令, got cmd=%v", dialer.lastCmd)
	}
}

// --- 成功路径:命令 array 化 + ok:true + 审计留痕 ---

func TestServiceActionSuccessAuditAndCmdArray(t *testing.T) {
	dialer := &capturingDialer{res: &target.ExecResult{Stdout: "", ExitCode: 0}}
	srv, client, csrf, rec := setupServiceOpsAPI(t, dialer)
	id := newServerAPI(t, client, srv.URL, csrf)

	resp := postServiceAction(t, client, srv.URL, csrf, id,
		`{"type":"systemd","target":"nginx","action":"restart"}`)
	raw, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("成功操作应 200, got %d body=%s", resp.StatusCode, raw)
	}
	var dto serviceActionDTO
	if err := json.Unmarshal(raw, &dto); err != nil {
		t.Fatalf("decode: %v body=%s", err, raw)
	}
	if !dto.OK {
		t.Fatalf("ok 应为 true, body=%s", raw)
	}
	if dto.ServerID != id || dto.Type != "systemd" || dto.Target != "nginx" || dto.Action != "restart" {
		t.Fatalf("响应回显不符: %+v", dto)
	}
	// 命令 array 化:systemctl restart nginx,三段独立,未拼 shell。
	want := []string{"systemctl", "restart", "nginx"}
	if !equalStrSlice(dialer.lastCmd, want) {
		t.Fatalf("命令应 array 化 %v, got %v", want, dialer.lastCmd)
	}

	// 审计留痕:action=service_op、target_type=server、target_id=server、detail 含 type/target/action/ok。
	res, err := rec.List(context.Background(), audit.ListFilter{Action: audit.ActionServiceOp})
	if err != nil {
		t.Fatalf("audit list: %v", err)
	}
	if len(res.Entries) != 1 {
		t.Fatalf("应有 1 条 service_op 审计, got %d", len(res.Entries))
	}
	e := res.Entries[0]
	if e.TargetType != audit.TargetServer || e.TargetID != id {
		t.Fatalf("审计 target 不符: type=%s id=%s want server/%s", e.TargetType, e.TargetID, id)
	}
	if e.Detail["type"] != "systemd" || e.Detail["target"] != "nginx" || e.Detail["action"] != "restart" {
		t.Fatalf("审计 detail 不符: %+v", e.Detail)
	}
	if e.Detail["ok"] != true {
		t.Fatalf("审计 detail.ok 应 true: %+v", e.Detail)
	}
}

// --- 命令失败(退出码非零)→ 200 + ok:false + 人读,不 500 ---

func TestServiceActionCommandFailsHumanReadable(t *testing.T) {
	dialer := &capturingDialer{res: &target.ExecResult{
		Stderr:   "Failed to restart nginx.service: Unit nginx.service not found.",
		ExitCode: 5,
	}}
	srv, client, csrf, rec := setupServiceOpsAPI(t, dialer)
	id := newServerAPI(t, client, srv.URL, csrf)

	resp := postServiceAction(t, client, srv.URL, csrf, id,
		`{"type":"systemd","target":"nginx","action":"restart"}`)
	raw, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("命令失败应 200(不 500), got %d body=%s", resp.StatusCode, raw)
	}
	var dto serviceActionDTO
	_ = json.Unmarshal(raw, &dto)
	if dto.OK {
		t.Fatalf("ok 应为 false, body=%s", raw)
	}
	if dto.Error == "" {
		t.Fatalf("失败应有人读 error, body=%s", raw)
	}
	// 即便失败也留痕(ok:false)。
	res, _ := rec.List(context.Background(), audit.ListFilter{Action: audit.ActionServiceOp})
	if len(res.Entries) != 1 || res.Entries[0].Detail["ok"] != false {
		t.Fatalf("失败操作也应留痕 ok:false, entries=%+v", res.Entries)
	}
}

// --- SSH 连接失败 → 200 + ok:false + 人读,绝无密钥,不 500 ---

func TestServiceActionSSHFailHumanReadable(t *testing.T) {
	dialer := &capturingDialer{err: target.ErrUnreachable}
	srv, client, csrf, _ := setupServiceOpsAPI(t, dialer)
	id := newServerAPI(t, client, srv.URL, csrf)

	resp := postServiceAction(t, client, srv.URL, csrf, id,
		`{"type":"docker","target":"app","action":"restart"}`)
	raw, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("SSH 失败应 200(不 500), got %d body=%s", resp.StatusCode, raw)
	}
	var dto serviceActionDTO
	_ = json.Unmarshal(raw, &dto)
	if dto.OK || dto.Error == "" {
		t.Fatalf("SSH 失败应 ok:false + 人读 error, body=%s", raw)
	}
}

// --- 服务器不存在 → 404 ---

func TestServiceActionServerNotFound(t *testing.T) {
	dialer := &capturingDialer{res: &target.ExecResult{ExitCode: 0}}
	srv, client, csrf, _ := setupServiceOpsAPI(t, dialer)

	resp := postServiceAction(t, client, srv.URL, csrf, "nope-id",
		`{"type":"systemd","target":"nginx","action":"restart"}`)
	raw, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("不存在服务器应 404, got %d body=%s", resp.StatusCode, raw)
	}
}

// --- 未认证 → 401 ---

func TestServiceActionRequiresAuth(t *testing.T) {
	dialer := &capturingDialer{res: &target.ExecResult{ExitCode: 0}}
	srv, _, _, _ := setupServiceOpsAPI(t, dialer)

	resp, err := http.Post(srv.URL+"/api/servers/x/service/action", "application/json",
		strings.NewReader(`{"type":"systemd","target":"nginx","action":"restart"}`))
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("未认证应 401, got %d", resp.StatusCode)
	}
}

// --- 缺 CSRF → 403 ---

func TestServiceActionRequiresCSRF(t *testing.T) {
	dialer := &capturingDialer{res: &target.ExecResult{ExitCode: 0}}
	srv, client, csrf, _ := setupServiceOpsAPI(t, dialer)
	id := newServerAPI(t, client, srv.URL, csrf)

	// 已认证(client 持会话 cookie)但不带 CSRF header → 403。
	resp := postServiceAction(t, client, srv.URL, csrf, id, "")
	// 用空 csrf 重发以确保不带 header。
	resp.Body.Close()
	resp2 := postServiceAction(t, client, srv.URL, "", id,
		`{"type":"systemd","target":"nginx","action":"restart"}`)
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusForbidden {
		t.Fatalf("缺 CSRF 应 403, got %d", resp2.StatusCode)
	}
}
