package auth

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/huangchengsir/pipewright/internal/storetest"
)

func TestMCPTokenLifecycle(t *testing.T) {
	db := storetest.OpenDB(t)
	ctx := context.Background()
	stamp := time.Now().UTC().Format(time.RFC3339)
	credential := uuid.NewString()
	projectID := uuid.NewString()
	if _, err := db.Exec(`INSERT INTO credentials (id,name,type,scope,ciphertext,masked_value,created_at,updated_at) VALUES (?,'test','git_token','',X'00','m',?,?)`, credential, stamp, stamp); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO projects (id,name,repo_url,default_branch,credential_id,created_at,updated_at) VALUES (?,'test','https://example.invalid/repo','main',?,?,?)`, projectID, credential, stamp, stamp); err != nil {
		t.Fatal(err)
	}
	s := NewMCPTokenStore(db)
	plaintext, item, err := s.Create(ctx, MCPTokenInput{Name: "reader", ProjectIDs: []string{projectID}})
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := hex.DecodeString(plaintext)
	if err != nil || len(decoded) != 32 {
		t.Fatal("not 32 random bytes")
	}
	if item.CanTrigger || item.RevokedAt != nil {
		t.Fatal("unsafe defaults")
	}
	expires, _ := time.Parse(time.RFC3339, item.ExpiresAt)
	created, _ := time.Parse(time.RFC3339, item.CreatedAt)
	if expires.Sub(created) != 7*24*time.Hour {
		t.Fatal("default TTL")
	}
	var stored string
	if err := db.QueryRow(`SELECT token_hash FROM mcp_tokens WHERE id=?`, item.ID).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte(plaintext))
	if stored != hex.EncodeToString(sum[:]) || stored == plaintext {
		t.Fatal("plaintext storage")
	}
	fresh, err := NewMCPTokenStore(db).Verify(ctx, plaintext)
	if err != nil {
		t.Fatal(err)
	}
	if !fresh.Allows(projectID, false) || fresh.Allows(projectID, true) || fresh.Allows("other", false) {
		t.Fatal("permissions")
	}
	if _, err := NewSessionStore(db).Get(plaintext); !errors.Is(err, ErrSessionNotFound) {
		t.Fatal("MCP token authenticates admin")
	}
	list, err := s.List(ctx)
	if err != nil || len(list) != 1 {
		t.Fatal(err)
	}
	blob, _ := json.Marshal(list)
	if strings.Contains(string(blob), plaintext) || strings.Contains(string(blob), stored) {
		t.Fatal("token list discloses secret")
	}
	if err := s.Revoke(ctx, item.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.Revoke(ctx, item.ID); err != nil {
		t.Fatal("revoke not idempotent", err)
	}
	if _, err := NewMCPTokenStore(db).Verify(ctx, plaintext); !errors.Is(err, ErrMCPTokenInvalid) {
		t.Fatal("revoke not persisted", err)
	}
	list, _ = s.List(ctx)
	if list[0].RevokedAt == nil {
		t.Fatal("revocation metadata missing")
	}
	trigger, _, err := s.Create(ctx, MCPTokenInput{Name: "trigger", ProjectIDs: []string{projectID}, CanTrigger: true, ExpiresInDays: 1})
	if err != nil {
		t.Fatal(err)
	}
	fresh, _ = s.Verify(ctx, trigger)
	if !fresh.Allows(projectID, true) {
		t.Fatal("explicit trigger permission missing")
	}
	if _, err := db.Exec(`UPDATE mcp_tokens SET expires_at=? WHERE id=?`, time.Now().Add(-time.Second).UTC().Format(time.RFC3339), fresh.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Verify(ctx, trigger); !errors.Is(err, ErrMCPTokenInvalid) {
		t.Fatal("expired token accepted")
	}
	for _, bad := range []string{"", plaintext + "a", strings.Repeat("z", 64), strings.Repeat("0", 64)} {
		if _, err := s.Verify(ctx, bad); !errors.Is(err, ErrMCPTokenInvalid) {
			t.Fatal("invalid accepted", err)
		}
	}
	for _, in := range []MCPTokenInput{
		{Name: "empty projects"}, {Name: "unknown", ProjectIDs: []string{uuid.NewString()}},
		{Name: "duplicate", ProjectIDs: []string{projectID, projectID}}, {Name: "too many", ProjectIDs: make([]string, 33)},
		{Name: "bad TTL", ProjectIDs: []string{projectID}, ExpiresInDays: 31}, {Name: "bad TTL", ProjectIDs: []string{projectID}, ExpiresInDays: -1},
		{Name: strings.Repeat("x", 129), ProjectIDs: []string{projectID}}, {Name: "bad\nname", ProjectIDs: []string{projectID}},
	} {
		if _, _, err := s.Create(ctx, in); !errors.Is(err, ErrMCPTokenInput) {
			t.Fatalf("invalid input accepted: %#v: %v", in, err)
		}
	}
}
