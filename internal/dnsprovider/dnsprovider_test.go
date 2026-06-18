package dnsprovider

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/huangchengsir/pipewright/internal/storetest"
	"github.com/huangchengsir/pipewright/internal/vault"
)

// --- 假 vault / RouteCreator / http transport -------------------------------

// stubVault 是注入用的假 vaultReader:按 id 返回 token 明文 + 存在性。
type stubVault struct {
	tokens map[string]string
	uncfg  bool // true → 模拟 vault 未配置
}

func (s stubVault) Reveal(id string) (string, error) {
	if s.uncfg {
		return "", vault.ErrVaultUnconfigured
	}
	t, ok := s.tokens[id]
	if !ok {
		return "", vault.ErrNotFound
	}
	return t, nil
}

func (s stubVault) Exists(id string) (bool, error) {
	if s.uncfg {
		return false, vault.ErrVaultUnconfigured
	}
	_, ok := s.tokens[id]
	return ok, nil
}

// stubRouteCreator 记录建路由调用,返回固定 routeID;可配置返回域名冲突错误模拟撞车重试。
type stubRouteCreator struct {
	calls       []CreateDNS01RouteInput
	failFirstN  int // 前 N 次返回「域名冲突」错误(模拟随机撞车),之后成功
	nextRouteID string
}

func (s *stubRouteCreator) CreateDNS01Route(_ context.Context, in CreateDNS01RouteInput) (string, error) {
	s.calls = append(s.calls, in)
	if len(s.calls) <= s.failFirstN {
		return "", errors.New("proxy: domain already in use")
	}
	id := s.nextRouteID
	if id == "" {
		id = "route-" + in.Domain
	}
	return id, nil
}

// roundTripFunc 是 mock http.RoundTripper。
type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func jsonResp(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Body:       io.NopCloser(strings.NewReader(body)),
		Header:     http.Header{"Content-Type": []string{"application/json"}},
	}
}

// newTestService 造一个注入了 stub 的 service(不触真网络)。
func newTestService(t *testing.T, vault vaultReader, routes RouteCreator, dial dialFactory) *service {
	t.Helper()
	st := storetest.OpenDB(t)
	svc := &service{
		store:   NewStore(st),
		vault:   vault,
		routes:  routes,
		dial:    dial,
		randGen: seqSuffix(),
	}
	return svc
}

// seqSuffix 产确定后缀(便于断言);每次调用递增,模拟「不同随机值」。
func seqSuffix() func(n int) (string, error) {
	i := 0
	return func(n int) (string, error) {
		i++
		base := []rune("aaaaaa")
		// 把计数编进最后一位,保证多次调用不同。
		base[5] = rune('a' + (i % 26))
		return string(base), nil
	}
}

// --- store CRUD -------------------------------------------------------------

func TestStoreCRUD(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t, stubVault{tokens: map[string]string{"cred-1": "tok"}}, nil, prodDialFactory)

	p, err := svc.Create(ctx, CreateInput{Type: "cloudflare", Name: "CF", CredentialID: "cred-1", BaseDomain: "Example.COM"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if p.BaseDomain != "example.com" {
		t.Fatalf("根域应归一化小写, got %q", p.BaseDomain)
	}

	got, err := svc.Get(ctx, p.ID)
	if err != nil || got.Name != "CF" {
		t.Fatalf("Get: %v / %+v", err, got)
	}
	list, err := svc.List(ctx)
	if err != nil || len(list) != 1 {
		t.Fatalf("List: %v / %d", err, len(list))
	}
	if err := svc.Delete(ctx, p.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := svc.Get(ctx, p.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("删后 Get 应 ErrNotFound, got %v", err)
	}
}

func TestCreateValidation(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t, stubVault{tokens: map[string]string{"cred-1": "tok"}}, nil, prodDialFactory)

	cases := []struct {
		in   CreateInput
		want error
	}{
		{CreateInput{Type: "route53", Name: "x", CredentialID: "cred-1", BaseDomain: "example.com"}, ErrInvalidType},
		{CreateInput{Type: "cloudflare", Name: "", CredentialID: "cred-1", BaseDomain: "example.com"}, ErrEmptyName},
		{CreateInput{Type: "cloudflare", Name: "x", CredentialID: "", BaseDomain: "example.com"}, ErrEmptyCredentialID},
		{CreateInput{Type: "cloudflare", Name: "x", CredentialID: "cred-1", BaseDomain: "*.example.com"}, ErrInvalidBaseDomain},
		{CreateInput{Type: "cloudflare", Name: "x", CredentialID: "cred-1", BaseDomain: "not a domain"}, ErrInvalidBaseDomain},
		{CreateInput{Type: "cloudflare", Name: "x", CredentialID: "no-such-cred", BaseDomain: "example.com"}, ErrCredentialNotFound},
	}
	for i, c := range cases {
		_, err := svc.Create(ctx, c.in)
		if !errors.Is(err, c.want) {
			t.Fatalf("case %d: want %v, got %v", i, c.want, err)
		}
	}
}

// --- Cloudflare 客户端(mock transport)-------------------------------------

func TestCloudflareVerifyZoneSuccess(t *testing.T) {
	rt := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.Header.Get("Authorization") != "Bearer cf-token" {
			t.Fatalf("应带 Bearer token")
		}
		if !strings.Contains(r.URL.String(), "/zones") {
			t.Fatalf("VerifyZone 应查 /zones, got %s", r.URL)
		}
		return jsonResp(200, `{"success":true,"result":[{"id":"zone123","name":"example.com"}]}`), nil
	})
	c := newCloudflareClient("cf-token", rt, "")
	if err := c.VerifyZone(context.Background(), "example.com"); err != nil {
		t.Fatalf("VerifyZone: %v", err)
	}
}

