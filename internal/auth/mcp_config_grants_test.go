package auth

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/huangchengsir/pipewright/internal/configstate"
	"github.com/huangchengsir/pipewright/internal/store"
	"github.com/huangchengsir/pipewright/internal/storetest"
)

func TestMCPConfigGrantsAndRevocation(t *testing.T) {
	storetest.ForEachDialect(t, func(t *testing.T, st *store.Store) {
		ctx := context.Background()
		pid := uuid.NewString()
		cid := uuid.NewString()
		now := time.Now().UTC().Format(time.RFC3339)
		if _, err := st.DB.Exec(`INSERT INTO credentials(id,name,type,scope,ciphertext,masked_value,created_at,updated_at) VALUES (?,'c','git_token','',X'00','m',?,?)`, cid, now, now); err != nil {
			t.Fatal(err)
		}
		if _, err := st.DB.Exec(`INSERT INTO projects(id,name,repo_url,default_branch,credential_id,created_at,updated_at) VALUES (?,'p','https://example.invalid/r','main',?,?,?)`, pid, cid, now, now); err != nil {
			t.Fatal(err)
		}
		service := NewMCPTokenStore(st.DB)
		legacy, old, err := service.Create(ctx, MCPTokenInput{Name: "legacy", ProjectIDs: []string{pid}, CanTrigger: true})
		if err != nil {
			t.Fatal(err)
		}
		if len(old.ConfigGrants) != 0 || old.AuthzRevision != 0 || old.AllowsConfig(pid, "config.read", "project", "") {
			t.Fatal("legacy token implicitly gained configuration rights")
		}
		if fresh, err := service.Verify(ctx, legacy); err != nil || !fresh.Allows(pid, true) {
			t.Fatal("legacy trigger changed", err)
		}
		grant := MCPConfigGrant{ProjectID: pid, Read: true, Propose: true, Apply: true, Sections: []string{"project"}, Operations: []string{"project.set_name"}}
		raw, token, err := service.Create(ctx, MCPTokenInput{Name: "config", ProjectIDs: []string{pid}, ConfigGrants: []MCPConfigGrant{grant}})
		if err != nil {
			t.Fatal(err)
		}
		if !token.AllowsConfig(pid, "config.apply", "project", "project.set_name") || token.AllowsConfig(pid, "config.apply", "project", "project.set_default_branch") || token.AllowsConfig(pid, "config.read", "pipeline", "") || token.Allows(pid, true) {
			t.Fatal("configuration selectors or trigger separation failed")
		}
		err = configstate.WithWriteTx(ctx, st.DB, func(q configstate.DBTX) error {
			fresh, err := LoadMCPToken(ctx, q, st.DB, token.ID, true)
			if err != nil {
				return err
			}
			if !fresh.AllowsConfig(pid, "config.apply", "project", "project.set_name") {
				t.Fatal("transaction authorization lost")
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		readOnly := grant
		readOnly.Propose = false
		readOnly.Apply = false
		readOnly.Operations = nil
		updated, err := service.UpdateConfigGrants(ctx, token.ID, 0, []MCPConfigGrant{readOnly})
		if err != nil || updated.AuthzRevision != 1 {
			t.Fatal("authorization CAS update", err)
		}
		if _, err := service.UpdateConfigGrants(ctx, token.ID, 0, []MCPConfigGrant{grant}); !errors.Is(err, configstate.ErrConflict) {
			t.Fatal("stale grants overwrote shrink", err)
		}
		fresh, err := service.Verify(ctx, raw)
		if err != nil || fresh.AllowsConfig(pid, "config.apply", "project", "project.set_name") || !fresh.AllowsConfig(pid, "config.read", "project", "") {
			t.Fatal("shrink not effective", err)
		}
		if err := service.Revoke(ctx, token.ID); err != nil {
			t.Fatal(err)
		}
		if err := service.Revoke(ctx, token.ID); err != nil {
			t.Fatal(err)
		}
		if _, err := service.Verify(ctx, raw); !errors.Is(err, ErrMCPTokenInvalid) {
			t.Fatal("revoked token still valid", err)
		}
		var revision int64
		if err := st.DB.QueryRow("SELECT authz_revision FROM mcp_tokens WHERE id=?", token.ID).Scan(&revision); err != nil || revision != 2 {
			t.Fatal("revoke version not monotonic/idempotent", revision, err)
		}
	})
}

func TestMCPConfigGrantValidation(t *testing.T) {
	cases := []MCPConfigGrant{
		{ProjectID: "other", Read: true, Sections: []string{"project"}},
		{ProjectID: "p", Read: true},
		{ProjectID: "p", Read: true, Apply: true, Sections: []string{"project"}},
		{ProjectID: "p", Read: true, Propose: true, Sections: []string{"project"}},
		{ProjectID: "p", Read: true, Propose: true, Sections: []string{"project"}, Operations: []string{"step.set_limits"}},
		{ProjectID: "p", Read: true, Sections: []string{"project", "project"}},
		{ProjectID: "p", Read: true, Propose: true, Sections: []string{"project"}, Operations: []string{"arbitrary.exec"}},
	}
	for i, grant := range cases {
		if _, err := NormalizeMCPConfigGrants([]string{"p"}, []MCPConfigGrant{grant}); !errors.Is(err, ErrMCPTokenInput) {
			t.Fatalf("invalid grant %d accepted: %v", i, err)
		}
	}
	if grants, err := NormalizeMCPConfigGrants([]string{"p"}, nil); err != nil || grants == nil || len(grants) != 0 {
		t.Fatal("default is not explicit empty grants")
	}
}
