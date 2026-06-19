package proxy

import (
	"strings"
	"testing"
)

// --- E4.2 渲染:多上游 + lb_policy + 健康检查 golden ------------------------

func TestRenderLoadBalancingWithHealth(t *testing.T) {
	out := renderCaddyfile([]Route{{
		Domain: "app.example.com", UpstreamContainer: "web1", UpstreamPort: 8080,
		Config: RouteConfig{
			Upstreams:      []Upstream{{Container: "web2", Port: 8080}, {Container: "web3", Port: 8080}},
			LBPolicy:       LBPolicyRoundRobin,
			HealthURI:      "/healthz",
			HealthInterval: "10s",
		},
	}}, nil)

	want := `app.example.com {
    reverse_proxy web1:8080 web2:8080 web3:8080 {
        lb_policy round_robin
        health_uri /healthz
        health_interval 10s
    }
}
`
	if !strings.Contains(out, want) {
		t.Fatalf("负载均衡 + 健康检查 golden 不符:\n--- got ---\n%s\n--- want ---\n%s", out, want)
	}
}

// TestRenderGRPC 验证 gRPC h2c transport 块渲染。
func TestRenderGRPC(t *testing.T) {
	out := renderCaddyfile([]Route{{
		Domain: "grpc.example.com", UpstreamContainer: "svc", UpstreamPort: 50051,
		Config: RouteConfig{GRPC: true},
	}}, nil)
	want := `grpc.example.com {
    reverse_proxy svc:50051 {
        transport http {
            versions h2c 2
        }
    }
}
`
	if !strings.Contains(out, want) {
		t.Fatalf("gRPC h2c golden 不符:\n--- got ---\n%s\n--- want ---\n%s", out, want)
	}
}

// TestRenderLBHealthGRPCCombined 是「多上游 + lb + 健康 + gRPC」合并块的完整 golden。
func TestRenderLBHealthGRPCCombined(t *testing.T) {
	out := renderCaddyfile([]Route{{
		Domain: "api.example.com", UpstreamContainer: "api1", UpstreamPort: 9000,
		Config: RouteConfig{
			Upstreams:      []Upstream{{Container: "api2", Port: 9000}},
			LBPolicy:       LBPolicyLeastConn,
			HealthURI:      "/healthz",
			HealthInterval: "5s",
			GRPC:           true,
		},
	}}, nil)
	want := `api.example.com {
    reverse_proxy api1:9000 api2:9000 {
        lb_policy least_conn
        health_uri /healthz
        health_interval 5s
        transport http {
            versions h2c 2
        }
    }
}
`
	if !strings.Contains(out, want) {
		t.Fatalf("LB+health+gRPC 合并 golden 不符:\n--- got ---\n%s\n--- want ---\n%s", out, want)
	}
}

// TestRenderLBFirstPolicyNoBlock 验证 first 策略(归一后空)+ 无健康检查 → 单行(不破坏 R1-R3 既有 golden)。
func TestRenderLBFirstPolicyNoBlock(t *testing.T) {
	out := renderCaddyfile([]Route{{
		Domain: "plain.example.com", UpstreamContainer: "web", UpstreamPort: 80,
		Config: RouteConfig{Upstreams: []Upstream{{Container: "web2", Port: 80}}}, // 仅多上游、无策略/健康
	}}, nil)
	if !strings.Contains(out, "    reverse_proxy web:80 web2:80\n") {
		t.Fatalf("无策略/健康时应单行多上游:\n%s", out)
	}
	if strings.Contains(out, "lb_policy") {
		t.Fatalf("未设策略不应渲染 lb_policy:\n%s", out)
	}
}

// --- E4.3 渲染:TCP 透传 layer4 全局块 ------------------------------------

func TestRenderTCPLayer4Global(t *testing.T) {
	out := renderCaddyfile([]Route{{
		Domain: "db.example.com", UpstreamContainer: "web", UpstreamPort: 80,
		Config: RouteConfig{TCPPassthrough: &TCPConfig{ListenPort: 5432, UpstreamContainer: "pg", UpstreamPort: 5432}},
	}}, nil)
	want := `{
    layer4 {
        :5432 {
            route {
                proxy pg:5432
            }
        }
    }
}
`
	if !strings.Contains(out, want) {
		t.Fatalf("TCP layer4 全局块不符:\n--- got ---\n%s\n--- want ---\n%s", out, want)
	}
	// layer4 全局块必须在站点块之前(Caddyfile 要求全局选项块为文件首个块)。
	g4 := strings.Index(out, "    layer4 {")
	site := strings.Index(out, "db.example.com {")
	if g4 == -1 || site == -1 || g4 > site {
		t.Fatalf("layer4 全局块须在站点块之前:layer4=%d site=%d\n%s", g4, site, out)
	}
}

// TestRenderNoTCPNoGlobalBlock 验证无 TCP 路由时不渲染 layer4 全局块(不影响纯 HTTP 配置)。
func TestRenderNoTCPNoGlobalBlock(t *testing.T) {
	out := renderCaddyfile([]Route{{
		Domain: "app.example.com", UpstreamContainer: "web", UpstreamPort: 80,
	}}, nil)
	if strings.Contains(out, "layer4") {
		t.Fatalf("无 TCP 路由不应有 layer4 块:\n%s", out)
	}
}

