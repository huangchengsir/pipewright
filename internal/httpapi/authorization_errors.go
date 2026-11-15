package httpapi

import (
	"errors"
	"github.com/huangchengsir/pipewright/internal/configstate"
	"github.com/huangchengsir/pipewright/internal/tenancy"
	"net/http"
)

// writeAuthorizationError is shared by domain adapters. Domain services still
// perform the checks; this only preserves stable transport semantics.
func writeAuthorizationError(w http.ResponseWriter, err error) bool {
	switch {
	case errors.Is(err, tenancy.ErrForbidden):
		writeError(w, 403, "access_denied", "没有此操作的权限")
	case errors.Is(err, tenancy.ErrConflict):
		writeError(w, 409, "permission_state_changed", "权限状态已变化，请刷新后重试")
	case errors.Is(err, configstate.ErrMaintenance), errors.Is(err, configstate.ErrMaintenanceForbidden):
		writeError(w, 409, "maintenance_active", "维护期间暂不能修改配置")
	default:
		return false
	}
	return true
}
