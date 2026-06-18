package proxy

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/huangchengsir/pipewright/internal/storetest"
	"github.com/huangchengsir/pipewright/internal/target"
)

// stubDNSResolver 是注入用的假 DNS 解析器:按 id 返回 (类型, token)。
type stubDNSResolver struct {
	creds map[string]dnsCred // providerID → (type, token)
	types map[string]string  // providerID → type(供 ProviderType)
}

func (s stubDNSResolver) Resolve(_ context.Context, id string) (string, string, bool, error) {
	c, ok := s.creds[id]
	if !ok {
		return "", "", false, nil
	}
	return c.Type, c.Token, true, nil
}

func (s stubDNSResolver) ProviderType(_ context.Context, id string) (string, bool, error) {
	t, ok := s.types[id]
	if !ok {
		return "", false, nil
	}
	return t, true, nil
}

// --- 渲染:DNS-01 / 通配符 / 路径路由 golden -------------------------------

func TestRenderDNS01Block(t *testing.T) {
	creds := map[string]dnsCred{"prov-1": {Type: "cloudflare", Token: "cf-secret-token"}}
	out := renderCaddyfile([]Route{{
		Domain: "app.example.com", UpstreamContainer: "web", UpstreamPort: 8080,
		Config: RouteConfig{DNSProviderID: "prov-1"},
	}}, creds)
	want := "    tls {\n        dns cloudflare cf-secret-token\n    }\n"
	if !strings.Contains(out, want) {
		t.Fatalf("DNS-01 tls 块不符:\n--- got ---\n%s\n--- want ---\n%s", out, want)
	}
	if !strings.Contains(out, "    reverse_proxy web:8080\n") {
		t.Fatalf("应仍有 reverse_proxy:\n%s", out)
	}
}

func TestRenderDNS01OmittedWhenNoToken(t *testing.T) {
	// 绑了提供商但 apply 取不到 token(map 缺该 id)→ 退回 HTTP-01 渲染(不写 tls 块,不泄 token)。
	out := renderCaddyfile([]Route{{
		Domain: "app.example.com", UpstreamContainer: "web", UpstreamPort: 8080,
		Config: RouteConfig{DNSProviderID: "prov-x"},
	}}, nil)
	if strings.Contains(out, "tls {") {
		t.Fatalf("无 token 不应渲染 tls 块:\n%s", out)
	}
}

func TestRenderWildcardWithDNS01(t *testing.T) {
	creds := map[string]dnsCred{"prov-1": {Type: "alidns", Token: "ali-token"}}
	out := renderCaddyfile([]Route{{
		Domain: "*.example.com", UpstreamContainer: "web", UpstreamPort: 80,
		Config: RouteConfig{DNSProviderID: "prov-1"},
	}}, creds)
	if !strings.Contains(out, "*.example.com {\n") {
		t.Fatalf("通配符站点块头不符:\n%s", out)
	}
	if !strings.Contains(out, "        dns alidns ali-token\n") {
		t.Fatalf("通配符应走 DNS-01:\n%s", out)
	}
}

func TestRenderPathRules(t *testing.T) {
	out := renderCaddyfile([]Route{{
		Domain: "app.example.com", UpstreamContainer: "web", UpstreamPort: 8080,
		Config: RouteConfig{PathRules: []PathRule{
			{Path: "/api/*", UpstreamContainer: "api_c", UpstreamPort: 8080},
			{Path: "/static/*", UpstreamContainer: "cdn_c", UpstreamPort: 80},
		}},
	}}, nil)
	for _, want := range []string{
		"    handle /api/* {\n        reverse_proxy api_c:8080\n    }\n",
		"    handle /static/* {\n        reverse_proxy cdn_c:80\n    }\n",
		"    handle {\n        reverse_proxy web:8080\n    }\n",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("路径路由块缺 %q:\n%s", want, out)
		}
	}
	// 有 PathRules 时不应再有裸 reverse_proxy(全部走 handle)。
	if strings.Contains(out, "\n    reverse_proxy web:8080\n") {
		t.Fatalf("有 PathRules 时主上游应在默认 handle 内,不应裸 reverse_proxy:\n%s", out)
	}
}

