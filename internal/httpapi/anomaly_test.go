package httpapi

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/huangchengsir/pipewright/internal/anomaly"
	"github.com/huangchengsir/pipewright/internal/auth"
	"github.com/huangchengsir/pipewright/internal/target"
	"github.com/huangchengsir/pipewright/internal/vault"
)

// setupAnomalyAPI 构造带 auth + vault + target + anomaly 的测试 server。
// anomaly 的 collector 复用同一 target.Service(经 collectServerMetrics 采集),与 main.go 一致。
func setupAnomalyAPI(t *testing.T, dialer target.SSHDialer) (*httptest.Server, *http.Client, string) {
	t.Helper()
	st := testStoreAuth(t)
	svc := auth.NewService(st.DB, nil)
	if err := svc.Bootstrap("admin", "testpass"); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	v := vault.New(st.DB, testMasterKey())
	tsvc := target.New(st.DB, v, dialer)
	anomalySvc := anomaly.New(st.DB, NewAnomalyCollector(tsvc))
	srv := httptest.NewServer(New(testWebFSAuth(), svc,
		WithVault(v), WithServers(tsvc), WithAnomaly(anomalySvc)))
	t.Cleanup(srv.Close)

	client := newTestClient(t)
	csrf := loginWithClient(t, client, srv.URL)
	return srv, client, csrf
}

// createRuleAPI 经 API 建一条规则,返回完整响应 map。
func createRuleAPI(t *testing.T, client *http.Client, srvURL, csrf, body string) (int, map[string]any) {
	t.Helper()
	resp := doJSON(t, client, http.MethodPost, srvURL+"/api/anomaly/rules", csrf, body)
	raw, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	var m map[string]any
	_ = json.Unmarshal(raw, &m)
	return resp.StatusCode, m
}

func TestAnomalyRuleCRUD(t *testing.T) {
	srv, client, csrf := setupAnomalyAPI(t, linuxLikeDialer())

	// Create global cpu rule.
	code, rule := createRuleAPI(t, client, srv.URL, csrf, `{"metric":"cpu","operator":"gt","threshold":90}`)
	if code != http.StatusCreated {
		t.Fatalf("create status = %d, want 201: %+v", code, rule)
	}
	id, _ := rule["id"].(string)
	if id == "" {
		t.Fatalf("no id: %+v", rule)
	}
	if rule["serverId"] != nil {
		t.Fatalf("global rule serverId should be null: %+v", rule["serverId"])
	}
	if rule["enabled"] != true {
		t.Fatalf("default enabled should be true: %+v", rule)
	}

	// List.
	lresp := doJSON(t, client, http.MethodGet, srv.URL+"/api/anomaly/rules", csrf, "")
	lraw, _ := io.ReadAll(lresp.Body)
	lresp.Body.Close()
	var list struct {
		Items []map[string]any `json:"items"`
	}
	_ = json.Unmarshal(lraw, &list)
	if len(list.Items) != 1 {
		t.Fatalf("want 1 rule, got %d", len(list.Items))
	}

	// Delete.
	dresp := doJSON(t, client, http.MethodDelete, srv.URL+"/api/anomaly/rules/"+id, csrf, "")
	dresp.Body.Close()
	if dresp.StatusCode != http.StatusNoContent {
		t.Fatalf("delete status = %d, want 204", dresp.StatusCode)
	}
	// Delete again → 404.
	d2 := doJSON(t, client, http.MethodDelete, srv.URL+"/api/anomaly/rules/"+id, csrf, "")
	d2.Body.Close()
	if d2.StatusCode != http.StatusNotFound {
		t.Fatalf("second delete = %d, want 404", d2.StatusCode)
	}
}

func TestAnomalyRuleInvalidEnum(t *testing.T) {
	srv, client, csrf := setupAnomalyAPI(t, linuxLikeDialer())
	code, body := createRuleAPI(t, client, srv.URL, csrf, `{"metric":"bogus","operator":"gt","threshold":90}`)
	if code != http.StatusBadRequest {
		t.Fatalf("bad metric status = %d, want 400: %+v", code, body)
	}
	code2, _ := createRuleAPI(t, client, srv.URL, csrf, `{"metric":"cpu","operator":"ge","threshold":90}`)
	if code2 != http.StatusBadRequest {
		t.Fatalf("bad operator status = %d, want 400", code2)
	}
}

