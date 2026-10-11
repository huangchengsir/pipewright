package opschat

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/huangchengsir/pipewright/internal/target"
	"github.com/huangchengsir/pipewright/internal/vault"
)

func execArgsJSON(argv ...string) string {
	raw, _ := json.Marshal(ToolArgs{Container: "app", Argv: argv})
	return string(raw)
}

func execInvocations(f *fakeExecutor) [][]string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out [][]string
	for _, v := range f.invocations {
		if len(v.argv) >= 2 && v.argv[0] == "docker" && v.argv[1] == "exec" {
			out = append(out, append([]string(nil), v.argv...))
		}
	}
	return out
}

func TestDockerExecStrictArgv(t *testing.T) {
	for _, argv := range [][]string{
		{"true"}, {"/usr/bin/redis-cli", "PING"}, {"./program", "", "--privileged", "a; $(id) 'quoted'"},
		{"sh", "-c", "printf '%s' 'a; b' && echo done"},
		{"echo", strings.Repeat("x", 2048), strings.Repeat("y", 2044)},
		append([]string{"echo"}, make([]string, 60)...),
	} {
		a, err := parseArgs("docker_exec", json.RawMessage(execArgsJSON(argv...)))
		if err != nil || !reflect.DeepEqual(a.Argv, argv) {
			t.Fatalf("valid literals rejected: %q: %v", argv, err)
		}
	}
	for _, argv := range [][]string{
		nil, {}, {""}, {"-i"}, {"--privileged"}, {"/"}, {"."}, {".."}, {"/bin/"}, {"echo hi"}, {"echo;id"},
		{"echo", "x\x00y"}, {"echo", "x\ty"}, {"echo", "x\ny"}, {"echo", "\u007f"}, {"echo", "\u0085"},
		{"echo", strings.Repeat("x", 2049)}, {"echo", strings.Repeat("x", 2048), strings.Repeat("y", 2045)},
		append([]string{"echo"}, make([]string, 61)...),
		append([]string{"echo"}, make([]string, 63)...),
	} {
		if _, err := parseArgs("docker_exec", json.RawMessage(execArgsJSON(argv...))); err != ErrInvalid {
			t.Fatalf("invalid argv accepted: %q: %v", argv, err)
		}
	}
	for _, raw := range []string{
		`{"container":"app"}`, `{"container":"app","argv":null}`, `{"container":"app","argv":"true"}`,
		`{"container":"app","argv":["echo",null]}`, `{"container":"app","argv":["echo",1]}`,
		`{"container":"app","argv":["echo",{}]}`, `{"container":"-i","argv":["true"]}`,
		`{"container":"app;id","argv":["true"]}`, `{"container":"app","argv":["true"],"privileged":true}`,
		`{"container":"app","argv":["true"],"env":[]}`, `{"container":"app","argv":["true"],"action":"start"}`,
		`{"container":"app","argv":["true"],"argv":["false"]}`,
		string(append([]byte(`{"container":"app","argv":["echo","`), append([]byte{0xff}, []byte(`"]}`)...)...)),
	} {
		if _, err := parseArgs("docker_exec", json.RawMessage(raw)); err != ErrInvalid {
			t.Fatalf("invalid JSON accepted: %q: %v", raw, err)
		}
	}
	if validExecArgv([]string{"echo", string([]byte{0xff})}) {
		t.Fatal("invalid UTF-8 accepted by command guard")
	}
	for _, tool := range Tools() {
		if tool.ID == "docker_exec" {
			if !tool.Mutation {
				t.Fatal("exec is not a mutation")
			}
			var schema struct {
				Properties map[string]struct {
					MaxItems int `json:"maxItems"`
				} `json:"properties"`
			}
			if err := json.Unmarshal(tool.Schema, &schema); err != nil || schema.Properties["argv"].MaxItems != 61 {
				t.Fatal("catalog does not reserve wrapper tokens", err)
			}
			continue
		}
		fields := map[string]any{"argv": []string{"true"}}
		switch tool.ID {
		case "docker_inspect", "docker_logs", "docker_action":
			fields["container"] = "app"
		case "systemd_status", "systemd_logs", "systemd_action":
			fields["unit"] = "app.service"
		}
		if mutation(tool.ID) {
			fields["action"] = "start"
		}
		raw, _ := json.Marshal(fields)
		if _, err := parseArgs(tool.ID, raw); err != ErrInvalid {
			t.Fatalf("old tool accepted exec argv: %s", tool.ID)
		}
	}
}

