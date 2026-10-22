package configdraft

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
	"github.com/huangchengsir/pipewright/internal/auth"
	"github.com/huangchengsir/pipewright/internal/configstate"
	"github.com/huangchengsir/pipewright/internal/storetest"
)

type fixture struct {
	db                         *sql.DB
	svc                        *Service
	project, token, credential string
	raw                        map[string]any
}

func str(v string) *string { return &v }
func integer(v int) *int   { return &v }
func setup(t *testing.T) *fixture {
	t.Helper()
	db := storetest.OpenDB(t)
	f := &fixture{db: db, project: uuid.NewString(), credential: uuid.NewString()}
	stamp := time.Now().UTC().Format(time.RFC3339)
	_, e := db.Exec(`INSERT INTO credentials(id,name,type,scope,ciphertext,masked_value,created_at,updated_at) VALUES (?,'test','git_token','',?,'masked',?,?)`, f.credential, []byte{0}, stamp, stamp)
	if e != nil {
		t.Fatal(e)
	}
	_, e = db.Exec(`INSERT INTO projects(id,name,repo_url,default_branch,credential_id,created_at,updated_at) VALUES (?,'Project A','https://example.invalid/repo','main',?,?,?)`, f.project, f.credential, stamp, stamp)
	if e != nil {
		t.Fatal(e)
	}
	f.raw = map[string]any{"unknownTop": map[string]any{"precise": json.Number("9007199254740993")}, "stages": []any{
		map[string]any{"id": "source", "name": "Source", "kind": "source", "jobs": []any{map[string]any{"id": "git", "name": "Git", "type": "git_source", "config": map[string]any{}}}},
		map[string]any{"id": "test", "name": "Tests", "kind": "custom", "needs": []string{"source"}, "post": []any{map[string]any{"condition": "always", "image": "alpine:3", "commands": []string{"true"}}}, "jobs": []any{map[string]any{"id": "script", "name": "Test", "type": "script", "config": map[string]any{"image": "alpine:3", "commands": "echo synthetic-secret", "timeoutSeconds": 60, "retries": 0, "cpu": "1", "memory": "512m", "unknown": map[string]any{"precise": json.Number("9007199254740993")}}}}},
		map[string]any{"id": "build", "name": "Build", "kind": "build", "needs": []string{"test"}, "jobs": []any{map[string]any{"id": "image", "name": "Package", "type": "build_image", "config": map[string]any{"artifactType": "jar", "buildModel": "toolchain", "toolchainLanguage": "maven", "toolchainVersion": "3-jdk17"}}}},
	}}
	b, _ := json.Marshal(f.raw)
	_, e = db.Exec(`INSERT INTO pipeline_configs(project_id,spec_json,spec_yaml,status,created_at,updated_at,saved_at) VALUES (?,?,'yaml','draft',?,?,?)`, f.project, string(b), stamp, stamp, stamp)
	if e != nil {
		t.Fatal(e)
	}
	_, e = db.Exec(`INSERT INTO pipeline_settings(project_id,build_json,environments_json,steps_json,created_at,updated_at) VALUES (?,?,'[]','[]',?,?)`, f.project, `{"model":"dockerfile","artifactType":"image","vars":[],"cache":{"enabled":false}}`, stamp, stamp)
	if e != nil {
		t.Fatal(e)
	}
	if e = configstate.EnsureProject(context.Background(), db, db, f.project); e != nil {
		t.Fatal(e)
	}
	_, tok, e := auth.NewMCPTokenStore(db).Create(context.Background(), auth.MCPTokenInput{Name: "config fixture", ProjectIDs: []string{f.project}, ConfigGrants: []auth.MCPConfigGrant{{ProjectID: f.project, Read: true, Propose: true, Apply: true, Sections: append([]string(nil), Sections...), Operations: append([]string(nil), OperationTypes...)}}})
	if e != nil {
		t.Fatal(e)
	}
	f.token = tok.ID
	f.svc = New(db, &Options{Runtime: RuntimeInfo{RunnerMode: "dag", ExecutorKind: "local_docker", Available: true}, ApplyEnabled: true, PrepareSecrets: func(context.Context) (func(string) string, error) {
		return func(v string) string { return strings.ReplaceAll(v, "synthetic-secret", "[MASKED]") }, nil
	}, AuthorizeAdmin: func(_ context.Context, _ configstate.DBTX, id string) error {
		if id != "fixture-admin" {
			return ErrForbidden
		}
		return nil
	}})
	f.svc.opts.RevalidateApprover = f.svc.opts.AuthorizeAdmin
	return f
}
func (f *fixture) snapshot(t *testing.T) *Snapshot {
	t.Helper()
	x, e := f.svc.Snapshot(context.Background(), f.token, SnapshotRequest{ProjectID: f.project, Section: "step_limits", BeginSnapshot: true})
	if e != nil {
		t.Fatal(e)
	}
	return x
}
func (f *fixture) create(t *testing.T, ops ...Operation) *Draft {
	t.Helper()
	x := f.snapshot(t)
	d, e := f.svc.Create(context.Background(), f.token, CreateRequest{ProjectID: f.project, BaseRevision: x.BaseRevision, PolicyVersion: x.PolicyVersion, RequestID: uuid.NewString(), Operations: ops})
	if e != nil {
		t.Fatal(e)
	}
	return d
}
func (f *fixture) approve(t *testing.T, d *Draft) *Approval {
	t.Helper()
	a, e := f.svc.Approve(context.Background(), "fixture-admin", d.DraftID, ApprovalRequest{ReviewDigest: d.ReviewDigest, BaseRevision: d.BaseRevision, PolicyVersion: d.PolicyVersion})
	if e != nil {
		t.Fatal(e)
	}
	return a
}
func timeoutOp(n int) Operation {
	return Operation{Type: "step.set_limits", StageID: "test", JobID: "script", Limits: &Limits{TimeoutSeconds: integer(n)}}
}
func (f *fixture) setConfig(t *testing.T, key string, value any) {
	t.Helper()
	st := f.raw["stages"].([]any)[1].(map[string]any)
	j := st["jobs"].([]any)[0].(map[string]any)
	j["config"].(map[string]any)[key] = value
	b, _ := json.Marshal(f.raw)
	if _, e := f.db.Exec(`UPDATE pipeline_configs SET spec_json=? WHERE project_id=?`, string(b), f.project); e != nil {
		t.Fatal(e)
	}
}
func (f *fixture) stored(t *testing.T) string {
	t.Helper()
	var raw string
	if e := f.db.QueryRow(`SELECT spec_json FROM pipeline_configs WHERE project_id=?`, f.project).Scan(&raw); e != nil {
		t.Fatal(e)
	}
	return raw
}

