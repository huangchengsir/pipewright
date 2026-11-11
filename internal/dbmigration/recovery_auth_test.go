package dbmigration

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRecoverySessionSurvivesRestartAndRejectsExpiredOrReplacedToken(t *testing.T) {
	dir := t.TempDir()
	key := [32]byte{8}
	now := time.Now().UTC().Truncate(time.Second)
	c, e := AcquireControl(dir, &key)
	if e != nil {
		t.Fatal(e)
	}
	a, b := profiles()
	s, e := c.Plan(a, b)
	if e != nil {
		t.Fatal(e)
	}
	token, csrf, e := c.IssueRecoverySession(s.TaskID, now)
	if e != nil {
		t.Fatal(e)
	}
	for _, name := range []string{"state", "recovery"} {
		raw, e := os.ReadFile(filepath.Join(dir, "database-migration", name+".sealed"))
		if e != nil {
			t.Fatal(e)
		}
		if bytes.Contains(raw, []byte(token)) || bytes.Contains(raw, []byte(csrf)) {
			t.Fatal("clear recovery secret")
		}
	}
	c.Close()
	c, e = AcquireControl(dir, &key)
	if e != nil {
		t.Fatal(e)
	}
	defer c.Close()
	if got, e := c.RecoverySession(token, now.Add(time.Minute)); e != nil || got != csrf {
		t.Fatal("restart lost valid session", e)
	}
	if _, e = c.RecoverySession(token, now.Add(RecoverySessionLifetime)); !errors.Is(e, ErrRecoveryAuth) {
		t.Fatal("expired session accepted", e)
	}
	next, _, e := c.IssueRecoverySession(s.TaskID, now.Add(time.Minute))
	if e != nil {
		t.Fatal(e)
	}
	if _, e = c.RecoverySession(token, now.Add(time.Minute)); !errors.Is(e, ErrRecoveryAuth) {
		t.Fatal("replaced token accepted", e)
	}
	if _, e = c.RecoverySession(next, now.Add(time.Minute)); e != nil {
		t.Fatal(e)
	}
	if _, e = c.AbortBeforeActivation(s.TaskID, s.Revision); e != nil {
		t.Fatal(e)
	}
	if _, e = c.RecoverySession(next, now.Add(time.Minute)); !errors.Is(e, ErrRecoveryAuth) {
		t.Fatal("aborted task session accepted", e)
	}
}
