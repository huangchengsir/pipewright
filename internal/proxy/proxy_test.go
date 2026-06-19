package proxy

import (
	"context"
	"io"
	"strings"
	"testing"

	"github.com/huangchengsir/pipewright/internal/storetest"
	"github.com/huangchengsir/pipewright/internal/target"
)

// --- 渲染单测(纯函数) ----------------------------------------------------

func TestRenderCaddyfileEmpty(t *testing.T) {
	out := renderCaddyfile(nil, nil)
	if strings.Contains(out, "reverse_proxy") {
		t.Fatalf("空路由不应渲染任何站点块:\n%s", out)
	}
	if !strings.Contains(out, "没有启用的反代路由") {
		t.Fatalf("空配置应含说明注释:\n%s", out)
	}
}

func TestRenderCaddyfileSingle(t *testing.T) {
	out := renderCaddyfile([]Route{
		{Domain: "app.example.com", UpstreamContainer: "web", UpstreamPort: 8080},
	}, nil)
	want := "app.example.com {\n    reverse_proxy web:8080\n}\n"
	if !strings.Contains(out, want) {
		t.Fatalf("单路由站点块不符:\n--- got ---\n%s\n--- want substring ---\n%s", out, want)
	}
}

func TestRenderCaddyfileMultipleSorted(t *testing.T) {
	// 故意乱序输入,断言渲染按 domain 升序稳定排列。
	out := renderCaddyfile([]Route{
		{Domain: "z.example.com", UpstreamContainer: "z", UpstreamPort: 3000},
		{Domain: "a.example.com", UpstreamContainer: "a", UpstreamPort: 80},
	}, nil)
	ai := strings.Index(out, "a.example.com {")
	zi := strings.Index(out, "z.example.com {")
	if ai < 0 || zi < 0 {
		t.Fatalf("两个站点块都应出现:\n%s", out)
	}
	if ai > zi {
		t.Fatalf("应按 domain 升序(a 在 z 前):\n%s", out)
	}
	if !strings.Contains(out, "reverse_proxy a:80") || !strings.Contains(out, "reverse_proxy z:3000") {
		t.Fatalf("上游映射不符:\n%s", out)
	}
}

// --- Create 命令序列单测(注入假 target.Service) --------------------------

// fakeTarget 是捕获每次 Exec / Upload 调用的假 target.Service(不触网)。
// execResults 按命令前缀匹配返回桩结果;未命中默认 exit 0、空输出。
type fakeTarget struct {
	execCalls   [][]string
	uploads     []string // 记录 Upload 的 remotePath
	uploadBytes map[string]string
	// resultFor 决定某次 Exec 返回的结果(按命令决定);nil 则默认 exit0。
	resultFor func(cmd []string) *target.ExecResult
}

func (f *fakeTarget) Exec(_ context.Context, _ string, cmd []string) (*target.ExecResult, error) {
	f.execCalls = append(f.execCalls, cmd)
	if f.resultFor != nil {
		if r := f.resultFor(cmd); r != nil {
			return r, nil
		}
	}
	return &target.ExecResult{ExitCode: 0}, nil
}

func (f *fakeTarget) Upload(_ context.Context, _ string, content io.Reader, remotePath string) error {
	f.uploads = append(f.uploads, remotePath)
	b, _ := io.ReadAll(content)
	if f.uploadBytes == nil {
		f.uploadBytes = map[string]string{}
	}
	f.uploadBytes[remotePath] = string(b)
	return nil
}

// 以下方法满足 target.Service 接口但 proxy 不调用(返回零值即可)。
func (f *fakeTarget) Get(context.Context, string) (*target.Server, error) { return nil, nil }
func (f *fakeTarget) List(context.Context) ([]*target.Server, error)      { return nil, nil }
func (f *fakeTarget) Create(context.Context, target.CreateInput) (*target.Server, error) {
	return nil, nil
}
func (f *fakeTarget) Update(context.Context, string, target.UpdateInput) (*target.Server, error) {
	return nil, nil
}
func (f *fakeTarget) Delete(context.Context, string) error                     { return nil }
func (f *fakeTarget) Test(context.Context, string) (*target.TestResult, error) { return nil, nil }
func (f *fakeTarget) ExecStream(context.Context, string, []string) (io.ReadCloser, error) {
	return nil, nil
}
func (f *fakeTarget) ExecInteractive(context.Context, string, []string) (target.Session, error) {
	return nil, nil
}

