package tenancy

import (
	"context"
	"errors"
	"testing"
)

func TestMissingControlCannotDisableExistingTenancy(t *testing.T) {
	r, s := testRepository(t)
	ctx := context.Background()
	if _, err := r.CreateTeam(ctx, "Existing"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.Exec(`DELETE FROM access_control`); err != nil {
		t.Fatal(err)
	}
	if _, err := QueryPredicate(ctx, s.DB, "project.read", "p.id"); !errors.Is(err, ErrConflict) {
		t.Fatal("missing control became unrestricted", err)
	}
	if err := r.InitializeControl(ctx); !errors.Is(err, ErrConflict) {
		t.Fatal("missing control reset", err)
	}
}
func TestAuthenticatedContextAndIllegalModeFailClosed(t *testing.T) {
	_, s := testRepository(t)
	ctx := context.Background()
	if _, err := s.DB.Exec(`DELETE FROM access_control`); err != nil {
		t.Fatal(err)
	}
	actorctx := WithActor(ctx, Actor{UserID: "user"}, "team")
	if _, err := QueryPredicate(actorctx, s.DB, "project.read", "p.id"); !errors.Is(err, ErrConflict) {
		t.Fatal("authenticated context lost mode", err)
	}
	if _, err := s.DB.Exec(`INSERT INTO access_control(id,enabled,ever_enabled,mode_epoch,authz_revision) VALUES(1,2,1,1,1)`); err != nil {
		t.Fatal(err)
	}
	if _, err := QueryPredicate(ctx, s.DB, "project.read", "p.id"); !errors.Is(err, ErrConflict) {
		t.Fatal("illegal mode became off", err)
	}
	if _, err := NewRepository(s.DB).Mode(ctx); !errors.Is(err, ErrConflict) {
		t.Fatal(err)
	}
}

func TestUnknownPersistentCatalogFailsClosed(t *testing.T) {
	accounts, r, actor := accountFixture(t)
	ctx := context.Background()
	if _, err := r.db.Exec(`UPDATE access_control SET catalog_version=2 WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Mode(ctx); !errors.Is(err, ErrConflict) {
		t.Fatal("unknown catalog accepted", err)
	}
	if _, err := QueryPredicate(WithActor(ctx, actor, "team"), r.db, "project.read", "p.id"); !errors.Is(err, ErrConflict) {
		t.Fatal(err)
	}
	if _, err := accounts.Create(ctx, actor, CreateUserInput{Username: "blocked", DisplayName: "Blocked"}); !errors.Is(err, ErrForbidden) {
		t.Fatal("account mutation accepted unknown catalog", err)
	}
}