func TestCloudflareVerifyZoneNoPermission(t *testing.T) {
	rt := roundTripFunc(func(_ *http.Request) (*http.Response, error) {
		// 成功但返回空 zone 列表(token 无该 zone 权限)。
		return jsonResp(200, `{"success":true,"result":[]}`), nil
	})
	c := newCloudflareClient("cf-token", rt, "")
	err := c.VerifyZone(context.Background(), "example.com")
	if !errors.Is(err, ErrVerifyFailed) {
		t.Fatalf("无权限应 ErrVerifyFailed, got %v", err)
	}
}

func TestCloudflareAPIError(t *testing.T) {
	rt := roundTripFunc(func(_ *http.Request) (*http.Response, error) {
		return jsonResp(403, `{"success":false,"errors":[{"code":9109,"message":"Invalid access token"}]}`), nil
	})
	c := newCloudflareClient("bad", rt, "")
	err := c.VerifyZone(context.Background(), "example.com")
	if !errors.Is(err, ErrVerifyFailed) {
		t.Fatalf("API 拒绝应 ErrVerifyFailed, got %v", err)
	}
	// token 绝不能出现在错误文本。
	if strings.Contains(err.Error(), "bad") {
		t.Fatalf("错误文本不应含 token: %v", err)
	}
}

func TestCloudflareEnsureARecordCreate(t *testing.T) {
	var posted bool
	rt := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		switch {
		case strings.Contains(r.URL.Path, "/zones") && !strings.Contains(r.URL.Path, "dns_records") && r.Method == http.MethodGet:
			return jsonResp(200, `{"success":true,"result":[{"id":"z1","name":"example.com"}]}`), nil
		case strings.Contains(r.URL.Path, "dns_records") && r.Method == http.MethodGet:
			// 无既有记录 → 应走 POST 新建。
			return jsonResp(200, `{"success":true,"result":[]}`), nil
		case strings.Contains(r.URL.Path, "dns_records") && r.Method == http.MethodPost:
			posted = true
			body, _ := io.ReadAll(r.Body)
			if !strings.Contains(string(body), `"content":"203.0.113.5"`) {
				t.Fatalf("POST 应含目标 IP, got %s", body)
			}
			return jsonResp(200, `{"success":true,"result":{"id":"rec1"}}`), nil
		}
		t.Fatalf("未预期请求 %s %s", r.Method, r.URL)
		return nil, nil
	})
	c := newCloudflareClient("cf-token", rt, "")
	if err := c.EnsureARecord(context.Background(), "example.com", "app-abc.example.com", "203.0.113.5"); err != nil {
		t.Fatalf("EnsureARecord: %v", err)
	}
	if !posted {
		t.Fatalf("无既有记录时应 POST 新建")
	}
}

