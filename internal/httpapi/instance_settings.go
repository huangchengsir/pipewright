package httpapi

import (
	"github.com/huangchengsir/pipewright/internal/tenancy"
	"net/http"
)

func requireInstanceSettings(accounts *tenancy.Accounts, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if accounts != nil {
			if err := accounts.AuthorizeInstanceMaintenance(r.Context()); err != nil {
				accessError(w, err)
				return
			}
		} else if session, _ := sessionFromContext(r.Context()); session != nil && session.MultiTenant {
			accessError(w, tenancy.ErrForbidden)
			return
		}
		next(w, r)
	}
}
