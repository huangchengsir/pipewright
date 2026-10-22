package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/huangchengsir/pipewright/internal/configstate"
	"github.com/huangchengsir/pipewright/internal/store"
)

var (
	ErrMCPTokenInvalid  = errors.New("auth: invalid MCP token")
	ErrMCPTokenInput    = errors.New("auth: invalid MCP token input")
	ErrMCPTokenNotFound = errors.New("auth: MCP token not found")
	ErrMCPForbidden     = errors.New("auth: MCP permission denied")
)

// MCPToken contains only administration metadata, never the stored digest.
type MCPToken struct {
	ID            string           `json:"id"`
	Name          string           `json:"name"`
	ProjectIDs    []string         `json:"projectIds"`
	CanTrigger    bool             `json:"canTrigger"`
	CreatedAt     string           `json:"createdAt"`
	ExpiresAt     string           `json:"expiresAt"`
	RevokedAt     *string          `json:"revokedAt"`
	ConfigGrants  []MCPConfigGrant `json:"configGrants"`
	AuthzRevision int64            `json:"authzRevision"`
}

func (t *MCPToken) Allows(projectID string, trigger bool) bool {
	if trigger && !t.CanTrigger {
		return false
	}
	for _, id := range t.ProjectIDs {
		if id == projectID {
			return true
		}
	}
	return false
}

type MCPTokenInput struct {
	Name          string           `json:"name"`
	ProjectIDs    []string         `json:"projectIds"`
	CanTrigger    bool             `json:"canTrigger"`
	ExpiresInDays int              `json:"expiresInDays"`
	ConfigGrants  []MCPConfigGrant `json:"configGrants"`
}

// MCPConfigGrant is an explicit project-local extension. Empty selectors never
// mean all sections or all operations; CanTrigger remains unrelated.
type MCPConfigGrant struct {
	ProjectID  string   `json:"projectId"`
	Read       bool     `json:"read"`
	Propose    bool     `json:"propose"`
	Apply      bool     `json:"apply"`
	Sections   []string `json:"sections"`
	Operations []string `json:"operations"`
}

var mcpConfigSections = map[string]bool{"project": true, "pipeline": true, "build": true, "step_limits": true, "source_and_impact": true}
var mcpConfigOperations = map[string]string{"project.set_name": "project", "project.set_default_branch": "project", "stage.rename": "pipeline", "job.rename": "pipeline", "step.set_limits": "step_limits", "build.set": "build"}

func (t *MCPToken) AllowsConfig(projectID, capability, section, operation string) bool {
	if t == nil || !t.Allows(projectID, false) {
		return false
	}
	for _, g := range t.ConfigGrants {
		if g.ProjectID != projectID || !g.Read || len(g.Sections) == 0 {
			continue
		}
		switch capability {
		case "config.read":
		case "config.propose":
			if !g.Propose {
				return false
			}
		case "config.apply":
			if !g.Propose || !g.Apply {
				return false
			}
		default:
			return false
		}
		contains := func(items []string, value string) bool {
			for _, item := range items {
				if item == value {
					return true
				}
			}
			return false
		}
		if section != "" && !contains(g.Sections, section) {
			return false
		}
		if operation != "" {
			if capability == "config.read" || !contains(g.Operations, operation) || !contains(g.Sections, mcpConfigOperations[operation]) {
				return false
			}
		}
		return true
	}
	return false
}

func NormalizeMCPConfigGrants(projectIDs []string, in []MCPConfigGrant) ([]MCPConfigGrant, error) {
	if len(in) > 32 {
		return nil, ErrMCPTokenInput
	}
	allowed := map[string]bool{}
	for _, id := range projectIDs {
		allowed[id] = true
	}
	out := make([]MCPConfigGrant, 0, len(in))
	seen := map[string]bool{}
	for _, g := range in {
		if !allowed[g.ProjectID] || seen[g.ProjectID] || !g.Read || (g.Apply && !g.Propose) || (g.Propose && !g.Read) || len(g.Sections) == 0 || len(g.Sections) > 5 || len(g.Operations) > 6 {
			return nil, ErrMCPTokenInput
		}
		seen[g.ProjectID] = true
		g.Sections = append([]string(nil), g.Sections...)
		sort.Strings(g.Sections)
		for i, s := range g.Sections {
			if !mcpConfigSections[s] || (i > 0 && s == g.Sections[i-1]) {
				return nil, ErrMCPTokenInput
			}
		}
		g.Operations = append([]string{}, g.Operations...)
		sort.Strings(g.Operations)
		if g.Propose && len(g.Operations) == 0 {
			return nil, ErrMCPTokenInput
		}
		if !g.Propose && len(g.Operations) > 0 {
			return nil, ErrMCPTokenInput
		}
		for i, op := range g.Operations {
			required, ok := mcpConfigOperations[op]
			if !ok || (i > 0 && op == g.Operations[i-1]) {
				return nil, ErrMCPTokenInput
			}
			found := false
			for _, s := range g.Sections {
				if s == required {
					found = true
				}
			}
			if !found {
				return nil, ErrMCPTokenInput
			}
		}
		out = append(out, g)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ProjectID < out[j].ProjectID })
	return out, nil
}

