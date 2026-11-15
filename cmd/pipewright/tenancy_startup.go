package main

import (
	"context"
	"database/sql"
	"github.com/huangchengsir/pipewright/internal/auth"
	"github.com/huangchengsir/pipewright/internal/config"
	"github.com/huangchengsir/pipewright/internal/tenancy"
	"net/http"
)

// Allow the verified team-isolation runtime to be enabled by deployment configuration.
const tenancyRuntimeCoverageReady = true

type accessRuntime struct {
	mode          tenancy.ModeState
	repository    *tenancy.Repository
	accounts      *tenancy.Accounts
	authenticator auth.Authenticator
	process       *tenancy.ProcessMode
}

func assembleAccess(ctx context.Context, db *sql.DB, legacy *auth.Service, requested config.MultiTenantMode, ready bool) (*accessRuntime, error) {
	repository := tenancy.NewRepository(db)
	state, e := repository.ReconcileMode(ctx, requested, ready)
	if e != nil {
		return nil, e
	}
	accounts := tenancy.NewAccounts(db)
	process := tenancy.PinProcessMode(db, state)
	var authenticator auth.Authenticator = tenancy.NewLegacyAuthenticator(legacy, process)
	if state.Enabled {
		authenticator = tenancy.NewAuthenticator(accounts, state.Epoch)
	}
	return &accessRuntime{state, repository, accounts, authenticator, process}, nil
}
func (a *accessRuntime) Guard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if e := a.process.Check(r.Context()); e != nil {
			w.Header().Set("Cache-Control", "no-store")
			http.Error(w, "instance mode changed; restart required", http.StatusServiceUnavailable)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// assembleMCPTokens binds employee ownership and live team permissions in both modes.
func assembleMCPTokens(db *sql.DB) *auth.MCPTokenStore {
	return auth.NewMCPTokenStore(db, tenancy.MCPAuthorization(db))
}
