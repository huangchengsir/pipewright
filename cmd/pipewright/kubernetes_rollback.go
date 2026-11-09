package main

import (
	"context"
	"database/sql"
	"errors"
	"github.com/google/uuid"
	"github.com/huangchengsir/pipewright/internal/build"
	"github.com/huangchengsir/pipewright/internal/configstate"
	"github.com/huangchengsir/pipewright/internal/kubernetes"
	"github.com/huangchengsir/pipewright/internal/pipeline"
	"github.com/huangchengsir/pipewright/internal/run"
	"github.com/huangchengsir/pipewright/internal/runretry"
	"time"
)

type kubernetesRollbackCoordinator struct {
	db         *sql.DB
	releases   *kubernetes.Service
	controller *kubernetes.ReleaseController
	retries    *runretry.Service
	runs       run.Service
	owner      string
}

func frozenRollbackSnapshot(snapshot runretry.Snapshot, source kubernetes.ReleaseRecord) (runretry.Snapshot, runretry.NodeRef, error) {
	var selected runretry.NodeRef
	found := false
	if snapshot.Compiled == nil {
		return snapshot, selected, runretry.ErrUnavailable
	}
	for _, node := range snapshot.Compiled.Nodes {
		if node.Key == source.NodeKey && node.Type == "deploy_kubernetes" {
			selected = node
			found = true
			break
		}
	}
	if !found {
		return snapshot, selected, runretry.ErrUnavailable
	}
	var stage pipeline.Stage
	var job pipeline.Job
	for _, st := range snapshot.Spec.Stages {
		if st.ID == selected.StageID {
			stage = st
			for _, j := range st.Jobs {
				if j.ID == selected.JobID {
					job = j
				}
			}
		}
	}
	if stage.ID == "" || job.ID == "" {
		return snapshot, selected, runretry.ErrUnavailable
	}
	stage.Needs = nil
	stage.Post = nil
	stage.Services = nil
	stage.AllowFailure = false
	stage.When = pipeline.When{}
	stage.Gate = true
	job.Needs = nil
	stage.Jobs = []pipeline.Job{job}
	if len(selected.Axes) > 0 {
		stage.Matrix = map[string][]string{}
		for key, value := range selected.Axes {
			stage.Matrix[key] = []string{value}
		}
	} else {
		stage.Matrix = nil
	}
	snapshot.Spec = pipeline.Spec{Stages: []pipeline.Stage{stage}}
	snapshot.Compiled = nil
	snapshot.Kubernetes = nil
	snapshot.KubernetesReleasePolicies = nil
	snapshot.KubernetesRollbacks = nil
	snapshot.Source = runretry.ConfigSource{Kind: "release_rollback", Ref: source.ID}
	snapshot.Trigger.Type = "kubernetes_rollback"
	snapshot.Trigger.Commit = ""
	snapshot.Trigger.ChainSourceRunID = ""
	snapshot.Trigger.ChainDepth = 0
	graph, e := runretry.Compile(snapshot.Spec)
	if e != nil {
		return snapshot, selected, e
	}
	matches := 0
	for _, n := range graph.Nodes {
		if n.JobID != "" {
			if n.Key != source.NodeKey {
				return snapshot, selected, runretry.ErrConflict
			}
			selected = n
			matches++
		}
	}
	if matches != 1 {
		return snapshot, selected, runretry.ErrInvalid
	}
	return snapshot, selected, nil
}

func (c kubernetesRollbackCoordinator) Start(ctx context.Context, in kubernetes.ReleaseRollbackRequest, commit kubernetes.ReleaseRollbackCommit) (out kubernetes.ReleaseRollbackStart, resultErr error) {
	if commit == nil || c.controller == nil || c.retries == nil {
		return out, runretry.ErrUnavailable
	}
	// Same request recovers a lost acknowledgement even after source revision moved.
	if previous, e := c.releases.LookupReleaseRollback(ctx, in); e == nil {
		return previous, nil
	} else if !errors.Is(e, kubernetes.ErrNotFound) {
		return out, e
	}
	source, e := c.releases.GetRelease(ctx, in.SourceID)
	if e != nil {
		return out, e
	}
	snapshot, e := c.retries.FrozenSnapshotForRelease(ctx, source.RunID)
	if e != nil {
		return out, e
	}
	snapshot, node, e := frozenRollbackSnapshot(snapshot, source)
	if e != nil {
		return out, e
	}
	creator, _ := run.RetryAdapters(c.runs)
	if creator == nil {
		return out, runretry.ErrUnavailable
	}
	id := uuid.NewString()
	_, e = c.controller.PrepareRollbackWithCommit(ctx, id, node.Key, c.owner, in.SourceID, in.ExpectedRevision, in.PlanID, func(ctx context.Context, plan kubernetes.ReleasePlan) (record kubernetes.ReleaseRecord, err error) {
		err = commit(ctx, func(tx context.Context) error {
			if previous, e := c.releases.LookupReleaseRollback(tx, in); e == nil {
				out = previous
				record = previous.Release
				return nil
			} else if !errors.Is(e, kubernetes.ErrNotFound) {
				return e
			}
			var e error
			record, e = c.releases.CreateRelease(tx, id, node.Key, c.owner, plan)
			if e != nil {
				return e
			}
			q := configstate.Queryer(tx, c.db)
			if e = creator(tx, q, runretry.ChildInput{ID: id, ProjectID: source.Plan.Authorization.Request.ProjectID, Trigger: snapshot.Trigger, CreatedAt: time.Now()}); e != nil {
				return e
			}
			if e = c.releases.BindReleaseRunReader(tx, record.ID, c.owner); e != nil {
				return e
			}
			record, e = c.releases.GetRelease(tx, record.ID)
			if e != nil {
				return e
			}
			snapshot.KubernetesReleasePolicies = map[string]kubernetes.ReleaseAuthorization{build.KubernetesBindingKey(node.ExecutionStageID, node.ExecutionJobID): plan.Authorization}
			snapshot.KubernetesRollbacks = map[string]string{node.Key: record.ID}
			if e = c.retries.SaveManifest(tx, id, snapshot); e != nil {
				return e
			}
			if e = c.releases.RecordReleaseRollbackRequest(tx, in, record); e != nil {
				return e
			}
			out = kubernetes.ReleaseRollbackStart{RunID: id, Release: record}
			return nil
		})
		return record, err
	})
	if e != nil {
		return kubernetes.ReleaseRollbackStart{}, e
	}
	return out, nil
}
