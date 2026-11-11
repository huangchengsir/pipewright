package dbmigration

import (
	"bytes"
	"context"
	"errors"
	"github.com/google/uuid"
	"os"
	"path/filepath"
	"testing"
)

func profiles() (ActiveConfig, ActiveConfig) {
	a := ActiveConfig{Connection: Connection{Driver: "sqlite", DSN: "source-secret-dsn"}, Endpoint: Endpoint{Dialect: "sqlite", FilePath: "/source", FileID: "one", Identity: uuid.NewString()}}
	b := ActiveConfig{Connection: Connection{Driver: "postgres", DSN: "postgres://user:target-secret-password@host/db"}, Endpoint: Endpoint{Dialect: "postgres", Server: "host:5432", Database: "db", Schema: "new"}}
	return a, b
}
func TestControlRequiresProcessDepartureAndVerifiedRestart(t *testing.T) {
	root := t.TempDir()
	var key [32]byte
	key[0] = 42
	c, err := AcquireControl(root, &key)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { c.Close() }()
	if _, err := AcquireControl(root, &key); !errors.Is(err, ErrLocked) {
		t.Fatalf("second owner allowed: %v", err)
	}
	a, b := profiles()
	s, err := c.Plan(a, b)
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := c.CheckBoot(a); err != nil || !ok {
		t.Fatal(ok, err)
	}
	s, err = c.BeginQuiesce(s.TaskID, s.Revision)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Takeover(s.TaskID, s.Revision); !errors.Is(err, ErrLocked) {
		t.Fatalf("same process became frozen: %v", err)
	}
	if ok, _ := c.CheckBoot(a); ok {
		t.Fatal("business allowed while quiescing")
	}
	c.Close()
	c, err = AcquireControl(root, &key)
	if err != nil {
		t.Fatal(err)
	}
	s, err = c.Takeover(s.TaskID, s.Revision)
	if err != nil {
		t.Fatal(err)
	}
	endpoint := b.Endpoint
	endpoint.Identity = uuid.NewString()
	s, err = c.BeginCopy(s.TaskID, s.Revision, endpoint)
	if err != nil {
		t.Fatal(err)
	}
	source := database(t, true)
	dest := database(t, false)
	target, err := InitializeTarget(context.Background(), dest, uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	report, err := CopySnapshot(context.Background(), source, target)
	if err != nil {
		t.Fatal(err)
	}
	s, err = c.RecordVerified(s.TaskID, s.Revision, report)
	if err != nil {
		t.Fatal(err)
	}
	s, err = c.StageActivation(s.TaskID, s.Revision)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.ConfirmActivation(s.TaskID, s.Revision, s.Target, report); !errors.Is(err, ErrLocked) {
		t.Fatal("in-process activation accepted", err)
	}
	if _, err := c.AbortBeforeActivation(s.TaskID, s.Revision); !errors.Is(err, ErrInvalidState) {
		t.Fatal("unsafe rollback accepted", err)
	}
	c.Close()
	c, err = AcquireControl(root, &key)
	if err != nil {
		t.Fatal(err)
	}
	wrong := s.Target
	wrong.Generation++
	if _, err := c.ConfirmActivation(s.TaskID, s.Revision, wrong, report); !errors.Is(err, ErrMismatch) {
		t.Fatal("wrong generation accepted", err)
	}
	fresh, err := VerifySnapshot(context.Background(), source, target)
	if err != nil {
		t.Fatal(err)
	}
	s, err = c.ConfirmActivation(s.TaskID, s.Revision, s.Target, fresh)
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := c.CheckBoot(s.Target); err != nil || !ok {
		t.Fatal(ok, err)
	}
	if ok, _ := c.CheckBoot(a); ok {
		t.Fatal("old source reopened after activation")
	}
	if _, err := c.AbortBeforeActivation(s.TaskID, s.Revision); !errors.Is(err, ErrInvalidState) {
		t.Fatal("completed rollback accepted")
	}
}
func TestJournalCiphertextWrongKeyCorruptionAndMissingLock(t *testing.T) {
	for _, scenario := range []string{"wrong-key", "corrupt", "replaced-lock"} {
		t.Run(scenario, func(t *testing.T) {
			root := t.TempDir()
			var key [32]byte
			key[0] = 7
			c, err := AcquireControl(root, &key)
			if err != nil {
				t.Fatal(err)
			}
			a, b := profiles()
			if _, err = c.Plan(a, b); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(root, "database-migration", "state.sealed")
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			for _, secret := range []string{a.Connection.DSN, b.Connection.DSN, "target-secret-password"} {
				if bytes.Contains(data, []byte(secret)) {
					t.Fatal("plaintext persisted")
				}
			}
			c.Close()
			switch scenario {
			case "wrong-key":
				key[0]++
			case "corrupt":
				data[len(data)-1] ^= 1
				if err = os.WriteFile(path, data, 0600); err != nil {
					t.Fatal(err)
				}
			case "replaced-lock":
				p := filepath.Join(root, "database-migration", "runtime.lock")
				if err = os.Rename(p, p+".previous"); err != nil {
					t.Fatal(err)
				}
			}
			if next, err := AcquireControl(root, &key); !errors.Is(err, ErrJournal) {
				if next != nil {
					next.Close()
				}
				t.Fatalf("corrupt state accepted %v", err)
			}
		})
	}
}
func TestVerificationDoesNotRepairMissingData(t *testing.T) {
	ctx := context.Background()
	source := database(t, true)
	dest := database(t, false)
	target, err := InitializeTarget(ctx, dest, uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = VerifySnapshot(ctx, source, target); !errors.Is(err, ErrMismatch) {
		t.Fatal(err)
	}
	var n int
	if err = dest.DB.QueryRow("SELECT count(*) FROM database_migration_batches").Scan(&n); err != nil || n != 1 {
		t.Fatal("verification wrote receipt", n, err)
	}
}
