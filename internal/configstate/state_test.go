package configstate

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/huangchengsir/pipewright/internal/store"
	"github.com/huangchengsir/pipewright/internal/storetest"
)

func seedProject(t *testing.T, st *store.Store) {
	t.Helper()
	for _, stmt := range []string{
		`INSERT INTO credentials(id,name,type,scope,ciphertext,masked_value,created_at,updated_at) VALUES ('c','c','git_token','',X'00','m','old','old')`,
		`INSERT INTO projects(id,name,repo_url,default_branch,credential_id,created_at,updated_at) VALUES ('p','before','https://example.invalid/r','main','c','old','old')`,
	} {
		if _, err := st.DB.Exec(stmt); err != nil {
			t.Fatal(err)
		}
	}
}

func TestTransactionsRevisionAndHooks(t *testing.T) {
	storetest.ForEachDialect(t, func(t *testing.T, st *store.Store) {
		seedProject(t, st)
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if _, err := ReadRevision(ctx, st.DB, "p"); !errors.Is(err, ErrNotFound) {
			t.Fatalf("read initialized missing row: %v", err)
		}
		called := 0
		err := WithWriteTxContext(ctx, st.DB, func(ctx context.Context, q DBTX) error {
			rev, err := LockProject(ctx, q, st.DB, "p")
			if err != nil {
				return err
			}
			if rev != 1 {
				t.Fatalf("initial revision %d", rev)
			}
			if Queryer(ctx, st.DB) != q {
				t.Fatal("transaction context not preserved")
			}
			if !AfterCommit(ctx, func() {
				var name string
				if err := st.DB.QueryRow("SELECT name FROM projects WHERE id='p'").Scan(&name); err != nil || name != "after" {
					t.Errorf("postcommit uses released connection: %s/%v", name, err)
				}
				called++
			}) {
				t.Fatal("callback not registered")
			}
			return WithWriteTx(ctx, st.DB, func(nested DBTX) error {
				if nested != q {
					t.Fatal("nested transaction did not reuse connection")
				}
				if _, err := nested.ExecContext(ctx, "UPDATE projects SET name='after' WHERE id='p'"); err != nil {
					return err
				}
				_, err := BumpRevision(ctx, nested, "p", rev)
				return err
			})
		})
		if err != nil || called != 1 {
			t.Fatalf("commit/hook %v/%d", err, called)
		}
		failure := errors.New("injected failure")
		err = WithWriteTxContext(ctx, st.DB, func(ctx context.Context, q DBTX) error {
			AfterCommit(ctx, func() { called++ })
			rev, err := LockProject(ctx, q, st.DB, "p")
			if err != nil {
				return err
			}
			if _, err = BumpRevision(ctx, q, "p", rev); err != nil {
				return err
			}
			return failure
		})
		if !errors.Is(err, failure) || called != 1 {
			t.Fatalf("rollback/hook %v/%d", err, called)
		}
		rev, err := ReadRevision(ctx, st.DB, "p")
		if err != nil || rev != 2 {
			t.Fatalf("rollback revision %d/%v", rev, err)
		}
		err = WithReadTxContext(ctx, st.DB, func(ctx context.Context, q DBTX) error {
			return WithWriteTx(ctx, st.DB, func(DBTX) error { return nil })
		})
		if !errors.Is(err, ErrReadOnly) {
			t.Fatalf("read transaction upgraded: %v", err)
		}
	})
}

