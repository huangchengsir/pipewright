package httpapi

import (
	"encoding/json"
	"github.com/huangchengsir/pipewright/internal/i18n"
	"github.com/huangchengsir/pipewright/internal/selfupdate"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestReviewMaintenanceErrorsUseAllRequestLocales(t *testing.T) {
	updater, fence := reviewAdmission(t)
	if e := fence("locale-install"); e != nil {
		t.Fatal(e)
	}
	cases := []struct {
		name, source, code string
		status             int
		handler            http.Handler
		body               string
	}{
		{"task", "升级任务暂不可用，请核对原任务状态", "update_unavailable", 503, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { updateError(w, selfupdate.ErrUnavailable) }), ""},
		{"decode", "请求格式错误", "bad_request", 400, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { decodeUpdate(w, r, new(selfupdate.Request)) }), "{bad"},
		{"admission", "系统正在安装升级，请稍后重试", "maintenance", 503, updateAdmissionMiddleware(updater)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { t.Error("blocked handler executed") })), ""},
	}
	for _, locale := range i18n.Supported {
		for _, tc := range cases {
			t.Run(locale+"/"+tc.name, func(t *testing.T) {
				req := httptest.NewRequest("POST", "/api/proxy/caddy", strings.NewReader(tc.body))
				req.Header.Set("X-Pipewright-Locale", locale)
				req.Header.Set("Accept-Language", "zh-CN")
				w := httptest.NewRecorder()
				localeMiddleware(tc.handler).ServeHTTP(w, req)
				var body errBody
				if e := json.Unmarshal(w.Body.Bytes(), &body); e != nil {
					t.Fatal(e)
				}
				if w.Code != tc.status || body.Error.Code != tc.code {
					t.Fatalf("localized error changed protocol: %d %s", w.Code, w.Body.String())
				}
				if locale == "zh-CN" {
					if body.Error.Message != tc.source {
						t.Fatal(body.Error.Message)
					}
				} else if body.Error.Message == "" || body.Error.Message == tc.source {
					t.Fatalf("untranslated %s error for %s: %q", tc.name, locale, body.Error.Message)
				}
				if body.Error.Message != i18n.T(locale, tc.source) {
					t.Fatal("request locale writer did not use exact catalog entry")
				}
			})
		}
	}
}
