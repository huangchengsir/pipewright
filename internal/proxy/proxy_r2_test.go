package proxy

import (
	"context"
	"strings"
	"testing"

	"github.com/huangchengsir/pipewright/internal/store"
	"github.com/huangchengsir/pipewright/internal/storetest"
	"github.com/huangchengsir/pipewright/internal/target"
	"golang.org/x/crypto/bcrypt"
)

// --- renderCaddyfile golden(R2 高级配置) ---------------------------------

func TestRenderCaddyfileAliases(t *testing.T) {
	out := renderCaddyfile([]Route{{
		Domain: "example.com", UpstreamContainer: "web", UpstreamPort: 8080,
		Config: RouteConfig{Aliases: []string{"www.example.com"}},
	}})
	if !strings.Contains(out, "example.com, www.example.com {") {
		t.Fatalf("站点块头应含主域名+别名:\n%s", out)
	}
	if !strings.Contains(out, "    reverse_proxy web:8080\n") {
		t.Fatalf("应有 reverse_proxy:\n%s", out)
	}
}

func TestRenderCaddyfileBasicAuth(t *testing.T) {
	out := renderCaddyfile([]Route{{
		Domain: "a.example.com", UpstreamContainer: "web", UpstreamPort: 80,
		Config: RouteConfig{BasicAuthUser: "admin", BasicAuthHash: "$2a$10$abcHASH"},
	}})
	if !strings.Contains(out, "    basic_auth {\n        admin $2a$10$abcHASH\n    }\n") {
		t.Fatalf("basic_auth 块不符:\n%s", out)
	}
}

func TestRenderCaddyfileBasicAuthOmittedWithoutHash(t *testing.T) {
	out := renderCaddyfile([]Route{{
		Domain: "a.example.com", UpstreamContainer: "web", UpstreamPort: 80,
		Config: RouteConfig{BasicAuthUser: "admin"}, // 无哈希 → 不渲染
	}})
	if strings.Contains(out, "basic_auth") {
		t.Fatalf("无哈希不应渲染 basic_auth:\n%s", out)
	}
}

func TestRenderCaddyfileIPAllow(t *testing.T) {
	out := renderCaddyfile([]Route{{
		Domain: "a.example.com", UpstreamContainer: "web", UpstreamPort: 80,
		Config: RouteConfig{IPAllow: []string{"10.0.0.0/8", "192.168.0.0/16"}},
	}})
	if !strings.Contains(out, "    @notallowed not remote_ip 10.0.0.0/8 192.168.0.0/16\n") ||
		!strings.Contains(out, "    respond @notallowed 403\n") {
		t.Fatalf("IP 白名单块不符:\n%s", out)
	}
}

func TestRenderCaddyfileIPDeny(t *testing.T) {
	out := renderCaddyfile([]Route{{
		Domain: "a.example.com", UpstreamContainer: "web", UpstreamPort: 80,
		Config: RouteConfig{IPDeny: []string{"1.2.3.4/32"}},
	}})
	if !strings.Contains(out, "    @denied remote_ip 1.2.3.4/32\n") ||
		!strings.Contains(out, "    respond @denied 403\n") {
		t.Fatalf("IP 黑名单块不符:\n%s", out)
	}
}

func TestRenderCaddyfileHeaders(t *testing.T) {
	out := renderCaddyfile([]Route{{
		Domain: "a.example.com", UpstreamContainer: "web", UpstreamPort: 80,
		Config: RouteConfig{HSTS: true, SecurityHeaders: true},
	}})
	for _, want := range []string{
		"    header {\n",
		"        Strict-Transport-Security \"max-age=31536000; includeSubDomains\"\n",
		"        X-Frame-Options \"DENY\"\n",
		"        X-Content-Type-Options \"nosniff\"\n",
		"        Referrer-Policy \"strict-origin-when-cross-origin\"\n",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("header 块缺 %q:\n%s", want, out)
		}
	}
}

func TestRenderCaddyfileCompression(t *testing.T) {
	out := renderCaddyfile([]Route{{
		Domain: "a.example.com", UpstreamContainer: "web", UpstreamPort: 80,
		Config: RouteConfig{Compression: true},
	}})
	if !strings.Contains(out, "    encode gzip zstd\n") {
		t.Fatalf("压缩指令不符:\n%s", out)
	}
}