func TestDockerExecJSONLiteralBoundaries(t *testing.T) {
	for _, value := range []string{strings.Repeat("<", 2048), strings.Repeat("&", 2048)} {
		raw := json.RawMessage(execArgsJSON("echo", value))
		if len(raw) <= 8<<10 {
			t.Fatal("fixture must exceed the old encoded limit")
		}
		a, err := parseArgs("docker_exec", raw)
		if err != nil || a.Argv[1] != value {
			t.Fatal("valid HTML-escaped argument rejected", err)
		}
		canonical, err := json.Marshal(a)
		if err != nil {
			t.Fatal(err)
		}
		if again, err := parseArgs("docker_exec", canonical); err != nil || !reflect.DeepEqual(again, a) {
			t.Fatal("canonical reparse changed literal arguments", err)
		}
	}
	for _, raw := range []string{
		`{"container":"app","argv":["echo","\ud800"]}`,
		`{"container":"app","argv":["echo","\udc00"]}`,
		`{"container":"app","argv":["echo","\ud800\u0041"]}`,
		`{"container":"app","argv":["echo","\ud800\ud800"]}`,
	} {
		if _, err := parseArgs("docker_exec", json.RawMessage(raw)); err != ErrInvalid {
			t.Fatal("unpaired surrogate accepted", raw, err)
		}
	}
	for raw, want := range map[string]string{
		`{"container":"app","argv":["echo","\ud83d\ude00"]}`: "\U0001f600",
		`{"container":"app","argv":["echo","\\ud800"]}`:      `\ud800`,
		`{"container":"app","argv":["echo","\uFFFD"]}`:       "\uFFFD",
	} {
		if a, err := parseArgs("docker_exec", json.RawMessage(raw)); err != nil || a.Argv[1] != want {
			t.Fatal("valid scalar or literal backslash changed", raw, err)
		}
	}
	if _, err := parseArgs("docker_exec", json.RawMessage(strings.Repeat(" ", 32<<10)+execArgsJSON("true"))); err != ErrInvalid {
		t.Fatal("exec encoded size limit not enforced", err)
	}
	if _, err := parseArgs("docker_inspect", json.RawMessage(strings.Repeat(" ", 8<<10)+`{"container":"app"}`)); err != ErrInvalid {
		t.Fatal("existing tool encoded limit broadened", err)
	}
	for _, status := range []string{Failed, PartialFailed, Interrupted, Unknown, Succeeded} {
		if retryable(callRecord{Body: callBody{View: Call{ToolID: "docker_exec", Status: status}}}) != ErrVerifyFirst {
			t.Fatal("generic exec can be replayed without fresh submission", status)
		}
	}
}

func TestDockerExecFixedWrapper(t *testing.T) {
	a := ToolArgs{Container: strings.Repeat("a", 64), Argv: []string{"sh", "-c", "echo '$HOME'; true", "", "--privileged"}}
	want := append([]string{"docker", "exec", a.Container}, a.Argv...)
	got := commands("docker_exec", a)
	if len(got) != 1 || !reflect.DeepEqual(got[0], want) || !mutationCommand(got[0]) {
		t.Fatal(got)
	}
	got[0][3] = "changed"
	if a.Argv[0] != "sh" {
		t.Fatal("wrapper aliases input argv")
	}
	for _, object := range []string{"app", "-i", strings.Repeat("A", 64), strings.Repeat("a", 63)} {
		a.Container = object
		if commands("docker_exec", a) != nil {
			t.Fatal("unfrozen container accepted", object)
		}
	}
}