type MCPTokenStore struct{ db *sql.DB }

func NewMCPTokenStore(db *sql.DB) *MCPTokenStore { return &MCPTokenStore{db: db} }

func (s *MCPTokenStore) Create(ctx context.Context, in MCPTokenInput) (string, *MCPToken, error) {
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" || len(in.Name) > 128 || !utf8.ValidString(in.Name) || strings.ContainsFunc(in.Name, unicode.IsControl) || len(in.ProjectIDs) < 1 || len(in.ProjectIDs) > 32 {
		return "", nil, ErrMCPTokenInput
	}
	if in.ExpiresInDays == 0 {
		in.ExpiresInDays = 7
	}
	if in.ExpiresInDays < 1 || in.ExpiresInDays > 30 {
		return "", nil, ErrMCPTokenInput
	}
	ids := append([]string(nil), in.ProjectIDs...)
	sort.Strings(ids)
	for i, id := range ids {
		if id == "" || len(id) > 36 || strings.TrimSpace(id) != id || (i > 0 && ids[i-1] == id) {
			return "", nil, ErrMCPTokenInput
		}
	}
	grants, err := NormalizeMCPConfigGrants(ids, in.ConfigGrants)
	if err != nil {
		return "", nil, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", nil, err
	}
	defer tx.Rollback()
	var count int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM mcp_tokens`).Scan(&count); err != nil {
		return "", nil, err
	}
	if count >= 500 {
		// Reclaim only ended access tokens; durable run-request identities are separate.
		if _, err := tx.ExecContext(ctx, `DELETE FROM mcp_tokens WHERE revoked_at IS NOT NULL OR expires_at <= ?`, time.Now().UTC().Format(time.RFC3339)); err != nil {
			return "", nil, err
		}
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM mcp_tokens`).Scan(&count); err != nil {
			return "", nil, err
		}
		if count >= 500 {
			return "", nil, ErrMCPTokenInput
		}
	}
	for _, id := range ids {
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM projects WHERE id = ?`, id).Scan(&count); err != nil {
			return "", nil, err
		}
		if count != 1 {
			return "", nil, ErrMCPTokenInput
		}
	}
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", nil, err
	}
	plaintext := hex.EncodeToString(raw[:])
	stamp := time.Now().UTC()
	token := &MCPToken{ID: uuid.NewString(), Name: in.Name, ProjectIDs: ids, CanTrigger: in.CanTrigger, CreatedAt: stamp.Format(time.RFC3339), ExpiresAt: stamp.Add(time.Duration(in.ExpiresInDays) * 24 * time.Hour).Format(time.RFC3339), ConfigGrants: grants}
	blob, _ := json.Marshal(ids)
	grantsJSON, _ := json.Marshal(grants)
	_, err = tx.ExecContext(ctx, `INSERT INTO mcp_tokens (id, name, token_hash, project_ids_json, can_trigger, created_at, expires_at,config_grants_json,authz_revision) VALUES (?, ?, ?, ?, ?, ?, ?, ?,0)`, token.ID, token.Name, mcpDigest(plaintext), string(blob), token.CanTrigger, token.CreatedAt, token.ExpiresAt, string(grantsJSON))
	if err != nil {
		return "", nil, err
	}
	if err := tx.Commit(); err != nil {
		return "", nil, err
	}
	return plaintext, token, nil
}

func mcpDigest(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

const mcpTokenColumns = `id, name, project_ids_json, can_trigger, created_at, expires_at, revoked_at,config_grants_json,authz_revision`

func scanMCPToken(row interface{ Scan(...any) error }) (*MCPToken, error) {
	var t MCPToken
	var ids string
	var grants string
	var revoked sql.NullString
	if err := row.Scan(&t.ID, &t.Name, &ids, &t.CanTrigger, &t.CreatedAt, &t.ExpiresAt, &revoked, &grants, &t.AuthzRevision); err != nil {
		return nil, err
	}
	if err := json.Unmarshal([]byte(ids), &t.ProjectIDs); err != nil {
		return nil, err
	}
	if err := json.Unmarshal([]byte(grants), &t.ConfigGrants); err != nil {
		return nil, ErrMCPTokenInvalid
	}
	var err error
	t.ConfigGrants, err = NormalizeMCPConfigGrants(t.ProjectIDs, t.ConfigGrants)
	if err != nil || t.AuthzRevision < 0 {
		return nil, ErrMCPTokenInvalid
	}
	if revoked.Valid {
		t.RevokedAt = &revoked.String
	}
	return &t, nil
}

// Verify always reads persistence, so existing clients cannot cache revocation.
func (s *MCPTokenStore) Verify(ctx context.Context, plaintext string) (*MCPToken, error) {
	if len(plaintext) != 64 {
		return nil, ErrMCPTokenInvalid
	}
	if _, err := hex.DecodeString(plaintext); err != nil {
		return nil, ErrMCPTokenInvalid
	}
	t, err := scanMCPToken(configstate.Queryer(ctx, s.db).QueryRowContext(ctx, `SELECT `+mcpTokenColumns+` FROM mcp_tokens WHERE token_hash = ?`, mcpDigest(plaintext)))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrMCPTokenInvalid
		}
		return nil, err
	}
	return validateMCPToken(t)
}

func validateMCPToken(t *MCPToken) (*MCPToken, error) {
	expires, err := time.Parse(time.RFC3339, t.ExpiresAt)
	if err != nil || t.RevokedAt != nil || !time.Now().Before(expires) {
		return nil, ErrMCPTokenInvalid
	}
	if len(t.ProjectIDs) < 1 || len(t.ProjectIDs) > 32 {
		return nil, ErrMCPTokenInvalid
	}
	return t, nil
}

// LoadMCPToken re-reads authorization using the caller's exact transaction.
// lock must be true for an apply transaction so revocation serializes with it.
func LoadMCPToken(ctx context.Context, q configstate.DBTX, db *sql.DB, id string, lock bool) (*MCPToken, error) {
	stmt := "SELECT " + mcpTokenColumns + " FROM mcp_tokens WHERE id=?"
	if lock && store.DialectOf(db) == store.MySQL {
		stmt += " FOR UPDATE"
	}
	t, err := scanMCPToken(q.QueryRowContext(ctx, stmt, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrMCPTokenInvalid
	}
	if err != nil {
		return nil, err
	}
	return validateMCPToken(t)
}
func (s *MCPTokenStore) VerifyByIDTx(ctx context.Context, q configstate.DBTX, id string) (*MCPToken, error) {
	return LoadMCPToken(ctx, q, s.db, id, true)
}

func (s *MCPTokenStore) UpdateConfigGrants(ctx context.Context, id string, expected int64, grants []MCPConfigGrant) (*MCPToken, error) {
	var out *MCPToken
	err := configstate.WithWriteTxContext(ctx, s.db, func(ctx context.Context, q configstate.DBTX) error {
		t, err := LoadMCPToken(ctx, q, s.db, id, true)
		if err != nil {
			return err
		}
		if t.AuthzRevision != expected {
			return configstate.ErrConflict
		}
		normalized, err := NormalizeMCPConfigGrants(t.ProjectIDs, grants)
		if err != nil {
			return err
		}
		blob, _ := json.Marshal(normalized)
		r, err := q.ExecContext(ctx, "UPDATE mcp_tokens SET config_grants_json=?,authz_revision=authz_revision+1 WHERE id=? AND authz_revision=? AND revoked_at IS NULL", string(blob), id, expected)
		if err != nil {
			return err
		}
		n, err := r.RowsAffected()
		if err != nil {
			return err
		}
		if n != 1 {
			return configstate.ErrConflict
		}
		out, err = LoadMCPToken(ctx, q, s.db, id, false)
		return err
	})
	return out, err
}

func (s *MCPTokenStore) List(ctx context.Context) ([]MCPToken, error) {
	rows, err := configstate.Queryer(ctx, s.db).QueryContext(ctx, `SELECT `+mcpTokenColumns+` FROM mcp_tokens ORDER BY created_at DESC, id LIMIT 500`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []MCPToken{}
	for rows.Next() {
		t, err := scanMCPToken(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *t)
	}
	return out, rows.Err()
}

func (s *MCPTokenStore) Revoke(ctx context.Context, id string) error {
	q := configstate.Queryer(ctx, s.db)
	res, err := q.ExecContext(ctx, `UPDATE mcp_tokens SET authz_revision=CASE WHEN revoked_at IS NULL THEN authz_revision+1 ELSE authz_revision END,revoked_at = COALESCE(revoked_at, ?) WHERE id = ?`, time.Now().UTC().Format(time.RFC3339), id)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		// MySQL may report zero changed rows for an already-revoked token.
		var exists int
		if err := q.QueryRowContext(ctx, `SELECT COUNT(*) FROM mcp_tokens WHERE id = ?`, id).Scan(&exists); err != nil {
			return err
		}
		if exists == 0 {
			return ErrMCPTokenNotFound
		}
	}
	return nil
}