func TestRenderCaddyfileRedirects(t *testing.T) {
	out := renderCaddyfile([]Route{{
		Domain: "a.example.com", UpstreamContainer: "web", UpstreamPort: 80,
		Config: RouteConfig{Redirects: []Redirect{
			{From: "/old", To: "/new", Status: 308},
			{From: "/foo", To: "/bar", Status: 301},
		}},
	}})
	if !strings.Contains(out, "    redir /old /new 308\n") ||
		!strings.Contains(out, "    redir /foo /bar 301\n") {
		t.Fatalf("重定向指令不符:\n%s", out)
	}
}

// TestRenderCaddyfileEverythingOn 是「全部开启」的完整 golden 断言(冻结渲染顺序)。
func TestRenderCaddyfileEverythingOn(t *testing.T) {
	out := renderCaddyfile([]Route{{
		Domain: "example.com", UpstreamContainer: "web", UpstreamPort: 8080,
		Config: RouteConfig{
			Aliases:         []string{"www.example.com"},
			ForceHTTPS:      true,
			HSTS:            true,
			SecurityHeaders: true,
			Compression:     true,
			BasicAuthUser:   "admin",
			BasicAuthHash:   "$2a$10$HASH",
			IPAllow:         []string{"10.0.0.0/8"},
			IPDeny:          []string{"1.2.3.4/32"},
			Redirects:       []Redirect{{From: "/old", To: "/new", Status: 308}},
		},
	}})

	want := `example.com, www.example.com {
    @denied remote_ip 1.2.3.4/32
    respond @denied 403
    @notallowed not remote_ip 10.0.0.0/8
    respond @notallowed 403
    basic_auth {
        admin $2a$10$HASH
    }
    header {
        Strict-Transport-Security "max-age=31536000; includeSubDomains"
        X-Frame-Options "DENY"
        X-Content-Type-Options "nosniff"
        Referrer-Policy "strict-origin-when-cross-origin"
    }
    encode gzip zstd
    redir /old /new 308
    reverse_proxy web:8080
}
`
	if !strings.Contains(out, want) {
		t.Fatalf("「全部开启」golden 不符:\n--- got ---\n%s\n--- want substring ---\n%s", out, want)
	}
}

// --- config JSON 持久化往返(含 bcrypt 哈希,经 DTO 剥离) -------------------

func TestStoreConfigRoundtrip(t *testing.T) {
	storetest.ForEachDialect(t, func(t *testing.T, st *store.Store) {
		ctx := context.Background()
		s := NewStore(st.DB)

		r := newRoute(CreateInput{ServerID: "srv-1", Domain: "cfg.example.com", UpstreamContainer: "web", UpstreamPort: 80})
		r.Config = RouteConfig{
			Aliases:         []string{"www.cfg.example.com"},
			HSTS:            true,
			SecurityHeaders: true,
			Compression:     true,
			BasicAuthUser:   "admin",
			BasicAuthHash:   "$2a$10$persistedHASH",
			IPAllow:         []string{"10.0.0.0/8"},
			Redirects:       []Redirect{{From: "/a", To: "/b", Status: 307}},
		}
		if err := s.insert(ctx, r); err != nil {
			t.Fatalf("insert: %v", err)
		}
		got, err := s.get(ctx, r.ID)
		if err != nil {
			t.Fatalf("get: %v", err)
		}
		if len(got.Config.Aliases) != 1 || got.Config.Aliases[0] != "www.cfg.example.com" {
			t.Fatalf("aliases 往返不符: %+v", got.Config.Aliases)
		}
		if !got.Config.HSTS || !got.Config.SecurityHeaders || !got.Config.Compression {
			t.Fatalf("布尔字段往返不符: %+v", got.Config)
		}
		if got.Config.BasicAuthUser != "admin" || got.Config.BasicAuthHash != "$2a$10$persistedHASH" {
			t.Fatalf("basic auth 往返不符: user=%q hash=%q", got.Config.BasicAuthUser, got.Config.BasicAuthHash)
		}
		if len(got.Config.IPAllow) != 1 || got.Config.IPAllow[0] != "10.0.0.0/8" {
			t.Fatalf("ipAllow 往返不符: %+v", got.Config.IPAllow)
		}
		if len(got.Config.Redirects) != 1 || got.Config.Redirects[0] != (Redirect{From: "/a", To: "/b", Status: 307}) {
			t.Fatalf("redirects 往返不符: %+v", got.Config.Redirects)
		}
	})
}

