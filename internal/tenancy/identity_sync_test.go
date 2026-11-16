package tenancy

import (
	"context"
	"errors"
	"github.com/huangchengsir/pipewright/internal/auth"
	"github.com/huangchengsir/pipewright/internal/config"
	"testing"
)

func TestBootstrapIdentityModeSwitchDoesNotResurrectOrSteal(t *testing.T) {
	repo, st := testRepository(t)
	ctx := context.Background()
	if err := auth.NewService(st.DB, nil).Bootstrap("admin", "administrator-password"); err != nil {
		t.Fatal(err)
	}
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := st.DB.Exec(q, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO login_identities(provider,issuer,subject,display_label,bound_at) VALUES('github','github','old','Original','now')`)
	on, off := config.MultiTenantMode{Enabled: true, Explicit: true}, config.MultiTenantMode{Explicit: true}
	if _, err := repo.ReconcileMode(ctx, on, true); err != nil {
		t.Fatal(err)
	}
	var bootstrap string
	if err := st.DB.QueryRow(`SELECT bootstrap_user_id FROM access_control WHERE id=1`).Scan(&bootstrap); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.ReconcileMode(ctx, off, true); err != nil {
		t.Fatal(err)
	}
	exec(`DELETE FROM login_identities WHERE provider='github'`)
	if _, err := repo.ReconcileMode(ctx, on, true); err != nil {
		t.Fatal(err)
	}
	var n int
	st.DB.QueryRow(`SELECT COUNT(*) FROM access_identities WHERE user_id=?`, bootstrap).Scan(&n)
	if n != 0 {
		t.Fatal("off-mode unbind resurrected", n)
	}
	// A new enabled-mode binding is mirrored on shutdown; the original one is not.
	exec(`INSERT INTO access_identities(id,user_id,provider,issuer,subject,identity_key,generation,created_at) VALUES('new',?,'github','github','new',?,2,1)`, bootstrap, externalIdentityKey("github", "github", "new"))
	if _, err := repo.ReconcileMode(ctx, off, true); err != nil {
		t.Fatal(err)
	}
	var subject string
	if err := st.DB.QueryRow(`SELECT subject FROM login_identities WHERE provider='github'`).Scan(&subject); err != nil || subject != "new" {
		t.Fatal(subject, err)
	}
	// Legitimate off-mode replacement becomes the same bootstrap user's binding.
	exec(`UPDATE login_identities SET subject='replacement',identity_generation=3 WHERE provider='github'`)
	if _, err := repo.ReconcileMode(ctx, on, true); err != nil {
		t.Fatal(err)
	}
	if err := st.DB.QueryRow(`SELECT subject FROM access_identities WHERE user_id=?`, bootstrap).Scan(&subject); err != nil || subject != "replacement" {
		t.Fatal(subject, err)
	}
	accounts := NewAccounts(st.DB)
	admin, err := accounts.Login(ctx, "admin", "administrator-password")
	if err != nil {
		t.Fatal(err)
	}
	employee, err := accounts.Create(ctx, admin.Actor, CreateUserInput{Username: "employee", DisplayName: "Employee"})
	if err != nil {
		t.Fatal(err)
	}
	exec(`INSERT INTO access_identities(id,user_id,provider,issuer,subject,identity_key,generation,created_at) VALUES('employee-id',?,'github','github','employee-subject',?,1,1)`, employee.User.ID, externalIdentityKey("github", "github", "employee-subject"))
	if _, err = repo.ReconcileMode(ctx, off, true); err != nil {
		t.Fatal(err)
	}
	exec(`UPDATE login_identities SET subject='employee-subject' WHERE provider='github'`)
	if _, err = repo.ReconcileMode(ctx, on, true); !errors.Is(err, ErrConflict) {
		t.Fatal("identity takeover transition accepted", err)
	}
	state, err := repo.Mode(ctx)
	if err != nil || state.Enabled {
		t.Fatal("failed transition was not atomic", state, err)
	}
	if err = st.DB.QueryRow(`SELECT user_id FROM access_identities WHERE identity_key=?`, externalIdentityKey("github", "github", "employee-subject")).Scan(&subject); err != nil || subject != employee.User.ID {
		t.Fatal("employee identity changed", subject, err)
	}
}
