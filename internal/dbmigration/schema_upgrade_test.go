package dbmigration

import (
	"github.com/google/uuid"
	"strings"
	"testing"
)

func TestHistoricalJournalDoesNotBlockLaterSchemaUpgrade(t *testing.T) {
	key := [32]byte{4}
	c, e := AcquireControl(t.TempDir(), &key)
	if e != nil {
		t.Fatal(e)
	}
	defer c.Close()
	a, b := profiles()
	s, e := c.Plan(a, b)
	if e != nil {
		t.Fatal(e)
	}
	s.Manifest = strings.Repeat("a", 64)
	s.Target.Endpoint.Identity = uuid.NewString()
	s.Phase = Completed
	if e = c.write("state", s); e != nil {
		t.Fatal(e)
	}
	if e = c.write("active", s.Target); e != nil {
		t.Fatal(e)
	}
	if allowed, e := c.CheckBoot(s.Target); e != nil || !allowed {
		t.Fatal("completed history blocked upgrade", e)
	}
	nextTarget := a
	nextTarget.Endpoint.Identity = ""
	nextTarget.Endpoint.FilePath = "/new-empty-target"
	nextTarget.Endpoint.FileID = "new"
	if _, e = c.Plan(s.Target, nextTarget); e != nil {
		t.Fatal("new migration after schema upgrade", e)
	}
	s.Phase = Aborted
	if e = c.write("state", s); e != nil {
		t.Fatal(e)
	}
	// A real return-to-source persists active=Source before the Aborted record.
	// The earlier completed fixture deliberately still points at Target here;
	// that inconsistent control state must not be accepted as old-schema history.
	if allowed, e := c.CheckBoot(s.Source); e == nil || allowed {
		t.Fatal("wrong active database accepted for aborted history", e)
	}
	if e = c.write("active", s.Source); e != nil {
		t.Fatal(e)
	}
	if allowed, e := c.CheckBoot(s.Source); e != nil || !allowed {
		t.Fatal("aborted history blocked upgrade", e)
	}
	s.Phase = Prepared
	if e = c.write("state", s); e != nil {
		t.Fatal(e)
	}
	if _, e = c.BeginQuiesce(s.TaskID, s.Revision); e == nil {
		t.Fatal("obsolete preflight started")
	}
	if allowed, e := c.CheckBoot(s.Source); e != nil || !allowed {
		t.Fatal("prepared task prevented re-preflight", e)
	}
	fresh, e := c.Plan(a, b)
	if e != nil || fresh.Manifest != ManifestDigest() || fresh.TaskID == s.TaskID {
		t.Fatal("fresh preflight unavailable", e)
	}
	s.Phase = Copying
	if e = c.write("state", s); e != nil {
		t.Fatal(e)
	}
	if _, e = c.State(); e == nil {
		t.Fatal("unfinished migration accepted another manifest")
	}
}
