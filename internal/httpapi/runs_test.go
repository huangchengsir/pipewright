package httpapi

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/huangchengsir/pipewright/internal/auth"
	"github.com/huangchengsir/pipewright/internal/project"
	"github.com/huangchengsir/pipewright/internal/run"
	"github.com/huangchengsir/pipewright/internal/vault"
)

// setupRunServer 构造带 auth + project + run(含 worker pool)的测试 server,返回项目 id。
func setupRunServer(t *testing.T) (*httptest.Server, *http.Client, string, string, run.Service) {
	t.Helper()
	st := testStoreAuth(t)
	svc := auth.NewService(st.DB, nil)
	if err := svc.Bootstrap("admin", "testpass"); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	v := vault.New(st.DB, testMasterKey())
	psvc := project.New(st.DB, v, stubProber{branch: "main"})
	rsvc := run.New(st.DB)
	pool := run.NewWorkerPool(rsvc)
	pool.Start()
	t.Cleanup(func() { pool.Stop(context.Background()) })

	srv := httptest.NewServer(New(testWebFSAuth(), svc,
		WithVault(v), WithProjects(psvc), WithRuns(rsvc, pool)))
	t.Cleanup(srv.Close)

	client := newTestClient(t)
	csrf := loginWithClient(t, client, srv.URL)

	credID := newGitCred(t, client, srv.URL, csrf, "ghp_token_for_run")
	resp := doJSON(t, client, http.MethodPost, srv.URL+"/api/projects", csrf,
		`{"name":"acme","repoUrl":"https://gitee.com/acme/shop.git","credentialId":"`+credID+`"}`)
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var p map[string]any
	_ = json.Unmarshal(raw, &p)
	projID, _ := p["id"].(string)
	if projID == "" {
		t.Fatalf("create project failed: %s", raw)
	}
	return srv, client, csrf, projID, rsvc
}

// waitRunStatus 经 GET /api/runs/{id} 轮询直到达到期望状态(降级轮询路径也被覆盖)。
func waitRunStatus(t *testing.T, client *http.Client, srv *httptest.Server, csrf, id, want string) map[string]any {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	var last map[string]any
	for time.Now().Before(deadline) {
		resp := doJSON(t, client, http.MethodGet, srv.URL+"/api/runs/"+id, csrf, "")
		raw, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		last = map[string]any{}
		_ = json.Unmarshal(raw, &last)
		if last["status"] == want {
			return last
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("run %s 未达到 %q, last=%v", id, want, last)
	return nil
}

// TestRunDetailDTOShape 验证 GET /api/runs/{id} 严格遵守冻结 DTO:targets/diagnosis=null,steps 存在。
func TestRunDetailDTOShape(t *testing.T) {
	srv, client, csrf, projID, rsvc := setupRunServer(t)
	r, err := rsvc.Create(context.Background(), projID, run.Trigger{Type: run.TriggerManual, Branch: "main", Actor: "admin"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	dto := waitRunStatus(t, client, srv, csrf, r.ID, run.StatusSuccess)

	// 冻结字段存在性。
	for _, k := range []string{"id", "projectId", "projectName", "status", "trigger", "steps", "createdAt", "startedAt", "finishedAt", "durationMs", "targets", "diagnosis"} {
		if _, ok := dto[k]; !ok {
			t.Fatalf("DTO 缺字段 %q: %v", k, dto)
		}
	}
	// targets/diagnosis 必须为 null(本期只填 null 块)。
	if dto["targets"] != nil {
		t.Fatalf("targets 本期应为 null, got %v", dto["targets"])
	}
	if dto["diagnosis"] != nil {
		t.Fatalf("diagnosis 本期应为 null, got %v", dto["diagnosis"])
	}
	if dto["projectName"] != "acme" {
		t.Fatalf("projectName 应 join 项目名 acme, got %v", dto["projectName"])
	}
	trig, _ := dto["trigger"].(map[string]any)
	if trig["type"] != "manual" || trig["branch"] != "main" || trig["actor"] != "admin" {
		t.Fatalf("trigger 块不符: %v", trig)
	}
	steps, _ := dto["steps"].([]any)
	if len(steps) == 0 {
		t.Fatalf("success run 应有步骤")
	}
	step0, _ := steps[0].(map[string]any)
	for _, k := range []string{"id", "name", "status", "startedAt", "finishedAt", "durationMs"} {
		if _, ok := step0[k]; !ok {
			t.Fatalf("step DTO 缺字段 %q: %v", k, step0)
		}
	}
}

// TestRunListPaging 验证 GET /api/runs 列表筛选 + 分页,且列表行不含 steps。
func TestRunListPaging(t *testing.T) {
	srv, client, csrf, projID, rsvc := setupRunServer(t)
	for i := 0; i < 3; i++ {
		if _, err := rsvc.Create(context.Background(), projID, run.Trigger{Type: run.TriggerManual}); err != nil {
			t.Fatalf("Create: %v", err)
		}
	}

	resp := doJSON(t, client, http.MethodGet, srv.URL+"/api/runs?projectId="+projID+"&page=1", csrf, "")
	raw, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	var list map[string]any
	_ = json.Unmarshal(raw, &list)
	if list["total"].(float64) != 3 {
		t.Fatalf("total 应 3, got %v", list["total"])
	}
	items, _ := list["items"].([]any)
	if len(items) != 3 {
		t.Fatalf("items 应 3, got %d", len(items))
	}
	row0, _ := items[0].(map[string]any)
	if _, ok := row0["steps"]; ok {
		t.Fatal("列表行不应含 steps")
	}
	if _, ok := row0["targets"]; ok {
		t.Fatal("列表行不应含 targets")
	}
	if _, ok := row0["durationMs"]; !ok {
		t.Fatal("列表行应含 durationMs")
	}
}

// TestRunListInvalidPage 验证非法/越界 page → 400(不静默回退,不溢出 OFFSET)。
func TestRunListInvalidPage(t *testing.T) {
	srv, client, csrf, projID, _ := setupRunServer(t)
	for _, p := range []string{"abc", "0", "-1", "99999999999999999999"} {
		resp := doJSON(t, client, http.MethodGet, srv.URL+"/api/runs?projectId="+projID+"&page="+p, csrf, "")
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("page=%q 应 400, got %d", p, resp.StatusCode)
		}
	}
	// 极大但语法合法的 page(超上界)→ 400(防 OFFSET 溢出)。
	resp := doJSON(t, client, http.MethodGet, srv.URL+"/api/runs?projectId="+projID+"&page=100001", csrf, "")
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("page 超上界应 400, got %d", resp.StatusCode)
	}
	// 合法 page=1 仍 200。
	ok := doJSON(t, client, http.MethodGet, srv.URL+"/api/runs?projectId="+projID+"&page=1", csrf, "")
	_ = ok.Body.Close()
	if ok.StatusCode != http.StatusOK {
		t.Fatalf("page=1 应 200, got %d", ok.StatusCode)
	}
}

// TestSSETerminalSnapshotEndsStream 验证已终态 run 的 SSE:发完初始帧立即收流(不挂起)。
func TestSSETerminalSnapshotEndsStream(t *testing.T) {
	srv, client, csrf, projID, rsvc := setupRunServer(t)
	r, _ := rsvc.Create(context.Background(), projID, run.Trigger{Type: run.TriggerManual})
	// 等到 run 终态后再连 SSE:快照即终态,handler 应发完初始 status 帧即收流(EOF)。
	waitRunStatus(t, client, srv, csrf, r.ID, run.StatusSuccess)

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/runs/"+r.ID+"/events", nil)
	req.Header.Set("X-CSRF-Token", csrf)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("SSE GET: %v", err)
	}
	defer resp.Body.Close()

	// 读到 EOF(流结束)应在短时间内发生,而非挂到心跳超时。
	done := make(chan struct{})
	var body []byte
	go func() {
		defer close(done)
		body, _ = io.ReadAll(resp.Body)
	}()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("终态 SSE 未及时收流(应发完初始帧即结束)")
	}
	if !strings.Contains(string(body), "event: status") {
		t.Fatalf("终态 SSE 应至少含一个 status 帧, got %q", body)
	}
}