func TestActivityAndDeletionTombstone(t *testing.T) {
	storetest.ForEachDialect(t, func(t *testing.T, st *store.Store) {
		seedProject(t, st)
		ctx := context.Background()
		var activity *Activity
		err := WithWriteTx(ctx, st.DB, func(q DBTX) error {
			var err error
			activity, err = AcquireActivityTx(ctx, q, st.DB, Activity{ProjectID: "p", OwnerEpoch: "boot-1", Kind: "run", Phase: "executing"})
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
		if !errors.Is(CheckIdle(ctx, st.DB, "p"), ErrBusy) {
			t.Fatal("activity did not block configuration")
		}
		if !errors.Is(ReleaseActivityTx(ctx, st.DB, activity.ID, "different-owner"), ErrOwner) {
			t.Fatal("foreign owner released activity")
		}
		if err := SetActivityPhaseTx(ctx, st.DB, activity.ID, "boot-1", "finalizing"); err != nil {
			t.Fatal(err)
		}
		if !errors.Is(CheckIdle(ctx, st.DB, "p"), ErrBusy) {
			t.Fatal("terminal finalization was considered idle")
		}
		if err := ReleaseActivityTx(ctx, st.DB, activity.ID, "boot-1"); err != nil {
			t.Fatal(err)
		}
		if err := ReleaseActivityTx(ctx, st.DB, activity.ID, "boot-1"); err != nil {
			t.Fatal("release not idempotent", err)
		}
		if err := CheckIdle(ctx, st.DB, "p"); err != nil {
			t.Fatal(err)
		}
		err = WithWriteTx(ctx, st.DB, func(q DBTX) error {
			rev, err := LockProject(ctx, q, st.DB, "p")
			if err != nil {
				return err
			}
			if err := MarkDeleted(ctx, q, "p", rev); err != nil {
				return err
			}
			_, err = q.ExecContext(ctx, "DELETE FROM projects WHERE id='p'")
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := ReadRevision(ctx, st.DB, "p"); !errors.Is(err, ErrNotFound) {
			t.Fatal("deleted project readable", err)
		}
		var count int
		if err := st.DB.QueryRow("SELECT COUNT(*) FROM project_config_versions WHERE project_id='p'").Scan(&count); err != nil || count != 1 {
			t.Fatal("deletion erased guard tombstone", err, count)
		}
	})
}

func TestEpochsAndStaleCAS(t *testing.T) {
	storetest.ForEachDialect(t, func(t *testing.T, st *store.Store) {
		seedProject(t, st)
		ctx := context.Background()
		err := WithWriteTx(ctx, st.DB, func(q DBTX) error {
			if err := LockEpochs(ctx, q, st.DB, EpochSecrets, EpochPolicy); err != nil {
				return err
			}
			if err := BumpEpoch(ctx, q, EpochSecrets); err != nil {
				return err
			}
			rev, err := LockProject(ctx, q, st.DB, "p")
			if err != nil {
				return err
			}
			_, err = BumpRevision(ctx, q, "p", rev)
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
		if value, err := ReadEpoch(ctx, st.DB, EpochSecrets); err != nil || value != 2 {
			t.Fatal(value, err)
		}
		if _, err := BumpRevision(ctx, st.DB, "p", 1); !errors.Is(err, ErrConflict) {
			t.Fatalf("stale version accepted: %v", err)
		}
	})
}

func TestSQLiteSeparateConnectionsSerializeCAS(t *testing.T) {
	path := filepath.Join(t.TempDir(), "shared.db")
	first, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	seedProject(t, first)
	second, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := WithWriteTx(ctx, first.DB, func(q DBTX) error { return EnsureProject(ctx, q, first.DB, "p") }); err != nil {
		t.Fatal(err)
	}
	locked := make(chan struct{})
	release := make(chan struct{})
	result := make(chan error, 1)
	go func() {
		result <- WithWriteTx(ctx, first.DB, func(q DBTX) error {
			if _, err := LockProject(ctx, q, first.DB, "p"); err != nil {
				return err
			}
			close(locked)
			<-release
			_, err := BumpRevision(ctx, q, "p", 1)
			return err
		})
	}()
	<-locked
	secondStarted := make(chan struct{})
	secondResult := make(chan error, 1)
	go func() {
		close(secondStarted)
		secondResult <- WithWriteTx(ctx, second.DB, func(q DBTX) error {
			if _, err := LockProject(ctx, q, second.DB, "p"); err != nil {
				return err
			}
			_, err := BumpRevision(ctx, q, "p", 1)
			return err
		})
	}()
	<-secondStarted
	close(release)
	if err := <-result; err != nil {
		t.Fatal(err)
	}
	if err := <-secondResult; !errors.Is(err, ErrConflict) {
		t.Fatalf("second writer bypassed CAS: %v", err)
	}
}