// fakeProber 是注入用的假证书探测器。
type fakeProber struct{ res ProbeResult }

func (p fakeProber) Probe(context.Context, string) ProbeResult { return p.res }

func cmdJoin(cmd []string) string { return strings.Join(cmd, " ") }

// TestCreateCommandSequence 断言 Create 的有序 docker 命令序列 + 下发的 Caddyfile 内容正确。
func TestCreateCommandSequence(t *testing.T) {
	ctx := context.Background()
	st := storetest.OpenDB(t)
	ft := &fakeTarget{
		// caddy 容器 inspect 返回非零(不存在)→ 触发起容器路径;其余默认 exit0。
		resultFor: func(cmd []string) *target.ExecResult {
			if len(cmd) >= 2 && cmd[0] == "docker" && cmd[1] == "inspect" {
				return &target.ExecResult{ExitCode: 1, Stderr: "No such object"}
			}
			return nil
		},
	}
	svc := &service{store: NewStore(st), tg: ft, prober: fakeProber{}}

	route, err := svc.Create(ctx, CreateInput{
		ServerID: "srv-1", Domain: "app.example.com", UpstreamContainer: "web", UpstreamPort: 8080,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if route.Domain != "app.example.com" {
		t.Fatalf("返回路由域名不符: %s", route.Domain)
	}

	calls := ft.execCalls
	joined := make([]string, len(calls))
	for i, c := range calls {
		joined[i] = cmdJoin(c)
	}

	// 断言关键命令按顺序出现(允许中间穿插探测命令如 ss -ltn)。
	mustHaveInOrder(t, joined, []string{
		"docker network inspect pipewright-proxy",
		"docker inspect pipewright-caddy",
		"docker run -d --name pipewright-caddy",       // 起 Caddy 容器
		"docker network connect pipewright-proxy web", // 上游接入网络
		"docker cp /tmp/pipewright-caddyfile pipewright-caddy:/etc/caddy/Caddyfile",
		"docker exec pipewright-caddy caddy reload",
	})

	// 端口探测必须发生在「起容器」之前(冲突要先拦)。
	runIdx := firstIndexWithPrefix(joined, "docker run -d --name pipewright-caddy")
	ssIdx := firstIndexWithPrefix(joined, "ss -ltn")
	if ssIdx == -1 || runIdx == -1 || ssIdx > runIdx {
		t.Fatalf("端口探测(ss -ltn)应在起容器前:ss=%d run=%d\n%v", ssIdx, runIdx, joined)
	}

	// 下发的 Caddyfile 内容正确。
	content, ok := ft.uploadBytes["/tmp/pipewright-caddyfile"]
	if !ok {
		t.Fatalf("未 Upload Caddyfile;uploads=%v", ft.uploads)
	}
	if !strings.Contains(content, "app.example.com {\n    reverse_proxy web:8080\n}") {
		t.Fatalf("下发的 Caddyfile 内容不符:\n%s", content)
	}
}

// TestCreatePortConflictAborts 验证 80/443 被占用时 Create 报 ErrPortConflict 且不留库(回滚)。
func TestCreatePortConflictAborts(t *testing.T) {
	ctx := context.Background()
	st := storetest.OpenDB(t)
	ft := &fakeTarget{
		resultFor: func(cmd []string) *target.ExecResult {
			switch {
			case len(cmd) >= 2 && cmd[0] == "docker" && cmd[1] == "inspect":
				return &target.ExecResult{ExitCode: 1} // caddy 不存在
			case len(cmd) >= 1 && cmd[0] == "ss":
				return &target.ExecResult{ExitCode: 0, Stdout: "LISTEN 0 128 0.0.0.0:80 0.0.0.0:*"}
			}
			return nil
		},
	}
	svc := &service{store: NewStore(st), tg: ft, prober: fakeProber{}}

	_, err := svc.Create(ctx, CreateInput{ServerID: "srv-1", Domain: "x.example.com", UpstreamContainer: "web", UpstreamPort: 80})
	if err == nil || !strings.Contains(err.Error(), "端口被占用") {
		t.Fatalf("应报端口冲突, got %v", err)
	}
	// 回滚:库里不应残留该路由。
	routes, _ := svc.List(ctx, "srv-1")
	if len(routes) != 0 {
		t.Fatalf("端口冲突应回滚,不留库, got %d 条", len(routes))
	}
}

// TestCreateInvalidDomain 验证非法域名早拒(不触网)。
func TestCreateInvalidDomain(t *testing.T) {
	ctx := context.Background()
	st := storetest.OpenDB(t)
	ft := &fakeTarget{}
	svc := &service{store: NewStore(st), tg: ft, prober: fakeProber{}}

	for _, bad := range []string{"", "no-dot", "http://app.example.com", "1.2.3.4", "app.example.com:8080"} {
		_, err := svc.Create(ctx, CreateInput{ServerID: "srv-1", Domain: bad, UpstreamContainer: "web", UpstreamPort: 80})
		if err == nil {
			t.Fatalf("域名 %q 应被拒", bad)
		}
	}
	if len(ft.execCalls) != 0 {
		t.Fatalf("非法域名不应触网, but got %d exec calls", len(ft.execCalls))
	}
}

// TestRefreshStatusMapping 验证 RefreshStatus 把探测结果回写 cert_status。
func TestRefreshStatusMapping(t *testing.T) {
	cases := []struct {
		name string
		res  ProbeResult
	}{
		{"issued", ProbeResult{Status: CertStatusIssued, Detail: "已由 R3 签发,到期 2030-01-01"}},
		{"pending", ProbeResult{Status: CertStatusPending, Detail: "证书尚未就绪"}},
		{"failed", ProbeResult{Status: CertStatusFailed, Detail: "无法连接 443"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ctx := context.Background()
			st := storetest.OpenDB(t)
			s := NewStore(st)
			r := newRoute(CreateInput{ServerID: "srv-1", Domain: "app.example.com", UpstreamContainer: "web", UpstreamPort: 80})
			if err := s.insert(ctx, r); err != nil {
				t.Fatalf("insert: %v", err)
			}
			svc := &service{store: s, tg: &fakeTarget{}, prober: fakeProber{res: c.res}}

			out, err := svc.RefreshStatus(ctx, r.ID)
			if err != nil {
				t.Fatalf("RefreshStatus: %v", err)
			}
			if out.CertStatus != c.res.Status {
				t.Fatalf("cert_status 应为 %s, got %s", c.res.Status, out.CertStatus)
			}
			if out.CertDetail != c.res.Detail {
				t.Fatalf("cert_detail 应为 %q, got %q", c.res.Detail, out.CertDetail)
			}
		})
	}
}

// --- CaddyStatus / RemoveCaddy 单测(注入假 target.Service) -----------------

// TestCaddyStatusRunning 验证 inspect 成功 → Installed/Running/Image/Ports 解析正确,RouteCount 反映 store。
func TestCaddyStatusRunning(t *testing.T) {
	ctx := context.Background()
	st := storetest.OpenDB(t)
	ft := &fakeTarget{
		resultFor: func(cmd []string) *target.ExecResult {
			if len(cmd) >= 2 && cmd[0] == "docker" && cmd[1] == "inspect" {
				j := cmdJoin(cmd)
				if strings.Contains(j, "NetworkSettings.Ports") {
					return &target.ExecResult{ExitCode: 0, Stdout: `{"443/tcp":[{"HostPort":"443"}],"80/tcp":[{"HostPort":"80"}]}`}
				}
				return &target.ExecResult{ExitCode: 0, Stdout: "true|ghc.io/x/pipewright-caddy:latest\n"}
			}
			return nil
		},
	}
	svc := &service{store: NewStore(st), tg: ft, prober: fakeProber{}}

	// 先建一条路由 → RouteCount 应为 1。
	if _, err := svc.Create(ctx, CreateInput{ServerID: "srv-1", Domain: "app.example.com", UpstreamContainer: "web", UpstreamPort: 8080}); err != nil {
		t.Fatalf("seed Create: %v", err)
	}

	got, err := svc.CaddyStatus(ctx, "srv-1")
	if err != nil {
		t.Fatalf("CaddyStatus: %v", err)
	}
	if !got.Installed || !got.Running {
		t.Fatalf("应为已安装且运行: %+v", got)
	}
	if got.Image != "ghc.io/x/pipewright-caddy:latest" {
		t.Fatalf("镜像解析不符: %q", got.Image)
	}
	if got.Ports != "80,443" {
		t.Fatalf("端口摘要应为 80,443(升序去后缀), got %q", got.Ports)
	}
	if got.ServerID != "srv-1" {
		t.Fatalf("serverID 不符: %q", got.ServerID)
	}
	if got.RouteCount != 1 {
		t.Fatalf("RouteCount 应为 1, got %d", got.RouteCount)
	}
}

// TestCaddyStatusAbsent 验证容器不存在(inspect 非零)→ Installed:false 且不报错。
func TestCaddyStatusAbsent(t *testing.T) {
	ctx := context.Background()
	st := storetest.OpenDB(t)
	ft := &fakeTarget{
		resultFor: func(cmd []string) *target.ExecResult {
			if len(cmd) >= 2 && cmd[0] == "docker" && cmd[1] == "inspect" {
				return &target.ExecResult{ExitCode: 1, Stderr: "Error: No such object: pipewright-caddy"}
			}
			return nil
		},
	}
	svc := &service{store: NewStore(st), tg: ft, prober: fakeProber{}}

	got, err := svc.CaddyStatus(ctx, "srv-1")
	if err != nil {
		t.Fatalf("容器不存在不应报错, got %v", err)
	}
	if got.Installed || got.Running {
		t.Fatalf("应为未安装: %+v", got)
	}
	if got.Image != "" || got.Ports != "" {
		t.Fatalf("未安装时镜像/端口应为空: %+v", got)
	}
}

// TestCaddyStatusStopped 验证容器存在但已停止 → Installed:true, Running:false。
func TestCaddyStatusStopped(t *testing.T) {
	ctx := context.Background()
	st := storetest.OpenDB(t)
	ft := &fakeTarget{
		resultFor: func(cmd []string) *target.ExecResult {
			if len(cmd) >= 2 && cmd[0] == "docker" && cmd[1] == "inspect" {
				if strings.Contains(cmdJoin(cmd), "NetworkSettings.Ports") {
					return &target.ExecResult{ExitCode: 0, Stdout: `{}`}
				}
				return &target.ExecResult{ExitCode: 0, Stdout: "false|caddy:2\n"}
			}
			return nil
		},
	}
	svc := &service{store: NewStore(st), tg: ft, prober: fakeProber{}}

	got, err := svc.CaddyStatus(ctx, "srv-1")
	if err != nil {
		t.Fatalf("CaddyStatus: %v", err)
	}
	if !got.Installed || got.Running {
		t.Fatalf("应为已安装但未运行: %+v", got)
	}
	if got.Image != "caddy:2" {
		t.Fatalf("镜像不符: %q", got.Image)
	}
	if got.Ports != "" {
		t.Fatalf("空 Ports 应为 \"\", got %q", got.Ports)
	}
}

// TestCaddyStatusTransportError 验证 SSH/传输层错误 → 返回 error(不掩盖为「未安装」)。
func TestCaddyStatusTransportError(t *testing.T) {
	ctx := context.Background()
	st := storetest.OpenDB(t)
	ft := &errTarget{err: target.ErrUnreachable}
	svc := &service{store: NewStore(st), tg: ft, prober: fakeProber{}}

	if _, err := svc.CaddyStatus(ctx, "srv-1"); err == nil {
		t.Fatalf("传输层错误应返回 error")
	}
}

// TestRemoveCaddySequence 验证 RemoveCaddy 按序发 docker stop → docker rm,且绝不删卷。
func TestRemoveCaddySequence(t *testing.T) {
	ctx := context.Background()
	st := storetest.OpenDB(t)
	ft := &fakeTarget{}
	svc := &service{store: NewStore(st), tg: ft, prober: fakeProber{}}

	if err := svc.RemoveCaddy(ctx, "srv-1"); err != nil {
		t.Fatalf("RemoveCaddy: %v", err)
	}
	joined := make([]string, len(ft.execCalls))
	for i, c := range ft.execCalls {
		joined[i] = cmdJoin(c)
	}
	mustHaveInOrder(t, joined, []string{
		"docker stop pipewright-caddy",
		"docker rm pipewright-caddy",
	})
	// 绝不删命名卷。
	for _, j := range joined {
		if strings.Contains(j, "volume rm") || strings.Contains(j, caddyDataVol) {
			t.Fatalf("RemoveCaddy 绝不可删卷, but issued: %q", j)
		}
	}
}

// TestRemoveCaddyIdempotentNoSuchContainer 验证容器已不存在(stop/rm 报 No such container)仍成功。
func TestRemoveCaddyIdempotentNoSuchContainer(t *testing.T) {
	ctx := context.Background()
	st := storetest.OpenDB(t)
	ft := &fakeTarget{
		resultFor: func(cmd []string) *target.ExecResult {
			if len(cmd) >= 2 && cmd[0] == "docker" && (cmd[1] == "stop" || cmd[1] == "rm") {
				return &target.ExecResult{ExitCode: 1, Stderr: "Error: No such container: pipewright-caddy"}
			}
			return nil
		},
	}
	svc := &service{store: NewStore(st), tg: ft, prober: fakeProber{}}

	if err := svc.RemoveCaddy(ctx, "srv-1"); err != nil {
		t.Fatalf("已不存在的容器移除应幂等成功, got %v", err)
	}
}

// errTarget 是每次 Exec/Upload 都返回传输层 error 的假 target.Service(测 SSH 不可达路径)。
type errTarget struct{ err error }

func (e *errTarget) Exec(context.Context, string, []string) (*target.ExecResult, error) {
	return nil, e.err
}
func (e *errTarget) Upload(context.Context, string, io.Reader, string) error { return e.err }
func (e *errTarget) Get(context.Context, string) (*target.Server, error)     { return nil, nil }
func (e *errTarget) List(context.Context) ([]*target.Server, error)          { return nil, nil }
func (e *errTarget) Create(context.Context, target.CreateInput) (*target.Server, error) {
	return nil, nil
}
func (e *errTarget) Update(context.Context, string, target.UpdateInput) (*target.Server, error) {
	return nil, nil
}
func (e *errTarget) Delete(context.Context, string) error                     { return nil }
func (e *errTarget) Test(context.Context, string) (*target.TestResult, error) { return nil, nil }
func (e *errTarget) ExecStream(context.Context, string, []string) (io.ReadCloser, error) {
	return nil, nil
}
func (e *errTarget) ExecInteractive(context.Context, string, []string) (target.Session, error) {
	return nil, nil
}

// --- 测试小工具 ------------------------------------------------------------

func firstIndexWithPrefix(haystack []string, prefix string) int {
	for i, s := range haystack {
		if strings.HasPrefix(s, prefix) {
			return i
		}
	}
	return -1
}

// mustHaveInOrder 断言 wantPrefixes 按给定顺序各自作为某条命令的前缀出现(允许穿插)。
func mustHaveInOrder(t *testing.T, calls []string, wantPrefixes []string) {
	t.Helper()
	idx := 0
	for _, want := range wantPrefixes {
		found := -1
		for i := idx; i < len(calls); i++ {
			if strings.HasPrefix(calls[i], want) {
				found = i
				break
			}
		}
		if found == -1 {
			t.Fatalf("命令序列缺少(或顺序错)前缀 %q\n实际序列:\n%s", want, strings.Join(calls, "\n"))
		}
		idx = found + 1
	}
}