// --- 校验拒绝(注入面) ----------------------------------------------------

func TestValidateConfigRejects(t *testing.T) {
	cases := []struct {
		name string
		cfg  RouteConfig
	}{
		{"bad alias", RouteConfig{Aliases: []string{"no_dot"}}},
		{"alias with space", RouteConfig{Aliases: []string{"a b.example.com"}}},
		{"bad CIDR allow", RouteConfig{IPAllow: []string{"not-a-cidr"}}},
		{"bad CIDR deny", RouteConfig{IPDeny: []string{"10.0.0.0/99"}}},
		{"redirect bad path", RouteConfig{Redirects: []Redirect{{From: "/a b", To: "/x", Status: 308}}}},
		{"redirect brace inject", RouteConfig{Redirects: []Redirect{{From: "/a", To: "/x}\nrespond 200", Status: 308}}}},
		{"redirect bad status", RouteConfig{Redirects: []Redirect{{From: "/a", To: "/b", Status: 418}}}},
		{"basic auth user space", RouteConfig{BasicAuthUser: "ad min"}},
		{"basic auth user brace", RouteConfig{BasicAuthUser: "a}b"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if err := validateConfig(c.cfg); err == nil {
				t.Fatalf("配置 %+v 应被拒", c.cfg)
			}
		})
	}
}

func TestValidateConfigAccepts(t *testing.T) {
	cfg := normalizeConfig(RouteConfig{
		Aliases:       []string{"www.example.com"},
		IPAllow:       []string{"10.0.0.0/8", "1.2.3.4"},
		IPDeny:        []string{"::1/128"},
		Redirects:     []Redirect{{From: "/old", To: "https://x.example.com/new", Status: 0}},
		BasicAuthUser: "admin",
	})
	if err := validateConfig(cfg); err != nil {
		t.Fatalf("合法配置不应被拒: %v", err)
	}
	if cfg.Redirects[0].Status != 308 {
		t.Fatalf("status 0 应默认 308, got %d", cfg.Redirects[0].Status)
	}
}

// --- Update:bcrypt 哈希 + 清认证 + 重 apply -------------------------------

