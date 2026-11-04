package kubernetes

import (
	"context"
	"errors"
	"github.com/huangchengsir/pipewright/internal/auth"
	"github.com/huangchengsir/pipewright/internal/store"
	"github.com/huangchengsir/pipewright/internal/storetest"
	"testing"
	"time"
)

func adminSession(t *testing.T, st *store.Store) *auth.Session {
	t.Helper()
	_, e := st.DB.Exec(`INSERT INTO admin_user(id,username,password_hash,created_at,updated_at) VALUES(1,'admin','test-only','now','now')`)
	if e != nil {
		t.Fatal(e)
	}
	s, e := auth.NewSessionStore(st.DB).Create()
	if e != nil {
		t.Fatal(e)
	}
	return s
}
func TestAdminMutationSessionAndRollback(t *testing.T) {
	storetest.ForEachDialect(t, func(t *testing.T, st *store.Store) {
		f := newFixture(t, st)
		sess := adminSession(t, st)
		ctx := context.Background()
		for _, tc := range []struct {
			name, query, token string
			want               error
		}{
			{"empty", "", "", ErrSession},
			{"missing", "", "missing", ErrSession},
			{"expired", `UPDATE sessions SET expires_at='2000-01-01T00:00:00Z'`, sess.Token, ErrSession},
			{"absolute", `UPDATE sessions SET expires_at='2099-01-01T00:00:00Z',absolute_expires_at='2000-01-01T00:00:00Z'`, sess.Token, ErrSession},
			{"unverified", `UPDATE sessions SET absolute_expires_at=NULL,password_verified_at=NULL`, sess.Token, ErrRecentPassword},
			{"future", `UPDATE sessions SET password_verified_at='2099-01-01T00:00:00Z'`, sess.Token, ErrRecentPassword},
			{"old", `UPDATE sessions SET password_verified_at='2000-01-01T00:00:00Z'`, sess.Token, ErrRecentPassword},
		} {
			t.Run(tc.name, func(t *testing.T) {
				if tc.query != "" {
					if _, e := st.DB.Exec(tc.query); e != nil {
						t.Fatal(e)
					}
				}
				called := false
				e := f.s.WithAdminMutation(ctx, tc.token, func(context.Context) error { called = true; return nil })
				if !errors.Is(e, tc.want) || called {
					t.Fatalf("gate err=%v called=%v", e, called)
				}
			})
		}
		st.DB.Exec(`UPDATE sessions SET password_verified_at=?`, time.Now().UTC().Format(time.RFC3339))
		sentinel := errors.New("rollback")
		e := f.s.WithAdminMutation(ctx, sess.Token, func(ctx context.Context) error {
			if _, e := f.s.SetCredentialEnabled(ctx, f.c.ID, f.c.Revision, false); e != nil {
				return e
			}
			return sentinel
		})
		if !errors.Is(e, sentinel) {
			t.Fatal(e)
		}
		c, e := f.s.GetCredential(ctx, f.c.ID)
		if e != nil || !c.Enabled || c.Revision != f.c.Revision {
			t.Fatal("partial write escaped transaction", e)
		}
		e = f.s.WithAdminMutation(ctx, sess.Token, func(ctx context.Context) error {
			_, e := f.s.SetCredentialEnabled(ctx, f.c.ID, f.c.Revision, false)
			return e
		})
		if e != nil {
			t.Fatal(e)
		}
		st.DB.Exec(`DELETE FROM sessions WHERE token=?`, sess.Token)
		if e = f.s.WithAdminMutation(ctx, sess.Token, func(context.Context) error { return nil }); !errors.Is(e, ErrSession) {
			t.Fatal("revoked session accepted", e)
		}
	})
}

func TestOwnerRecoveryOnlyProvenOwnersAndSettlement(t *testing.T) {
	st := storetest.Open(t)
	f := newFixture(t, st)
	ctx := context.Background()
	var ops []Operation
	for i, owner := range []string{"proven-owner", "different-directory-owner"} {
		run := []string{"run-1", "run-2"}[i]
		_, e := st.DB.Exec(`INSERT INTO pipeline_runs(id,project_id,status,created_at) VALUES(?,'project-1','failed','now')`, run)
		if e != nil {
			t.Fatal(e)
		}
		o := f.operation(t, run)
		_, e = st.DB.Exec(`UPDATE k8s_operations SET owner_epoch=? WHERE id=?`, owner, o.ID)
		if e != nil {
			t.Fatal(e)
		}
		o.OwnerEpoch = owner
		ops = append(ops, o)
	}
	controller := NewController(f.s, nil, "current-owner")
	if e := controller.RecoverSettled(ctx, []string{"proven-owner"}); e != nil {
		t.Fatal(e)
	}
	proven, e := f.s.GetOperation(ctx, ops[0].ID)
	if e != nil || !proven.OwnerFenced || proven.Cleanup != CleanupVerified || proven.Business != Failed {
		t.Fatal("proven never-started owner did not settle", e)
	}
	foreign, e := f.s.GetOperation(ctx, ops[1].ID)
	if e != nil || foreign.OwnerFenced || foreign.Business != NotDispatched {
		t.Fatal("unproven owner changed", e)
	}
	safe, e := f.s.SettledOwners(ctx, []string{"proven-owner", "different-directory-owner", "unused-owner"})
	if e != nil || len(safe) != 2 || safe[0] != "proven-owner" || safe[1] != "unused-owner" {
		t.Fatal("unsafe settlement", safe, e)
	}
}
