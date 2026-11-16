package tenancy

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"time"
	"unicode"

	"github.com/google/uuid"
	"github.com/huangchengsir/pipewright/internal/auth"
	"github.com/huangchengsir/pipewright/internal/authz"
	"github.com/huangchengsir/pipewright/internal/configstate"
)

var (
	ErrForbidden        = errors.New("tenancy: access denied")
	ErrInvalidInput     = errors.New("tenancy: invalid account input")
	ErrAccountConflict  = errors.New("tenancy: account cannot be created")
	ErrProtectedAccount = errors.New("tenancy: protected administrator account")
)

// Actor is obtained from a verified session. These claims are rechecked under
// the write fence; request JSON must never bind directly into this type.
type Actor struct {
	SessionHash                   string
	UserID                        string
	ModeEpoch, SecurityGeneration uint64
}
type User struct {
	ID                 string `json:"id"`
	Username           string `json:"username"`
	DisplayName        string `json:"displayName"`
	Enabled            bool   `json:"enabled"`
	InstanceAdmin      bool   `json:"instanceAdmin"`
	MustChangePassword bool   `json:"mustChangePassword"`
	SecurityGeneration uint64 `json:"-"`
}
type MemberAssignment struct {
	TeamID, RoleID string
	Scope          authz.Scope
}
type CreateUserInput struct {
	Username, DisplayName string
	Assignments           []MemberAssignment
}

// InitialCredential is returned only from create/reset. It is never persisted
// as plaintext, included in User, or emitted through an audit/logging method.
type InitialCredential struct {
	User      User   `json:"user"`
	Password  string `json:"temporaryPassword"`
	ExpiresAt int64  `json:"expiresAt"`
}
type Accounts struct {
	lockouts        accountGuards
	unknownLockouts accountGuards
	loginSlots      chan struct{}
	db              *sql.DB
	now             func() time.Time
}