func TestCloudflareEnsureARecordUpdate(t *testing.T) {
	var putID string
	rt := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		switch {
		case strings.Contains(r.URL.Path, "/zones") && !strings.Contains(r.URL.Path, "dns_records") && r.Method == http.MethodGet:
			return jsonResp(200, `{"success":true,"result":[{"id":"z1","name":"example.com"}]}`), nil
		case strings.Contains(r.URL.Path, "dns_records") && r.Method == http.MethodGet:
			return jsonResp(200, `{"success":true,"result":[{"id":"recOLD","name":"app.example.com","type":"A","content":"1.1.1.1"}]}`), nil
		case strings.Contains(r.URL.Path, "dns_records/recOLD") && r.Method == http.MethodPut:
			putID = "recOLD"
			return jsonResp(200, `{"success":true,"result":{"id":"recOLD"}}`), nil
		}
		t.Fatalf("未预期请求 %s %s", r.Method, r.URL)
		return nil, nil
	})
	c := newCloudflareClient("cf-token", rt, "")
	if err := c.EnsureARecord(context.Background(), "example.com", "app.example.com", "203.0.113.9"); err != nil {
		t.Fatalf("EnsureARecord: %v", err)
	}
	if putID != "recOLD" {
		t.Fatalf("有既有记录应 PUT 更新该记录, got %q", putID)
	}
}

// --- AllocateSubdomain ------------------------------------------------------

// dialDNS 造一个用 mock transport 的 Cloudflare client(供 AllocateSubdomain 测试)。
func dialDNS(rt http.RoundTripper) dialFactory {
	return func(providerType, token string) DNSClient {
		return newDNSClient(providerType, token, rt, "")
	}
}

func TestAllocateSubdomainHappyPath(t *testing.T) {
	ctx := context.Background()
	rt := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		switch {
		case strings.Contains(r.URL.Path, "/zones") && !strings.Contains(r.URL.Path, "dns_records") && r.Method == http.MethodGet:
			return jsonResp(200, `{"success":true,"result":[{"id":"z1","name":"example.com"}]}`), nil
		case strings.Contains(r.URL.Path, "dns_records") && r.Method == http.MethodGet:
			return jsonResp(200, `{"success":true,"result":[]}`), nil
		case strings.Contains(r.URL.Path, "dns_records") && r.Method == http.MethodPost:
			return jsonResp(200, `{"success":true,"result":{"id":"rec1"}}`), nil
		}
		return jsonResp(200, `{"success":true,"result":[]}`), nil
	})
	rc := &stubRouteCreator{nextRouteID: "route-xyz"}
	svc := newTestService(t, stubVault{tokens: map[string]string{"cred-1": "cf-token"}}, rc, dialDNS(rt))

	p, err := svc.Create(ctx, CreateInput{Type: "cloudflare", Name: "CF", CredentialID: "cred-1", BaseDomain: "example.com"})
	if err != nil {
		t.Fatalf("Create provider: %v", err)
	}
	ref, err := svc.AllocateSubdomain(ctx, AllocateInput{
		ProviderID: p.ID, ServerID: "srv-1", UpstreamContainer: "web", UpstreamPort: 8080, HostIP: "203.0.113.5",
	})
	if err != nil {
		t.Fatalf("AllocateSubdomain: %v", err)
	}
	if ref.RouteID != "route-xyz" {
		t.Fatalf("应返回新建路由 id, got %q", ref.RouteID)
	}
	if !strings.HasPrefix(ref.Domain, "app-") || !strings.HasSuffix(ref.Domain, ".example.com") {
		t.Fatalf("子域名形态不符: %q", ref.Domain)
	}
	if len(rc.calls) != 1 || rc.calls[0].DNSProviderID != p.ID {
		t.Fatalf("应建一条带提供商引用的 DNS-01 路由, got %+v", rc.calls)
	}
}

