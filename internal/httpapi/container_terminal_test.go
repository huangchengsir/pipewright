package httpapi

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/huangchengsir/pipewright/internal/audit"
	"github.com/huangchengsir/pipewright/internal/auth"
	"github.com/huangchengsir/pipewright/internal/mask"
	"github.com/huangchengsir/pipewright/internal/target"
	"github.com/huangchengsir/pipewright/internal/vault"
)

// --- 纯函数:容器目标校验(AC-SEC-02 要害) ---

func TestValidateContainerTarget(t *testing.T) {
	cases := []struct {
		name      string
		container string
		shell     string
		wantErr   bool
		wantShell string
	}{
		{"合法容器名 + 默认 shell", "app_1", "", false, "/bin/sh"},
		{"合法容器名 + bash", "web.1", "/bin/bash", false, "/bin/bash"},
		{"合法长 ID", "a1b2c3d4e5f6", "sh", false, "sh"},
		// 注入 / flag 注入(AC-SEC-02):一律拒。
		{"分号注入拒", "app;rm -rf /", "", true, ""},
		{"管道注入拒", "app|sh", "", true, ""},
		{"命令替换拒", "$(reboot)", "", true, ""},
		{"反引号拒", "`id`", "", true, ""},
		{"首字符 - flag 注入拒", "-it", "", true, ""},
		{"含空格拒", "my app", "", true, ""},
		{"含斜杠拒", "a/b", "", true, ""},
		{"空容器拒", "", "", true, ""},
		// shell 白名单:非法 shell 拒(防注入任意命令到 exec 参数)。
		{"非白名单 shell 拒", "app", "/usr/bin/python", true, ""},
		{"shell 注入拒", "app", "sh;rm", true, ""},
		{"shell 带参数拒", "app", "bash -c id", true, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			shell, err := validateContainerTarget(c.container, c.shell)
			if (err != nil) != c.wantErr {
				t.Fatalf("validateContainerTarget(%q,%q) err=%v wantErr=%v", c.container, c.shell, err, c.wantErr)
			}
			if !c.wantErr && shell != c.wantShell {
				t.Fatalf("shell = %q, want %q", shell, c.wantShell)
			}
		})
	}
}

func TestBuildContainerExecCmd(t *testing.T) {
	got := buildContainerExecCmd("c1", "/bin/bash")
	want := []string{"docker", "exec", "-it", "c1", "/bin/bash"}
	if !equalStrSlice(got, want) {
		t.Fatalf("cmd = %v, want %v", got, want)
	}
}

func TestParseResize(t *testing.T) {
	cols, rows, ok := parseResize([]byte(`{"type":"resize","cols":120,"rows":40}`))
	if !ok || cols != 120 || rows != 40 {
		t.Fatalf("resize parse = %d,%d,%v want 120,40,true", cols, rows, ok)
	}
	// 普通输入(非控制帧)不应被当作 resize。
	if _, _, ok := parseResize([]byte("ls -la\n")); ok {
		t.Fatalf("plain input mis-parsed as resize")
	}
	if _, _, ok := parseResize([]byte(`{"type":"other"}`)); ok {
		t.Fatalf("non-resize JSON mis-parsed")
	}
}

// --- HTTP/WS 集成 ---

// fakeInteractiveDialer 返回内存版交互会话,记录命令(断言 array 化)。
type fakeInteractiveDialer struct {
	mu      sync.Mutex
	lastCmd []string
	sess    *memSession
	dialErr error
}

func (d *fakeInteractiveDialer) Run(_ context.Context, _ string, _ target.SSHConfig, _ []string) (*target.ExecResult, error) {
	return &target.ExecResult{}, nil
}
func (d *fakeInteractiveDialer) RunStream(_ context.Context, _ string, _ target.SSHConfig, _ []string) (io.ReadCloser, error) {
	return io.NopCloser(strings.NewReader("")), nil
}
func (d *fakeInteractiveDialer) RunInteractive(_ context.Context, _ string, _ target.SSHConfig, cmd []string) (target.Session, error) {
	d.mu.Lock()
	d.lastCmd = append([]string(nil), cmd...)
	d.mu.Unlock()
	if d.dialErr != nil {
		return nil, d.dialErr
	}
	if d.sess == nil {
		d.sess = newMemSession()
	}
	return d.sess, nil
}
func (d *fakeInteractiveDialer) cmd() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.lastCmd
}

