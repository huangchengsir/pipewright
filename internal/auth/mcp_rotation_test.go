package auth

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/huangchengsir/pipewright/internal/storetest"
)

func TestMCPTokenRotationReclaimsOnlyEndedMetadata(t *testing.T) {
	for _, ended := range []bool{true, false} {
		t.Run(map[bool]string{true: "rotation", false: "active_limit"}[ended], func(t *testing.T) {
			db := storetest.OpenDB(t)
			ctx := context.Background()
			stamp := time.Now().UTC().Format(time.RFC3339)
			credential, projectID := uuid.NewString(), uuid.NewString()
			if _, err := db.Exec(`INSERT INTO credentials (id,name,type,scope,ciphertext,masked_value,created_at,updated_at) VALUES (?,'fixture','git_token','',X'00','m',?,?)`, credential, stamp, stamp); err != nil {
				t.Fatal(err)
			}
			if _, err := db.Exec(`INSERT INTO projects (id,name,repo_url,default_branch,credential_id,created_at,updated_at) VALUES (?,'fixture','https://example.invalid/repo','main',?,?,?)`, projectID, credential, stamp, stamp); err != nil {
				t.Fatal(err)
			}
			s := NewMCPTokenStore(db)
			active, item, err := s.Create(ctx, MCPTokenInput{Name: "keep", ProjectIDs: []string{projectID}})
			if err != nil {
				t.Fatal(err)
			}
			tx, err := db.BeginTx(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			oldScope := ""
			for i := 0; i < 499; i++ {
				id := uuid.NewString()
				oldScope = id
				expires := item.ExpiresAt
				var revoked any
				if ended {
					if i%2 == 0 {
						expires = time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)
					} else {
						revoked = stamp
					}
				}
				if _, err := tx.ExecContext(ctx, `INSERT INTO mcp_tokens (id,name,token_hash,project_ids_json,can_trigger,created_at,expires_at,revoked_at) VALUES (?,'historical',?,'[]',0,?,?,?)`, id, mcpDigest(id), stamp, expires, revoked); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO mcp_run_requests (scope,request_id,fingerprint,run_id) VALUES (?,'retained',?,?)`, oldScope, strings.Repeat("a", 64), uuid.NewString()); err != nil {
				t.Fatal(err)
			}
			if err := tx.Commit(); err != nil {
				t.Fatal(err)
			}
			_, _, err = s.Create(ctx, MCPTokenInput{Name: "next", ProjectIDs: []string{projectID}})
			if ended && err != nil || !ended && !errors.Is(err, ErrMCPTokenInput) {
				t.Fatal("rotation or active quota incorrect", err)
			}
			if _, err := s.Verify(ctx, active); err != nil {
				t.Fatal("active token reclaimed", err)
			}
			var count int
			if err := db.QueryRow(`SELECT COUNT(*) FROM mcp_run_requests WHERE scope = ?`, oldScope).Scan(&count); err != nil || count != 1 {
				t.Fatal("request tombstone reclaimed", count, err)
			}
		})
	}
}