// TestRenderWildcardDNS01PathRulesCombined 是「通配符 + DNS-01 + 路径路由 + R2 安全头」的完整 golden。
func TestRenderWildcardDNS01PathRulesCombined(t *testing.T) {
	creds := map[string]dnsCred{"prov-1": {Type: "cloudflare", Token: "TKN"}}
	out := renderCaddyfile([]Route{{
		Domain: "*.apps.example.com", UpstreamContainer: "web", UpstreamPort: 8080,
		Config: RouteConfig{
			DNSProviderID:   "prov-1",
			HSTS:            true,
			SecurityHeaders: true,
			PathRules: []PathRule{
				{Path: "/api/*", UpstreamContainer: "api_c", UpstreamPort: 9000},
			},
		},
	}}, creds)

	want := `*.apps.example.com {
    tls {
        dns cloudflare TKN
    }
    header {
        Strict-Transport-Security "max-age=31536000; includeSubDomains"
        X-Frame-Options "DENY"
        X-Content-Type-Options "nosniff"
        Referrer-Policy "strict-origin-when-cross-origin"
    }
    handle /api/* {
        reverse_proxy api_c:9000
    }
    handle {
        reverse_proxy web:8080
    }
}
`
	if !strings.Contains(out, want) {
		t.Fatalf("组合 golden 不符:\n--- got ---\n%s\n--- want ---\n%s", out, want)
	}
}

// --- 校验:通配符必须绑 DNS;非法路径/zone 早拒 ---------------------------

func TestWildcardRejectedWithoutDNSProvider(t *testing.T) {
	ctx := context.Background()
	st := storetest.OpenDB(t)
	ft := &fakeTarget{}
	svc := &service{store: NewStore(st), tg: ft, prober: fakeProber{}}

	_, err := svc.Create(ctx, CreateInput{
		ServerID: "srv-1", Domain: "*.example.com", UpstreamContainer: "web", UpstreamPort: 80,
	})
	if err == nil || err != ErrWildcardNeedsDNS {
		t.Fatalf("通配符无 DNS 提供商应报 ErrWildcardNeedsDNS, got %v", err)
	}
	if len(ft.execCalls) != 0 {
		t.Fatalf("非法早拒不应触网, got %d exec calls", len(ft.execCalls))
	}
}

func TestWildcardAllowedWithDNSProvider(t *testing.T) {
	ctx := context.Background()
	st := storetest.OpenDB(t)
	ft := &fakeTarget{
		resultFor: func(cmd []string) *target.ExecResult {
			if len(cmd) >= 2 && cmd[0] == "docker" && cmd[1] == "inspect" {
				return &target.ExecResult{ExitCode: 1, Stderr: "No such object"}
			}
			return nil
		},
	}
	dns := stubDNSResolver{
		creds: map[string]dnsCred{"prov-1": {Type: "cloudflare", Token: "TKN"}},
		types: map[string]string{"prov-1": "cloudflare"},
	}
	svc := &service{store: NewStore(st), tg: ft, prober: fakeProber{}, dns: dns}

	route, err := svc.Create(ctx, CreateInput{
		ServerID: "srv-1", Domain: "*.example.com", UpstreamContainer: "web", UpstreamPort: 80,
		DNSProviderID: "prov-1",
	})
	if err != nil {
		t.Fatalf("通配符 + DNS 提供商应成功: %v", err)
	}
	if route.Config.DNSProviderID != "prov-1" {
		t.Fatalf("应持久化 DNS 提供商引用, got %q", route.Config.DNSProviderID)
	}
	// 下发的 Caddyfile 应含 DNS-01 tls 块(token 经 resolver 注入)。
	content := ft.uploadBytes["/tmp/pipewright-caddyfile"]
	if !strings.Contains(content, "dns cloudflare TKN") {
		t.Fatalf("通配符路由 apply 应注入 DNS-01 token:\n%s", content)
	}
	// 临时文件应被 chmod 0600(含 token)。
	joined := make([]string, len(ft.execCalls))
	for i, c := range ft.execCalls {
		joined[i] = cmdJoin(c)
	}
	if firstIndexWithPrefix(joined, "chmod 600 /tmp/pipewright-caddyfile") == -1 {
		t.Fatalf("含 token 的临时 Caddyfile 应 chmod 0600:\n%s", strings.Join(joined, "\n"))
	}
}

