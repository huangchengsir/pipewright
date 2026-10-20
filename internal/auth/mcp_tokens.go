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
)

var (
	ErrMCPTokenInvalid  = errors.New("auth: invalid MCP token")
	ErrMCPTokenInput    = errors.New("auth: invalid MCP token input")
	ErrMCPTokenNotFound = errors.New("auth: MCP token not found")
	ErrMCPForbidden     = errors.New("auth: MCP permission denied")
)

// MCPToken contains only administration metadata, never the stored digest.
type MCPToken struct {
	ID         string   `json:"id"`
	Name       string   `json:"name"`
	ProjectIDs []string `json:"projectIds"`
	CanTrigger bool     `json:"canTrigger"`
	CreatedAt  string   `json:"createdAt"`
	ExpiresAt  string   `json:"expiresAt"`
	RevokedAt  *string  `json:"revokedAt"`
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
	Name          string   `json:"name"`
	ProjectIDs    []string `json:"projectIds"`
	CanTrigger    bool     `json:"canTrigger"`
	ExpiresInDays int      `json:"expiresInDays"`
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
	token := &MCPToken{ID: uuid.NewString(), Name: in.Name, ProjectIDs: ids, CanTrigger: in.CanTrigger, CreatedAt: stamp.Format(time.RFC3339), ExpiresAt: stamp.Add(time.Duration(in.ExpiresInDays) * 24 * time.Hour).Format(time.RFC3339)}
	blob, _ := json.Marshal(ids)
	_, err = tx.ExecContext(ctx, `INSERT INTO mcp_tokens (id, name, token_hash, project_ids_json, can_trigger, created_at, expires_at) VALUES (?, ?, ?, ?, ?, ?, ?)`, token.ID, token.Name, mcpDigest(plaintext), string(blob), token.CanTrigger, token.CreatedAt, token.ExpiresAt)
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

const mcpTokenColumns = `id, name, project_ids_json, can_trigger, created_at, expires_at, revoked_at`

func scanMCPToken(row interface{ Scan(...any) error }) (*MCPToken, error) {
	var t MCPToken
	var ids string
	var revoked sql.NullString
	if err := row.Scan(&t.ID, &t.Name, &ids, &t.CanTrigger, &t.CreatedAt, &t.ExpiresAt, &revoked); err != nil {
		return nil, err
	}
	if err := json.Unmarshal([]byte(ids), &t.ProjectIDs); err != nil {
		return nil, err
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
	t, err := scanMCPToken(s.db.QueryRowContext(ctx, `SELECT `+mcpTokenColumns+` FROM mcp_tokens WHERE token_hash = ?`, mcpDigest(plaintext)))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrMCPTokenInvalid
		}
		return nil, err
	}
	expires, err := time.Parse(time.RFC3339, t.ExpiresAt)
	if err != nil || t.RevokedAt != nil || !time.Now().Before(expires) {
		return nil, ErrMCPTokenInvalid
	}
	if len(t.ProjectIDs) < 1 || len(t.ProjectIDs) > 32 {
		return nil, ErrMCPTokenInvalid
	}
	return t, nil
}

func (s *MCPTokenStore) List(ctx context.Context) ([]MCPToken, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+mcpTokenColumns+` FROM mcp_tokens ORDER BY created_at DESC, id LIMIT 500`)
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
	res, err := s.db.ExecContext(ctx, `UPDATE mcp_tokens SET revoked_at = COALESCE(revoked_at, ?) WHERE id = ?`, time.Now().UTC().Format(time.RFC3339), id)
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
		if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM mcp_tokens WHERE id = ?`, id).Scan(&exists); err != nil {
			return err
		}
		if exists == 0 {
			return ErrMCPTokenNotFound
		}
	}
	return nil
}
