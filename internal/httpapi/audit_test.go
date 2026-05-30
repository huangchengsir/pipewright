package httpapi

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/huangjiawei/devopstool/internal/audit"
	"github.com/huangjiawei/devopstool/internal/auth"
	"github.com/huangjiawei/devopstool/internal/mask"
	"github.com/huangjiawei/devopstool/internal/vault"
)

// setupAuditServer 构造带认证 + vault + audit 的测试 server。
func setupAuditServer(t *testing.T) (*httptest.Server, *http.Client, string, audit.Recorder) {
	t.Helper()
	st := testStoreAuth(t)
	svc := auth.NewService(st.DB, nil)
	if err := svc.Bootstrap("admin", "testpass"); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	v := vault.New(st.DB, testMasterKey())
	rec := audit.New(st.DB, mask.NewMasker(), nil)
	srv := httptest.NewServer(New(testWebFSAuth(), svc, WithVault(v), WithAudit(rec)))
	t.Cleanup(srv.Close)

	client := newTestClient(t)
	csrf := loginWithClient(t, client, srv.URL)
	return srv, client, csrf, rec
}

// TestAuditRequiresAuth GET /api/audit 未认证 → 401。
func TestAuditRequiresAuth(t *testing.T) {
	srv, _, _, _ := setupAuditServer(t)
	resp, err := http.Get(srv.URL + "/api/audit")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("未认证应 401, got %d", resp.StatusCode)
	}
}

// TestCredentialCreateProducesAuditEntry 真实写操作 → GET /api/audit 见
// credential_create 一行,且 detail 不含明文 secret。
func TestCredentialCreateProducesAuditEntry(t *testing.T) {
	srv, client, csrf, _ := setupAuditServer(t)

	const secret = "ghp_plaintext_marker_secret_value_999"
	body := `{"name":"ci deploy","type":"git_token","scope":"prod","secret":"` + secret + `"}`
	resp := doJSON(t, client, http.MethodPost, srv.URL+"/api/credentials", csrf, body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create status = %d, want 201", resp.StatusCode)
	}

	auditResp := doJSON(t, client, http.MethodGet, srv.URL+"/api/audit", csrf, "")
	defer auditResp.Body.Close()
	if auditResp.StatusCode != http.StatusOK {
		t.Fatalf("audit status = %d, want 200", auditResp.StatusCode)
	}
	raw, _ := io.ReadAll(auditResp.Body)
	if strings.Contains(string(raw), secret) {
		t.Fatalf("审计响应泄漏明文 secret: %s", raw)
	}

	var out struct {
		Entries []struct {
			Action     string         `json:"action"`
			TargetType string         `json:"targetType"`
			TargetID   string         `json:"targetId"`
			Actor      string         `json:"actor"`
			Detail     map[string]any `json:"detail"`
			Timestamp  string         `json:"timestamp"`
			IP         string         `json:"ip"`
		} `json:"entries"`
		NextBefore *string `json:"nextBefore"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("decode audit: %v (%s)", err, raw)
	}
	if len(out.Entries) != 1 {
		t.Fatalf("应有 1 条审计, got %d", len(out.Entries))
	}
	e := out.Entries[0]
	if e.Action != "credential_create" || e.TargetType != "credential" {
		t.Fatalf("审计字段错: %+v", e)
	}
	if e.Actor != "admin" {
		t.Fatalf("actor = %q, want admin", e.Actor)
	}
	if e.TargetID == "" || e.Timestamp == "" {
		t.Fatalf("targetId/timestamp 不应为空: %+v", e)
	}
	// detail 仅元数据(name/type/scope),绝不含 secret 字段或明文。
	if _, ok := e.Detail["secret"]; ok {
		t.Fatalf("detail 不应含 secret 字段: %v", e.Detail)
	}
	if e.Detail["name"] != "ci deploy" {
		t.Fatalf("detail.name 错: %v", e.Detail)
	}
	if out.NextBefore != nil {
		t.Fatalf("仅一条时 nextBefore 应为 null, got %v", *out.NextBefore)
	}
}

// TestAuditFilterByAction 过滤 action 参数生效。
func TestAuditFilterByAction(t *testing.T) {
	srv, client, csrf, rec := setupAuditServer(t)
	ctx := t.Context()

	// 直接经 Recorder 注入两类审计(避免依赖多个 handler 装配)。
	_ = rec.Record(ctx, audit.Entry{Actor: "admin", Action: audit.ActionProjectCreate, TargetType: audit.TargetProject, TargetID: "p1"})
	_ = rec.Record(ctx, audit.Entry{Actor: "admin", Action: audit.ActionCredentialDelete, TargetType: audit.TargetCredential, TargetID: "c1"})

	resp := doJSON(t, client, http.MethodGet, srv.URL+"/api/audit?action=project_create", csrf, "")
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var out struct {
		Entries []struct {
			Action string `json:"action"`
		} `json:"entries"`
	}
	_ = json.Unmarshal(raw, &out)
	if len(out.Entries) != 1 || out.Entries[0].Action != "project_create" {
		t.Fatalf("action 过滤失败: %s", raw)
	}
}

// TestAuditPaginationCursor limit + nextBefore 游标翻页。
func TestAuditPaginationCursor(t *testing.T) {
	srv, client, csrf, rec := setupAuditServer(t)
	ctx := t.Context()
	for i := 0; i < 5; i++ {
		_ = rec.Record(ctx, audit.Entry{Actor: "admin", Action: audit.ActionProjectCreate, TargetType: audit.TargetProject, TargetID: "p"})
	}

	// 第一页 limit=2 → 2 条 + nextBefore 非 null。
	resp := doJSON(t, client, http.MethodGet, srv.URL+"/api/audit?limit=2", csrf, "")
	raw, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	var page struct {
		Entries    []struct{ ID string } `json:"entries"`
		NextBefore *string               `json:"nextBefore"`
	}
	_ = json.Unmarshal(raw, &page)
	if len(page.Entries) != 2 || page.NextBefore == nil {
		t.Fatalf("第一页应 2 条 + nextBefore: %s", raw)
	}

	// 第二页用游标。
	resp = doJSON(t, client, http.MethodGet, srv.URL+"/api/audit?limit=2&before="+*page.NextBefore, csrf, "")
	raw2, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	var page2 struct {
		Entries []struct{ ID string } `json:"entries"`
	}
	_ = json.Unmarshal(raw2, &page2)
	if len(page2.Entries) != 2 {
		t.Fatalf("第二页应 2 条: %s", raw2)
	}
	if page2.Entries[0].ID == page.Entries[0].ID {
		t.Fatal("游标翻页出现重叠")
	}
}