// memSession 是内存版 target.Session:写入 stdin 即原样回显到 out(像 echo 终端)。
type memSession struct {
	mu       sync.Mutex
	cond     *sync.Cond
	buf      []byte
	closed   bool
	lastCols int
	lastRows int
}

func newMemSession() *memSession {
	m := &memSession{}
	m.cond = sync.NewCond(&m.mu)
	return m
}

func (m *memSession) Read(p []byte) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for len(m.buf) == 0 && !m.closed {
		m.cond.Wait()
	}
	if len(m.buf) == 0 && m.closed {
		return 0, io.EOF
	}
	n := copy(p, m.buf)
	m.buf = m.buf[n:]
	return n, nil
}

func (m *memSession) Write(p []byte) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return 0, io.ErrClosedPipe
	}
	m.buf = append(m.buf, p...) // echo
	m.cond.Broadcast()
	return len(p), nil
}

func (m *memSession) Resize(cols, rows int) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.lastCols, m.lastRows = cols, rows
	return nil
}

func (m *memSession) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.closed = true
	m.cond.Broadcast()
	return nil
}

func (m *memSession) resizeDims() (int, int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.lastCols, m.lastRows
}

func setupTerminalAPI(t *testing.T, dialer target.SSHDialer) (*httptest.Server, *http.Client, string, audit.Recorder) {
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

func wsURL(httpURL string) string {
	return "ws" + strings.TrimPrefix(httpURL, "http")
}

func mustParseURL(t *testing.T, raw string) *url.URL {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("parse url %q: %v", raw, err)
	}
	return u
}

// --- 鉴权:未登录 → 升级失败(401),绝不触达 SSH ---

func TestContainerTerminalRejectsUnauthenticated(t *testing.T) {
	dialer := &fakeInteractiveDialer{}
	srv, client, csrf, _ := setupTerminalAPI(t, dialer)
	id := newServerAPI(t, client, srv.URL, csrf)

	// 用一个**不带 session cookie**的裸客户端发 WS 升级。
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	u := wsURL(srv.URL) + "/api/servers/" + id + "/containers/c1/terminal"
	conn, resp, err := websocket.Dial(ctx, u, nil)
	if err == nil {
		_ = conn.Close(websocket.StatusNormalClosure, "")
		t.Fatalf("未登录应升级失败")
	}
	if resp == nil || resp.StatusCode != http.StatusUnauthorized {
		got := 0
		if resp != nil {
			got = resp.StatusCode
		}
		t.Fatalf("未登录应 401, got %d", got)
	}
	if dialer.cmd() != nil {
		t.Fatalf("未鉴权不应触达 SSH, cmd=%v", dialer.cmd())
	}
}

// --- AC-SEC-02:非法容器 ID → 400,绝不触达 SSH ---

func TestContainerTerminalRejectsInjection(t *testing.T) {
	dialer := &fakeInteractiveDialer{}
	srv, client, csrf, _ := setupTerminalAPI(t, dialer)
	id := newServerAPI(t, client, srv.URL, csrf)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	// 非法容器 ID(分号);URL 编码后仍应被后端白名单拒。
	u := wsURL(srv.URL) + "/api/servers/" + id + "/containers/app%3Brm/terminal"
	// 带 cookie 的已登录客户端发起。
	hdr := http.Header{}
	for _, c := range client.Jar.Cookies(mustParseURL(t, srv.URL)) {
		hdr.Add("Cookie", c.Name+"="+c.Value)
	}
	conn, resp, err := websocket.Dial(ctx, u, &websocket.DialOptions{HTTPHeader: hdr})
	if err == nil {
		_ = conn.Close(websocket.StatusNormalClosure, "")
		t.Fatalf("非法容器 ID 应升级失败")
	}
	if resp == nil || resp.StatusCode != http.StatusBadRequest {
		got := 0
		if resp != nil {
			got = resp.StatusCode
		}
		t.Fatalf("非法容器 ID 应 400, got %d", got)
	}
	if dialer.cmd() != nil {
		t.Fatalf("非法入参不应触达 SSH, cmd=%v", dialer.cmd())
	}
}

// --- 同源校验:跨站 Origin → 升级被拒(403),绝不触达 SSH ---

