package dbmigration

import (
	"errors"
	"testing"

	"github.com/google/uuid"
)

func terminalFixture(t *testing.T, phase Phase) (*Control, *State, ActiveConfig) {
	t.Helper()
	key := [32]byte{71}
	c, err := AcquireControl(t.TempDir(), &key)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	a, b := profiles()
	a.Managed = true
	s, err := c.Plan(a, b)
	if err != nil {
		t.Fatal(err)
	}
	s.Target.Endpoint.Identity = uuid.NewString()
	s.Target.Managed = true
	s.Phase = phase
	actual := s.Source
	if phase == Completed {
		actual = s.Target
	}
	if err = c.write("state", s); err != nil {
		t.Fatal(err)
	}
	if err = c.write("active", actual); err != nil {
		t.Fatal(err)
	}
	return c, s, actual
}

func TestTerminalBootRotatesOnlyCredentials(t *testing.T) {
	for _, phase := range []Phase{Completed, Aborted} {
		t.Run(string(phase), func(t *testing.T) {
			c, _, actual := terminalFixture(t, phase)
			original, err := c.read("state")
			if err != nil {
				t.Fatal(err)
			}
			actual.Connection.DSN = "replacement-credential-dsn"
			if ok, err := c.CheckBoot(actual); err != nil || !ok {
				t.Fatal(ok, err)
			}
			saved, err := c.Active()
			if err != nil || saved.Connection != actual.Connection {
				t.Fatal("new connection not durable", err)
			}
			history, err := c.read("state")
			if err != nil || string(history) != string(original) {
				t.Fatal("historical receipt/profile rewritten", err)
			}
			if ok, err := c.CheckBoot(actual); err != nil || !ok {
				t.Fatal("second boot", ok, err)
			}
			_, next := profiles()
			next.Endpoint.Database = "next-database"
			next.Paths = actual.Paths
			if _, err = c.Plan(actual, next); err != nil {
				t.Fatal("next migration with rotated connection", err)
			}
		})
	}
}

func TestTerminalBootRejectsChangedIdentityOrControlBoundary(t *testing.T) {
	changes := map[string]func(*ActiveConfig){
		"identity":     func(a *ActiveConfig) { a.Endpoint.Identity = uuid.NewString() },
		"server":       func(a *ActiveConfig) { a.Endpoint.Server = "different" },
		"database":     func(a *ActiveConfig) { a.Endpoint.Database = "different" },
		"schema":       func(a *ActiveConfig) { a.Endpoint.Schema = "different" },
		"fileIdentity": func(a *ActiveConfig) { a.Endpoint.FileID = "different" },
		"driver":       func(a *ActiveConfig) { a.Connection.Driver = "dm" },
		"paths":        func(a *ActiveConfig) { a.Paths.Artifacts = "different" },
		"generation":   func(a *ActiveConfig) { a.Generation++ },
		"task":         func(a *ActiveConfig) { a.TaskID = uuid.NewString() },
		"managed":      func(a *ActiveConfig) { a.Managed = !a.Managed },
	}
	for name, change := range changes {
		t.Run(name, func(t *testing.T) {
			c, _, actual := terminalFixture(t, Completed)
			original := actual
			actual.Connection.DSN = "replacement-credential-dsn"
			change(&actual)
			if ok, err := c.CheckBoot(actual); err != nil || ok {
				t.Fatal("changed boundary accepted", ok, err)
			}
			saved, err := c.Active()
			if err != nil || profileDigest(*saved) != profileDigest(original) {
				t.Fatal("rejected input persisted", err)
			}
		})
	}
}

func TestPreparedCredentialRotationInvalidatesApproval(t *testing.T) {
	c, s, actual := terminalFixture(t, Prepared)
	actual.Connection.DSN = "replacement-credential-dsn"
	if ok, err := c.CheckBoot(actual); err != nil || !ok {
		t.Fatal(ok, err)
	}
	updated, err := c.State()
	if err != nil || updated.Phase != Aborted || updated.Revision != s.Revision+1 {
		t.Fatal("preflight not invalidated", err)
	}
	if _, err = c.BeginQuiesce(s.TaskID, s.Revision); !errors.Is(err, ErrInvalidState) {
		t.Fatal("stale approval started", err)
	}
	// Model a crash after the authenticated invalidation but before active was
	// replaced. A new boot rechecks the same identity and finishes the local write.
	if err = c.write("active", s.Source); err != nil {
		t.Fatal(err)
	}
	if ok, err := c.CheckBoot(actual); err != nil || !ok {
		t.Fatal("partial rotation not reconciled", ok, err)
	}
	saved, err := c.Active()
	if err != nil || saved.Connection != actual.Connection {
		t.Fatal("rotation not durable", err)
	}
}

func TestActiveMigrationDoesNotPermitCredentialRotation(t *testing.T) {
	for _, phase := range []Phase{Quiescing, Frozen, Copying, Verified, ActivationIntent, AwaitingRestart} {
		t.Run(string(phase), func(t *testing.T) {
			c, _, actual := terminalFixture(t, phase)
			actual.Connection.DSN = "replacement-credential-dsn"
			if ok, err := c.CheckBoot(actual); err != nil || ok {
				t.Fatal("unfinished migration opened", ok, err)
			}
		})
	}
}
