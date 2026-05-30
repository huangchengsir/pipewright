package httpapi

import (
	"io"
	"net/http"
	"testing"

	"github.com/huangjiawei/devopstool/internal/notify"
	"github.com/huangjiawei/devopstool/internal/run"
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
