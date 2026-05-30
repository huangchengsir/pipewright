package httpapi

import (
	"io"
	"net/http"
	"testing"

	"github.com/huangchengsir/pipewright/internal/notify"
	"github.com/huangchengsir/pipewright/internal/run"
)

// TestRoutesCRUDLifecycle 验证事件路由 CRUD（Story 5.2 / FR-20）：建渠道→建路由→列出→删。
// 非法事件 → 422 invalid_event；引用不存在渠道 → 422 route_channel_not_found。
func TestRoutesCRUDLifecycle(t *testing.T) {
	srv, client, csrf := setupNotifyServer(t, nil)
	chBase := srv.URL + "/api/notifications/channels"
	rtBase := srv.URL + "/api/notifications/routes"

	// 空路由列表。
	resp := doJSON(t, client, http.MethodGet, rtBase, csrf, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list routes status = %d", resp.StatusCode)
	}
	m := decodeMap(t, resp)
	resp.Body.Close()
	if items, _ := m["items"].([]any); items == nil || len(items) != 0 {
		t.Fatalf("初始路由应空: %v", m["items"])
	}

	// 建渠道。
	resp = doJSON(t, client, http.MethodPost, chBase, csrf,
		`{"name":"ops","type":"webhook","enabled":true,"config":{"url":"http://10.0.0.2/hook"}}`)
	if resp.StatusCode != http.StatusCreated {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("create channel = %d (%s)", resp.StatusCode, raw)
	}
	chID, _ := decodeMap(t, resp)["id"].(string)
	resp.Body.Close()

	// 非法事件 → 422 invalid_event。
	resp = doJSON(t, client, http.MethodPost, rtBase, csrf,
		`{"event":"nope","channelId":"`+chID+`"}`)
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("invalid event status = %d", resp.StatusCode)
	}
	errObj, _ := decodeMap(t, resp)["error"].(map[string]any)
	if errObj == nil || errObj["code"] != "invalid_event" {
		t.Fatalf("expected invalid_event code, got %v", errObj)
	}
	resp.Body.Close()

	// 渠道不存在 → 422 route_channel_not_found。
	resp = doJSON(t, client, http.MethodPost, rtBase, csrf,
		`{"event":"build_failed","channelId":"missing"}`)
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("missing channel status = %d", resp.StatusCode)
	}
	resp.Body.Close()

	// 建路由。
	resp = doJSON(t, client, http.MethodPost, rtBase, csrf,
		`{"event":"build_failed","channelId":"`+chID+`","enabled":true}`)
	if resp.StatusCode != http.StatusCreated {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("create route = %d (%s)", resp.StatusCode, raw)
	}
	created := decodeMap(t, resp)
	resp.Body.Close()
	rtID, _ := created["id"].(string)
	if rtID == "" || created["event"] != "build_failed" || created["channelId"] != chID {
		t.Fatalf("unexpected route: %+v", created)
	}

	// 列出 → 1 条。
	resp = doJSON(t, client, http.MethodGet, rtBase, csrf, "")
	items, _ := decodeMap(t, resp)["items"].([]any)
	resp.Body.Close()
	if len(items) != 1 {
		t.Fatalf("expected 1 route, got %d", len(items))
	}

	// 删除 → 204；再删 → 404。
	resp = doJSON(t, client, http.MethodDelete, rtBase+"/"+rtID, csrf, "")
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("delete route = %d", resp.StatusCode)
	}
	resp.Body.Close()
	resp = doJSON(t, client, http.MethodDelete, rtBase+"/"+rtID, csrf, "")
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("delete missing route = %d", resp.StatusCode)
	}
	resp.Body.Close()
}