func TestApprovedAtomicApplyPreservesHiddenStateAndIdentity(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	before := f.stored(t)
	d := f.create(t, Operation{Type: "project.set_name", Value: str("New Project")}, timeoutOp(300))
	if len(d.Diff) != 2 || !d.Validation.ApplyAllowed {
		t.Fatalf("incomplete draft: %#v", d)
	}
	if f.stored(t) != before {
		t.Fatal("draft changed formal configuration")
	}
	blob, _ := json.Marshal(d)
	if strings.Contains(string(blob), "synthetic-secret") || strings.Contains(string(blob), "9007199254740993") {
		t.Fatal("private config exported")
	}
	a := f.approve(t, d)
	if f.stored(t) != before {
		t.Fatal("approve applied configuration")
	}
	view, e := f.svc.Get(ctx, f.token, d.DraftID)
	if e != nil || view.Approval.ID != a.ID || !view.Approval.Eligible {
		t.Fatalf("approval not discoverable: %v %#v", e, view)
	}
	in := ApplyRequest{DraftID: d.DraftID, ApprovalID: a.ID, RequestID: uuid.NewString()}
	op, e := f.svc.Apply(ctx, f.token, in)
	if e != nil || op.State != "applied" {
		t.Fatalf("apply: %#v %v", op, e)
	}
	var name string
	_ = f.db.QueryRow(`SELECT name FROM projects WHERE id=?`, f.project).Scan(&name)
	if name != "New Project" {
		t.Fatal("project part missing")
	}
	after := f.stored(t)
	if !strings.Contains(after, "9007199254740993") || !strings.Contains(after, "synthetic-secret") || !strings.Contains(after, `"timeoutSeconds":300`) {
		t.Fatal("hidden state or intended leaf lost", after)
	}
	replay, e := f.svc.Apply(ctx, f.token, in)
	if e != nil || replay.OperationID != op.OperationID || replay.State != "applied" {
		t.Fatal("replay reapplied or lost result", e)
	}
	query, e := f.svc.Operation(ctx, f.token, OperationRequest{ProjectID: f.project, RequestID: in.RequestID})
	if e != nil || query.OperationID != op.OperationID {
		t.Fatal("lost-response recovery unavailable", e)
	}
	var n int
	_ = f.db.QueryRow(`SELECT COUNT(*) FROM mcp_config_operations WHERE state='applied'`).Scan(&n)
	if n != 1 {
		t.Fatal("multiple applications")
	}
}
func TestSnapshotPermissionsVersionsAndNoInitialization(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	grants := []auth.MCPConfigGrant{{ProjectID: f.project, Read: true, Propose: true, Sections: []string{"step_limits"}, Operations: []string{"step.set_limits"}}}
	raw, _ := json.Marshal(grants)
	if _, e := f.db.Exec(`UPDATE mcp_tokens SET config_grants_json=? WHERE id=?`, string(raw), f.token); e != nil {
		t.Fatal(e)
	}
	x := f.snapshot(t)
	if _, e := f.svc.Snapshot(ctx, f.token, SnapshotRequest{ProjectID: f.project, Section: "project", BeginSnapshot: true}); !errors.Is(e, ErrForbidden) {
		t.Fatal("cross section allowed", e)
	}
	if _, e := f.svc.Snapshot(ctx, f.token, SnapshotRequest{ProjectID: f.project, Section: "step_limits"}); !errors.Is(e, ErrInvalid) {
		t.Fatal("missing version allowed", e)
	}
	if _, e := f.db.Exec(`UPDATE project_config_versions SET revision=revision+1 WHERE project_id=?`, f.project); e != nil {
		t.Fatal(e)
	}
	if _, e := f.svc.Snapshot(ctx, f.token, SnapshotRequest{ProjectID: f.project, Section: "step_limits", ExpectedBaseRevision: x.BaseRevision, ExpectedPolicyVersion: x.PolicyVersion}); !errors.Is(e, ErrStale) {
		t.Fatal("mixed version read", e)
	}
	var n int
	_ = f.db.QueryRow(`SELECT COUNT(*) FROM pipeline_triggers WHERE project_id=?`, f.project).Scan(&n)
	if n != 0 {
		t.Fatal("read initialized webhook")
	}
}
func TestTypedLimitsRejectEveryProtectedTemplateSink(t *testing.T) {
	for _, tc := range []struct {
		key   string
		value any
	}{{"commandTemplate", "echo {{cpu}}"}, {"image", "alpine:{{cpu}}"}, {"workDir", "{{cpu}}"}, {"cachePaths", "cache/{{cpu}}"}, {"artifactPath", "dist/{{cpu}}"}} {
		t.Run(tc.key, func(t *testing.T) {
			f := setup(t)
			f.setConfig(t, tc.key, tc.value)
			x := f.snapshot(t)
			_, e := f.svc.Create(context.Background(), f.token, CreateRequest{ProjectID: f.project, BaseRevision: x.BaseRevision, PolicyVersion: x.PolicyVersion, RequestID: uuid.NewString(), Operations: []Operation{{Type: "step.set_limits", StageID: "test", JobID: "script", Limits: &Limits{CPU: str("2")}}}})
			if !errors.Is(e, ErrTemplate) {
				t.Fatal("protected template changed", e)
			}
		})
	}
}
func TestLegalRenameAndBuildImageSelectionAreNotGlobalExemptions(t *testing.T) {
	f := setup(t)
	d := f.create(t, Operation{Type: "stage.rename", StageID: "test", Value: str("Renamed stage")}, Operation{Type: "job.rename", StageID: "test", JobID: "script", Value: str("Renamed job")}, Operation{Type: "build.set", StageID: "build", JobID: "image", Build: &BuildFields{ToolchainVersion: str("3-jdk21")}})
	if len(d.Diff) != 3 {
		t.Fatal("legitimate derived changes rejected", d)
	}
	f.setConfig(t, "commandTemplate", "echo {{cpu}}")
	x := f.snapshot(t)
	_, e := f.svc.Create(context.Background(), f.token, CreateRequest{ProjectID: f.project, BaseRevision: x.BaseRevision, PolicyVersion: x.PolicyVersion, RequestID: uuid.NewString(), Operations: []Operation{{Type: "job.rename", StageID: "test", JobID: "script", Value: str("New display")}, {Type: "step.set_limits", StageID: "test", JobID: "script", Limits: &Limits{CPU: str("2")}}}})
	if !errors.Is(e, ErrTemplate) {
		t.Fatal("rename masked command change", e)
	}
}
func TestUnconsumedBuildFieldsAndUnsupportedRuntimeAreReadOnly(t *testing.T) {
	f := setup(t)
	x := f.snapshot(t)
	_, e := f.svc.Create(context.Background(), f.token, CreateRequest{ProjectID: f.project, BaseRevision: x.BaseRevision, PolicyVersion: x.PolicyVersion, RequestID: uuid.NewString(), Operations: []Operation{{Type: "build.set", StageID: "build", JobID: "image", Build: &BuildFields{Context: str("subdir")}}}})
	if !errors.Is(e, ErrUnsupported) {
		t.Fatal("unused field editable", e)
	}
	for _, mode := range []string{"legacy", "stub", "unknown"} {
		f.svc.opts.Runtime.RunnerMode = mode
		x = f.snapshot(t)
		if x.ExecutionApplicability != "read_only" {
			t.Fatal("unsupported mode writable", mode)
		}
	}
	f.svc.opts.Runtime.RunnerMode = "dag"
	f.svc.opts.Runtime.PACOverride = true
	x = f.snapshot(t)
	if x.SourceMode != "repo_preferred" || len(x.EditableOperations) != 0 {
		t.Fatal("PAC rename allowed")
	}
}
func TestNoChangesAndDuplicateInputRemainIdempotent(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	x := f.snapshot(t)
	in := CreateRequest{ProjectID: f.project, BaseRevision: x.BaseRevision, PolicyVersion: x.PolicyVersion, RequestID: uuid.NewString(), Operations: []Operation{timeoutOp(60)}}
	a, e := f.svc.Create(ctx, f.token, in)
	if e != nil || a.State != "no_changes" || a.DraftID != "" {
		t.Fatalf("same numeric leaf became change: %#v %v", a, e)
	}
	b, e := f.svc.Create(ctx, f.token, in)
	if e != nil || b.State != "no_changes" {
		t.Fatal("no changes replay lost", e)
	}
	in.Operations = []Operation{timeoutOp(61)}
	if _, e = f.svc.Create(ctx, f.token, in); !errors.Is(e, ErrIdempotency) {
		t.Fatal("same request changed payload", e)
	}
	in.RequestID = uuid.NewString()
	in.Operations = []Operation{timeoutOp(61), timeoutOp(62)}
	if _, e = f.svc.Create(ctx, f.token, in); !errors.Is(e, ErrInvalid) {
		t.Fatal("duplicate field accepted", e)
	}
}
func TestApplyAuditFailureRollsBackEveryFormalWrite(t *testing.T) {
	f := setup(t)
	d := f.create(t, Operation{Type: "project.set_name", Value: str("Rollback candidate")}, timeoutOp(120))
	a := f.approve(t, d)
	before := f.stored(t)
	rev := d.BaseRevision
	f.svc.opts.AppendAudit = func(ctx context.Context, q configstate.DBTX, e audit.Entry) error {
		if e.Action == "mcp_config_apply" {
			return errors.New("injected local audit failure")
		}
		return nil
	}
	op, e := f.svc.Apply(context.Background(), f.token, ApplyRequest{DraftID: d.DraftID, ApprovalID: a.ID, RequestID: uuid.NewString()})
	if e != nil || op.State != "failed" {
		t.Fatal("audit failure outcome", op, e)
	}
	if f.stored(t) != before || f.snapshot(t).BaseRevision != rev {
		t.Fatal("partial config or revision commit")
	}
	var name string
	_ = f.db.QueryRow(`SELECT name FROM projects WHERE id=?`, f.project).Scan(&name)
	if name != "Project A" {
		t.Fatal("project update escaped rollback")
	}
	var consumed, applied sql.NullString
	_ = f.db.QueryRow(`SELECT consumed_by_operation_id FROM mcp_config_approvals WHERE id=?`, a.ID).Scan(&consumed)
	_ = f.db.QueryRow(`SELECT applied_operation_id FROM mcp_config_drafts WHERE id=?`, d.DraftID).Scan(&applied)
	if consumed.Valid || applied.Valid {
		t.Fatal("approval or draft partially consumed")
	}
}
func TestActivityAndConflictsRejectWithoutQueuingOrConsuming(t *testing.T) {
	for _, reason := range []string{"activity", "revision"} {
		t.Run(reason, func(t *testing.T) {
			f := setup(t)
			d := f.create(t, timeoutOp(120))
			a := f.approve(t, d)
			before := f.stored(t)
			if reason == "activity" {
				_, e := f.db.Exec(`INSERT INTO project_activity(activity_id,project_id,owner_epoch,kind,phase,created_at,heartbeat_at) VALUES (?,?,'test-owner','run','executing',0,0)`, uuid.NewString(), f.project)
				if e != nil {
					t.Fatal(e)
				}
			} else {
				_, _ = f.db.Exec(`UPDATE project_config_versions SET revision=revision+1 WHERE project_id=?`, f.project)
			}
			op, e := f.svc.Apply(context.Background(), f.token, ApplyRequest{DraftID: d.DraftID, ApprovalID: a.ID, RequestID: uuid.NewString()})
			if e != nil || op.State != "rejected" {
				t.Fatal(op, e)
			}
			expected := "active_runs"
			if reason == "revision" {
				expected = "revision_conflict"
			}
			if op.Reason != expected || f.stored(t) != before {
				t.Fatal("incorrect rejection", op)
			}
		})
	}
}
func TestConcurrentSameRequestAppliesOnce(t *testing.T) {
	f := setup(t)
	d := f.create(t, timeoutOp(120))
	a := f.approve(t, d)
	in := ApplyRequest{DraftID: d.DraftID, ApprovalID: a.ID, RequestID: uuid.NewString()}
	var wg sync.WaitGroup
	results := make(chan *OperationResult, 2)
	errs := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Go(func() { r, e := f.svc.Apply(context.Background(), f.token, in); results <- r; errs <- e })
	}
	wg.Wait()
	close(results)
	close(errs)
	for e := range errs {
		if e != nil {
			t.Fatal(e)
		}
	}
	id := ""
	for r := range results {
		if r == nil {
			t.Fatal("missing result")
		}
		if id != "" && id != r.OperationID {
			t.Fatal("two intent identities")
		}
		id = r.OperationID
	}
	var n int
	_ = f.db.QueryRow(`SELECT COUNT(*) FROM mcp_config_operations WHERE state='applied'`).Scan(&n)
	if n != 1 {
		t.Fatal("applied more than once", n)
	}
}
func TestRevocationAndBrowserAuthorityAreRechecked(t *testing.T) {
	f := setup(t)
	d := f.create(t, timeoutOp(120))
	a := f.approve(t, d)
	f.svc.opts.RevalidateApprover = func(context.Context, configstate.DBTX, string) error { return ErrForbidden }
	op, e := f.svc.Apply(context.Background(), f.token, ApplyRequest{DraftID: d.DraftID, ApprovalID: a.ID, RequestID: uuid.NewString()})
	if e != nil || op.State != "rejected" || op.Reason != "approval_invalid" {
		t.Fatal("stale browser authority accepted", op, e)
	}
	if e = auth.NewMCPTokenStore(f.db).Revoke(context.Background(), f.token); e != nil {
		t.Fatal(e)
	}
	if _, e = f.svc.Get(context.Background(), f.token, d.DraftID); !errors.Is(e, ErrForbidden) {
		t.Fatal("revoked token read", e)
	}
}
func TestSourceByteBudgetAndSecretGuardFailClosed(t *testing.T) {
	f := setup(t)
	f.raw["oversized"] = strings.Repeat("界", 400000)
	b, _ := json.Marshal(f.raw)
	_, _ = f.db.Exec(`UPDATE pipeline_configs SET spec_json=? WHERE project_id=?`, string(b), f.project)
	if _, e := f.svc.Snapshot(context.Background(), f.token, SnapshotRequest{ProjectID: f.project, Section: "pipeline", BeginSnapshot: true}); !errors.Is(e, ErrLarge) {
		t.Fatal("Unicode byte budget bypass", e)
	}
	f.svc.opts.PrepareSecrets = nil
	if _, e := f.svc.Snapshot(context.Background(), f.token, SnapshotRequest{ProjectID: f.project, Section: "pipeline", BeginSnapshot: true}); !errors.Is(e, ErrSecret) {
		t.Fatal("no guard allowed data", e)
	}
}
func TestPendingFencingStopsReplay(t *testing.T) {
	f := setup(t)
	d := f.create(t, timeoutOp(120))
	a := f.approve(t, d)
	in := ApplyRequest{DraftID: d.DraftID, ApprovalID: a.ID, RequestID: uuid.NewString()}
	id := uuid.NewString()
	base, _ := parseRevision(d.BaseRevision)
	_, e := f.db.Exec(`INSERT INTO mcp_config_operations(id,token_id,project_id,request_id,input_digest,draft_id,approval_id,owner_epoch,state,reason,before_revision,result_json,created_at,updated_at) VALUES (?,?,?,?,?,?,?,'old-owner','pending','',?,'{}',0,0)`, id, f.token, f.project, in.RequestID, digest(in), d.DraftID, a.ID, base)
	if e != nil {
		t.Fatal(e)
	}
	if e = f.svc.FencePending(context.Background(), "fixture-admin", id); e != nil {
		t.Fatal(e)
	}
	op, e := f.svc.Apply(context.Background(), f.token, in)
	if e != nil || op.State != "failed" || op.Reason != "interrupted_not_applied" {
		t.Fatal("fenced intent executed", op, e)
	}
}

