package httpapi

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/huangchengsir/pipewright/internal/auth"
	"github.com/huangchengsir/pipewright/internal/proxy"
)

// stubProxyService 是不触网的 proxy.Service 桩,捕获 Update 入参、按需返回固定路由。
type stubProxyService struct {
	updateIn    proxy.UpdateInput
	updateID    string
	updateRoute proxy.Route
	updateErr   error
	overview    []proxy.RouteWithServer
}

func (s *stubProxyService) List(context.Context, string) ([]proxy.Route, error) { return nil, nil }
func (s *stubProxyService) Create(context.Context, proxy.CreateInput) (*proxy.Route, error) {
	return nil, nil
}
func (s *stubProxyService) Delete(context.Context, string) error           { return nil }
func (s *stubProxyService) SetEnabled(context.Context, string, bool) error { return nil }
func (s *stubProxyService) RefreshStatus(context.Context, string) (*proxy.Route, error) {
	return nil, nil
}
func (s *stubProxyService) Update(_ context.Context, id string, in proxy.UpdateInput) (*proxy.Route, error) {
	s.updateID = id
	s.updateIn = in
	if s.updateErr != nil {
		return nil, s.updateErr
	}
	r := s.updateRoute
	return &r, nil
}
func (s *stubProxyService) Overview(context.Context) ([]proxy.RouteWithServer, error) {
	return s.overview, nil
}

func setupProxyServer(t *testing.T, px proxy.Service) (*httptest.Server, *http.Client, string) {
	t.Helper()
	st := testStoreAuth(t)
	svc := auth.NewService(st.DB, nil)
	if err := svc.Bootstrap("admin", "testpass"); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	srv := httptest.NewServer(New(testWebFSAuth(), svc, WithProxy(px)))
	t.Cleanup(srv.Close)
	client := newTestClient(t)
	csrf := loginWithClient(t, client, srv.URL)
	return srv, client, csrf
}

// TestProxyUpdate_HashOutOfDTO 验证 PUT /api/proxy/routes/{id} 返回的 DTO 绝无 bcrypt 哈希/明文口令,
// 且口令明文经入参传给领域层(由领域层 bcrypt)。
func TestProxyUpdate_HashOutOfDTO(t *testing.T) {
	now := time.Now().UTC()
	px := &stubProxyService{
		updateRoute: proxy.Route{
			ID: "r1", ServerID: "srv-1", Domain: "u.example.com",
			UpstreamContainer: "web", UpstreamPort: 8080, TLSMode: "auto", Enabled: true,
			CertStatus: "pending",
			Config: proxy.RouteConfig{
				Aliases:       []string{"www.u.example.com"},
				HSTS:          true,
				BasicAuthUser: "admin",
				BasicAuthHash: "$2a$10$SECRETHASHvalue", // 领域层有哈希,但 DTO 必须剥离
			},
			CreatedAt: now, UpdatedAt: now,
		},
	}
	srv, client, csrf := setupProxyServer(t, px)

	body := `{"upstreamContainer":"web","upstreamPort":8080,"basicAuthPassword":"s3cret","config":{"aliases":["www.u.example.com"],"hsts":true,"basicAuthUser":"admin","ipAllow":["10.0.0.0/8"],"redirects":[{"from":"/old","to":"/new","status":308}]}}`
	resp := doJSON(t, client, http.MethodPut, srv.URL+"/api/proxy/routes/r1", csrf, body)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 200 (%s)", resp.StatusCode, raw)
	}
	raw, _ := io.ReadAll(resp.Body)

	// 哈希/明文口令绝不出现在响应体。
	if strings.Contains(string(raw), "SECRETHASH") || strings.Contains(string(raw), "s3cret") {
		t.Fatalf("响应体泄露哈希/口令: %s", raw)
	}
	var dto proxyRouteDTO
	if err := json.Unmarshal(raw, &dto); err != nil {
		t.Fatalf("unmarshal: %v (%s)", err, raw)
	}
	if !dto.Config.BasicAuthEnabled {
		t.Fatalf("basicAuthEnabled 应为 true(有哈希)")
	}
	if dto.Config.BasicAuthUser != "admin" {
		t.Fatalf("basicAuthUser 应为 admin, got %q", dto.Config.BasicAuthUser)
	}
	if len(dto.Config.Aliases) != 1 || dto.Config.Aliases[0] != "www.u.example.com" {
		t.Fatalf("aliases 不符: %+v", dto.Config.Aliases)
	}

	// 入参把明文口令透传给领域层(由领域层哈希)。
	if px.updateID != "r1" {
		t.Fatalf("update id 不符: %q", px.updateID)
	}
	if px.updateIn.BasicAuthPassword != "s3cret" {
		t.Fatalf("明文口令应透传领域层, got %q", px.updateIn.BasicAuthPassword)
	}
	if px.updateIn.Config.BasicAuthUser != "admin" {
		t.Fatalf("用户名应透传, got %q", px.updateIn.Config.BasicAuthUser)
	}
}

