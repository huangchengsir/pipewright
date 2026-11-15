package tenancy

import (
	"context"
	"errors"
	"github.com/huangchengsir/pipewright/internal/auth"
	"github.com/huangchengsir/pipewright/internal/config"
	"testing"
)

func TestFirstEnableAtomicPreservesLegacyAndRevoke(t *testing.T) {
	r, s := testRepository(t)
	ctx := context.Background()
	old := auth.NewService(s.DB, nil)
	if err := old.Bootstrap("Original.Admin", "old-admin-password"); err != nil {
		t.Fatal(err)
	}
	oldSession, err := old.Login("Original.Admin", "old-admin-password")
	if err != nil {
		t.Fatal(err)
	}
	for _, stmt := range []string{
		`INSERT INTO credentials(id,name,type,ciphertext,masked_value,created_at,updated_at) VALUES('credential','git','git_token',X'00','hidden','now','now')`,
		`INSERT INTO projects(id,name,repo_url,credential_id,created_at,updated_at) VALUES('p','Existing','https://example.invalid/repo','credential','now','now')`,
		`INSERT INTO project_environments(project_id,chain_json,updated_at) VALUES('p','[{"name":"test","gated":false}]','now')`,
		`INSERT INTO login_identities(provider,issuer,subject,display_label,bound_at) VALUES('github','github','123','Existing','now')`,
		`INSERT INTO mcp_tokens(id,name,token_hash,project_ids_json,created_at,expires_at) VALUES('legacy-token','token','hash','[]','now','later')`,
	} {
		if _, err = s.DB.Exec(stmt); err != nil {
			t.Fatal(err)
		}
	}
	off, err := r.ReconcileMode(ctx, config.MultiTenantMode{}, false)
	if err != nil || off.Enabled {
		t.Fatal(off, err)
	}
	var n int
	s.DB.QueryRow(`SELECT COUNT(*) FROM access_users`).Scan(&n)
	if n != 0 {
		t.Fatal("default-off imported users")
	}
	on := config.MultiTenantMode{Enabled: true, Explicit: true}
	state, err := r.ReconcileMode(ctx, on, true)
	if err != nil {
		t.Fatal(err)
	}
	if !state.Enabled || state.Epoch != 2 {
		t.Fatal(state)
	}
	if _, err = old.Verify(oldSession.Token); err == nil {
		t.Fatal("legacy session survived transition")
	}
	var user, team string
	if err = s.DB.QueryRow(`SELECT bootstrap_user_id,default_team_id FROM access_control WHERE id=1`).Scan(&user, &team); err != nil {
		t.Fatal(err)
	}
	var aHash, oldHash, username string
	if err = s.DB.QueryRow(`SELECT username,password_hash FROM access_users WHERE id=?`, user).Scan(&username, &aHash); err != nil {
		t.Fatal(err)
	}
	s.DB.QueryRow(`SELECT password_hash FROM admin_user WHERE id=1`).Scan(&oldHash)
	if aHash != oldHash || username != "Original.Admin" {
		t.Fatal("legacy identity changed")
	}
	var owner any
	var mapped, purpose string
	if err = s.DB.QueryRow(`SELECT tenant_id,owner_id FROM access_ownership WHERE resource_kind='project' AND resource_id='p'`).Scan(&mapped, &owner); err != nil {
		t.Fatal(err)
	}
	if mapped != team || owner != nil {
		t.Fatal("legacy asset owner was invented")
	}
	if err = s.DB.QueryRow(`SELECT purpose FROM access_environments WHERE project_id='p' AND name='test'`).Scan(&purpose); err != nil || purpose != "other" {
		t.Fatal(purpose, err)
	}
	s.DB.QueryRow(`SELECT COUNT(*) FROM access_identities WHERE user_id=? AND subject='123'`, user).Scan(&n)
	if n != 1 {
		t.Fatal("identity binding not preserved")
	}
	s.DB.QueryRow(`SELECT COUNT(*) FROM mcp_tokens WHERE revoked_at IS NOT NULL`).Scan(&n)
	if n != 1 {
		t.Fatal("MCP token not revoked")
	}
	again, err := r.ReconcileMode(ctx, on, true)
	if err != nil || again != state {
		t.Fatal("restart changed mode", again, err)
	}
	s.DB.QueryRow(`SELECT COUNT(*) FROM access_teams`).Scan(&n)
	if n != 1 {
		t.Fatal("team duplicated")
	}
	if _, err = r.ReconcileMode(ctx, config.MultiTenantMode{}, true); !errors.Is(err, ErrExplicitModeRequired) {
		t.Fatal(err)
	}
	disabled, err := r.ReconcileMode(ctx, config.MultiTenantMode{Explicit: true}, true)
	if err != nil || disabled.Enabled || disabled.Epoch != 3 {
		t.Fatal(disabled, err)
	}
	// A legitimate password change in off mode becomes the same bootstrap identity on re-enable.
	signed, err := old.Login("Original.Admin", "old-admin-password")
	if err != nil {
		t.Fatal(err)
	}
	if err = old.ChangePassword("old-admin-password", "off-mode-new-password", signed.Token); err != nil {
		t.Fatal(err)
	}
	if _, err = r.ReconcileMode(ctx, on, true); err != nil {
		t.Fatal(err)
	}
	accounts := NewAccounts(s.DB)
	if _, err = accounts.Login(ctx, "original.admin", "old-admin-password"); err == nil {
		t.Fatal("old password resurrected")
	}
	if _, err = accounts.Login(ctx, "ORIGINAL.ADMIN", "off-mode-new-password"); err != nil {
		t.Fatal(err)
	}
}
func TestModeBlockedDoesNotPartiallyBootstrap(t *testing.T) {
	r, s := testRepository(t)
	ctx := context.Background()
	if err := auth.NewService(s.DB, nil).Bootstrap("admin", "password-value"); err != nil {
		t.Fatal(err)
	}
	on := config.MultiTenantMode{Enabled: true, Explicit: true}
	if _, err := r.ReconcileMode(ctx, on, false); !errors.Is(err, ErrNotReady) {
		t.Fatal(err)
	}
	if _, err := s.DB.Exec(`INSERT INTO selfupdate_leases(id,kind,created_at) VALUES('unresolved','terminal_pty',1)`); err != nil {
		t.Fatal(err)
	}
	if _, err := r.ReconcileMode(ctx, on, true); !errors.Is(err, ErrUnfinishedOperations) {
		t.Fatal(err)
	}
	state, err := r.Mode(ctx)
	if err != nil || state.Enabled || state.Epoch != 1 {
		t.Fatal(state, err)
	}
	var count int
	s.DB.QueryRow(`SELECT COUNT(*) FROM access_users`).Scan(&count)
	if count != 0 {
		t.Fatal("partial bootstrap")
	}
	// Default-off startup must not become a new maintenance blocker.
	if _, err = s.DB.Exec(`UPDATE selfupdate_control SET task_id='existing-upgrade' WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	if _, err = r.ReconcileMode(ctx, config.MultiTenantMode{}, false); err != nil {
		t.Fatal(err)
	}
}

func TestFirstEnablePreservesAllLegacyPlatformBindings(t *testing.T) {
	r, st := testRepository(t)
	ctx := context.Background()
	if e := auth.NewService(st.DB, nil).Bootstrap("original", "legacy-admin-password"); e != nil {
		t.Fatal(e)
	}
	providers := []string{"github", "gitlab", "gitee", "google", "microsoft"}
	for _, provider := range providers {
		if _, e := st.DB.Exec(`INSERT INTO login_identities(provider,issuer,subject,display_label,bound_at) VALUES(?,?,?,'Original','now')`, provider, "https://identity.example/"+provider, "same-subject"); e != nil {
			t.Fatal(e)
		}
	}
	for attempt := 0; attempt < 2; attempt++ {
		if _, e := r.ReconcileMode(ctx, config.MultiTenantMode{Enabled: true, Explicit: true}, true); e != nil {
			t.Fatal(e)
		}
		var bootstrap string
		if e := st.DB.QueryRow(`SELECT bootstrap_user_id FROM access_control WHERE id=1`).Scan(&bootstrap); e != nil {
			t.Fatal(e)
		}
		var count int
		if e := st.DB.QueryRow(`SELECT COUNT(*) FROM access_identities`).Scan(&count); e != nil || count != 5 {
			t.Fatal("bindings duplicated or lost", count, e)
		}
		for _, provider := range providers {
			var owner, issuer, subject string
			if e := st.DB.QueryRow(`SELECT user_id,issuer,subject FROM access_identities WHERE provider=?`, provider).Scan(&owner, &issuer, &subject); e != nil || owner != bootstrap || issuer != "https://identity.example/"+provider || subject != "same-subject" {
				t.Fatal("identity remapped", provider, e)
			}
		}
	}
}
