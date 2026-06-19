package httpapi

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/huangchengsir/pipewright/internal/auth"
	"github.com/huangchengsir/pipewright/internal/previewenv"
)

// setupPreviewServer 构造带 auth + 预览环境端点的测试 server。
func setupPreviewServer(t *testing.T) (*httptest.Server, *http.Client, string) {
	t.Helper()
	st := testStoreAuth(t)
	svc := auth.NewService(st.DB, nil)
	if err := svc.Bootstrap("admin", "testpass"); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	pv := previewenv.New(st.DB)

	srv := httptest.NewServer(New(testWebFSAuth(), svc, WithPreviewEnvs(pv)))
	t.Cleanup(srv.Close)
	client := newTestClient(t)
	csrf := loginWithClient(t, client, srv.URL)
	return srv, client, csrf
}

func TestPreviewConfigGetPutRoundTrip(t *testing.T) {
	srv, client, csrf := setupPreviewServer(t)

	// 默认未设 → enabled false。
	resp := doJSON(t, client, http.MethodGet, srv.URL+"/api/projects/p1/preview-config", csrf, "")
	body := drain(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET config status %d: %s", resp.StatusCode, body)
	}
	var got map[string]any
	_ = json.Unmarshal([]byte(body), &got)
	if got["enabled"] != false || got["projectId"] != "p1" {
		t.Fatalf("默认配置应未启用: %s", body)
	}

	// PUT 开启(给齐 DNS 提供商 + 根域)。
	resp = doJSON(t, client, http.MethodPut, srv.URL+"/api/projects/p1/preview-config", csrf,
		`{"enabled":true,"dnsProviderId":"prov-1","baseDomain":"preview.example.com"}`)
	body = drain(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("PUT config status %d: %s", resp.StatusCode, body)
	}
	_ = json.Unmarshal([]byte(body), &got)
	if got["enabled"] != true || got["baseDomain"] != "preview.example.com" {
		t.Fatalf("PUT 应启用并存根域: %s", body)
	}
}

func TestPreviewConfigEnableRequiresProvider(t *testing.T) {
	srv, client, csrf := setupPreviewServer(t)
	resp := doJSON(t, client, http.MethodPut, srv.URL+"/api/projects/p1/preview-config", csrf,
		`{"enabled":true,"baseDomain":"preview.example.com"}`) // 缺 dnsProviderId
	body := drain(t, resp)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("开启缺 DNS 提供商应 400, got %d: %s", resp.StatusCode, body)
	}
}

func TestPreviewEnvsListAndReclaim(t *testing.T) {
	srv, client, csrf := setupPreviewServer(t)

	// 初始无环境 → 列空。
	resp := doJSON(t, client, http.MethodGet, srv.URL+"/api/preview-envs?projectId=p1", csrf, "")
	body := drain(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list status %d: %s", resp.StatusCode, body)
	}
	var listed struct {
		Items []map[string]any `json:"items"`
	}
	_ = json.Unmarshal([]byte(body), &listed)
	if len(listed.Items) != 0 {
		t.Fatalf("初始应无预览环境: %s", body)
	}

	// 回收不存在的环境 → 404。
	resp = doJSON(t, client, http.MethodPost, srv.URL+"/api/preview-envs/nope/reclaim", csrf, "")
	body = drain(t, resp)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("回收不存在环境应 404, got %d: %s", resp.StatusCode, body)
	}
}

func drain(t *testing.T, resp *http.Response) string {
	t.Helper()
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	return string(b)
}
