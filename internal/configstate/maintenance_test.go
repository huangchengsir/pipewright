package configstate

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/huangchengsir/pipewright/internal/store"
	"github.com/huangchengsir/pipewright/internal/storetest"
)

func maintenanceFixture(t *testing.T, st *store.Store, owner string) (*MaintenanceService, string) {
	t.Helper()
	seedProject(t, st)
	ctx := context.Background()
	id := uuid.NewString()
	err := WithWriteTxContext(ctx, st.DB, func(txctx context.Context, q DBTX) error {
		_, err := AcquireActivityTx(txctx, q, st.DB, Activity{ID: id, ProjectID: "p", OwnerEpoch: owner, Kind: "run", Phase: "finalizing"})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	s := NewMaintenance(st.DB, &MaintenanceOptions{AuthorizeAdmin: func(context.Context, DBTX, string) error { return nil }})
	return s, id
}
func maintenanceRequest(t *testing.T, s *MaintenanceService, id string) MaintenanceRecoverRequest {
	t.Helper()
	snapshot, err := s.List(context.Background(), "admin")
	if err != nil {
		t.Fatal(err)
	}
	for _, i := range snapshot.Items {
		if i.ActivityID == id {
			return MaintenanceRecoverRequest{ActivityID: id, ExpectedDigest: i.Digest, EvidenceID: i.EvidenceID}
		}
	}
	t.Fatal("hold missing from maintenance list")
	return MaintenanceRecoverRequest{}
}

func TestMaintenanceRejectsLegacyForeignAndActiveReaders(t *testing.T) {
	storetest.ForEachDialect(t, func(t *testing.T, st *store.Store) {
		s, id := maintenanceFixture(t, st, "legacy-unreconciled")
		in := maintenanceRequest(t, s, id)
		in.EvidenceID = "client-cannot-assert-exit"
		if _, err := s.Recover(context.Background(), "admin", in); !errors.Is(err, ErrEvidenceUnavailable) {
			t.Fatal(err)
		}
		// Even accidentally registering a legacy row cannot turn it recoverable.
		if err := StartActivityReaders(id, "legacy-unreconciled"); err != nil {
			t.Fatal(err)
		}
		RecordActivityReadersExited(id, "legacy-unreconciled")
		if in := maintenanceRequest(t, s, id); in.EvidenceID != "" {
			t.Fatal("legacy received exit proof")
		}
		ForgetActivityReaders(id, "legacy-unreconciled")
		foreign := uuid.NewString()
		if err := WithWriteTxContext(context.Background(), st.DB, func(ctx context.Context, q DBTX) error {
			_, err := AcquireActivityTx(ctx, q, st.DB, Activity{ID: foreign, ProjectID: "p", OwnerEpoch: "other-instance", Kind: "deploy", Phase: "executing"})
			return err
		}); err != nil {
			t.Fatal(err)
		}
		if in := maintenanceRequest(t, s, foreign); in.EvidenceID != "" {
			t.Fatal("foreign owner received local proof")
		}
		if err := StartActivityReaders(foreign, "other-instance"); err != nil {
			t.Fatal(err)
		}
		if in := maintenanceRequest(t, s, foreign); in.EvidenceID != "" {
			t.Fatal("active reader received exit proof")
		}
		ForgetActivityReaders(foreign, "other-instance")
	})
}

func TestMaintenanceCASAuditAndOwnerFence(t *testing.T) {
	storetest.ForEachDialect(t, func(t *testing.T, st *store.Store) {
		ctx := context.Background()
		owner := uuid.NewString()
		s, id := maintenanceFixture(t, st, owner)
		if err := StartActivityReaders(id, owner); err != nil {
			t.Fatal(err)
		}
		RecordActivityReadersExited(id, owner)
		in := maintenanceRequest(t, s, id)
		if in.EvidenceID == "" {
			t.Fatal("local exit evidence absent")
		}
		if err := WithWriteTxContext(ctx, st.DB, func(ctx context.Context, q DBTX) error { _, err := BumpRevision(ctx, q, "p", 1); return err }); err != nil {
			t.Fatal(err)
		}
		if _, err := s.Recover(ctx, "admin", in); !errors.Is(err, ErrConflict) {
			t.Fatalf("stale digest accepted: %v", err)
		}
		in = maintenanceRequest(t, s, id)
		out, err := s.Recover(ctx, "admin", in)
		if err != nil {
			t.Fatal(err)
		}
		if out.State != "released" || out.ExecutorEpoch != 2 {
			t.Fatalf("recovery %+v", out)
		}
		if err := StartActivityReaders(id, owner); !errors.Is(err, ErrOwner) {
			t.Fatalf("recovered original owner resumed: %v", err)
		}
		var n int
		var current string
		if err := st.DB.QueryRow(`SELECT owner_epoch FROM project_activity WHERE activity_id=? AND released_at IS NOT NULL`, id).Scan(&current); err != nil || current == owner {
			t.Fatalf("owner not fenced %s/%v", current, err)
		}
		if err := st.DB.QueryRow(`SELECT COUNT(*) FROM audit_log WHERE action='config_activity_recover' AND target_id='p'`).Scan(&n); err != nil || n != 1 {
			t.Fatalf("atomic audit %d/%v", n, err)
		}
		if _, err := s.Recover(ctx, "admin", in); !errors.Is(err, ErrEvidenceUnavailable) {
			t.Fatalf("proof reused: %v", err)
		}
	})
}

func TestMaintenanceAuditFailureRollsBackRelease(t *testing.T) {
	storetest.ForEachDialect(t, func(t *testing.T, st *store.Store) {
		ctx := context.Background()
		owner := uuid.NewString()
		s, id := maintenanceFixture(t, st, owner)
		if err := StartActivityReaders(id, owner); err != nil {
			t.Fatal(err)
		}
		RecordActivityReadersExited(id, owner)
		in := maintenanceRequest(t, s, id)
		// Fault injection only: a failed append must roll back every prior write.
		stmt := `CREATE TRIGGER fail_recovery_audit BEFORE INSERT ON audit_log BEGIN SELECT RAISE(ABORT,'injected append failure'); END`
		if st.Dialect == store.MySQL {
			stmt = `CREATE TRIGGER fail_recovery_audit BEFORE INSERT ON audit_log FOR EACH ROW SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='injected append failure'`
		}
		if _, err := st.DB.Exec(stmt); err != nil {
			t.Fatal(err)
		}
		if _, err := s.Recover(ctx, "admin", in); err == nil || !strings.Contains(err.Error(), "injected append failure") {
			t.Fatalf("append failure missing: %v", err)
		}
		var n int
		if err := st.DB.QueryRow(`SELECT COUNT(*) FROM project_activity WHERE activity_id=? AND owner_epoch=? AND released_at IS NULL`, id, owner).Scan(&n); err != nil || n != 1 {
			t.Fatalf("release partially committed %d/%v", n, err)
		}
		if epoch, err := ReadEpoch(ctx, st.DB, EpochExecutor); err != nil || epoch != 1 {
			t.Fatalf("epoch partially committed %d/%v", epoch, err)
		}
		if again := maintenanceRequest(t, s, id); again.EvidenceID != in.EvidenceID {
			t.Fatal("rollback consumed local evidence")
		}
		ForgetActivityReaders(id, owner)
	})
}

func TestMaintenanceRecoverFencesConcurrentLocalAdmission(t *testing.T) {
	storetest.ForEachDialect(t, func(t *testing.T, st *store.Store) {
		ctx := context.Background()
		owner := uuid.NewString()
		s, id := maintenanceFixture(t, st, owner)
		if err := StartActivityReaders(id, owner); err != nil {
			t.Fatal(err)
		}
		RecordActivityReadersExited(id, owner)
		in := maintenanceRequest(t, s, id)
		entered, proceed := make(chan struct{}), make(chan struct{})
		s.opts.AuthorizeAdmin = func(context.Context, DBTX, string) error { close(entered); <-proceed; return nil }
		recovered, resumed := make(chan error, 1), make(chan error, 1)
		go func() { _, err := s.Recover(ctx, "admin", in); recovered <- err }()
		<-entered
		go func() { resumed <- StartActivityReaders(id, owner) }()
		close(proceed)
		if err := <-recovered; err != nil {
			t.Fatal(err)
		}
		if err := <-resumed; !errors.Is(err, ErrOwner) {
			t.Fatalf("late original reader not fenced: %v", err)
		}
	})
}