func TestAllocateSubdomainCollisionRetries(t *testing.T) {
	ctx := context.Background()
	rt := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		switch {
		case strings.Contains(r.URL.Path, "/zones") && !strings.Contains(r.URL.Path, "dns_records"):
			return jsonResp(200, `{"success":true,"result":[{"id":"z1","name":"example.com"}]}`), nil
		case r.Method == http.MethodGet:
			return jsonResp(200, `{"success":true,"result":[]}`), nil
		default:
			return jsonResp(200, `{"success":true,"result":{"id":"rec1"}}`), nil
		}
	})
	// 前两次建路由「域名冲突」→ 应重试第三个后缀成功。
	rc := &stubRouteCreator{failFirstN: 2, nextRouteID: "route-final"}
	svc := newTestService(t, stubVault{tokens: map[string]string{"cred-1": "cf-token"}}, rc, dialDNS(rt))

	p, _ := svc.Create(ctx, CreateInput{Type: "cloudflare", Name: "CF", CredentialID: "cred-1", BaseDomain: "example.com"})
	ref, err := svc.AllocateSubdomain(ctx, AllocateInput{
		ProviderID: p.ID, ServerID: "srv-1", UpstreamContainer: "web", UpstreamPort: 80, HostIP: "203.0.113.5",
	})
	if err != nil {
		t.Fatalf("应在重试后成功: %v", err)
	}
	if ref.RouteID != "route-final" {
		t.Fatalf("应返回最终路由 id, got %q", ref.RouteID)
	}
	if len(rc.calls) != 3 {
		t.Fatalf("应尝试 3 次(2 撞车 + 1 成功), got %d", len(rc.calls))
	}
}

func TestAllocateSubdomainRejectsBadHostIP(t *testing.T) {
	ctx := context.Background()
	rc := &stubRouteCreator{}
	svc := newTestService(t, stubVault{tokens: map[string]string{"cred-1": "cf-token"}}, rc, dialDNS(roundTripFunc(func(_ *http.Request) (*http.Response, error) {
		return jsonResp(200, `{"success":true,"result":[]}`), nil
	})))
	p, _ := svc.Create(ctx, CreateInput{Type: "cloudflare", Name: "CF", CredentialID: "cred-1", BaseDomain: "example.com"})
	for _, badIP := range []string{"", "not-an-ip", "2001:db8::1", "example.com"} {
		_, err := svc.AllocateSubdomain(ctx, AllocateInput{
			ProviderID: p.ID, ServerID: "srv-1", UpstreamContainer: "web", UpstreamPort: 80, HostIP: badIP,
		})
		if !errors.Is(err, ErrAllocate) {
			t.Fatalf("非法 hostIP %q 应 ErrAllocate, got %v", badIP, err)
		}
	}
	if len(rc.calls) != 0 {
		t.Fatalf("非法 IP 不应建任何路由")
	}
}

func TestAllocateSubdomainDNSPodNotImplemented(t *testing.T) {
	ctx := context.Background()
	rc := &stubRouteCreator{}
	svc := newTestService(t, stubVault{tokens: map[string]string{"cred-1": "dp-token"}}, rc, prodDialFactory)
	p, _ := svc.Create(ctx, CreateInput{Type: "dnspod", Name: "DP", CredentialID: "cred-1", BaseDomain: "example.com"})
	_, err := svc.AllocateSubdomain(ctx, AllocateInput{
		ProviderID: p.ID, ServerID: "srv-1", UpstreamContainer: "web", UpstreamPort: 80, HostIP: "203.0.113.5",
	})
	if !errors.Is(err, ErrProviderNotImplemented) {
		t.Fatalf("DNSPod 自动建 A 记录应 ErrProviderNotImplemented, got %v", err)
	}
	if len(rc.calls) != 0 {
		t.Fatalf("未建 A 记录就不应建路由")
	}
}

// --- 安全:token 绝不进 DTO/Provider 视图 ----------------------------------

func TestProviderViewHasNoToken(t *testing.T) {
	ctx := context.Background()
	const secret = "super-secret-cf-token"
	svc := newTestService(t, stubVault{tokens: map[string]string{"cred-1": secret}}, nil, prodDialFactory)
	p, err := svc.Create(ctx, CreateInput{Type: "cloudflare", Name: "CF", CredentialID: "cred-1", BaseDomain: "example.com"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	// 领域视图只持 CredentialID 引用,绝无 token 明文。
	if p.CredentialID != "cred-1" {
		t.Fatalf("应持凭据引用")
	}
	// 把整个 Provider 渲染成字符串也不应出现 token。
	if strings.Contains(provString(*p), secret) {
		t.Fatalf("Provider 视图泄漏了 token")
	}
	list, _ := svc.List(ctx)
	for _, pp := range list {
		if strings.Contains(provString(pp), secret) {
			t.Fatalf("List 视图泄漏了 token")
		}
	}
}

func provString(p Provider) string {
	return strings.Join([]string{p.ID, p.Type, p.Name, p.CredentialID, p.BaseDomain}, "|")
}
