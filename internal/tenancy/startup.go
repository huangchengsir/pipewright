package tenancy

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/huangchengsir/pipewright/internal/authz"
	"github.com/huangchengsir/pipewright/internal/config"
	"github.com/huangchengsir/pipewright/internal/configstate"
	"github.com/huangchengsir/pipewright/internal/maintenance"
)

// ReconcileMode is startup-only: callers must own the controller process lock,
// before starting HTTP listeners, schedulers or recovery workers. Admission and
// unresolved-effect checks share a transaction with mapping and revocation.
// coverageReady must only be true after all runtime enforcement is installed.
func (r *Repository) ReconcileMode(ctx context.Context, requested config.MultiTenantMode, coverageReady bool) (ModeState, error) {
	if err := r.InitializeControl(ctx); err != nil {
		return ModeState{}, err
	}
	initial, err := r.Mode(ctx)
	if err != nil {
		return ModeState{}, err
	}
	planned, err := PlanMode(initial, requested, true, coverageReady)
	if err != nil {
		return ModeState{}, err
	}
	if planned == initial {
		return initial, nil
	}
	var result ModeState
	err = configstate.WithWriteTx(ctx, r.db, func(q configstate.DBTX) error {
		if err := configstate.CheckMaintenanceAdmission(ctx, q); err != nil {
			return err
		}
		if _, err := q.ExecContext(ctx, `UPDATE access_control SET mode_epoch=mode_epoch WHERE id=1`); err != nil {
			return err
		}
		var current ModeState
		var enabled, ever, catalog int
		var bootstrap, team sql.NullString
		if err := q.QueryRowContext(ctx, `SELECT enabled,ever_enabled,mode_epoch,authz_revision,bootstrap_user_id,default_team_id,catalog_version FROM access_control WHERE id=1`).Scan(&enabled, &ever, &current.Epoch, &current.Revision, &bootstrap, &team, &catalog); err != nil {
			return err
		}
		if catalog != authz.CatalogVersion || (enabled != 0 && enabled != 1) || (ever != 0 && ever != 1) || enabled > ever {
			return ErrConflict
		}
		current.Enabled, current.EverEnabled = enabled == 1, ever == 1
		next, err := PlanMode(current, requested, true, coverageReady)
		if err != nil {
			return err
		}
		if next == current {
			result = current
			return nil
		}
		if err = maintenance.Inspect(ctx, q); err != nil {
			if errors.Is(err, maintenance.ErrBlocked) {
				return ErrUnfinishedOperations
			}
			return err
		}
		// Inspect does not include the updater's task row in every recovery state.
		var activeUpdates int
		if err = q.QueryRowContext(ctx, `SELECT COUNT(*) FROM selfupdate_tasks WHERE active_key IS NOT NULL`).Scan(&activeUpdates); err != nil {
			return err
		}
		if activeUpdates != 0 {
			return ErrUnfinishedOperations
		}
		if !current.EverEnabled {
			var e error
			bootstrap.String, team.String, e = bootstrapLegacy(ctx, q)
			if e != nil {
				return e
			}
		} else if !bootstrap.Valid || !team.Valid {
			return ErrConflict
		}
		if next.Enabled {
			// Off mode may have legitimately changed the original administrator password.
			// This sync runs only after the old process has stopped and all leases drained.
			if err = syncBootstrapFromLegacy(ctx, q, bootstrap.String); err != nil {
				return err
			}
			if err = mapLegacyAssets(ctx, q, team.String, bootstrap.String); err != nil {
				return err
			}
		}
		if err = syncBootstrapIdentities(ctx, q, bootstrap.String, next.Enabled); err != nil {
			return err
		}
		for _, statement := range []string{`DELETE FROM sessions`, `DELETE FROM access_sessions`, `DELETE FROM login_flows`} {
			if _, err = q.ExecContext(ctx, statement); err != nil {
				return err
			}
		}
		now := time.Now()
		if _, err = q.ExecContext(ctx, `UPDATE mcp_tokens SET revoked_at=? WHERE revoked_at IS NULL`, now.UTC().Format(time.RFC3339Nano)); err != nil {
			return err
		}
		if _, err = q.ExecContext(ctx, `UPDATE mcp_config_approvals SET revoked_at=? WHERE revoked_at IS NULL`, now.UnixMilli()); err != nil {
			return err
		}
		nextEnabled := 0
		if next.Enabled {
			nextEnabled = 1
		}
		if _, err = q.ExecContext(ctx, `UPDATE access_control SET enabled=?,ever_enabled=1,mode_epoch=?,authz_revision=?,bootstrap_user_id=?,default_team_id=? WHERE id=1`, nextEnabled, next.Epoch, next.Revision, bootstrap.String, team.String); err != nil {
			return err
		}
		result = next
		return nil
	})
	if err != nil {
		return ModeState{}, err
	}
	return result, nil
}