type execValidationOnlyDialer struct{ target.SSHDialer }

func TestDockerExecFitsActualBoundedValidator(t *testing.T) {
	a := ToolArgs{Container: strings.Repeat("a", 64), Argv: append([]string{"echo"}, make([]string, 60)...)}
	wrapped := commands("docker_exec", a)
	if len(wrapped) != 1 || len(wrapped[0]) != 64 {
		t.Fatal("maximum exec argv exceeds executor limit", wrapped)
	}
	// No bounded runner: valid commands stop locally before Capture, database access, or SSH.
	executor := target.New(nil, nil, execValidationOnlyDialer{}).(target.LimitedExecutor)
	if _, err := executor.ExecLimited(context.Background(), target.ConnectionSnapshot{}, wrapped[0], target.ExecutionLimits{}); err != target.ErrLimitedUnavailable {
		t.Fatal("maximum wrapper rejected by actual target validator", err)
	}
	if _, err := executor.ExecLimited(context.Background(), target.ConnectionSnapshot{}, append(wrapped[0], "extra"), target.ExecutionLimits{}); err == nil || err.Error() != "target: invalid command" {
		t.Fatal("generic executor token limit changed", err)
	}
	a.Argv = append(a.Argv, "extra")
	if len(a.Argv) != 62 || validExecArgv(a.Argv) || commands("docker_exec", a) != nil {
		t.Fatal("62 container argv tokens admitted")
	}
}

func TestDockerExecApprovalFrozenAuditAndReplay(t *testing.T) {
	id := uuid.NewString()
	f := executor(id)
	s, _, db := fixture(t, f, nil)
	frozen := strings.Repeat("a", 64)
	f.run = func(_ context.Context, _ target.ConnectionSnapshot, argv []string, limits target.ExecutionLimits) (*target.LimitedResult, error) {
		if argv[1] == "inspect" {
			return &target.LimitedResult{Stdout: frozen + "\n"}, nil
		}
		var intents int
		if err := db.QueryRow("SELECT COUNT(*) FROM audit_log WHERE action='ops_chat_intent'").Scan(&intents); err != nil || intents != 1 {
			t.Error("dispatch did not follow local audit", intents, err)
		}
		if limits.Bytes != (64<<10)-65 || limits.Lines != 999 {
			t.Errorf("resolution did not share exec budget: %+v", limits)
		}
		return &target.LimitedResult{Stdout: "completed", Truncated: true}, nil
	}
	c := chat(t, s, id)
	in := TurnInput{ClientRequestID: uuid.NewString(), Revision: c.Revision, ToolID: "docker_exec", Args: json.RawMessage(execArgsJSON("sh", "-c", "printf '%s' 'literal; $(id)'", "", "--privileged"))}
	r, err := s.Submit(context.Background(), c.ID, in)
	if err != nil {
		t.Fatal(err)
	}
	snap := waitRun(t, s, c.ID, r.ID, AwaitingConfirmation)
	waitJobs(t, s)
	if len(execInvocations(f)) != 0 {
		t.Fatal("exec before human approval")
	}
	call := snap.Calls[0]
	if call.Object != frozen || call.Args.Container != frozen || call.ArgsHash != digest(call.Args) {
		t.Fatal("approval not bound to immutable container and argv", call)
	}
	var encrypted string
	if err := db.QueryRow("SELECT body FROM ops_chat_calls WHERE id=?", call.ID).Scan(&encrypted); err != nil || strings.Contains(encrypted, "printf") {
		t.Fatal("exec args not encrypted", err)
	}
	frozen = strings.Repeat("b", 64)
	a := snap.Confirmations[0]
	confirmation := ConfirmInput{RunID: r.ID, Nonce: a.Nonce, Calls: a.Calls}
	if _, err = s.Confirm(context.Background(), c.ID, confirmation); err != nil {
		t.Fatal(err)
	}
	snap = waitRun(t, s, c.ID, r.ID, Succeeded)
	waitJobs(t, s)
	want := append([]string{"docker", "exec", strings.Repeat("a", 64)}, call.Args.Argv...)
	if got := execInvocations(f); len(got) != 1 || !reflect.DeepEqual(got[0], want) {
		t.Fatal("name re-resolved or literals changed", got)
	}
	f.mu.Lock()
	n := len(f.invocations)
	f.mu.Unlock()
	if n != 2 || !snap.Calls[0].Truncated || snap.Calls[0].ExitCode == nil || *snap.Calls[0].ExitCode != 0 {
		t.Fatal("exec incorrectly effect-verified or completion lost", n, snap.Calls)
	}
	if _, err = s.Confirm(context.Background(), c.ID, confirmation); err == nil {
		t.Fatal("approval replay accepted")
	}
	replayed, err := s.Submit(context.Background(), c.ID, in)
	if err != nil || replayed.ID != r.ID || len(execInvocations(f)) != 1 {
		t.Fatal("idempotency replay executed again", replayed, err)
	}
}

