package httpapi

import (
	"github.com/huangchengsir/pipewright/internal/auth"
	"net/http"
)

func restrictedAccountRoute(method, path string) bool {
	return (method == http.MethodGet && (path == "/api/auth/session" || path == "/api/access/self")) || (method == http.MethodPost && (path == "/api/auth/logout" || path == "/api/access/self/password"))
}
func tenantSessionDTO(s *auth.Session) any {
	return struct {
		Username           string `json:"username"`
		UserID             string `json:"userId"`
		DisplayName        string `json:"displayName"`
		MultiTenant        bool   `json:"multiTenant"`
		InstanceAdmin      bool   `json:"instanceAdmin"`
		MustChangePassword bool   `json:"mustChangePassword"`
	}{s.Username, s.UserID, s.DisplayName, true, s.InstanceAdmin, s.MustChangePassword}
}
