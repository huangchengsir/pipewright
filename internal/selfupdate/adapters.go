package selfupdate

import (
	"context"
	"database/sql"
	"github.com/huangchengsir/pipewright/internal/maintenance"
	"github.com/huangchengsir/pipewright/internal/store"
	"github.com/huangchengsir/pipewright/internal/version"
)

func (s *Service) Admit(ctx context.Context, kind string) (func(), error) {
	return maintenance.Enter(ctx, s.db, kind)
}

// Called after a failed exec, with a freshly opened store after graceful close.
func (s *Service) RecordRestartFailure(ctx context.Context, db *sql.DB) {
	copy := &Service{db: db, instance: s.instance}
	t, e := copy.load(ctx, "active_key IS NOT NULL")
	if e == nil {
		_ = copy.transition(t, "restart_required", "restart_required")
	}
}

// RequiresPrivateBuild follows the live connection, including managed database
// configuration. Public release binaries do not contain the optional DM driver.
func (s *Service) RequiresPrivateBuild() bool { return store.DialectOf(s.db) == store.DM }

func (s *Service) Capabilities() (version.DeploymentMode, bool) {
	if s.RequiresPrivateBuild() {
		return version.Mode(), false
	}
	if s.opts.Capabilities != nil {
		return s.opts.Capabilities()
	}
	return version.Mode(), version.CanSelfReplace()
}