func TestDraftPagesNeverClaimPartialReviewIsComplete(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	d := f.create(t, Operation{Type: "project.set_name", Value: str("Renamed")}, Operation{Type: "step.set_limits", StageID: "test", JobID: "script", Limits: &Limits{TimeoutSeconds: integer(120), Retries: integer(1)}})
	first, err := f.svc.GetPage(ctx, f.token, d.DraftID, "", 1)
	if err != nil || first.Complete || first.NextCursor == "" || len(first.Diff) != 1 {
		t.Fatal("first page claims full review", first, err)
	}
	second, err := f.svc.GetPage(ctx, f.token, d.DraftID, first.NextCursor, 2)
	if err != nil || second.Complete || second.NextCursor != "" || len(second.Diff) != 2 {
		t.Fatal("last partial page claims full review", second, err)
	}
	full, err := f.svc.GetPage(ctx, f.token, d.DraftID, "", 32)
	if err != nil || !full.Complete || len(full.Diff) != 3 {
		t.Fatal("bounded full page unavailable", full, err)
	}
	review, err := f.svc.Review(ctx, "fixture-admin", d.DraftID)
	if err != nil || !review.Complete || len(review.Diff) != 3 || review.NextCursor != "" {
		t.Fatal("browser review is incomplete", review, err)
	}
	if _, err = f.svc.GetPage(ctx, f.token, d.DraftID, first.NextCursor+"x", 2); !errors.Is(err, ErrInvalid) {
		t.Fatal("tampered cursor accepted", err)
	}
	d2 := f.create(t, timeoutOp(180))
	if _, err = f.svc.GetPage(ctx, f.token, d2.DraftID, first.NextCursor, 2); !errors.Is(err, ErrStale) {
		t.Fatal("cross-draft cursor accepted", err)
	}
}