// TestProxyUpdate_ValidationError 验证领域层校验错误映射为 400。
func TestProxyUpdate_ValidationError(t *testing.T) {
	px := &stubProxyService{updateErr: proxy.ErrInvalidCIDR}
	srv, client, csrf := setupProxyServer(t, px)
	resp := doJSON(t, client, http.MethodPut, srv.URL+"/api/proxy/routes/r1", csrf,
		`{"config":{"ipAllow":["bogus"]}}`)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
}

// TestProxyOverview 验证 GET /api/proxy/overview 返回 items(含 serverName,无哈希)。
func TestProxyOverview(t *testing.T) {
	now := time.Now().UTC()
	px := &stubProxyService{
		overview: []proxy.RouteWithServer{
			{
				Route: proxy.Route{
					ID: "r1", ServerID: "srv-1", Domain: "o.example.com",
					UpstreamContainer: "web", UpstreamPort: 80, TLSMode: "auto",
					Enabled: true, CertStatus: "issued",
					Config:    proxy.RouteConfig{BasicAuthUser: "admin", BasicAuthHash: "$2a$10$HIDDEN"},
					CreatedAt: now, UpdatedAt: now,
				},
				ServerName: "生产机",
			},
		},
	}
	srv, client, csrf := setupProxyServer(t, px)
	resp := doJSON(t, client, http.MethodGet, srv.URL+"/api/proxy/overview", csrf, "")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	raw, _ := io.ReadAll(resp.Body)
	if strings.Contains(string(raw), "HIDDEN") {
		t.Fatalf("overview 泄露哈希: %s", raw)
	}
	var body struct {
		Items []proxyOverviewItemDTO `json:"items"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("unmarshal: %v (%s)", err, raw)
	}
	if len(body.Items) != 1 {
		t.Fatalf("应有 1 项, got %d", len(body.Items))
	}
	it := body.Items[0]
	if it.ServerName != "生产机" {
		t.Fatalf("serverName 不符: %q", it.ServerName)
	}
	if it.CertStatus != "issued" || it.Domain != "o.example.com" {
		t.Fatalf("路由字段不符: %+v", it)
	}
	if !it.Config.BasicAuthEnabled {
		t.Fatalf("basicAuthEnabled 应为 true")
	}
}

// TestProxyUpdate_Unconfigured503 验证未注入 proxy 时端点 503。
func TestProxyUpdate_Unconfigured503(t *testing.T) {
	st := testStoreAuth(t)
	svc := auth.NewService(st.DB, nil)
	if err := svc.Bootstrap("admin", "testpass"); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	srv := httptest.NewServer(New(testWebFSAuth(), svc)) // 不注入 WithProxy
	t.Cleanup(srv.Close)
	client := newTestClient(t)
	csrf := loginWithClient(t, client, srv.URL)

	resp := doJSON(t, client, http.MethodPut, srv.URL+"/api/proxy/routes/r1", csrf, `{"config":{}}`)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", resp.StatusCode)
	}
}
