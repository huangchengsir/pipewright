package tenancy

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/huangchengsir/pipewright/internal/authz"
	"github.com/huangchengsir/pipewright/internal/configstate"
)

// Repository is storage only, not an authorization boundary. It must not be
// exposed directly by HTTP, MCP or agent tools. Mutations serialize on control.
type Repository struct {
	db           *sql.DB
	lookupBudget LookupBudget
}

func NewRepository(db *sql.DB) *Repository { return &Repository{db: db} }

func (r *Repository) Mode(ctx context.Context) (ModeState, error) {
	var state ModeState
	var enabled, ever, catalog int
	err := r.db.QueryRowContext(ctx, `SELECT enabled,ever_enabled,mode_epoch,authz_revision,catalog_version FROM access_control WHERE id=1`).Scan(&enabled, &ever, &state.Epoch, &state.Revision, &catalog)
	if err != nil {
		return state, err
	}
	if catalog != authz.CatalogVersion || (enabled != 0 && enabled != 1) || (ever != 0 && ever != 1) || enabled > ever || state.Epoch == 0 || state.Revision == 0 {
		return ModeState{}, ErrConflict
	}
	state.Enabled = enabled == 1
	state.EverEnabled = ever == 1
	return state, nil
}

// InitializeControl creates the disabled control record once. On a concurrent
// insertion, callers must re-read the winner; no existing mode is overwritten.
func (r *Repository) InitializeControl(ctx context.Context) error {
	var id int
	err := r.db.QueryRowContext(ctx, `SELECT id FROM access_control WHERE id=1`).Scan(&id)
	if err == nil {
		return nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if err = checkUninitialized(ctx, r.db); err != nil {
		return err
	}
	_, err = r.db.ExecContext(ctx, `INSERT INTO access_control(id,enabled,ever_enabled,mode_epoch,authz_revision,bootstrap_user_id,default_team_id) VALUES(1,0,0,1,1,NULL,NULL)`)
	if err != nil {
		if _, readErr := r.Mode(ctx); readErr == nil {
			return nil
		}
	}
	return err
}
func lockControl(ctx context.Context, tx *sql.Tx) error {
	if err := configstate.CheckMaintenanceAdmission(ctx, tx); err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, `UPDATE access_control SET authz_revision=authz_revision+1 WHERE id=1`)
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
func (r *Repository) CreateTeam(ctx context.Context, name string) (string, error) {
	if name == "" || len([]rune(name)) > 128 {
		return "", errors.New("tenancy: invalid team name")
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	if err = lockControl(ctx, tx); err != nil {
		return "", err
	}
	id := uuid.NewString()
	_, err = tx.ExecContext(ctx, `INSERT INTO access_teams(id,name,enabled,revision,created_at) VALUES(?,?,1,1,?)`, id, name, time.Now().Unix())
	if err != nil {
		return "", err
	}
	if err = seedRoles(ctx, tx, id); err != nil {
		return "", err
	}
	if err = tx.Commit(); err != nil {
		return "", err
	}
	return id, nil
}

// EnsurePresets does not repair an existing seed or overwrite edited/deleted
// roles. The retained seed receipt makes deletion survive startup and upgrades.
func (r *Repository) EnsurePresets(ctx context.Context, teamID string) error {
	var count int
	if err := r.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM access_role_seeds WHERE tenant_id=?`, teamID).Scan(&count); err != nil {
		return err
	}
	if count == len(authz.Presets(teamID)) {
		return nil
	}
	if count != 0 {
		return ErrConflict
	}

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = lockControl(ctx, tx); err != nil {
		return err
	}
	if err = seedRoles(ctx, tx, teamID); err != nil {
		return err
	}
	return tx.Commit()
}
func seedRoles(ctx context.Context, tx configstate.DBTX, teamID string) error {
	var team int
	if err := tx.QueryRowContext(ctx, `SELECT enabled FROM access_teams WHERE id=?`, teamID).Scan(&team); err != nil {
		return err
	}
	for _, role := range authz.Presets(teamID) {
		var previous string
		err := tx.QueryRowContext(ctx, `SELECT role_id FROM access_role_seeds WHERE tenant_id=? AND preset_key=?`, teamID, role.PresetKey).Scan(&previous)
		if err == nil {
			continue
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		pages, err := json.Marshal(role.Pages)
		if err != nil {
			return err
		}
		grants, err := json.Marshal(role.Grants)
		if err != nil {
			return err
		}
		selected := 0
		if role.RequiresSelectedBinding {
			selected = 1
		}
		id := uuid.NewString()
		_, err = tx.ExecContext(ctx, `INSERT INTO access_roles(id,tenant_id,name,preset_key,seed_version,requires_selected_binding,deleted,revision,pages_json,grants_json) VALUES(?,?,?,?,?,?,0,1,?,?)`, id, teamID, role.NameKey, role.PresetKey, role.SeedVersion, selected, string(pages), string(grants))
		if err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO access_ownership(resource_kind,resource_id,tenant_id) VALUES('role',?,?)`, id, teamID); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO access_role_seeds(tenant_id,preset_key,role_id,seed_version) VALUES(?,?,?,?)`, teamID, role.PresetKey, id, role.SeedVersion); err != nil {
			return err
		}
	}
	return nil
}
func (r *Repository) Roles(ctx context.Context, teamID string) ([]authz.Role, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT id,name,preset_key,seed_version,requires_selected_binding,deleted,pages_json,grants_json FROM access_roles WHERE tenant_id=? ORDER BY id`, teamID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []authz.Role
	for rows.Next() {
		var role authz.Role
		var selected, deleted int
		var pages, grants string
		role.TenantID = teamID
		if err = rows.Scan(&role.ID, &role.NameKey, &role.PresetKey, &role.SeedVersion, &selected, &deleted, &pages, &grants); err != nil {
			return nil, err
		}
		role.RequiresSelectedBinding = selected == 1
		role.Deleted = deleted == 1
		if json.Unmarshal([]byte(pages), &role.Pages) != nil || json.Unmarshal([]byte(grants), &role.Grants) != nil {
			return nil, fmt.Errorf("tenancy: malformed stored role")
		}
		out = append(out, role)
	}
	return out, rows.Err()
}

// A missing control record is a fresh off-mode instance only when no tenancy
// state exists. Losing/deleting control must never resurrect unrestricted mode.
func checkUninitialized(ctx context.Context, q configstate.DBTX) error {
	var count int
	err := q.QueryRowContext(ctx, `SELECT (SELECT COUNT(*) FROM access_users)+(SELECT COUNT(*) FROM access_teams)+(SELECT COUNT(*) FROM access_roles)+(SELECT COUNT(*) FROM access_ownership)`).Scan(&count)
	if err != nil {
		return err
	}
	if count != 0 {
		return ErrConflict
	}
	return nil
}
