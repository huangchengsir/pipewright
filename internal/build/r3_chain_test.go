package build

import (
	"context"
	"github.com/huangchengsir/pipewright/internal/deploy"
	"github.com/huangchengsir/pipewright/internal/notify"
	"github.com/huangchengsir/pipewright/internal/pipeline"
	"github.com/huangchengsir/pipewright/internal/run"
	"github.com/huangchengsir/pipewright/internal/target"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

type r3Targets struct {
	target.Service
	calls atomic.Int32
}

func (t *r3Targets) Get(context.Context, string) (*target.Server, error) {
	return &target.Server{ID: "a", Name: "fixture", Host: "127.0.0.1", Port: 22, User: "fixture", CredentialID: "cred"}, nil
}
func (t *r3Targets) Exec(context.Context, string, []string) (*target.ExecResult, error) {
	t.calls.Add(1)
	return &target.ExecResult{ExitCode: 0}, nil
}
func TestR3BuildRecoveryContinuesPreviouslyUnstartedDeploymentAndNotify(t *testing.T) {
	for _, allowFailure := range []bool{false, true} {
		name := "not_started"
		if allowFailure {
			name = "successful_descendant_invalidated"
		}
		t.Run(name, func(t *testing.T) {
			spec := pipeline.Spec{Stages: []pipeline.Stage{
				{ID: "build", Name: "build", Kind: "build", AllowFailure: allowFailure, Jobs: []pipeline.Job{retryJob("build")}},
				{ID: "deploy", Name: "deploy", Kind: "deploy", Needs: []string{"build"}, Jobs: []pipeline.Job{{ID: "deploy", Name: "deploy", Type: "deploy_ssh", Config: map[string]any{"serverId": "a", "artifactType": "command", "restartCommand": "safe-fixture-command"}}}},
				{ID: "notify", Name: "notify", Kind: "notify", Needs: []string{"deploy"}, Jobs: []pipeline.Job{{ID: "notify", Name: "notify", Type: "notify", Config: map[string]any{"channel": "fixture-channel"}}}},
			}}
			h := newRetryHarness(t, spec)
			targets := &r3Targets{}
			h.b.deployer = deploy.New(targets, run.New(h.store.DB), deploy.WithBatchDB(h.store.DB), deploy.WithRetryLedger(h.s))
			var delivered atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { delivered.Add(1); w.WriteHeader(200) }))
			defer server.Close()
			h.b.notifier = notify.New(h.store.DB, nil, server.Client())
			if _, e := h.b.notifier.Create(context.Background(), notify.CreateInput{Name: "fixture-channel", Type: notify.TypeWebhook, Enabled: true, Config: notify.Config{URL: server.URL}}); e != nil {
				t.Fatal(e)
			}
			h.driver.fail["build"] = true
			if h.execute(t, "root") == nil {
				t.Fatal("expected initial build failure")
			}
			before := int32(0)
			if allowFailure {
				before = 1
			}
			if targets.calls.Load() != before || delivered.Load() != before {
				t.Fatalf("initial effects deploy=%d notify=%d", targets.calls.Load(), delivered.Load())
			}
			h.driver.fail["build"] = false
			child := h.submit(t, "root", []string{h.node(t, "root", "build", "build").Key}, nil)
			if e := h.execute(t, child); e != nil {
				t.Fatal(e)
			}
			var unreleased, exits int
			if e := h.store.DB.QueryRow("SELECT COUNT(*) FROM project_activity WHERE run_id=? AND released_at IS NULL", child).Scan(&unreleased); e != nil {
				t.Fatal(e)
			}
			if e := h.store.DB.QueryRow("SELECT COUNT(*) FROM run_retry_reader_exits WHERE attempt_id=?", child).Scan(&exits); e != nil || unreleased != 0 || exits == 0 {
				t.Fatalf("settle bypassed drain proof: unreleased=%d exits=%d err=%v", unreleased, exits, e)
			}
			if targets.calls.Load() != before+1 || delivered.Load() != before+1 || h.driver.counts["build"] != 2 {
				t.Fatalf("counts build=%v deploy=%d notify=%d", h.driver.counts, targets.calls.Load(), delivered.Load())
			}
			var parentTargets int
			if e := h.store.DB.QueryRow("SELECT COUNT(*) FROM deploy_targets WHERE run_id='root'").Scan(&parentTargets); e != nil || parentTargets != int(before) {
				t.Fatal("parent deployment history changed", parentTargets, e)
			}
		})
	}
}
func TestR3DeclaredNonReusableOutputNeedsExplicitProducerRebuild(t *testing.T) {
	a := retryJob("A")
	a.Config["artifacts"] = `[{"path":"out.txt"}]`
	a.Config["checkpoint"] = map[string]any{"reusable": false}
	h := newRetryHarness(t, retrySpec(a, retryJob("B", "A")))
	h.driver.fail["B"] = true
	if h.execute(t, "root") == nil {
		t.Fatal("expected failure")
	}
	b := h.node(t, "root", "stage", "B")
	aNode := h.node(t, "root", "stage", "A")
	p, e := h.s.Plan(context.Background(), "root", []string{b.Key})
	if e != nil || p.CanSubmit || len(p.RebuildGroups) != 1 || p.RebuildGroups[0].ReasonCode != "checkpoint_nonreusable" {
		t.Fatal(p, e)
	}
	h.driver.fail["B"] = false
	child := h.submit(t, "root", []string{b.Key}, []string{aNode.Key})
	if e := h.execute(t, child); e != nil {
		t.Fatal(e)
	}
	if h.driver.counts["A"] != 2 || h.driver.counts["B"] != 2 {
		t.Fatal(h.driver.counts)
	}
}