func TestDockerExecTamperedOrExpiredApprovalBlocked(t *testing.T) {
	for _, change := range []string{"argv", "argv-and-hash", "container", "expiry", "confirmation", "target"} {
		t.Run(change, func(t *testing.T) {
			id := uuid.NewString()
			f := executor(id)
			s, _, db := fixture(t, f, nil)
			c := chat(t, s, id)
			r := submitTool(t, s, c, "docker_exec", execArgsJSON("echo", "approved"))
			snap := waitRun(t, s, c.ID, r.ID, AwaitingConfirmation)
			waitJobs(t, s)
			a := snap.Confirmations[0]
			switch change {
			case "argv", "argv-and-hash", "container":
				err := s.transaction(context.Background(), true, func(tx *sql.Tx) error {
					stored, err := s.call(context.Background(), tx, c.ID, snap.Calls[0].ID)
					if err != nil {
						return err
					}
					if change == "container" {
						stored.Body.View.Args.Container = strings.Repeat("b", 64)
					} else {
						stored.Body.View.Args.Argv[1] = "unapproved"
					}
					if change != "argv" {
						stored.Body.View.ArgsHash = digest(stored.Body.View.Args)
					}
					return s.saveCall(context.Background(), tx, stored)
				})
				if err != nil {
					t.Fatal(err)
				}
			case "expiry":
				if _, err := db.Exec("UPDATE ops_chat_approvals SET expires_at=? WHERE run_id=?", time.Now().Add(-time.Second).UnixMilli(), r.ID); err != nil {
					t.Fatal(err)
				}
			case "confirmation":
				a.Calls[0].ArgsHash = "unapproved"
			case "target":
				f.mu.Lock()
				changed := f.snapshots[id]
				changed.CredentialVersion = "rotated"
				f.snapshots[id] = changed
				f.mu.Unlock()
			}
			_, err := s.Confirm(context.Background(), c.ID, ConfirmInput{RunID: r.ID, Nonce: a.Nonce, Calls: a.Calls})
			if err == nil {
				waitRun(t, s, c.ID, r.ID, Failed)
				waitJobs(t, s)
			}
			if len(execInvocations(f)) != 0 {
				t.Fatal("tampered or expired exec dispatched")
			}
		})
	}
}