func bootstrapLegacy(ctx context.Context, q configstate.DBTX) (string, string, error) {
	var count int
	if err := q.QueryRowContext(ctx, `SELECT COUNT(*) FROM access_users`).Scan(&count); err != nil {
		return "", "", err
	}
	if count != 0 {
		return "", "", ErrConflict
	}
	var username, password string
	var generation uint64
	if err := q.QueryRowContext(ctx, `SELECT username,password_hash,security_generation FROM admin_user WHERE id=1`).Scan(&username, &password, &generation); err != nil {
		return "", "", err
	}
	if !validName(username) || generation == 0 {
		return "", "", ErrConflict
	}
	userID, teamID := uuid.NewString(), uuid.NewString()
	now := time.Now().Unix()
	if _, err := q.ExecContext(ctx, `INSERT INTO access_users(id,username,username_key,display_name,password_hash,enabled,instance_admin,must_change_password,password_expires_at,password_consumed_at,security_generation,created_at,updated_at) VALUES(?,?,?,?,?,1,1,0,NULL,NULL,?,?,?)`, userID, username, strings.ToLower(username), username, password, generation, now, now); err != nil {
		return "", "", err
	}
	if _, err := q.ExecContext(ctx, `INSERT INTO access_teams(id,name,enabled,revision,created_at) VALUES(?,?,1,1,?)`, teamID, "Default", now); err != nil {
		return "", "", err
	}
	if err := seedRoles(ctx, q, teamID); err != nil {
		return "", "", err
	}
	memberID := uuid.NewString()
	if _, err := q.ExecContext(ctx, `INSERT INTO access_members(id,tenant_id,user_id,enabled,revision) VALUES(?,?,?,1,1)`, memberID, teamID, userID); err != nil {
		return "", "", err
	}
	if _, err := q.ExecContext(ctx, `INSERT INTO access_ownership(resource_kind,resource_id,tenant_id,owner_id) VALUES('member',?,?,?)`, memberID, teamID, userID); err != nil {
		return "", "", err
	}
	var roleID string
	if err := q.QueryRowContext(ctx, `SELECT role_id FROM access_role_seeds WHERE tenant_id=? AND preset_key='team-admin'`, teamID).Scan(&roleID); err != nil {
		return "", "", err
	}
	if _, err := q.ExecContext(ctx, `INSERT INTO access_bindings(id,tenant_id,member_id,role_id,scope_json) VALUES(?,?,?,?,?)`, uuid.NewString(), teamID, memberID, roleID, `{"kind":"team"}`); err != nil {
		return "", "", err
	}
	// Identity subjects are copied without treating an email/display label as proof.
	rows, err := q.QueryContext(ctx, `SELECT provider,issuer,subject,identity_generation FROM login_identities`)
	if err != nil {
		return "", "", err
	}
	type identity struct {
		provider, issuer, subject string
		generation                uint64
	}
	var identities []identity
	for rows.Next() {
		var v identity
		if err = rows.Scan(&v.provider, &v.issuer, &v.subject, &v.generation); err != nil {
			rows.Close()
			return "", "", err
		}
		identities = append(identities, v)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return "", "", err
	}
	for _, v := range identities {
		key, _ := json.Marshal([]string{v.provider, v.issuer, v.subject})
		digest := sha256.Sum256(key)
		if _, err = q.ExecContext(ctx, `INSERT INTO access_identities(id,user_id,provider,issuer,subject,identity_key,generation,created_at) VALUES(?,?,?,?,?,?,?,?)`, uuid.NewString(), userID, v.provider, v.issuer, v.subject, hex.EncodeToString(digest[:]), v.generation, now); err != nil {
			return "", "", err
		}
	}
	return userID, teamID, nil
}
func syncBootstrapFromLegacy(ctx context.Context, q configstate.DBTX, id string) error {
	var username, password string
	var generation uint64
	if err := q.QueryRowContext(ctx, `SELECT username,password_hash,security_generation FROM admin_user WHERE id=1`).Scan(&username, &password, &generation); err != nil {
		return err
	}
	result, err := q.ExecContext(ctx, `UPDATE access_users SET password_hash=?,security_generation=?,must_change_password=0,password_expires_at=NULL,password_consumed_at=NULL WHERE id=? AND username=? AND instance_admin=1 AND enabled=1`, password, generation, id, username)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrConflict
	}
	return nil
}