func TestCreateBadDNSProviderRejected(t *testing.T) {
	ctx := context.Background()
	st := storetest.OpenDB(t)
	ft := &fakeTarget{}
	// resolver 不认识 prov-x → ProviderType ok=false → ErrInvalidDNSProvider。
	dns := stubDNSResolver{types: map[string]string{}}
	svc := &service{store: NewStore(st), tg: ft, prober: fakeProber{}, dns: dns}

	_, err := svc.Create(ctx, CreateInput{
		ServerID: "srv-1", Domain: "app.example.com", UpstreamContainer: "web", UpstreamPort: 80,
		DNSProviderID: "prov-x",
	})
	if err == nil || err != ErrInvalidDNSProvider {
		t.Fatalf("引用不存在的 DNS 提供商应报 ErrInvalidDNSProvider, got %v", err)
	}
	if len(ft.execCalls) != 0 {
		t.Fatalf("非法早拒不应触网")
	}
}

func TestUpdateRejectsBadPathRule(t *testing.T) {
	ctx := context.Background()
	st := storetest.OpenDB(t)
	s := NewStore(st)
	r := newRoute(CreateInput{ServerID: "srv-1", Domain: "app.example.com", UpstreamContainer: "web", UpstreamPort: 80})
	if err := s.insert(ctx, r); err != nil {
		t.Fatalf("insert: %v", err)
	}
	svc := &service{store: s, tg: &fakeTarget{}, prober: fakeProber{}}

	bad := []PathRule{
		{Path: "api/*", UpstreamContainer: "api_c", UpstreamPort: 8080},   // 未以 / 起
		{Path: "/api/ x", UpstreamContainer: "api_c", UpstreamPort: 8080}, // 含空白
		{Path: "/api/*", UpstreamContainer: "-evil", UpstreamPort: 8080},  // 容器 flag 注入
		{Path: "/api/*", UpstreamContainer: "api_c", UpstreamPort: 70000}, // 端口越界
	}
	for i, pr := range bad {
		_, err := svc.Update(ctx, r.ID, UpdateInput{Config: RouteConfig{PathRules: []PathRule{pr}}})
		if err != ErrInvalidPathRule {
			t.Fatalf("第 %d 条非法路径规则应报 ErrInvalidPathRule, got %v", i, err)
		}
	}
}

func TestUpdateWildcardCannotDropDNSProvider(t *testing.T) {
	ctx := context.Background()
	st := storetest.OpenDB(t)
	s := NewStore(st)
	// 直接插一条通配符路由(绑了提供商)。
	r := newRoute(CreateInput{ServerID: "srv-1", Domain: "*.example.com", UpstreamContainer: "web", UpstreamPort: 80, DNSProviderID: "prov-1"})
	if err := s.insert(ctx, r); err != nil {
		t.Fatalf("insert: %v", err)
	}
	svc := &service{store: s, tg: &fakeTarget{}, prober: fakeProber{}}

	// Update 把 DNS 提供商摘掉(config 不带 dnsProviderId)→ 应报 ErrWildcardNeedsDNS。
	_, err := svc.Update(ctx, r.ID, UpdateInput{Config: RouteConfig{}})
	if err != ErrWildcardNeedsDNS {
		t.Fatalf("通配符路由 Update 摘 DNS 提供商应报 ErrWildcardNeedsDNS, got %v", err)
	}
}

// --- 自定义镜像:docker pull 配置镜像 + env 覆盖 --------------------------