// TestRoutesProjectScope 验证 HTTP 层项目维度（Story 5.4 / FR-20）：
//   - POST 带 projectId → 项目级路由；GET ?projectId= 仅返回该项目；GET（无 query）仅全局。
func TestRoutesProjectScope(t *testing.T) {
	srv, client, csrf := setupNotifyServer(t, nil)
	chBase := srv.URL + "/api/notifications/channels"
	rtBase := srv.URL + "/api/notifications/routes"

	// 建渠道。
	resp := doJSON(t, client, http.MethodPost, chBase, csrf,
		`{"name":"ops","type":"webhook","enabled":true,"config":{"url":"http://10.0.0.2/hook"}}`)
	if resp.StatusCode != http.StatusCreated {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("create channel = %d (%s)", resp.StatusCode, raw)
	}
	chID, _ := decodeMap(t, resp)["id"].(string)
	resp.Body.Close()

	// 全局路由。
	resp = doJSON(t, client, http.MethodPost, rtBase, csrf,
		`{"event":"build_failed","channelId":"`+chID+`","enabled":true}`)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create global route = %d", resp.StatusCode)
	}
	resp.Body.Close()

	// 项目级路由（projectId=proj-x）。
	resp = doJSON(t, client, http.MethodPost, rtBase, csrf,
		`{"projectId":"proj-x","event":"build_failed","channelId":"`+chID+`","enabled":true}`)
	if resp.StatusCode != http.StatusCreated {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("create project route = %d (%s)", resp.StatusCode, raw)
	}
	pcreated := decodeMap(t, resp)
	resp.Body.Close()
	if pcreated["projectId"] != "proj-x" {
		t.Fatalf("create 应回显 projectId=proj-x，实际 %+v", pcreated)
	}

	// GET（无 query）→ 仅全局 1 条。
	resp = doJSON(t, client, http.MethodGet, rtBase, csrf, "")
	gitems, _ := decodeMap(t, resp)["items"].([]any)
	resp.Body.Close()
	if len(gitems) != 1 {
		t.Fatalf("全局 List 应 1 条，实际 %d", len(gitems))
	}
	if g0, _ := gitems[0].(map[string]any); g0["projectId"] != nil && g0["projectId"] != "" {
		t.Fatalf("全局 List 项不应带 projectId，实际 %+v", g0)
	}

	// GET ?projectId=proj-x → 仅该项目 1 条。
	resp = doJSON(t, client, http.MethodGet, rtBase+"?projectId=proj-x", csrf, "")
	pitems, _ := decodeMap(t, resp)["items"].([]any)
	resp.Body.Close()
	if len(pitems) != 1 {
		t.Fatalf("项目 List 应 1 条，实际 %d", len(pitems))
	}
	if p0, _ := pitems[0].(map[string]any); p0["projectId"] != "proj-x" {
		t.Fatalf("项目 List 项 projectId 应为 proj-x，实际 %+v", p0)
	}

	// GET ?projectId=proj-y（无覆盖）→ 空。
	resp = doJSON(t, client, http.MethodGet, rtBase+"?projectId=proj-y", csrf, "")
	yitems, _ := decodeMap(t, resp)["items"].([]any)
	resp.Body.Close()
	if len(yitems) != 0 {
		t.Fatalf("未覆盖项目 List 应空，实际 %d", len(yitems))
	}
}

// TestRoutesRequireAuth 验证路由端点需认证。
func TestRoutesRequireAuth(t *testing.T) {
	srv, _, _ := setupNotifyServer(t, nil)
	anon := newTestClient(t)
	resp, err := anon.Get(srv.URL + "/api/notifications/routes")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", resp.StatusCode)
	}
}

// TestNotifyHookEventMapping 验证 run 终态 → event 映射（NewNotifyHook 适配器）。
func TestNotifyHookEventMapping(t *testing.T) {
	cases := []struct {
		status string
		event  string
		ok     bool
	}{
		{run.StatusSuccess, notify.EventBuildSucceeded, true},
		{run.StatusFailed, notify.EventBuildFailed, true},
		{run.StatusPartialFailed, notify.EventDeployFailed, true},
		{run.StatusRolledBack, notify.EventRollback, true},
		{run.StatusQueued, "", false},
		{run.StatusRunning, "", false},
	}
	for _, c := range cases {
		ev, ok := eventForStatus(c.status)
		if ok != c.ok || ev != c.event {
			t.Fatalf("status %q → (%q,%v), want (%q,%v)", c.status, ev, ok, c.event, c.ok)
		}
	}
}