// TestRunGetNotFound 验证未知 id → 404。
func TestRunGetNotFound(t *testing.T) {
	srv, client, csrf, _, _ := setupRunServer(t)
	resp := doJSON(t, client, http.MethodGet, srv.URL+"/api/runs/nope", csrf, "")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
}

// TestRunRequiresAuth 验证未登录访问运行端点 → 401(SSE 也认证)。
func TestRunRequiresAuth(t *testing.T) {
	srv, _, _, _, _ := setupRunServer(t)
	anon := newTestClient(t)
	for _, path := range []string{"/api/runs", "/api/runs/x", "/api/runs/x/events"} {
		resp, err := anon.Get(srv.URL + path)
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("%s 未认证应 401, got %d", path, resp.StatusCode)
		}
	}
}

// TestCancelRequiresCSRF 验证取消(写方法)缺 CSRF → 403。
func TestCancelRequiresCSRF(t *testing.T) {
	srv, client, csrf, projID, rsvc := setupRunServer(t)
	r, _ := rsvc.Create(context.Background(), projID, run.Trigger{Type: run.TriggerManual})

	// 缺 CSRF header。
	resp := doJSON(t, client, http.MethodPost, srv.URL+"/api/runs/"+r.ID+"/cancel", "", "")
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("缺 CSRF 应 403, got %d", resp.StatusCode)
	}

	// 带 CSRF:对终态运行取消应 409(success 后)或成功取消 running/queued。
	got := waitRunStatus(t, client, srv, csrf, r.ID, run.StatusSuccess)
	if got["status"] != run.StatusSuccess {
		t.Fatalf("应已 success")
	}
	resp2 := doJSON(t, client, http.MethodPost, srv.URL+"/api/runs/"+r.ID+"/cancel", csrf, "")
	_ = resp2.Body.Close()
	if resp2.StatusCode != http.StatusConflict {
		t.Fatalf("终态取消应 409, got %d", resp2.StatusCode)
	}
}

// TestSSEReadsStatusEvent 验证 SSE 端点输出 text/event-stream 且能读到 status 事件。
func TestSSEReadsStatusEvent(t *testing.T) {
	srv, client, csrf, projID, rsvc := setupRunServer(t)
	r, _ := rsvc.Create(context.Background(), projID, run.Trigger{Type: run.TriggerManual})

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/runs/"+r.ID+"/events", nil)
	req.Header.Set("X-CSRF-Token", csrf)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("SSE GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("SSE status = %d, want 200", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		t.Fatalf("Content-Type = %q, want text/event-stream", ct)
	}

	// 读若干帧,直到见到一个 status 事件(初始快照即为 status)。
	sc := bufio.NewScanner(resp.Body)
	sawStatus := false
	done := make(chan struct{})
	go func() {
		defer close(done)
		for sc.Scan() {
			line := sc.Text()
			if strings.HasPrefix(line, "event: status") {
				sawStatus = true
				return
			}
		}
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("未在期限内读到 status 事件")
	}
	if !sawStatus {
		t.Fatal("应读到至少一个 status 事件")
	}
}