func TestEnsureCaddyPullsConfiguredImage(t *testing.T) {
	ctx := context.Background()
	st := storetest.OpenDB(t)
	ft := &fakeTarget{
		resultFor: func(cmd []string) *target.ExecResult {
			if len(cmd) >= 2 && cmd[0] == "docker" && cmd[1] == "inspect" {
				return &target.ExecResult{ExitCode: 1, Stderr: "No such object"}
			}
			return nil
		},
	}
	svc := &service{store: NewStore(st), tg: ft, prober: fakeProber{}}
	if _, err := svc.Create(ctx, CreateInput{ServerID: "srv-1", Domain: "a.example.com", UpstreamContainer: "web", UpstreamPort: 80}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	joined := make([]string, len(ft.execCalls))
	for i, c := range ft.execCalls {
		joined[i] = cmdJoin(c)
	}
	// 默认镜像被 pull,且 docker run 用同一镜像。
	if firstIndexWithPrefix(joined, "docker pull "+defaultCaddyImage) == -1 {
		t.Fatalf("应 docker pull 默认镜像 %s:\n%s", defaultCaddyImage, strings.Join(joined, "\n"))
	}
	runIdx := firstIndexWithPrefix(joined, "docker run -d --name pipewright-caddy")
	pullIdx := firstIndexWithPrefix(joined, "docker pull "+defaultCaddyImage)
	if runIdx == -1 || pullIdx == -1 || pullIdx > runIdx {
		t.Fatalf("docker pull 应在 docker run 之前:pull=%d run=%d", pullIdx, runIdx)
	}
	// 起容器命令里应含默认镜像引用作为位置参数。
	if !sliceContains(ft.execCalls, defaultCaddyImage) {
		t.Fatalf("docker run 应使用默认镜像 %s", defaultCaddyImage)
	}
}

func TestEnsureCaddyEnvOverridesImage(t *testing.T) {
	const custom = "registry.example.com/myteam/caddy:2.8-dns"
	t.Setenv(caddyImageEnv, custom)

	ctx := context.Background()
	st := storetest.OpenDB(t)
	ft := &fakeTarget{
		resultFor: func(cmd []string) *target.ExecResult {
			if len(cmd) >= 2 && cmd[0] == "docker" && cmd[1] == "inspect" {
				return &target.ExecResult{ExitCode: 1}
			}
			return nil
		},
	}
	svc := &service{store: NewStore(st), tg: ft, prober: fakeProber{}}
	if _, err := svc.Create(ctx, CreateInput{ServerID: "srv-1", Domain: "b.example.com", UpstreamContainer: "web", UpstreamPort: 80}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	joined := make([]string, len(ft.execCalls))
	for i, c := range ft.execCalls {
		joined[i] = cmdJoin(c)
	}
	if firstIndexWithPrefix(joined, "docker pull "+custom) == -1 {
		t.Fatalf("PIPEWRIGHT_CADDY_IMAGE 应被尊重 pull %s:\n%s", custom, strings.Join(joined, "\n"))
	}
	if !sliceContains(ft.execCalls, custom) {
		t.Fatalf("docker run 应使用覆盖镜像 %s", custom)
	}
}

func TestCaddyImageRefRejectsInjection(t *testing.T) {
	// 含 shell 元字符/空格的 env 值非法 → 回退默认(不让 env 注入 docker flag/参数)。
	for _, bad := range []string{"caddy:2 --privileged", "caddy:2; rm -rf /", "-evilflag", "caddy:2|sh"} {
		os.Setenv(caddyImageEnv, bad)
		if got := caddyImageRef(); got != defaultCaddyImage {
			t.Fatalf("非法镜像 env %q 应回退默认, got %q", bad, got)
		}
	}
	os.Unsetenv(caddyImageEnv)
}

// sliceContains 报告某条命令的某个参数恰为 want(用于断言镜像位置参数)。
func sliceContains(calls [][]string, want string) bool {
	for _, c := range calls {
		for _, a := range c {
			if a == want {
				return true
			}
		}
	}
	return false
}