func TestApplyYAMLRetainsDAGControlsAndUnknownValues(t *testing.T) {
	f := setup(t)
	d := f.create(t, timeoutOp(120))
	a := f.approve(t, d)
	op, err := f.svc.Apply(context.Background(), f.token, ApplyRequest{DraftID: d.DraftID, ApprovalID: a.ID, RequestID: uuid.NewString()})
	if err != nil || op.State != "applied" {
		t.Fatal(op, err)
	}
	var y string
	if err = f.db.QueryRow(`SELECT spec_yaml FROM pipeline_configs WHERE project_id=?`, f.project).Scan(&y); err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{"9007199254740993", "unknownTop", "post:", "needs:", "id:", "synthetic-secret"} {
		if !strings.Contains(y, value) {
			t.Fatalf("YAML lost protected field %q", value)
		}
	}
}

func TestSnapshotAndOptionsCursorBindAuthorizationRevision(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	page, err := f.svc.Snapshot(ctx, f.token, SnapshotRequest{ProjectID: f.project, Section: "pipeline", BeginSnapshot: true, PageSize: 1})
	if err != nil || page.NextCursor == "" {
		t.Fatal(page, err)
	}
	options, err := f.svc.OptionsList(ctx, f.token, OptionsRequest{ProjectID: f.project, Kind: "operations", ExpectedBaseRevision: page.BaseRevision, ExpectedPolicyVersion: page.PolicyVersion, PageSize: 1})
	if err != nil || options.NextCursor == "" {
		t.Fatal(options, err)
	}
	if _, err = f.db.Exec(`UPDATE mcp_tokens SET authz_revision=authz_revision+1 WHERE id=?`, f.token); err != nil {
		t.Fatal(err)
	}
	if _, err = f.svc.Snapshot(ctx, f.token, SnapshotRequest{ProjectID: f.project, Section: "pipeline", ExpectedBaseRevision: page.BaseRevision, ExpectedPolicyVersion: page.PolicyVersion, Cursor: page.NextCursor, PageSize: 1}); !errors.Is(err, ErrStale) {
		t.Fatal("snapshot cursor survived changed authorization", err)
	}
	if _, err = f.svc.OptionsList(ctx, f.token, OptionsRequest{ProjectID: f.project, Kind: "operations", ExpectedBaseRevision: page.BaseRevision, ExpectedPolicyVersion: page.PolicyVersion, Cursor: options.NextCursor, PageSize: 1}); !errors.Is(err, ErrStale) {
		t.Fatal("options cursor survived changed authorization", err)
	}
}

