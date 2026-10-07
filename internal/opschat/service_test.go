package opschat

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/huangchengsir/pipewright/internal/audit"
	"github.com/huangchengsir/pipewright/internal/mask"
	"github.com/huangchengsir/pipewright/internal/storetest"
	"github.com/huangchengsir/pipewright/internal/target"
	"github.com/huangchengsir/pipewright/internal/vault"
)

func TestCancelDispatchedMutationUnknownAndDeleteNoOrphans(t *testing.T) {
	id := uuid.NewString()
	f := executor(id)
	dispatched := make(chan struct{}, 1)
	f.run = func(ctx context.Context, _ target.ConnectionSnapshot, argv []string, _ target.ExecutionLimits) (*target.LimitedResult, error) {
		if argv[0] == "docker" && argv[1] == "inspect" {
			return &target.LimitedResult{Stdout: strings.Repeat("a", 64) + "\n"}, nil
		}
		dispatched <- struct{}{}
		<-ctx.Done()
		return nil, ctx.Err()
	}
	s, _, db := fixture(t, f, nil)
	c := chat(t, s, id)
	r := submitTool(t, s, c, "docker_action", `{"container":"app","action":"restart"}`)
	snap := waitRun(t, s, c.ID, r.ID, AwaitingConfirmation)
	a := snap.Confirmations[0]
	if _, err := s.Confirm(context.Background(), c.ID, ConfirmInput{RunID: r.ID, Nonce: a.Nonce, Calls: a.Calls}); err != nil {
		t.Fatal(err)
	}
	<-dispatched
	if err := s.Delete(context.Background(), c.ID); err != ErrConflict {
		t.Fatal(err)
	}
	if _, err := s.Cancel(context.Background(), c.ID, r.ID); err != nil {
		t.Fatal(err)
	}
	snap = waitRun(t, s, c.ID, r.ID, Unknown)
	if snap.Calls[0].Status != Unknown {
		t.Fatal(snap.Calls)
	}
	waitJobs(t, s)
	if err := s.Delete(context.Background(), c.ID); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"ops_chat_runs", "ops_chat_calls", "ops_chat_entries", "ops_chat_approvals"} {
		var n int
		if err := db.QueryRow("SELECT COUNT(*) FROM "+table+" WHERE session_id=?", c.ID).Scan(&n); err != nil || n != 0 {
			t.Fatal(table, n, err)
		}
	}
	var n int
	if err := db.QueryRow("SELECT COUNT(*) FROM audit_log WHERE action='ops_chat_intent'").Scan(&n); err != nil || n != 1 {
		t.Fatal(n, err)
	}
}
func TestConcurrencyFourGlobalOnePerServerAndDetachedHTTP(t *testing.T) {
	ids := []string{uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()}
	f := executor(ids...)
	entered := make(chan struct{}, 8)
	release := make(chan struct{})
	f.run = func(ctx context.Context, _ target.ConnectionSnapshot, _ []string, _ target.ExecutionLimits) (*target.LimitedResult, error) {
		entered <- struct{}{}
		select {
		case <-release:
			return &target.LimitedResult{Stdout: "ok"}, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	s, _, _ := fixture(t, f, nil)
	runs := []*Run{}
	chats := []*Session{}
	for i := 0; i < 6; i++ {
		c := chat(t, s, ids[i%len(ids)])
		chats = append(chats, c)
		httpCtx, cancel := context.WithCancel(context.Background())
		r, e := s.Submit(httpCtx, c.ID, TurnInput{ClientRequestID: uuid.NewString(), Revision: c.Revision, ToolID: "host_ports", Args: json.RawMessage("{}")})
		cancel()
		if e != nil {
			t.Fatal(e)
		}
		runs = append(runs, r)
	}
	for i := 0; i < 4; i++ {
		select {
		case <-entered:
		case <-time.After(5 * time.Second):
			t.Fatal("slots blocked")
		}
	}
	select {
	case <-entered:
		t.Fatal("more than 4 SSH")
	case <-time.After(30 * time.Millisecond):
	}
	close(release)
	for i, r := range runs {
		waitRun(t, s, chats[i].ID, r.ID, Succeeded)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.peak != 4 || f.serverPeak != 1 {
		t.Fatal(f.peak, f.serverPeak)
	}
}
func TestRecoveryQueuedPlanningAndDispatchUnknown(t *testing.T) {
	id := uuid.NewString()
	f := executor(id)
	s, v, db := fixture(t, f, nil)
	chats := []*Session{chat(t, s, id), chat(t, s, id), chat(t, s, id)}
	runIDs := []string{uuid.NewString(), uuid.NewString(), uuid.NewString()}
	for i, status := range []string{Queued, Planning, Running} {
		c := chats[i]
		snap, _ := f.Capture(context.Background(), id)
		body, _ := s.seal(runBody{Turn: TurnInput{ToolID: "host_ports", Args: json.RawMessage("{}")}, Targets: []target.ConnectionSnapshot{snap}})
		_, e := db.Exec("INSERT INTO ops_chat_runs(id,session_id,request_id,payload_hash,status,body,created_at) VALUES(?,?,?,?,?,?,?)", runIDs[i], c.ID, uuid.NewString(), "hash", status, body, time.Now().UnixMilli())
		if e != nil {
			t.Fatal(e)
		}
		if _, e = db.Exec("UPDATE ops_chat_sessions SET active_run=? WHERE id=?", runIDs[i], c.ID); e != nil {
			t.Fatal(e)
		}
		if i == 2 {
			cid := uuid.NewString()
			cb, _ := s.seal(callBody{Snapshot: snap, Dispatched: true, View: Call{ID: cid, SessionID: c.ID, RunID: runIDs[i], ServerID: id, ToolID: "host_ports", Status: Running, TargetHash: snap.Fingerprint(), ArgsHash: "hash"}})
			if _, e = db.Exec("INSERT INTO ops_chat_calls(id,session_id,run_id,server_id,tool_id,status,args_hash,target_hash,body,created_at,ordinal) VALUES(?,?,?,?,?,?,?,?,?,?,?)", cid, c.ID, runIDs[i], id, "host_ports", Running, "hash", snap.Fingerprint(), cb, time.Now().UnixMilli(), 0); e != nil {
				t.Fatal(e)
			}
		}
	}
	if e := s.Close(context.Background()); e != nil {
		t.Fatal(e)
	}
	next, e := New(Options{DB: db, Vault: v, Executor: f})
	if e != nil {
		t.Fatal(e)
	}
	if e = next.Start(context.Background()); e != nil {
		t.Fatal(e)
	}
	defer next.Close(context.Background())
	for i, c := range chats {
		status := Interrupted
		if i == 2 {
			status = Unknown
		}
		snap := waitRun(t, next, c.ID, runIDs[i], status)
		if snap.Session.ActiveRunID != "" {
			t.Fatal(snap.Session)
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.invocations) != 0 {
		t.Fatal("restart replayed SSH")
	}
}
func TestProviderDisabledAndChangedTargetsRejectConfirmation(t *testing.T) {
	id := uuid.NewString()
	f := executor(id)
	m := model()
	m.info = ProviderInfo{}
	s, _, _ := fixture(t, f, m)
	if s.Capabilities().ModelAvailable {
		t.Fatal("disabled provider shown as available")
	}
	c := chat(t, s, id)
	if _, e := s.Submit(context.Background(), c.ID, TurnInput{ClientRequestID: uuid.NewString(), Revision: c.Revision, Text: "question"}); e != ErrUnavailable {
		t.Fatal(e)
	}
	r := submitTool(t, s, c, "systemd_action", `{"unit":"nginx.service","action":"restart"}`)
	snap := waitRun(t, s, c.ID, r.ID, AwaitingConfirmation)
	a := snap.Confirmations[0]
	f.mu.Lock()
	changed := f.snapshots[id]
	changed.CredentialVersion = "new ciphertext"
	f.snapshots[id] = changed
	f.mu.Unlock()
	if _, e := s.Confirm(context.Background(), c.ID, ConfirmInput{RunID: r.ID, Nonce: a.Nonce, Calls: a.Calls}); e != ErrApproval {
		t.Fatal(e)
	}
	if f.countMutation() != 0 {
		t.Fatal("changed target dispatched")
	}
}
func TestSharedReadBudgetProjectionAndSecretOutput(t *testing.T) {
	id := uuid.NewString()
	f := executor(id)
	s, v, _ := fixture(t, f, nil)
	if _, e := v.Create(vault.CreateInput{Name: "secret", Type: vault.TypeSSHPassword, Secret: "known-password"}); e != nil {
		t.Fatal(e)
	}
	f.run = func(_ context.Context, _ target.ConnectionSnapshot, argv []string, l target.ExecutionLimits) (*target.LimitedResult, error) {
		if argv[1] == "inspect" {
			return &target.LimitedResult{Stdout: strings.Repeat("a", 64) + "\n"}, nil
		}
		if l.Bytes != 64<<10-65 || l.Lines != 999 {
			t.Errorf("resolver and result not sharing budget: %+v", l)
		}
		return &target.LimitedResult{Stdout: "known-password\n", Stderr: "known-password\n", Truncated: true}, nil
	}
	c := chat(t, s, id)
	r := submitTool(t, s, c, "docker_logs", `{"container":"app"}`)
	snap := waitRun(t, s, c.ID, r.ID, Succeeded)
	if !snap.Calls[0].Truncated || strings.Contains(snap.Calls[0].Output, "known-password") {
		t.Fatal(snap.Calls)
	}
	for _, tool := range []string{"docker_inspect", "docker_containers", "host_processes"} {
		a := ToolArgs{Container: strings.Repeat("a", 64)}
		cmd := commands(tool, a)
		raw, _ := json.Marshal(cmd)
		if strings.Contains(string(raw), "Config") || strings.Contains(string(raw), "Env") || strings.Contains(string(raw), "args,") {
			t.Fatal(string(raw))
		}
	}
}
func TestEventsWatermarkDroppedWakeupsAndReset(t *testing.T) {
	s, _, db := fixture(t, executor(), nil)
	c := chat(t, s)
	err := s.transaction(context.Background(), true, func(tx *sql.Tx) error {
		for i := 0; i < 4001; i++ {
			if e := s.append(context.Background(), tx, Entry{SessionID: c.ID, Kind: "event"}, false, false); e != nil {
				return e
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	page, err := s.Events(context.Background(), c.ID, EventCursor(c.ID, 0), 100)
	if err != ErrReset || !page.Reset || page.Watermark != 4002 {
		t.Fatal(page, err)
	}
	page, err = s.Events(context.Background(), c.ID, EventCursor(c.ID, 2), 100)
	if err != nil || len(page.Entries) != 100 || page.Entries[0].Seq != 3 || page.Cursor != EventCursor(c.ID, 102) {
		t.Fatal(page, err)
	}
	if _, e := s.Events(context.Background(), c.ID, EventCursor(uuid.NewString(), 2), 100); e != ErrInvalid {
		t.Fatal(e)
	}
	if _, e := s.Entries(context.Background(), c.ID, "", 101); e != ErrInvalid {
		t.Fatal(e)
	}
	if _, e := db.Exec("UPDATE ops_chat_entries SET created_at=? WHERE session_id=?", time.Now().Add(-31*24*time.Hour).UnixMilli(), c.ID); e != nil {
		t.Fatal(e)
	}
	page, err = s.Events(context.Background(), c.ID, EventCursor(c.ID, 4001), 100)
	if err != ErrReset || !page.Reset {
		t.Fatal(page, err)
	}
}
func TestAdmissionQuotasAndBadPlanNoDispatch(t *testing.T) {
	id := uuid.NewString()
	f := executor(id)
	m := model()
	s, _, db := fixture(t, f, m)
	c := chat(t, s, id)
	for _, assignment := range []string{"visible_count=1975"} {
		if _, e := db.Exec("UPDATE ops_chat_sessions SET "+assignment+" WHERE id=?", c.ID); e != nil {
			t.Fatal(e)
		}
		if _, e := s.Submit(context.Background(), c.ID, TurnInput{ClientRequestID: uuid.NewString(), Revision: c.Revision, ToolID: "host_ports", Args: json.RawMessage("{}")}); e != ErrQuota {
			t.Fatal(assignment, e)
		}
		if _, e := db.Exec("UPDATE ops_chat_sessions SET visible_count=0,body_bytes=0 WHERE id=?", c.ID); e != nil {
			t.Fatal(e)
		}
	}
	m.mu.Lock()
	m.plan = Plan{Actions: []Action{{ToolID: "host_ports", Args: json.RawMessage("{}"), TargetIndexes: []int{0}}, {ToolID: "host_ports", Args: json.RawMessage(`{"argv":["rm"]}`), TargetIndexes: []int{0}}}}
	m.mu.Unlock()
	r, e := s.Submit(context.Background(), c.ID, TurnInput{ClientRequestID: uuid.NewString(), Revision: c.Revision, Text: "inspect"})
	if e != nil {
		t.Fatal(e)
	}
	waitRun(t, s, c.ID, r.ID, Failed)
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.invocations) != 0 {
		t.Fatal("partially validated plan dispatched")
	}
}

type invocation struct {
	server string
	argv   []string
	limits target.ExecutionLimits
}
type fakeExecutor struct {
	mu          sync.Mutex
	snapshots   map[string]target.ConnectionSnapshot
	invocations []invocation
	run         func(context.Context, target.ConnectionSnapshot, []string, target.ExecutionLimits) (*target.LimitedResult, error)
	active      int
	peak        int
	byServer    map[string]int
	serverPeak  int
}

func executor(ids ...string) *fakeExecutor {
	f := &fakeExecutor{snapshots: map[string]target.ConnectionSnapshot{}, byServer: map[string]int{}}
	for _, id := range ids {
		f.snapshots[id] = target.ConnectionSnapshot{Server: target.Server{ID: id, Name: "node", Host: "192.0.2.1", CredentialID: uuid.NewString()}, CredentialVersion: "ciphertext-version-1"}
	}
	return f
}
func (f *fakeExecutor) Capture(ctx context.Context, id string) (target.ConnectionSnapshot, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	s, ok := f.snapshots[id]
	if !ok {
		return s, target.ErrNotFound
	}
	return s, nil
}
func (f *fakeExecutor) ExecLimited(ctx context.Context, snap target.ConnectionSnapshot, argv []string, l target.ExecutionLimits) (*target.LimitedResult, error) {
	f.mu.Lock()
	f.invocations = append(f.invocations, invocation{snap.Server.ID, append([]string{}, argv...), l})
	f.active++
	f.byServer[snap.Server.ID]++
	f.peak = max(f.peak, f.active)
	f.serverPeak = max(f.serverPeak, f.byServer[snap.Server.ID])
	run := f.run
	f.mu.Unlock()
	defer func() { f.mu.Lock(); f.active--; f.byServer[snap.Server.ID]--; f.mu.Unlock() }()
	if deadline, ok := ctx.Deadline(); !ok || time.Until(deadline) > 30*time.Second {
		return nil, errors.New("unbounded context")
	}
	if run != nil {
		return run(ctx, snap, argv, l)
	}
	if len(argv) > 1 && argv[0] == "docker" && argv[1] == "inspect" {
		for _, a := range argv {
			if a == "{{.Id}}" {
				return &target.LimitedResult{Stdout: strings.Repeat("a", 64) + "\n"}, nil
			}
		}
		return &target.LimitedResult{Stdout: `{"Running":true,"Paused":false}`}, nil
	}
	if argv[0] == "systemctl" && argv[len(argv)-1] == "--value" {
		return &target.LimitedResult{Stdout: "active\n"}, nil
	}
	return &target.LimitedResult{Stdout: "local-output\n"}, nil
}
func (f *fakeExecutor) countMutation() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, call := range f.invocations {
		if len(call.argv) > 1 && (call.argv[0] == "docker" || call.argv[0] == "systemctl") {
			switch call.argv[1] {
			case "start", "stop", "restart", "pause", "unpause":
				n++
			}
		}
	}
	return n
}

type fakeModel struct {
	mu       sync.Mutex
	info     ProviderInfo
	plans    []PlanRequest
	analyses []AnalysisRequest
	plan     Plan
}

func model() *fakeModel {
	return &fakeModel{info: ProviderInfo{Provider: "fake", Model: "test", ConfigHash: strings.Repeat("0", 64)}, plan: Plan{Text: "plain-response"}}
}
func (m *fakeModel) Info() ProviderInfo { m.mu.Lock(); defer m.mu.Unlock(); return m.info }
func (m *fakeModel) Plan(ctx context.Context, in PlanRequest) (Plan, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.plans = append(m.plans, in)
	return m.plan, nil
}
func (m *fakeModel) Analyze(ctx context.Context, in AnalysisRequest) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.analyses = append(m.analyses, in)
	if in.Provider != m.info {
		return "", ErrConsent
	}
	return "derived-sensitive-analysis", nil
}
func newService(t *testing.T, db *sql.DB, key *[32]byte, f *fakeExecutor, m Model) (*Service, vault.Vault) {
	t.Helper()
	v := vault.New(db, key)
	masker := mask.NewMasker()
	s, err := New(Options{DB: db, Vault: v, Executor: f, Model: m, LocalRecorder: audit.New(db, masker, nil), Masker: masker})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := s.Close(context.Background()); err != nil {
			t.Error(err)
		}
	})
	return s, v
}
func fixture(t *testing.T, f *fakeExecutor, m Model) (*Service, vault.Vault, *sql.DB) {
	t.Helper()
	db := storetest.OpenDB(t)
	key := &[32]byte{1}
	s, v := newService(t, db, key, f, m)
	return s, v, db
}
func chat(t *testing.T, s *Service, ids ...string) *Session {
	t.Helper()
	c, e := s.Create(context.Background(), CreateInput{ServerIDs: ids})
	if e != nil {
		t.Fatal(e)
	}
	return c
}
func submitTool(t *testing.T, s *Service, c *Session, id, args string) *Run {
	t.Helper()
	r, e := s.Submit(context.Background(), c.ID, TurnInput{ClientRequestID: uuid.NewString(), Revision: c.Revision, ToolID: id, Args: json.RawMessage(args)})
	if e != nil {
		t.Fatal(e)
	}
	return r
}
func waitRun(t *testing.T, s *Service, id, run string, statuses ...string) *Snapshot {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		snap, err := s.Snapshot(context.Background(), id)
		if err != nil {
			t.Fatal(err)
		}
		for _, r := range snap.Runs {
			if r.ID == run {
				for _, status := range statuses {
					if r.Status == status {
						return snap
					}
				}
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
	snap, _ := s.Snapshot(context.Background(), id)
	t.Fatalf("run did not converge: %+v", snap)
	return nil
}
func waitJobs(t *testing.T, s *Service) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		s.mu.Lock()
		n := len(s.jobs)
		s.mu.Unlock()
		if n == 0 {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("worker did not finish")
}
func TestCRUDEncryptionCASAndKeys(t *testing.T) {
	s, v, db := fixture(t, executor(), nil)
	secret := "ssh-known-secret"
	if _, e := v.Create(vault.CreateInput{Name: "credential", Type: vault.TypeSSHPassword, Secret: secret}); e != nil {
		t.Fatal(e)
	}
	c := chat(t, s)
	draft := "private-draft " + secret
	title := "private-title"
	next, e := s.Patch(context.Background(), c.ID, PatchInput{Revision: c.Revision, Draft: &draft, Title: &title})
	if e != nil {
		t.Fatal(e)
	}
	if strings.Contains(next.Draft, secret) || !strings.Contains(next.Draft, "[MASKED]") {
		t.Fatal(next.Draft)
	}
	if _, e = s.Patch(context.Background(), c.ID, PatchInput{Revision: c.Revision, Draft: &draft}); e != ErrConflict {
		t.Fatal(e)
	}
	if e = s.Activate(context.Background(), c.ID); e != nil {
		t.Fatal(e)
	}
	page, e := s.List(context.Background(), "", 0)
	if e != nil || page.ActiveSessionID != c.ID {
		t.Fatal(page, e)
	}
	var blob []byte
	if e = db.QueryRow("SELECT body FROM ops_chat_sessions WHERE id=?", c.ID).Scan(&blob); e != nil {
		t.Fatal(e)
	}
	for _, value := range []string{"private-draft", "private-title", secret} {
		if strings.Contains(string(blob), value) {
			t.Fatal("plaintext persisted")
		}
	}
	if _, e = New(Options{DB: db, Vault: vault.New(db, nil)}); e != ErrUnavailable {
		t.Fatal(e)
	}
	if _, e = New(Options{DB: db, Vault: vault.New(db, &[32]byte{2})}); e != ErrDecrypt {
		t.Fatal(e)
	}
	got, e := s.Get(context.Background(), c.ID)
	if e != nil || got.Draft != next.Draft {
		t.Fatal(got, e)
	}
	if e = s.Delete(context.Background(), c.ID); e != nil {
		t.Fatal(e)
	}
	for _, table := range []string{"sessions", "entries", "runs", "calls", "approvals"} {
		var n int
		if e = db.QueryRow("SELECT COUNT(*) FROM ops_chat_"+table+" WHERE "+map[bool]string{true: "id", false: "session_id"}[table == "sessions"]+"=?", c.ID).Scan(&n); e != nil || n != 0 {
			t.Fatal(table, n, e)
		}
	}
}
func TestReadOnlyNilModelIdempotencyAndPartial(t *testing.T) {
	a, b := uuid.NewString(), uuid.NewString()
	f := executor(a, b)
	f.run = func(ctx context.Context, snap target.ConnectionSnapshot, argv []string, l target.ExecutionLimits) (*target.LimitedResult, error) {
		if snap.Server.ID == b {
			return nil, errors.New("secret remote exception")
		}
		return &target.LimitedResult{Stdout: "ok\n"}, nil
	}
	s, _, _ := fixture(t, f, nil)
	c := chat(t, s, a, b)
	req := TurnInput{ClientRequestID: uuid.NewString(), Revision: c.Revision, ToolID: "host_ports", Args: json.RawMessage("{}")}
	r, e := s.Submit(context.Background(), c.ID, req)
	if e != nil {
		t.Fatal(e)
	}
	duplicate, e := s.Submit(context.Background(), c.ID, req)
	if e != nil || duplicate.ID != r.ID {
		t.Fatal(duplicate, e)
	}
	req.ToolID = "host_processes"
	if _, e = s.Submit(context.Background(), c.ID, req); e != ErrConflict {
		t.Fatal(e)
	}
	snap := waitRun(t, s, c.ID, r.ID, PartialFailed)
	if len(snap.Calls) != 2 {
		t.Fatal(snap.Calls)
	}
	success, failed := 0, 0
	for _, call := range snap.Calls {
		if call.Status == Succeeded {
			success++
		}
		if call.Status == Failed {
			failed++
		}
		if strings.Contains(call.Error, "secret") {
			t.Fatal(call.Error)
		}
	}
	if success != 1 || failed != 1 {
		t.Fatal(snap.Calls)
	}
	f.mu.Lock()
	before := len(f.invocations)
	f.mu.Unlock()
	_, _ = s.Get(context.Background(), c.ID)
	_, _ = s.Snapshot(context.Background(), c.ID)
	_, _ = s.List(context.Background(), "", 50)
	_, _ = s.Entries(context.Background(), c.ID, "", 50)
	_, _ = s.Events(context.Background(), c.ID, EventCursor(c.ID, 0), 100)
	f.mu.Lock()
	after := len(f.invocations)
	f.mu.Unlock()
	if before != after {
		t.Fatal("GET dispatched SSH")
	}
}
func TestMutationConfirmationBindingAuditAndReplay(t *testing.T) {
	id := uuid.NewString()
	f := executor(id)
	s, _, db := fixture(t, f, nil)
	c := chat(t, s, id)
	r := submitTool(t, s, c, "docker_action", `{"container":"app","action":"restart"}`)
	snap := waitRun(t, s, c.ID, r.ID, AwaitingConfirmation)
	if f.countMutation() != 0 || len(snap.Confirmations) != 1 {
		t.Fatal("unapproved mutation")
	}
	a := snap.Confirmations[0]
	if a.Calls[0].Object != strings.Repeat("a", 64) || !a.Valid {
		t.Fatal(a)
	}
	bad := ConfirmInput{RunID: r.ID, Nonce: a.Nonce, Calls: append([]BoundCall{}, a.Calls...)}
	bad.Calls[0].ArgsHash = "forged"
	if _, e := s.Confirm(context.Background(), c.ID, bad); e != ErrApproval {
		t.Fatal(e)
	}
	in := ConfirmInput{RunID: r.ID, Nonce: a.Nonce, Calls: a.Calls}
	if _, e := s.Confirm(context.Background(), c.ID, in); e != nil {
		t.Fatal(e)
	}
	waitRun(t, s, c.ID, r.ID, Succeeded)
	if _, e := s.Confirm(context.Background(), c.ID, in); e != ErrApproval {
		t.Fatal(e)
	}
	if f.countMutation() != 1 {
		t.Fatal(f.countMutation())
	}
	var detail, ip string
	if e := db.QueryRow("SELECT detail_json,COALESCE(ip,'') FROM audit_log WHERE action='ops_chat_intent'").Scan(&detail, &ip); e != nil {
		t.Fatal(e)
	}
	if strings.Contains(detail, "app") || strings.Contains(detail, "192.0.2.1") || ip != "" {
		t.Fatal(detail, ip)
	}
}
func TestAuditFailureFailsClosed(t *testing.T) {
	id := uuid.NewString()
	f := executor(id)
	s, _, db := fixture(t, f, nil)
	c := chat(t, s, id)
	r := submitTool(t, s, c, "systemd_action", `{"unit":"nginx.service","action":"start"}`)
	snap := waitRun(t, s, c.ID, r.ID, AwaitingConfirmation)
	if _, e := db.Exec("DROP TABLE audit_log"); e != nil {
		t.Fatal(e)
	}
	a := snap.Confirmations[0]
	if _, e := s.Confirm(context.Background(), c.ID, ConfirmInput{RunID: r.ID, Nonce: a.Nonce, Calls: a.Calls}); e != nil {
		t.Fatal(e)
	}
	waitRun(t, s, c.ID, r.ID, Failed)
	if f.countMutation() != 0 {
		t.Fatal("audit failure dispatched SSH")
	}
}
func TestAnalysisConsentAndDerivedContextExcluded(t *testing.T) {
	id := uuid.NewString()
	f := executor(id)
	m := model()
	f.run = func(context.Context, target.ConnectionSnapshot, []string, target.ExecutionLimits) (*target.LimitedResult, error) {
		return &target.LimitedResult{Stdout: "private-collected-output"}, nil
	}
	s, _, _ := fixture(t, f, m)
	c := chat(t, s, id)
	r := submitTool(t, s, c, "host_ports", "{}")
	snap := waitRun(t, s, c.ID, r.ID, Succeeded)
	waitJobs(t, s)
	call := snap.Calls[0]
	m.mu.Lock()
	if len(m.plans) != 0 || len(m.analyses) != 0 {
		t.Fatal("automatic model call")
	}
	m.mu.Unlock()
	preview, e := s.AnalysisPreview(context.Background(), c.ID, []string{call.ID})
	if e != nil {
		t.Fatal(e)
	}
	in := AnalysisInput{ClientRequestID: uuid.NewString(), Revision: c.Revision, CallIDs: preview.CallIDs, PreviewHash: preview.Hash, Provider: preview.Provider, Consent: true}
	in.PreviewHash = "wrong"
	if _, e = s.Analyze(context.Background(), c.ID, in); e != ErrConsent {
		t.Fatal(e)
	}
	in.PreviewHash = preview.Hash
	ar, e := s.Analyze(context.Background(), c.ID, in)
	if e != nil {
		t.Fatal(e)
	}
	waitRun(t, s, c.ID, ar.ID, Succeeded)
	waitJobs(t, s)
	next, e := s.Submit(context.Background(), c.ID, TurnInput{ClientRequestID: uuid.NewString(), Revision: c.Revision, Text: "next plain question"})
	if e != nil {
		t.Fatal(e)
	}
	waitRun(t, s, c.ID, next.ID, Succeeded)
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.analyses) != 1 || m.analyses[0].Provider != preview.Provider {
		t.Fatal(m.analyses)
	}
	data, _ := json.Marshal(m.plans)
	if strings.Contains(string(data), "private-collected-output") || strings.Contains(string(data), "derived-sensitive-analysis") || strings.Contains(string(data), "192.0.2.1") {
		t.Fatal(string(data))
	}
}
func TestStrictTools(t *testing.T) {
	invalid := []struct{ id, args string }{
		{"docker_action", `{"container":"app","action":"kill"}`}, {"docker_action", `{"container":"app","action":"rm"}`},
		{"docker_logs", `{"container":"-app"}`}, {"docker_logs", `{"container":"app","lines":1001}`},
		{"docker_logs", `{"container":"app","lines":0}`}, {"docker_logs", `{"container":"app","lines":2.2}`},
		{"docker_logs", `{"container":"app","container":"other"}`}, {"docker_logs", `{"container":"app","argv":["rm"]}`},
		{"host_ports", `{"shell":"ls"}`}, {"host_ports", `{"container":""}`}, {"host_ports", `null`},
		{"systemd_status", `{"unit":"nginx\u2028.service"}`}, {"systemd_action", `{"unit":"nginx","action":"pause"}`},
	}
	for _, in := range invalid {
		if _, e := parseArgs(in.id, json.RawMessage(in.args)); e != ErrInvalid {
			t.Fatalf("%s/%s: %v", in.id, in.args, e)
		}
	}
	for _, tool := range Tools() {
		raw := "{}"
		if strings.HasPrefix(tool.ID, "docker_") && (tool.ID == "docker_logs" || tool.ID == "docker_inspect" || tool.ID == "docker_action") {
			raw = `{"container":"app"}`
			if tool.Mutation {
				raw = `{"container":"app","action":"start"}`
			}
		}
		if strings.HasPrefix(tool.ID, "systemd_") {
			raw = `{"unit":"nginx.service"}`
			if tool.Mutation {
				raw = `{"unit":"nginx.service","action":"start"}`
			}
		}
		if _, e := parseArgs(tool.ID, json.RawMessage(raw)); e != nil {
			t.Fatal(tool.ID, e)
		}
	}
}
func TestCloseUnsubscribeAndIdleDeleteDuringOtherJob(t *testing.T) {
	id := uuid.NewString()
	f := executor(id)
	entered := make(chan struct{}, 1)
	f.run = func(ctx context.Context, _ target.ConnectionSnapshot, _ []string, _ target.ExecutionLimits) (*target.LimitedResult, error) {
		entered <- struct{}{}
		<-ctx.Done()
		return nil, ctx.Err()
	}
	s, _, _ := fixture(t, f, nil)
	busy := chat(t, s, id)
	idle := chat(t, s)
	ch, cancel, e := s.Subscribe(context.Background(), idle.ID)
	if e != nil {
		t.Fatal(e)
	}
	submitTool(t, s, busy, "host_ports", "{}")
	<-entered
	if e = s.Delete(context.Background(), idle.ID); e != nil {
		t.Fatal(e)
	}
	if e = s.Close(context.Background()); e != nil {
		t.Fatal(e)
	}
	cancel()
	cancel()
	for {
		select {
		case _, open := <-ch:
			if !open {
				goto drained
			}
		default:
			t.Fatal("subscription not closed")
		}
	}
drained:
	s.mu.Lock()
	n := len(s.jobs)
	s.mu.Unlock()
	if n != 0 {
		t.Fatal("Close returned with workers")
	}
}
func TestLeaseAndRecoveryNoReplay(t *testing.T) {
	id := uuid.NewString()
	f := executor(id)
	s, v, db := fixture(t, f, nil)
	c := chat(t, s, id)
	other, e := New(Options{DB: db, Vault: v, Executor: f})
	if e != nil {
		t.Fatal(e)
	}
	if e = other.Start(context.Background()); e != ErrLease {
		t.Fatal(e)
	}
	r := submitTool(t, s, c, "docker_action", `{"container":"app","action":"restart"}`)
	snap := waitRun(t, s, c.ID, r.ID, AwaitingConfirmation)
	waitJobs(t, s)
	old := snap.Confirmations[0]
	if e = s.Close(context.Background()); e != nil {
		t.Fatal(e)
	}
	next, e := New(Options{DB: db, Vault: v, Executor: f, LocalRecorder: audit.New(db, mask.NewMasker(), nil)})
	if e != nil {
		t.Fatal(e)
	}
	if e = next.Start(context.Background()); e != nil {
		t.Fatal(e)
	}
	defer next.Close(context.Background())
	restored, e := next.Snapshot(context.Background(), c.ID)
	if e != nil {
		t.Fatal(e)
	}
	if restored.Confirmations[0].Valid || restored.Confirmations[0].Nonce != "" {
		t.Fatal("old nonce survived restart")
	}
	if _, e = next.Confirm(context.Background(), c.ID, ConfirmInput{RunID: r.ID, Nonce: old.Nonce, Calls: old.Calls}); e != ErrApproval {
		t.Fatal(e)
	}
	renewed, e := next.ReissueConfirmation(context.Background(), c.ID, r.ID)
	if e != nil || renewed.Nonce == old.Nonce {
		t.Fatal(renewed, e)
	}
	if f.countMutation() != 0 {
		t.Fatal("replayed mutation")
	}
}