func TestDockerExecCompletionUnknownAndRetry(t *testing.T) {
	for _, outcome := range []string{"nonzero", "transport", "timeout", "overflow", "cancel"} {
		t.Run(outcome, func(t *testing.T) {
			id := uuid.NewString()
			f := executor(id)
			entered := make(chan struct{}, 1)
			f.run = func(ctx context.Context, _ target.ConnectionSnapshot, argv []string, l target.ExecutionLimits) (*target.LimitedResult, error) {
				if argv[1] == "inspect" {
					return &target.LimitedResult{Stdout: strings.Repeat("a", 64)}, nil
				}
				entered <- struct{}{}
				switch outcome {
				case "nonzero":
					return &target.LimitedResult{ExitCode: 7, Stderr: "command failed"}, nil
				case "transport":
					return &target.LimitedResult{Stdout: "partial"}, errors.New("connection lost")
				case "timeout":
					return nil, context.DeadlineExceeded
				case "overflow":
					return &target.LimitedResult{Stdout: strings.Repeat("x", l.Bytes+1)}, nil
				default:
					<-ctx.Done()
					return nil, ctx.Err()
				}
			}
			s, _, db := fixture(t, f, nil)
			c := chat(t, s, id)
			r := submitTool(t, s, c, "docker_exec", execArgsJSON("true"))
			a := waitRun(t, s, c.ID, r.ID, AwaitingConfirmation).Confirmations[0]
			waitJobs(t, s)
			if _, err := s.Confirm(context.Background(), c.ID, ConfirmInput{RunID: r.ID, Nonce: a.Nonce, Calls: a.Calls}); err != nil {
				t.Fatal(err)
			}
			select {
			case <-entered:
			case <-time.After(5 * time.Second):
				t.Fatal("exec not dispatched")
			}
			if outcome == "cancel" {
				if _, err := s.Cancel(context.Background(), c.ID, r.ID); err != nil {
					t.Fatal(err)
				}
			}
			want := Unknown
			if outcome == "nonzero" {
				want = Failed
			}
			snap := waitRun(t, s, c.ID, r.ID, want)
			waitJobs(t, s)
			stored, err := s.call(context.Background(), db, c.ID, snap.Calls[0].ID)
			if err != nil || stored.Body.View.Status != want || !stored.Body.Dispatched || retryable(stored) != ErrVerifyFirst {
				t.Fatal("unsafe exec outcome/retry", stored, err)
			}
			if _, err := s.RetryFailed(context.Background(), c.ID, RetryInput{ClientRequestID: uuid.NewString(), Revision: c.Revision, CallIDs: []string{snap.Calls[0].ID}}); err != ErrVerifyFirst {
				t.Fatal("dispatched exec admitted to retry", err)
			}
			if outcome == "nonzero" && (snap.Calls[0].ExitCode == nil || *snap.Calls[0].ExitCode != 7) {
				t.Fatal("completed exit code lost")
			}
			if len(execInvocations(f)) != 1 {
				t.Fatal("unknown command replayed")
			}
		})
	}
	if retryable(callRecord{Body: callBody{View: Call{ToolID: "docker_exec", Status: Interrupted}}}) != ErrVerifyFirst {
		t.Fatal("undispatched exec should require fresh submission")
	}
	if retryable(callRecord{Body: callBody{Dispatched: true, View: Call{ToolID: "docker_exec", Status: Succeeded}}}) != ErrVerifyFirst {
		t.Fatal("completed exec should require fresh submission")
	}
}