func NewAccounts(db *sql.DB) *Accounts {
	a := &Accounts{db: db, now: time.Now, loginSlots: make(chan struct{}, 2)}
	return a
}
func validName(name string) bool {
	return name != "" && name == strings.TrimSpace(name) && len([]rune(name)) <= 128 && !strings.ContainsFunc(name, unicode.IsControl)
}
func validUsername(s string) bool {
	if !validName(s) {
		return false
	}
	for _, r := range s {
		if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' || r == '-' || r == '.') {
			return false
		}
	}
	return true
}
func temporaryPassword() (string, error) {
	var raw [24]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw[:]), nil
}
func verifyActor(ctx context.Context, tx configstate.DBTX, actor Actor, admin bool) (User, error) {
	var user User
	var mode, enabled, isAdmin, mustChange, catalog int
	var epoch uint64
	if actor.UserID == "" || actor.SessionHash == "" {
		return user, ErrForbidden
	}
	if err := tx.QueryRowContext(ctx, `SELECT enabled,mode_epoch,catalog_version FROM access_control WHERE id=1`).Scan(&mode, &epoch, &catalog); err != nil || catalog != authz.CatalogVersion || mode != 1 || epoch != actor.ModeEpoch {
		return user, ErrForbidden
	}
	err := tx.QueryRowContext(ctx, `SELECT id,username,display_name,enabled,instance_admin,must_change_password,security_generation FROM access_users WHERE id=?`, actor.UserID).Scan(&user.ID, &user.Username, &user.DisplayName, &enabled, &isAdmin, &mustChange, &user.SecurityGeneration)
	if err != nil || enabled != 1 || user.SecurityGeneration != actor.SecurityGeneration || mustChange != 0 || (admin && isAdmin != 1) {
		return User{}, ErrForbidden
	}
	var sessionCount int
	if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM access_sessions WHERE token_hash=? AND user_id=? AND mode_epoch=? AND security_generation=? AND expires_at>?`, actor.SessionHash, actor.UserID, actor.ModeEpoch, actor.SecurityGeneration, time.Now().Unix()).Scan(&sessionCount); err != nil || sessionCount != 1 {
		return User{}, ErrForbidden
	}
	user.Enabled = true
	user.InstanceAdmin = isAdmin == 1
	user.MustChangePassword = mustChange == 1
	return user, nil
}
func (a *Accounts) Create(ctx context.Context, actor Actor, input CreateUserInput) (InitialCredential, error) {
	if err := a.preflightAdministrator(ctx, actor); err != nil {
		return InitialCredential{}, err
	}
	select {
	case a.loginSlots <- struct{}{}:
		defer func() { <-a.loginSlots }()
	case <-ctx.Done():
		return InitialCredential{}, ctx.Err()
	}

	if !validUsername(input.Username) || !validName(input.DisplayName) {
		return InitialCredential{}, ErrInvalidInput
	}
	password, err := temporaryPassword()
	if err != nil {
		return InitialCredential{}, err
	}
	hash, err := auth.HashPassword(password)
	if err != nil {
		return InitialCredential{}, err
	}
	tx, err := a.db.BeginTx(ctx, nil)
	if err != nil {
		return InitialCredential{}, err
	}
	defer tx.Rollback()
	if err = lockControl(ctx, tx); err != nil {
		return InitialCredential{}, err
	}
	if _, err = verifyActor(ctx, tx, actor, true); err != nil {
		return InitialCredential{}, err
	}
	id := uuid.NewString()
	now := a.now().Unix()
	expires := now + 24*60*60
	_, err = tx.ExecContext(ctx, `INSERT INTO access_users(id,username,username_key,display_name,password_hash,enabled,instance_admin,must_change_password,password_expires_at,security_generation,created_at,updated_at) VALUES(?,?,?,?,?,1,0,1,?,1,?,?)`, id, input.Username, strings.ToLower(input.Username), input.DisplayName, hash, expires, now, now)
	if err != nil {
		return InitialCredential{}, ErrAccountConflict
	}
	memberships := map[string]string{}
	for _, assignment := range input.Assignments {
		role, err := assignmentRole(ctx, tx, id, assignment)
		if err != nil {
			return InitialCredential{}, err
		}
		memberID := memberships[assignment.TeamID]
		if memberID == "" {
			memberID = uuid.NewString()
			_, err = tx.ExecContext(ctx, `INSERT INTO access_members(id,tenant_id,user_id,enabled,revision) VALUES(?,?,?,1,1)`, memberID, assignment.TeamID, id)
			if err != nil {
				return InitialCredential{}, err
			}
			if _, err = tx.ExecContext(ctx, `INSERT INTO access_ownership(resource_kind,resource_id,tenant_id,owner_id) VALUES('member',?,?,?)`, memberID, assignment.TeamID, id); err != nil {
				return InitialCredential{}, err
			}
			memberships[assignment.TeamID] = memberID
		}
		body, err := json.Marshal(assignment.Scope)
		if err != nil {
			return InitialCredential{}, err
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO access_bindings(id,tenant_id,member_id,role_id,scope_json) VALUES(?,?,?,?,?)`, uuid.NewString(), assignment.TeamID, memberID, role.ID, string(body)); err != nil {
			return InitialCredential{}, ErrInvalidInput
		}
	}
	if err = tx.Commit(); err != nil {
		return InitialCredential{}, err
	}
	return InitialCredential{User: User{ID: id, Username: input.Username, DisplayName: input.DisplayName, Enabled: true, MustChangePassword: true, SecurityGeneration: 1}, Password: password, ExpiresAt: expires}, nil
}
func assignmentRole(ctx context.Context, tx *sql.Tx, userID string, assignment MemberAssignment) (authz.Role, error) {
	var role authz.Role
	var pages, grants string
	var enabled, deleted, selected int
	err := tx.QueryRowContext(ctx, `SELECT r.id,r.tenant_id,r.pages_json,r.grants_json,r.deleted,r.requires_selected_binding,t.enabled FROM access_roles r JOIN access_teams t ON t.id=r.tenant_id WHERE r.id=? AND r.tenant_id=?`, assignment.RoleID, assignment.TeamID).Scan(&role.ID, &role.TenantID, &pages, &grants, &deleted, &selected, &enabled)
	if err != nil || enabled != 1 || deleted != 0 {
		return role, ErrInvalidInput
	}
	role.RequiresSelectedBinding = selected == 1
	if json.Unmarshal([]byte(pages), &role.Pages) != nil || json.Unmarshal([]byte(grants), &role.Grants) != nil {
		return role, ErrInvalidInput
	}
	if _, err = authz.New(authz.Snapshot{CatalogVersion: authz.CatalogVersion, ModeEpoch: 1, Revision: 1, Roles: []authz.Role{role}, Bindings: []authz.Binding{{SubjectID: userID, TenantID: role.TenantID, RoleID: role.ID, Limit: assignment.Scope}}}); err != nil {
		return role, ErrInvalidInput
	}
	for _, project := range assignment.Scope.ProjectIDs {
		var tenant string
		if err = tx.QueryRowContext(ctx, `SELECT tenant_id FROM access_ownership WHERE resource_kind='project' AND resource_id=?`, project).Scan(&tenant); err != nil || tenant != assignment.TeamID {
			return role, ErrInvalidInput
		}
	}
	// Resource binding IDs are intentionally not accepted until the API supplies
	// typed resource references; untyped IDs could collide across resource kinds.
	if len(assignment.Scope.ResourceIDs) > 0 {
		return role, ErrInvalidInput
	}
	for _, ref := range assignment.Scope.ResourceRefs {
		var tenant string
		if err = tx.QueryRowContext(ctx, `SELECT tenant_id FROM access_ownership WHERE resource_kind=? AND resource_id=?`, ref.Kind, ref.ID).Scan(&tenant); err != nil || tenant != assignment.TeamID {
			return role, ErrInvalidInput
		}
	}
	for _, environment := range assignment.Scope.EnvironmentIDs {
		var tenant, project string
		if err = tx.QueryRowContext(ctx, `SELECT tenant_id,project_id FROM access_environments WHERE id=?`, environment).Scan(&tenant, &project); err != nil || tenant != assignment.TeamID {
			return role, ErrInvalidInput
		}
		if assignment.Scope.Kind == authz.ScopeSelected {
			found := false
			for _, id := range assignment.Scope.ProjectIDs {
				found = found || id == project
			}
			if !found {
				return role, ErrInvalidInput
			}
		}
	}
	return role, nil
}
func (a *Accounts) UpdateDisplayName(ctx context.Context, actor Actor, targetID, name string) error {
	if !validName(name) {
		return ErrInvalidInput
	}
	tx, err := a.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = lockControl(ctx, tx); err != nil {
		return err
	}
	if _, err = verifyActor(ctx, tx, actor, targetID != actor.UserID); err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, `UPDATE access_users SET display_name=?,updated_at=? WHERE id=?`, name, a.now().Unix(), targetID)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrInvalidInput
	}
	return tx.Commit()
}
func (a *Accounts) ResetPassword(ctx context.Context, actor Actor, targetID string) (InitialCredential, error) {
	if err := a.preflightAdministrator(ctx, actor); err != nil {
		return InitialCredential{}, err
	}
	select {
	case a.loginSlots <- struct{}{}:
		defer func() { <-a.loginSlots }()
	case <-ctx.Done():
		return InitialCredential{}, ctx.Err()
	}

	password, err := temporaryPassword()
	if err != nil {
		return InitialCredential{}, err
	}
	hash, err := auth.HashPassword(password)
	if err != nil {
		return InitialCredential{}, err
	}
	tx, err := a.db.BeginTx(ctx, nil)
	if err != nil {
		return InitialCredential{}, err
	}
	defer tx.Rollback()
	if err = lockControl(ctx, tx); err != nil {
		return InitialCredential{}, err
	}
	if _, err = verifyActor(ctx, tx, actor, true); err != nil {
		return InitialCredential{}, err
	}
	var bootstrap sql.NullString
	if err = tx.QueryRowContext(ctx, `SELECT bootstrap_user_id FROM access_control WHERE id=1`).Scan(&bootstrap); err != nil {
		return InitialCredential{}, err
	}
	if targetID == bootstrap.String || targetID == actor.UserID {
		return InitialCredential{}, ErrProtectedAccount
	}
	var user User
	var enabled, admin int
	if err = tx.QueryRowContext(ctx, `SELECT id,username,display_name,enabled,instance_admin,security_generation FROM access_users WHERE id=?`, targetID).Scan(&user.ID, &user.Username, &user.DisplayName, &enabled, &admin, &user.SecurityGeneration); err != nil {
		return InitialCredential{}, ErrInvalidInput
	}
	expires := a.now().Add(24 * time.Hour).Unix()
	_, err = tx.ExecContext(ctx, `UPDATE access_users SET password_hash=?,must_change_password=1,password_expires_at=?,password_consumed_at=NULL,security_generation=security_generation+1,updated_at=? WHERE id=?`, hash, expires, a.now().Unix(), targetID)
	if err != nil {
		return InitialCredential{}, err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM access_sessions WHERE user_id=?`, targetID); err != nil {
		return InitialCredential{}, err
	}
	if err = tx.Commit(); err != nil {
		return InitialCredential{}, err
	}
	user.Enabled = enabled == 1
	user.InstanceAdmin = admin == 1
	user.MustChangePassword = true
	user.SecurityGeneration++
	return InitialCredential{User: user, Password: password, ExpiresAt: expires}, nil
}
func (a *Accounts) SetEnabled(ctx context.Context, actor Actor, targetID string, enabled bool) error {
	tx, err := a.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = lockControl(ctx, tx); err != nil {
		return err
	}
	if _, err = verifyActor(ctx, tx, actor, true); err != nil {
		return err
	}
	var bootstrap sql.NullString
	if err = tx.QueryRowContext(ctx, `SELECT bootstrap_user_id FROM access_control WHERE id=1`).Scan(&bootstrap); err != nil {
		return err
	}
	if !enabled && (targetID == bootstrap.String || targetID == actor.UserID) {
		return ErrProtectedAccount
	}
	var admin int
	if err = tx.QueryRowContext(ctx, `SELECT instance_admin FROM access_users WHERE id=?`, targetID).Scan(&admin); err != nil {
		return ErrInvalidInput
	}
	if !enabled && admin == 1 {
		var count int
		if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM access_users WHERE enabled=1 AND instance_admin=1 AND id<>?`, targetID).Scan(&count); err != nil {
			return err
		}
		if count == 0 {
			return ErrProtectedAccount
		}
	}
	protectedTeams := map[string]int{}
	if !enabled {
		rows, e := tx.QueryContext(ctx, `SELECT tenant_id FROM access_members WHERE user_id=? AND enabled=1`, targetID)
		if e != nil {
			return e
		}
		var teams []string
		for rows.Next() {
			var team string
			if e = rows.Scan(&team); e != nil {
				rows.Close()
				return e
			}
			teams = append(teams, team)
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return e
		}
		for _, team := range teams {
			count, e := teamAdministratorCount(ctx, tx, team)
			if e != nil {
				return e
			}
			protectedTeams[team] = count
		}
	}
	value := 0
	if enabled {
		value = 1
	}
	if _, err = tx.ExecContext(ctx, `UPDATE access_users SET enabled=?,security_generation=security_generation+1,updated_at=? WHERE id=?`, value, a.now().Unix(), targetID); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM access_sessions WHERE user_id=?`, targetID); err != nil {
		return err
	}
	for team, before := range protectedTeams {
		after, e := teamAdministratorCount(ctx, tx, team)
		if e != nil {
			return e
		}
		if before > 0 && after == 0 {
			return ErrProtectedAccount
		}
	}
	return tx.Commit()
}

func (a *Accounts) preflightAdministrator(ctx context.Context, actor Actor) error {
	tx, err := a.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	_, err = verifyActor(ctx, tx, actor, true)
	return err
}
