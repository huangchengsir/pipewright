package httpapi

import (
	"encoding/json"
	"github.com/huangchengsir/pipewright/internal/configstate"
	"github.com/huangchengsir/pipewright/internal/i18n"
	"github.com/huangchengsir/pipewright/internal/tenancy"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAuthorizationErrorsAllLocales(t *testing.T) {
	for _, tc := range []struct {
		err         error
		key, source string
		status      int
	}{
		{tenancy.ErrForbidden, "access_denied", "没有此操作的权限", 403},
		{tenancy.ErrConflict, "permission_state_changed", "权限状态已变化，请刷新后重试", 409},
		{configstate.ErrMaintenance, "maintenance_active", "维护期间暂不能修改配置", 409},
	} {
		for _, locale := range i18n.Supported {
			t.Run(tc.key+"/"+locale, func(t *testing.T) {
				h := localeMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if !writeAuthorizationError(w, tc.err) {
						t.Fatal("unmapped error")
					}
				}))
				req := httptest.NewRequest("GET", "/", nil)
				req.Header.Set("X-Pipewright-Locale", locale)
				w := httptest.NewRecorder()
				h.ServeHTTP(w, req)
				var body errBody
				if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
					t.Fatal(err)
				}
				if w.Code != tc.status || body.Error.Code != tc.key || body.Error.Message == "" {
					t.Fatal(w.Code, w.Body.String())
				}
				if locale != "zh-CN" && body.Error.Message == tc.source {
					t.Fatal("missing locale", locale)
				}
			})
		}
	}
}