// TestRenderMultiTCPSorted 验证多条 TCP 透传按监听端口升序稳定渲染(reload 幂等)。
func TestRenderMultiTCPSorted(t *testing.T) {
	out := renderCaddyfile([]Route{
		{Domain: "a.example.com", UpstreamContainer: "w", UpstreamPort: 80,
			Config: RouteConfig{TCPPassthrough: &TCPConfig{ListenPort: 6379, UpstreamContainer: "redis", UpstreamPort: 6379}}},
		{Domain: "b.example.com", UpstreamContainer: "w", UpstreamPort: 80,
			Config: RouteConfig{TCPPassthrough: &TCPConfig{ListenPort: 5432, UpstreamContainer: "pg", UpstreamPort: 5432}}},
	}, nil)
	i5432 := strings.Index(out, ":5432 {")
	i6379 := strings.Index(out, ":6379 {")
	if i5432 == -1 || i6379 == -1 || i5432 > i6379 {
		t.Fatalf("TCP 监听端口应升序:5432=%d 6379=%d\n%s", i5432, i6379, out)
	}
}

// --- E4.2/E4.3 校验:非法值早拒 -------------------------------------------

func TestValidateConfigR4Rejections(t *testing.T) {
	cases := []struct {
		name string
		cfg  RouteConfig
		want error
	}{
		{"bad lb_policy", RouteConfig{LBPolicy: "weighted"}, ErrInvalidLBPolicy},
		{"bad upstream container", RouteConfig{Upstreams: []Upstream{{Container: "-evil", Port: 80}}}, ErrInvalidUpstream},
		{"bad upstream port", RouteConfig{Upstreams: []Upstream{{Container: "web", Port: 70000}}}, ErrInvalidUpstream},
		{"bad health_uri no slash", RouteConfig{HealthURI: "healthz"}, ErrInvalidHealthURI},
		{"bad health_uri space", RouteConfig{HealthURI: "/health z"}, ErrInvalidHealthURI},
		{"bad health_interval", RouteConfig{HealthInterval: "tenseconds"}, ErrInvalidHealthInterval},
		{"bad tcp listen port", RouteConfig{TCPPassthrough: &TCPConfig{ListenPort: 0, UpstreamContainer: "pg", UpstreamPort: 5432}}, ErrInvalidTCPPassthrough},
		{"bad tcp upstream port", RouteConfig{TCPPassthrough: &TCPConfig{ListenPort: 5432, UpstreamContainer: "pg", UpstreamPort: 99999}}, ErrInvalidTCPPassthrough},
		{"bad tcp container", RouteConfig{TCPPassthrough: &TCPConfig{ListenPort: 5432, UpstreamContainer: "-evil", UpstreamPort: 5432}}, ErrInvalidTCPPassthrough},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if err := validateConfig(normalizeConfig(c.cfg)); err != c.want {
				t.Fatalf("期望 %v, got %v", c.want, err)
			}
		})
	}
}

// TestValidateConfigR4Accepts 验证合法 R4 配置过校验。
func TestValidateConfigR4Accepts(t *testing.T) {
	cfg := normalizeConfig(RouteConfig{
		Upstreams:      []Upstream{{Container: "web2", Port: 8080}},
		LBPolicy:       LBPolicyRandom,
		HealthURI:      "/healthz",
		HealthInterval: "30s",
		GRPC:           true,
		WebSocket:      true,
		TCPPassthrough: &TCPConfig{ListenPort: 5432, UpstreamContainer: "pg", UpstreamPort: 5432},
	})
	if err := validateConfig(cfg); err != nil {
		t.Fatalf("合法 R4 配置应过校验, got %v", err)
	}
}

// TestNormalizeFirstPolicyCleared 验证 first 策略归一化为空(= Caddy 默认,不渲染)。
func TestNormalizeFirstPolicyCleared(t *testing.T) {
	got := normalizeConfig(RouteConfig{LBPolicy: "First"})
	if got.LBPolicy != "" {
		t.Fatalf("first 策略应归一为空, got %q", got.LBPolicy)
	}
}

// TestR4ConfigRoundTrip 验证 R4 字段经 marshal/unmarshal 无损往返(持久化契约)。
func TestR4ConfigRoundTrip(t *testing.T) {
	in := RouteConfig{
		Upstreams:      []Upstream{{Container: "web2", Port: 8080}},
		LBPolicy:       LBPolicyRoundRobin,
		HealthURI:      "/healthz",
		HealthInterval: "10s",
		WebSocket:      true,
		GRPC:           true,
		TCPPassthrough: &TCPConfig{ListenPort: 5432, UpstreamContainer: "pg", UpstreamPort: 5432},
	}
	s, err := marshalConfig(in)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	out, err := unmarshalConfig(s)
	if err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(out.Upstreams) != 1 || out.Upstreams[0] != (Upstream{Container: "web2", Port: 8080}) {
		t.Fatalf("upstreams 往返丢失: %+v", out.Upstreams)
	}
	if out.LBPolicy != LBPolicyRoundRobin || out.HealthURI != "/healthz" || out.HealthInterval != "10s" {
		t.Fatalf("lb/health 往返丢失: %+v", out)
	}
	if !out.WebSocket || !out.GRPC || out.TCPPassthrough == nil || *out.TCPPassthrough != *in.TCPPassthrough {
		t.Fatalf("ws/grpc/tcp 往返丢失: %+v", out)
	}
}