func TestUpdateHashesPasswordAndApplies(t *testing.T) {
	ctx := context.Background()
	st := storetest.OpenDB(t)
	ft := &fakeTarget{}
	s := NewStore(st)
	r := newRoute(CreateInput{ServerID: "srv-1", Domain: "u.example.com", UpstreamContainer: "web", UpstreamPort: 80})
	if err := s.insert(ctx, r); err != nil {
		t.Fatalf("insert: %v", err)
	}
	svc := &service{store: s, tg: ft, prober: fakeProber{}}

	out, err := svc.Update(ctx, r.ID, UpdateInput{
		Config:            RouteConfig{BasicAuthUser: "admin", HSTS: true},
		BasicAuthPassword: "s3cret-pass",
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if out.Config.BasicAuthHash == "" {
		t.Fatalf("应生成 bcrypt 哈希")
	}
	if bcrypt.CompareHashAndPassword([]byte(out.Config.BasicAuthHash), []byte("s3cret-pass")) != nil {
		t.Fatalf("bcrypt 哈希应能校验原口令")
	}
	if !out.Config.HSTS {
		t.Fatalf("HSTS 应持久化")
	}
	// 应触发 reload(enabled 路由)。
	reloaded := false
	for _, c := range ft.execCalls {
		if strings.HasPrefix(cmdJoin(c), "docker exec pipewright-caddy caddy reload") {
			reloaded = true
		}
	}
	if !reloaded {
		t.Fatalf("Update 应重 apply(reload)。calls=%v", ft.execCalls)
	}

	// 清认证:basicAuthUser 为空 → 哈希清空。
	out2, err := svc.Update(ctx, r.ID, UpdateInput{Config: RouteConfig{BasicAuthUser: ""}})
	if err != nil {
		t.Fatalf("Update clear: %v", err)
	}
	if out2.Config.BasicAuthUser != "" || out2.Config.BasicAuthHash != "" {
		t.Fatalf("清认证后用户名/哈希应空: user=%q hash=%q", out2.Config.BasicAuthUser, out2.Config.BasicAuthHash)
	}
}

// TestUpdateKeepsHashWhenNoNewPassword 验证仅改其它配置(不给口令)时沿用旧哈希。
func TestUpdateKeepsHashWhenNoNewPassword(t *testing.T) {
	ctx := context.Background()
	st := storetest.OpenDB(t)
	s := NewStore(st)
	r := newRoute(CreateInput{ServerID: "srv-1", Domain: "k.example.com", UpstreamContainer: "web", UpstreamPort: 80})
	if err := s.insert(ctx, r); err != nil {
		t.Fatalf("insert: %v", err)
	}
	svc := &service{store: s, tg: &fakeTarget{}, prober: fakeProber{}}

	if _, err := svc.Update(ctx, r.ID, UpdateInput{
		Config:            RouteConfig{BasicAuthUser: "admin"},
		BasicAuthPassword: "first-pass",
	}); err != nil {
		t.Fatalf("Update set: %v", err)
	}
	out, err := svc.Update(ctx, r.ID, UpdateInput{Config: RouteConfig{BasicAuthUser: "admin", Compression: true}})
	if err != nil {
		t.Fatalf("Update again: %v", err)
	}
	if bcrypt.CompareHashAndPassword([]byte(out.Config.BasicAuthHash), []byte("first-pass")) != nil {
		t.Fatalf("未给新口令应沿用旧哈希")
	}
	if !out.Config.Compression {
		t.Fatalf("Compression 应更新")
	}
}

// TestUpdateRejectsBadConfig 验证非法配置被 Update 拒(不落库、不触网)。
func TestUpdateRejectsBadConfig(t *testing.T) {
	ctx := context.Background()
	st := storetest.OpenDB(t)
	ft := &fakeTarget{}
	s := NewStore(st)
	r := newRoute(CreateInput{ServerID: "srv-1", Domain: "bad.example.com", UpstreamContainer: "web", UpstreamPort: 80})
	if err := s.insert(ctx, r); err != nil {
		t.Fatalf("insert: %v", err)
	}
	svc := &service{store: s, tg: ft, prober: fakeProber{}}

	if _, err := svc.Update(ctx, r.ID, UpdateInput{Config: RouteConfig{IPAllow: []string{"bogus"}}}); err == nil {
		t.Fatalf("非法 CIDR 应被拒")
	}
	if len(ft.execCalls) != 0 {
		t.Fatalf("非法配置不应触网, got %d calls", len(ft.execCalls))
	}
}

// --- Overview:路由 + 主机展示名 ------------------------------------------

// fakeTargetWithServers 在 fakeTarget 基础上让 List 返回固定主机清单(供 Overview 关联展示名)。
type fakeTargetWithServers struct {
	fakeTarget
	servers []*target.Server
}

func (f *fakeTargetWithServers) List(context.Context) ([]*target.Server, error) {
	return f.servers, nil
}

func TestOverviewJoinsServerName(t *testing.T) {
	ctx := context.Background()
	st := storetest.OpenDB(t)
	s := NewStore(st)
	r1 := newRoute(CreateInput{ServerID: "srv-1", Domain: "o1.example.com", UpstreamContainer: "web", UpstreamPort: 80})
	r2 := newRoute(CreateInput{ServerID: "srv-2", Domain: "o2.example.com", UpstreamContainer: "web", UpstreamPort: 80})
	if err := s.insert(ctx, r1); err != nil {
		t.Fatalf("insert r1: %v", err)
	}
	if err := s.insert(ctx, r2); err != nil {
		t.Fatalf("insert r2: %v", err)
	}
	ft := &fakeTargetWithServers{servers: []*target.Server{
		{ID: "srv-1", Name: "生产机"},
		// srv-2 故意缺失 → 回退 server_id。
	}}
	svc := &service{store: s, tg: ft, prober: fakeProber{}}

	items, err := svc.Overview(ctx)
	if err != nil {
		t.Fatalf("Overview: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("应有 2 条, got %d", len(items))
	}
	names := map[string]string{}
	for _, it := range items {
		names[it.Domain] = it.ServerName
	}
	if names["o1.example.com"] != "生产机" {
		t.Fatalf("srv-1 展示名应为「生产机」, got %q", names["o1.example.com"])
	}
	if names["o2.example.com"] != "srv-2" {
		t.Fatalf("srv-2 缺主机应回退 server_id, got %q", names["o2.example.com"])
	}
}