func TestDockerExecSecretArgsAndProjectionCopy(t *testing.T) {
	id := uuid.NewString()
	f := executor(id)
	s, v, db := fixture(t, f, nil)
	secret := "known-exec-secret"
	if _, err := v.Create(vault.CreateInput{Name: "exec secret", Type: vault.TypeSSHPassword, Secret: secret}); err != nil {
		t.Fatal(err)
	}
	c := chat(t, s, id)
	prefix := "known-exec"
	for _, raw := range []string{
		execArgsJSON("echo", secret), execArgsJSON("echo", prefix), execArgsJSON(prefix),
		`{"container":"app","argv":["echo","known-\u0065xec-secret"]}`,
		`{"container":"app","argv":["echo","known-\u0065xec"]}`,
		`{"container":"known-exec","argv":["true"]}`,
	} {
		if _, err := s.Submit(context.Background(), c.ID, TurnInput{ClientRequestID: uuid.NewString(), Revision: c.Revision, ToolID: "docker_exec", Args: json.RawMessage(raw)}); err != ErrInvalid {
			t.Fatal("secret argv admitted", err)
		}
	}
	f.mu.Lock()
	invocations := len(f.invocations)
	f.mu.Unlock()
	if invocations != 0 || !s.secretArgs(ToolArgs{Argv: []string{"echo", "safe", secret}}) {
		t.Fatal("not every argv checked for secrets")
	}
	for _, table := range []string{"ops_chat_runs", "ops_chat_calls", "ops_chat_approvals"} {
		var count int
		if err := db.QueryRow("SELECT COUNT(*) FROM "+table+" WHERE session_id=?", c.ID).Scan(&count); err != nil || count != 0 {
			t.Fatal("hidden argument admitted before inspection/approval", table, count, err)
		}
	}
	for _, args := range []ToolArgs{
		{Argv: []string{"echo", prefix}}, {Container: prefix}, {Unit: prefix}, {Action: prefix},
	} {
		if !s.secretArgs(args) {
			t.Fatal("projected field differs from executable argument", args)
		}
	}
	source := Call{Args: ToolArgs{Container: prefix, Argv: []string{"echo", secret, strings.Repeat("x", 2049), prefix}}}
	view := s.publicCall(source)
	if strings.Contains(view.Args.Argv[1], secret) || source.Args.Argv[1] != secret || len(view.Args.Argv[2]) > 2048 || len(source.Args.Argv[2]) != 2049 || view.Args.Argv[3] == prefix || source.Args.Argv[3] != prefix || view.Args.Container == prefix || source.Args.Container != prefix {
		t.Fatal("public projection leaked secret or mutated source", source, view)
	}
	view.Args.Argv[0] = "changed"
	if source.Args.Argv[0] != "echo" {
		t.Fatal("projection shares argv storage")
	}
}

func TestDockerExecRestartPendingNeedsNewApproval(t *testing.T) {
	id := uuid.NewString()
	f := executor(id)
	s, v, db := fixture(t, f, nil)
	c := chat(t, s, id)
	r := submitTool(t, s, c, "docker_exec", execArgsJSON("true"))
	a := waitRun(t, s, c.ID, r.ID, AwaitingConfirmation).Confirmations[0]
	waitJobs(t, s)
	if err := s.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	next, err := New(Options{DB: db, Vault: v, Executor: f})
	if err != nil {
		t.Fatal(err)
	}
	if err = next.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer next.Close(context.Background())
	if _, err = next.Confirm(context.Background(), c.ID, ConfirmInput{RunID: r.ID, Nonce: a.Nonce, Calls: a.Calls}); err != ErrApproval {
		t.Fatal("previous-process approval accepted", err)
	}
	if len(execInvocations(f)) != 0 {
		t.Fatal("restart replayed pending exec")
	}
}