func TestContainerTerminalRejectsCrossOrigin(t *testing.T) {
	dialer := &fakeInteractiveDialer{}
	srv, client, csrf, _ := setupTerminalAPI(t, dialer)
	id := newServerAPI(t, client, srv.URL, csrf)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	u := wsURL(srv.URL) + "/api/servers/" + id + "/containers/c1/terminal"
	hdr := http.Header{}
	for _, c := range client.Jar.Cookies(mustParseURL(t, srv.URL)) {
		hdr.Add("Cookie", c.Name+"="+c.Value)
	}
	// 伪造一个跨站 Origin。
	hdr.Set("Origin", "https://evil.example.com")
	conn, resp, err := websocket.Dial(ctx, u, &websocket.DialOptions{HTTPHeader: hdr})
	if err == nil {
		_ = conn.Close(websocket.StatusNormalClosure, "")
		t.Fatalf("跨站 Origin 应升级失败")
	}
	if resp == nil || resp.StatusCode != http.StatusForbidden {
		got := 0
		if resp != nil {
			got = resp.StatusCode
		}
		t.Fatalf("跨站 Origin 应 403, got %d", got)
	}
	if dialer.cmd() != nil {
		t.Fatalf("跨站请求不应触达 SSH, cmd=%v", dialer.cmd())
	}
}

// --- 端到端 happy path:WS↔SSH 双向泵 + resize + 命令 array 化 + 审计 ---

func TestContainerTerminalHappyPath(t *testing.T) {
	dialer := &fakeInteractiveDialer{}
	srv, client, csrf, rec := setupTerminalAPI(t, dialer)
	id := newServerAPI(t, client, srv.URL, csrf)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	u := wsURL(srv.URL) + "/api/servers/" + id + "/containers/myapp/terminal?shell=/bin/bash"
	hdr := http.Header{}
	for _, c := range client.Jar.Cookies(mustParseURL(t, srv.URL)) {
		hdr.Add("Cookie", c.Name+"="+c.Value)
	}
	conn, _, err := websocket.Dial(ctx, u, &websocket.DialOptions{HTTPHeader: hdr})
	if err != nil {
		t.Fatalf("WS dial: %v", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "")

	// 发一个 resize 控制帧。
	if err := conn.Write(ctx, websocket.MessageText, []byte(`{"type":"resize","cols":100,"rows":30}`)); err != nil {
		t.Fatalf("write resize: %v", err)
	}
	// 发普通输入,后端 memSession 回显(echo)。
	if err := conn.Write(ctx, websocket.MessageText, []byte("hello\n")); err != nil {
		t.Fatalf("write input: %v", err)
	}

	// 读回显。
	got := readWSUntil(t, ctx, conn, "hello\n")
	if !strings.Contains(got, "hello") {
		t.Fatalf("echo not received, got %q", got)
	}

	// 命令 array 化:docker exec -it myapp /bin/bash,容器名/ shell 为独立 argv。
	cmd := dialer.cmd()
	want := []string{"docker", "exec", "-it", "myapp", "/bin/bash"}
	if !equalStrSlice(cmd, want) {
		t.Fatalf("cmd = %v, want %v", cmd, want)
	}

	// resize 透传到会话。
	if dialer.sess != nil {
		cols, rows := dialer.sess.resizeDims()
		if cols != 100 || rows != 30 {
			t.Fatalf("resize not propagated: %dx%d", cols, rows)
		}
	}

	// 审计:开终端这一高危操作已留痕。
	res, err := rec.List(context.Background(), audit.ListFilter{Action: audit.ActionContainerTerminal})
	if err != nil {
		t.Fatalf("audit list: %v", err)
	}
	if len(res.Entries) != 1 {
		t.Fatalf("审计应有 1 条 container_terminal, got %d", len(res.Entries))
	}
	e := res.Entries[0]
	if e.TargetID != id || e.Detail["containerId"] != "myapp" {
		t.Fatalf("审计内容不符: targetID=%q detail=%v", e.TargetID, e.Detail)
	}
}

// readWSUntil 累积读 WS 帧直到累计文本含 needle 或超时。
func readWSUntil(t *testing.T, ctx context.Context, conn *websocket.Conn, needle string) string {
	t.Helper()
	var acc bytes.Buffer
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		rctx, rcancel := context.WithTimeout(ctx, 2*time.Second)
		_, data, err := conn.Read(rctx)
		rcancel()
		if err != nil {
			break
		}
		acc.Write(data)
		if strings.Contains(acc.String(), needle) {
			break
		}
	}
	return acc.String()
}
