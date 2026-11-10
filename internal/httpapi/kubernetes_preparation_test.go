package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/huangchengsir/pipewright/internal/auth"
	"github.com/huangchengsir/pipewright/internal/build"
	"github.com/huangchengsir/pipewright/internal/dagrun"
	"github.com/huangchengsir/pipewright/internal/kubernetes"
	"github.com/huangchengsir/pipewright/internal/pipeline"
	"github.com/huangchengsir/pipewright/internal/run"
	"github.com/huangchengsir/pipewright/internal/vault"
	"strings"
	"testing"
	"time"
)

func TestKubernetesPreparationFailureReachesWorkerPoolAndHTTPLogs(t *testing.T) {
	for _, typed := range []bool{false, true} {
		t.Run(map[bool]string{false: "generic", true: "builder"}[typed], func(t *testing.T) {
			f, _, _, sess, _ := configHTTPFixture(t)
			key := [32]byte{19, 23}
			k := kubernetes.New(f.db, vault.New(f.db, &key))
			b := &build.Builder{}
			build.WithKubernetes(k, kubernetes.NewController(k, nil), "phase-owner")(b)
			build.WithKubernetesReleases(kubernetes.NewReleaseController(k, nil))(b)
			spec := pipeline.Spec{Stages: []pipeline.Stage{{ID: "deploy", Name: "Kubernetes", Kind: pipeline.KindDeploy, Jobs: []pipeline.Job{{ID: "kube", Name: "Release", Type: pipeline.KubernetesJobType, Config: map[string]any{"version": 2}}}}}}
			loader := dagrun.SpecLoaderFunc(func(context.Context, string) (*pipeline.Config, error) { return &pipeline.Config{Spec: spec}, nil })
			prepare := b.PrepareKubernetesStage
			if !typed {
				prepare = func(context.Context, *run.Run, pipeline.Stage) (dagrun.StagePreparation, error) {
					return dagrun.StagePreparation{}, errors.New("private-server-response-env-command")
				}
			}
			runner := dagrun.New(loader, dagrun.WithStagePreparation(prepare))
			pool := run.NewWorkerPool(f.runs, run.WithRunner(runner))
			pool.Start()
			defer pool.Stop(context.Background())
			admin := New(testWebFSAuth(), configSessionAuth{store: auth.NewSessionStore(f.db)}, WithRuns(f.runs, pool))
			started := configAdminRequest(admin, sess, "POST", "/api/projects/"+f.project+"/runs", `{"branch":"main"}`, "", true)
			var detail runDetailDTO
			if started.Code != 201 || json.Unmarshal(started.Body.Bytes(), &detail) != nil {
				t.Fatal("HTTP run admission failed", started.Code)
			}
			deadline := time.Now().Add(3 * time.Second)
			for time.Now().Before(deadline) {
				rn, e := f.runs.Get(context.Background(), detail.ID)
				if e != nil {
					t.Fatal(e)
				}
				if rn.Status == run.StatusFailed {
					break
				}
				time.Sleep(5 * time.Millisecond)
			}
			got := configAdminRequest(admin, sess, "GET", "/api/runs/"+detail.ID+"/logs", "", "", false)
			var logs runLogsDTO
			if got.Code != 200 || json.Unmarshal(got.Body.Bytes(), &logs) != nil || len(logs.Lines) == 0 {
				t.Fatal("preparation failure has no durable HTTP logs", got.Code)
			}
			want := "stage preparation: phase=prepare reason=preparation_failed"
			if typed {
				want = "kubernetes preparation: phase=snapshot reason=preparation_failed"
			}
			found := false
			for _, line := range logs.Lines {
				found = found || line.Text == want
				if strings.Contains(line.Text, "private-server-response") {
					t.Fatal("raw preparation error leaked")
				}
			}
			if !found {
				t.Fatal("closed phase/reason missing from run logs")
			}
			final, e := f.runs.Get(context.Background(), detail.ID)
			if e != nil || final.Status != run.StatusFailed {
				t.Fatal("worker did not fail before dispatch")
			}
		})
	}
}