func TestDraftPageInvalidatesAcrossAuthorizationPolicyAndCatalogChanges(t *testing.T) {
	for _, change := range []string{"authorization", "policy", "catalog", "executor"} {
		t.Run(change, func(t *testing.T) {
			f := setup(t)
			d := f.create(t, Operation{Type: "project.set_name", Value: str("Renamed")}, timeoutOp(120))
			page, err := f.svc.GetPage(context.Background(), f.token, d.DraftID, "", 1)
			if err != nil || page.NextCursor == "" {
				t.Fatal(page, err)
			}
			if change == "authorization" {
				_, err = f.db.Exec(`UPDATE mcp_tokens SET authz_revision=authz_revision+1 WHERE id=?`, f.token)
			} else {
				epoch := map[string]string{"policy": configstate.EpochPolicy, "catalog": configstate.EpochSecrets, "executor": configstate.EpochExecutor}[change]
				_, err = f.db.Exec(`UPDATE config_control_epochs SET value=value+1 WHERE name=?`, epoch)
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, err = f.svc.GetPage(context.Background(), f.token, d.DraftID, page.NextCursor, 1); !errors.Is(err, ErrStale) {
				t.Fatal("old page cursor survived control change", err)
			}
			current, err := f.svc.Get(context.Background(), f.token, d.DraftID)
			if err != nil || current.State != "invalidated" || len(current.Diff) != 0 || len(current.Operations) != 0 || current.Validation.ApplyAllowed {
				t.Fatal("invalidated draft exposed old editable data", current, err)
			}
		})
	}
}

func TestDeliveryRechecksControlChangesAfterCollection(t *testing.T) {
	for _, change := range []string{"authorization", "policy", "catalog"} {
		t.Run(change, func(t *testing.T) {
			f := setup(t)
			f.svc.opts.AppendAudit = func(ctx context.Context, q configstate.DBTX, _ audit.Entry) error {
				var err error
				if change == "authorization" {
					_, err = q.ExecContext(ctx, `UPDATE mcp_tokens SET authz_revision=authz_revision+1 WHERE id=?`, f.token)
				} else {
					epoch := configstate.EpochPolicy
					if change == "catalog" {
						epoch = configstate.EpochSecrets
					}
					_, err = q.ExecContext(ctx, `UPDATE config_control_epochs SET value=value+1 WHERE name=?`, epoch)
				}
				return err
			}
			out, err := f.svc.Snapshot(context.Background(), f.token, SnapshotRequest{ProjectID: f.project, Section: "project", BeginSnapshot: true})
			want := map[string]error{"authorization": ErrForbidden, "policy": ErrPolicy, "catalog": ErrCatalog}[change]
			if out != nil || !errors.Is(err, want) {
				t.Fatal("collected data delivered after control change", out, err)
			}
		})
	}
}

func TestApplyUsesStoredApproverVerifierWithoutBrowserContext(t *testing.T) {
	f := setup(t)
	d := f.create(t, timeoutOp(120))
	a := f.approve(t, d)
	f.svc.opts.AuthorizeAdmin = func(context.Context, configstate.DBTX, string) error { return ErrForbidden }
	op, err := f.svc.Apply(context.Background(), f.token, ApplyRequest{DraftID: d.DraftID, ApprovalID: a.ID, RequestID: uuid.NewString()})
	if err != nil || op.State != "applied" {
		t.Fatal("MCP apply required current browser context", op, err)
	}
	if _, err = f.svc.Review(context.Background(), "fixture-admin", d.DraftID); !errors.Is(err, ErrForbidden) {
		t.Fatal("browser read bypassed live authorization", err)
	}
}