// TestAnomalyCheckDiskHits 端到端真验:disk gt 1 必命中(linux dialer 磁盘 ~25%)→ 产告警。
func TestAnomalyCheckDiskHits(t *testing.T) {
	srv, client, csrf := setupAnomalyAPI(t, linuxLikeDialer())
	credID := newSSHCredAPI(t, client, srv.URL, csrf, "pw")
	_ = createServerAPI(t, client, srv.URL, csrf, credID)

	// disk gt 1 → 必命中。
	createRuleAPI(t, client, srv.URL, csrf, `{"metric":"disk","operator":"gt","threshold":1}`)
	// cpu gt 9999 → 不命中。
	createRuleAPI(t, client, srv.URL, csrf, `{"metric":"cpu","operator":"gt","threshold":9999}`)

	resp := doJSON(t, client, http.MethodPost, srv.URL+"/api/anomaly/check", csrf, "")
	raw, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("check status = %d, want 200: %s", resp.StatusCode, raw)
	}
	var out struct {
		Alerts []anomalyAlertDTO `json:"alerts"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("unmarshal: %v: %s", err, raw)
	}
	if len(out.Alerts) != 1 {
		t.Fatalf("want exactly 1 alert (disk), got %d: %+v", len(out.Alerts), out.Alerts)
	}
	a := out.Alerts[0]
	if a.Metric != "disk" || a.Value <= a.Threshold {
		t.Fatalf("disk alert wrong: %+v", a)
	}

	// GET alerts 列表可见。
	aresp := doJSON(t, client, http.MethodGet, srv.URL+"/api/anomaly/alerts?limit=10", csrf, "")
	araw, _ := io.ReadAll(aresp.Body)
	aresp.Body.Close()
	var alist struct {
		Alerts []anomalyAlertDTO `json:"alerts"`
	}
	_ = json.Unmarshal(araw, &alist)
	if len(alist.Alerts) != 1 || alist.Alerts[0].Metric != "disk" {
		t.Fatalf("alerts list wrong: %+v", alist.Alerts)
	}
}

// TestAnomalyCheckUnreachableSkipped:不可达服务器跳过(不误报)。
func TestAnomalyCheckUnreachableSkipped(t *testing.T) {
	srv, client, csrf := setupAnomalyAPI(t, stubDialer{err: target.ErrUnreachable})
	credID := newSSHCredAPI(t, client, srv.URL, csrf, "pw")
	_ = createServerAPI(t, client, srv.URL, csrf, credID)
	createRuleAPI(t, client, srv.URL, csrf, `{"metric":"disk","operator":"gt","threshold":1}`)

	resp := doJSON(t, client, http.MethodPost, srv.URL+"/api/anomaly/check", csrf, "")
	raw, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	var out struct {
		Alerts []anomalyAlertDTO `json:"alerts"`
	}
	_ = json.Unmarshal(raw, &out)
	if len(out.Alerts) != 0 {
		t.Fatalf("unreachable server must be skipped, got %+v", out.Alerts)
	}
}

// TestAnomalyCheckNoRules:无规则 → check 返回空 alerts、不报错。
func TestAnomalyCheckNoRules(t *testing.T) {
	srv, client, csrf := setupAnomalyAPI(t, linuxLikeDialer())
	resp := doJSON(t, client, http.MethodPost, srv.URL+"/api/anomaly/check", csrf, "")
	raw, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("no-rules check status = %d, want 200: %s", resp.StatusCode, raw)
	}
	var out struct {
		Alerts []anomalyAlertDTO `json:"alerts"`
	}
	_ = json.Unmarshal(raw, &out)
	if len(out.Alerts) != 0 {
		t.Fatalf("want empty alerts, got %+v", out.Alerts)
	}
}

func TestAnomalyRequiresAuth(t *testing.T) {
	srv, _, _ := setupAnomalyAPI(t, linuxLikeDialer())
	bare := &http.Client{}
	resp, _ := bare.Get(srv.URL + "/api/anomaly/rules")
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("rules status = %d, want 401", resp.StatusCode)
	}
	resp2, _ := bare.Get(srv.URL + "/api/anomaly/alerts")
	resp2.Body.Close()
	if resp2.StatusCode != http.StatusUnauthorized {
		t.Fatalf("alerts status = %d, want 401", resp2.StatusCode)
	}
}

// TestAnomalyCheckRequiresCSRF:写方法缺 CSRF → 403。
func TestAnomalyCheckRequiresCSRF(t *testing.T) {
	srv, client, _ := setupAnomalyAPI(t, linuxLikeDialer())
	// 不带 X-CSRF-Token 头的写请求。
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/api/anomaly/check", nil)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("check without csrf = %d, want 403", resp.StatusCode)
	}
}
