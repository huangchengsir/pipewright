package dbmigration

import (
	"context"
	"github.com/huangchengsir/pipewright/internal/maintenance"
	"github.com/huangchengsir/pipewright/internal/store"
	"github.com/huangchengsir/pipewright/internal/vault"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRuntimeDrainCopyRestartAndActivationReceipt(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	key := [32]byte{91}
	sourcePath := filepath.Join(dir, "source.db")
	destPath := filepath.Join(dir, "target.db")
	source, e := store.Open(sourcePath)
	if e != nil {
		t.Fatal(e)
	}
	defer source.Close()
	cipher, e := vault.New(source.DB, &key).SealSecret([]byte("private secret 😀"))
	if e != nil {
		t.Fatal(e)
	}
	if _, e = source.DB.Exec("INSERT INTO credentials(id,name,type,scope,ciphertext,masked_value,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?)", "secret", "secret", "git_token", "", cipher, "***", "now", "now"); e != nil {
		t.Fatal(e)
	}
	if _, e = source.DB.Exec("INSERT INTO sessions(token,csrf_token,created_at,expires_at,last_seen_at) VALUES(?,?,?,?,?)", "old-session", "csrf", "now", "later", "now"); e != nil {
		t.Fatal(e)
	}
	endpoint, e := Describe(ctx, source, sourcePath)
	if e != nil {
		t.Fatal(e)
	}
	paths, e := ResolvePaths(dir, sourcePath, "", "", "")
	if e != nil {
		t.Fatal(e)
	}
	a := ActiveConfig{Connection: Connection{Driver: "sqlite", DSN: sourcePath}, Endpoint: endpoint, Paths: paths}
	b := ActiveConfig{Connection: Connection{Driver: "sqlite", DSN: destPath}, Endpoint: Endpoint{Dialect: "sqlite", FilePath: destPath}, Paths: paths, Managed: true}
	c, e := AcquireControl(dir, &key)
	if e != nil {
		t.Fatal(e)
	}
	defer func() { c.Close() }()
	s, e := c.Plan(a, b)
	if e != nil {
		t.Fatal(e)
	}
	restart := make(chan struct{}, 2)
	r := NewRuntime(c, a, source, &key, false, func() error { restart <- struct{}{}; return nil })
	release, e := maintenance.Enter(ctx, source.DB, "existing")
	if e != nil {
		t.Fatal(e)
	}
	s, e = r.Start(ctx, s.TaskID, s.Revision)
	if e != nil {
		t.Fatal(e)
	}
	blockers, e := r.Blockers(ctx)
	if e != nil || len(blockers) == 0 {
		t.Fatal("existing work not visible", e)
	}
	select {
	case <-restart:
		t.Fatal("restart before drain")
	default:
	}
	if done, e := maintenance.Enter(ctx, source.DB, "new"); e == nil {
		done()
		t.Fatal("new work admitted")
	}
	release()
	select {
	case <-restart:
	case <-time.After(5 * time.Second):
		t.Fatal("drain did not restart")
	}
	r.Close()
	c.Close()
	c, e = AcquireControl(dir, &key)
	if e != nil {
		t.Fatal(e)
	}
	r = NewRuntime(c, a, source, &key, true, func() error { return nil })
	defer r.Close()
	s, e = r.ContinueCopy(ctx, s.TaskID, s.Revision)
	if e != nil {
		t.Fatal(e)
	}
	if s.Phase != Verified {
		t.Fatal(s.Phase)
	}
	// A failed active-config replacement must not change the source fence, even
	// when the operator explicitly requests return-to-source before removing the
	// obstruction. Reconciliation can then verify the unchanged source/target.
	blockedPath := filepath.Join(dir, "database-migration", "active.sealed")
	if e = os.Mkdir(blockedPath, 0700); e != nil {
		t.Fatal(e)
	}
	if _, e = r.Stage(ctx, s.TaskID, s.Revision); e == nil {
		t.Fatal("blocked activation persisted")
	}
	s, e = c.State()
	if e != nil || s.Phase != ActivationIntent {
		t.Fatal("intent missing", e)
	}
	if _, e = r.Abort(ctx, s.TaskID, s.Revision); e == nil {
		t.Fatal("blocked source restore accepted")
	}
	var fence string
	if e = source.DB.QueryRow("SELECT task_id FROM selfupdate_control WHERE id=1").Scan(&fence); e != nil || fence != s.TaskID {
		t.Fatal("failed abort changed source fence", e)
	}
	if e = os.Remove(blockedPath); e != nil {
		t.Fatal(e)
	}
	s, e = r.Reconcile(ctx, s.TaskID, s.Revision)
	if e != nil {
		t.Fatal(e)
	}
	saved, e := os.ReadFile(filepath.Join(dir, "database-migration", "state.sealed"))
	if e != nil {
		t.Fatal(e)
	}
	r.Close()
	c.Close()
	c, e = AcquireControl(dir, &key)
	if e != nil {
		t.Fatal(e)
	}
	r = NewRuntime(c, s.Target, source, &key, true, func() error { return nil })
	defer r.Close()
	if _, e = r.ConfirmBoot(ctx, s.Target); e != nil {
		t.Fatal(e)
	}
	// The target commit succeeded but the durable completion acknowledgement was
	// lost: restore the preceding valid journal and inspect instead of replaying.
	if e = os.WriteFile(filepath.Join(dir, "database-migration", "state.sealed"), saved, 0600); e != nil {
		t.Fatal(e)
	}
	if _, e = r.Reconcile(ctx, s.TaskID, s.Revision); e != nil {
		t.Fatal("receipt recovery", e)
	}
	target, e := OpenExisting(s.Target.Connection)
	if e != nil {
		t.Fatal(e)
	}
	defer target.Close()
	for _, tc := range []struct {
		db    *store.Store
		table string
		want  int
	}{{source, "sessions", 1}, {target, "sessions", 0}, {target, "audit_log", 1}} {
		var count int
		if e = tc.db.DB.QueryRow("SELECT COUNT(*) FROM " + tc.table).Scan(&count); e != nil || count != tc.want {
			t.Fatal(tc.table, count, e)
		}
	}
	if allowed, e := c.CheckBoot(s.Target); e != nil || !allowed {
		t.Fatal("confirmed target not allowed", e)
	}
	if _, e = r.Abort(ctx, s.TaskID, s.Revision); e == nil {
		t.Fatal("completed migration rolled back")
	}
}

func TestReturnToSourceAfterStagedActivation(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	key := [32]byte{32}
	sourcePath := filepath.Join(dir, "source.db")
	targetPath := filepath.Join(dir, "target.db")
	source, e := store.Open(sourcePath)
	if e != nil {
		t.Fatal(e)
	}
	defer source.Close()
	endpoint, e := Describe(ctx, source, sourcePath)
	if e != nil {
		t.Fatal(e)
	}
	paths, e := ResolvePaths(dir, sourcePath, "", "", "")
	if e != nil {
		t.Fatal(e)
	}
	a := ActiveConfig{Connection: Connection{Driver: "sqlite", DSN: sourcePath}, Endpoint: endpoint, Paths: paths}
	b := ActiveConfig{Connection: Connection{Driver: "sqlite", DSN: targetPath}, Endpoint: Endpoint{Dialect: "sqlite", FilePath: targetPath}, Paths: paths, Managed: true}
	control, e := AcquireControl(dir, &key)
	if e != nil {
		t.Fatal(e)
	}
	defer func() { control.Close() }()
	state, e := control.Plan(a, b)
	if e != nil {
		t.Fatal(e)
	}
	state, e = control.BeginQuiesce(state.TaskID, state.Revision)
	if e != nil {
		t.Fatal(e)
	}
	if e = maintenance.Drain(ctx, source.DB, state.TaskID); e != nil {
		t.Fatal(e)
	}
	control.Close()
	control, e = AcquireControl(dir, &key)
	if e != nil {
		t.Fatal(e)
	}
	r := NewRuntime(control, a, source, &key, true, func() error { return nil })
	defer r.Close()
	state, e = r.ContinueCopy(ctx, state.TaskID, state.Revision)
	if e != nil {
		t.Fatal(e)
	}
	state, e = r.Stage(ctx, state.TaskID, state.Revision)
	if e != nil {
		t.Fatal(e)
	}
	state, e = r.Abort(ctx, state.TaskID, state.Revision)
	if e != nil || state.Phase != Aborted {
		t.Fatal("return to source failed", e)
	}
	active, e := control.Active()
	if e != nil || active == nil || profileDigest(*active) != profileDigest(a) {
		t.Fatal("wrong active profile", e)
	}
	var fence string
	if e = source.DB.QueryRow("SELECT task_id FROM selfupdate_control WHERE id=1").Scan(&fence); e != nil || fence != "" {
		t.Fatal("source remains fenced", e)
	}
	if allowed, e := control.CheckBoot(a); e != nil || !allowed {
		t.Fatal("source boot refused", e)
	}
	if _, e = os.Stat(targetPath); e != nil {
		t.Fatal("target evidence removed", e)
	}
}