func TestDockerExecApprovalExpiresDuringSlotWait(t *testing.T) {
	id := uuid.NewString()
	f := executor(id)
	s, _, db := fixture(t, f, nil)
	c := chat(t, s, id)
	r := submitTool(t, s, c, "docker_exec", execArgsJSON("true"))
	a := waitRun(t, s, c.ID, r.ID, AwaitingConfirmation).Confirmations[0]
	waitJobs(t, s)
	release, err := s.takeSlot(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if release != nil {
			release()
		}
	}()
	if _, err = s.Confirm(context.Background(), c.ID, ConfirmInput{RunID: r.ID, Nonce: a.Nonce, Calls: a.Calls}); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		var intents int
		if err := db.QueryRow("SELECT COUNT(*) FROM audit_log WHERE action='ops_chat_intent'").Scan(&intents); err != nil {
			t.Fatal(err)
		}
		if intents == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("worker did not audit before slot wait")
		}
		time.Sleep(time.Millisecond)
	}
	// Give the earlier audit check time to complete while the execution slot is held.
	time.Sleep(20 * time.Millisecond)
	if _, err := db.Exec("UPDATE ops_chat_approvals SET expires_at=? WHERE run_id=?", time.Now().Add(-time.Second).UnixMilli(), r.ID); err != nil {
		t.Fatal(err)
	}
	release()
	release = nil
	snap := waitRun(t, s, c.ID, r.ID, Failed)
	waitJobs(t, s)
	stored, err := s.call(context.Background(), db, c.ID, snap.Calls[0].ID)
	if err != nil || stored.Body.Dispatched || stored.Body.View.Status != Interrupted || len(execInvocations(f)) != 0 {
		t.Fatal("expired approval dispatched after slot wait", stored, err)
	}
}

func TestDockerExecAuditFailureBlocksDispatch(t *testing.T) {
	id := uuid.NewString()
	f := executor(id)
	s, _, db := fixture(t, f, nil)
	c := chat(t, s, id)
	r := submitTool(t, s, c, "docker_exec", execArgsJSON("true"))
	a := waitRun(t, s, c.ID, r.ID, AwaitingConfirmation).Confirmations[0]
	waitJobs(t, s)
	if _, err := db.Exec(`CREATE TRIGGER exec_audit_failure BEFORE INSERT ON audit_log BEGIN SELECT RAISE(FAIL, 'local audit unavailable'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Confirm(context.Background(), c.ID, ConfirmInput{RunID: r.ID, Nonce: a.Nonce, Calls: a.Calls}); err != nil {
		t.Fatal(err)
	}
	snap := waitRun(t, s, c.ID, r.ID, Failed)
	waitJobs(t, s)
	stored, err := s.call(context.Background(), db, c.ID, snap.Calls[0].ID)
	if err != nil || stored.Body.Dispatched || len(execInvocations(f)) != 0 {
		t.Fatal("audit failure did not block exec", stored, err)
	}
}

func TestDockerExecRestartAfterDispatchNeverReplays(t *testing.T) {
	id := uuid.NewString()
	f := executor(id)
	entered := make(chan struct{}, 1)
	f.run = func(ctx context.Context, _ target.ConnectionSnapshot, argv []string, _ target.ExecutionLimits) (*target.LimitedResult, error) {
		if argv[1] == "inspect" {
			return &target.LimitedResult{Stdout: strings.Repeat("a", 64)}, nil
		}
		entered <- struct{}{}
		<-ctx.Done()
		return nil, ctx.Err()
	}
	s, v, db := fixture(t, f, nil)
	c := chat(t, s, id)
	r := submitTool(t, s, c, "docker_exec", execArgsJSON("true"))
	a := waitRun(t, s, c.ID, r.ID, AwaitingConfirmation).Confirmations[0]
	waitJobs(t, s)
	if _, err := s.Confirm(context.Background(), c.ID, ConfirmInput{RunID: r.ID, Nonce: a.Nonce, Calls: a.Calls}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("exec not dispatched")
	}
	if err := s.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	next, err := New(Options{DB: db, Vault: v, Executor: f})
	if err != nil {
		t.Fatal(err)
	}
	if err = next.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer next.Close(context.Background())
	snap := waitRun(t, next, c.ID, r.ID, Unknown)
	if snap.Calls[0].Status != Unknown || len(execInvocations(f)) != 1 {
		t.Fatal("restart replayed dispatched exec", snap.Calls)
	}
	if _, err = next.RetryFailed(context.Background(), c.ID, RetryInput{ClientRequestID: uuid.NewString(), Revision: c.Revision, CallIDs: []string{snap.Calls[0].ID}}); err != ErrVerifyFirst {
		t.Fatal("restart made unknown exec retryable", err)
	}
}
