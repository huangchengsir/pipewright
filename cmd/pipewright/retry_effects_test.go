package main

import (
	"context"
	"github.com/huangchengsir/pipewright/internal/chain"
	"github.com/huangchengsir/pipewright/internal/pipeline"
	"github.com/huangchengsir/pipewright/internal/run"
	"github.com/huangchengsir/pipewright/internal/runretry"
	"github.com/huangchengsir/pipewright/internal/store"
	"github.com/huangchengsir/pipewright/internal/storetest"
	"github.com/huangchengsir/pipewright/internal/vault"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestRetryTerminalEffectsPersistAcrossCallbacksAndServiceRestart(t *testing.T) {
	storetest.ForEachDialect(t, func(t *testing.T, st *store.Store) {
		ctx := context.Background()
		for _, q := range []string{
			"INSERT INTO credentials(id,name,type,scope,ciphertext,masked_value,created_at,updated_at) VALUES('cred','fixture','git_token','',X'00','m','old','old')",
			"INSERT INTO projects(id,name,repo_url,default_branch,credential_id,created_at,updated_at) VALUES('project','fixture','https://example.invalid/repo','main','cred','old','old'),('down','down','https://example.invalid/down','main','cred','old','old')",
			"INSERT INTO pipeline_runs(id,project_id,status,trigger_type,trigger_branch,trigger_actor,chain_depth,created_at,finished_at) VALUES('root','project','success','chain','main','original',2,'2026-10-28T00:00:00Z','2026-10-28T00:01:00Z')",
		} {
			if _, e := st.DB.Exec(q); e != nil {
				t.Fatal(e)
			}
		}
		var key [32]byte
		key[0] = 41
		v := vault.New(st.DB, &key)
		rr := runretry.New(st.DB, v)
		snap := runretry.Snapshot{ProjectID: "project", RequestedRef: "main", CommitSHA: strings.Repeat("a", 40), Spec: pipeline.Spec{Stages: []pipeline.Stage{{ID: "stage", Jobs: []pipeline.Job{{ID: "job", Type: "script"}}}}}}
		if e := rr.SaveManifest(ctx, "root", snap); e != nil {
			t.Fatal(e)
		}
		if _, e := st.DB.Exec("INSERT INTO pipeline_runs(id,project_id,status,trigger_type,trigger_branch,trigger_actor,chain_depth,created_at,finished_at) SELECT 'child',project_id,status,trigger_type,trigger_branch,trigger_actor,chain_depth,created_at,finished_at FROM pipeline_runs WHERE id='root'"); e != nil {
			t.Fatal(e)
		}
		if _, e := st.DB.Exec("INSERT INTO run_retry_attempts(run_id,root_id,parent_id,revision,execution_kind,status,created_at,initiator) VALUES('child','root','root',2,'recovery','success',1,'new-operator')"); e != nil {
			t.Fatal(e)
		}
		chains := chain.NewService(st.DB)
		if _, e := chains.Save(ctx, "project", []chain.Target{{DownstreamProjectID: "down", Enabled: true}}); e != nil {
			t.Fatal(e)
		}
		hook := retryTerminalOnce(rr, "chain:success", false, true, chain.NewHook(chains, run.New(st.DB), 5))
		hook(ctx, "root", run.StatusFailed) // failure must not consume success reservation
		var wg sync.WaitGroup
		for i := 0; i < 8; i++ {
			wg.Add(1)
			go func() { defer wg.Done(); hook(ctx, "root", run.StatusSuccess) }()
		}
		wg.Wait()
		restarted := runretry.New(st.DB, v)
		retryTerminalOnce(restarted, "chain:success", false, true, chain.NewHook(chains, run.New(st.DB), 5))(ctx, "child", run.StatusSuccess)
		var count, depth int
		var source string
		if e := st.DB.QueryRow("SELECT COUNT(*),MAX(chain_depth),MAX(chain_source_run_id) FROM pipeline_runs WHERE project_id='down'").Scan(&count, &depth, &source); e != nil {
			t.Fatal(e)
		}
		if count != 1 || depth != 3 || source != "root" {
			t.Fatal(count, depth, source)
		}
		var notifications atomic.Int32
		notify := retryTerminalOnce(restarted, "notify:completion", true, false, func(context.Context, string, string) { notifications.Add(1) })
		notify(ctx, "root", "success")
		notify(ctx, "root", "success")
		notify(ctx, "child", "success")
		notify(ctx, "child", "success")
		if notifications.Load() != 2 {
			t.Fatal(notifications.Load())
		}
		withoutKey := runretry.New(st.DB, nil)
		identity, e := withoutKey.Identity(ctx, "child")
		if e != nil || identity.Initiator != "new-operator" || identity.SourceRunID != "root" || identity.ExecutionKind != "recovery" {
			t.Fatal(identity, e)
		}
		projected := run.New(st.DB)
		detail, e := projected.Get(ctx, "child")
		if e != nil || detail.ExecutionKind != "recovery" || detail.LogicalRunID != "root" || detail.SourceRunID != "root" || detail.RetryInitiator != "new-operator" || detail.AttemptNumber != 2 {
			t.Fatal(detail, e)
		}
		listed, e := projected.List(ctx, run.ListFilter{ProjectID: "project", PageSize: 100})
		if e != nil || len(listed.Items) != 2 {
			t.Fatal(listed, e)
		}
		kinds := map[string]string{}
		for _, item := range listed.Items {
			kinds[item.ID] = item.ExecutionKind
		}
		if kinds["root"] != "original" || kinds["child"] != "recovery" {
			t.Fatal(kinds)
		}
		records, e := run.NewMetricsService(projected).MetricsRecords(ctx, "project", time.Time{})
		if e != nil || len(records) != 2 {
			t.Fatal(records, e)
		}
		countRecovery := 0
		for _, item := range records {
			if item.ExecutionKind == "recovery" {
				countRecovery++
				if item.SourceRunID != "root" || item.LogicalRunID != "root" {
					t.Fatal(item)
				}
			}
		}
		if countRecovery != 1 {
			t.Fatal(countRecovery)
		}
		page, e := withoutKey.Attempts(ctx, "child", 1, 20)
		if e != nil || page.Total != 2 {
			t.Fatal(page, e)
		}
		found := false
		for _, attempt := range page.Items {
			if attempt.RunID == "child" {
				found = attempt.Initiator == "new-operator"
			}
		}
		if !found {
			t.Fatal("history lost recovery initiator")
		}

		if _, e := st.DB.Exec("DELETE FROM pipeline_runs WHERE id='root'"); e != nil {
			t.Fatal(e)
		}
		if claimed, e := restarted.ClaimEffect(ctx, "root", "late:retired"); e != nil || claimed {
			t.Fatal("late retired run reclaimed effect", claimed, e)
		}

	})
}
