package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"sort"
	"strings"

	"github.com/huangchengsir/pipewright/internal/artifactstore"
	"github.com/huangchengsir/pipewright/internal/configstate"
	"github.com/huangchengsir/pipewright/internal/dagrun"
	"github.com/huangchengsir/pipewright/internal/pipeline"
	"github.com/huangchengsir/pipewright/internal/project"
	"github.com/huangchengsir/pipewright/internal/run"
	"github.com/huangchengsir/pipewright/internal/runner"
	"github.com/huangchengsir/pipewright/internal/runretry"
	"github.com/huangchengsir/pipewright/internal/target"
)

type nodeRetryBindings struct {
	db               *sql.DB
	runs             run.Service
	projects         project.Service
	settings         pipeline.SettingsService
	targets          target.Service
	runners          runner.Service
	loader           dagrun.SpecLoader
	artifacts        *artifactstore.Store
	real             bool
	imageVerifier    func(context.Context, runretry.ImageRef) error
	releaseImage     func(context.Context, runretry.ImageRef) error
	targetVerifier   runretry.TargetVerifier
	resourceVerifier runretry.ResourceVerifier
}
type retryArtifactBlobs struct{ bindings *nodeRetryBindings }

func (b retryArtifactBlobs) Open(key string) (io.ReadCloser, error) {
	if b.bindings.artifacts == nil {
		return nil, runretry.ErrUnavailable
	}
	return b.bindings.artifacts.Open(key)
}
func newNodeRetryService(db *sql.DB, sealer runretry.Sealer, b *nodeRetryBindings) *runretry.Service {
	child, dispatch := run.RetryAdapters(b.runs)
	return runretry.New(db, sealer, runretry.WithChildCreator(child), runretry.WithDispatch(dispatch),
		runretry.WithRejectedChild(run.RejectRetryQueued(b.runs)),
		runretry.WithSnapshotProvider(b.capture), runretry.WithBlobStore(retryArtifactBlobs{b}),
		runretry.WithPreflightCheck(b.preflight),
		runretry.WithAdmissionCheck(func(context.Context, configstate.DBTX, runretry.Snapshot) error {
			if !b.real {
				return runretry.ErrUnavailable
			}
			return nil
		}),
		runretry.WithTargetVerifier(func(ctx context.Context, ref runretry.TargetOperation) (string, runretry.TargetProof, error) {
			if b.targetVerifier == nil {
				return "", runretry.TargetProof{}, runretry.ErrUnavailable
			}
			return b.targetVerifier(ctx, ref)
		}),
		runretry.WithResourceVerifier(func(ctx context.Context, ref runretry.ResourceRef) (bool, error) {
			if b.resourceVerifier == nil {
				return false, runretry.ErrUnavailable
			}
			return b.resourceVerifier(ctx, ref)
		}),
		runretry.WithImageVerifier(func(ctx context.Context, image runretry.ImageRef) error {
			if b.imageVerifier == nil {
				return runretry.ErrUnavailable
			}
			return b.imageVerifier(ctx, image)
		}))
}
func snapshotMap(v any) (map[string]any, error) {
	data, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	var out map[string]any
	err = json.Unmarshal(data, &out)
	return out, err
}
func collectReferenceIDs(v any, credentials, servers map[string]bool) {
	switch value := v.(type) {
	case map[string]any:
		for key, item := range value {
			k := strings.ToLower(key)
			if text, ok := item.(string); ok && text != "" {
				if k == "credentialid" {
					credentials[text] = true
				}
				if k == "serverid" || k == "runnerserverid" {
					servers[text] = true
				}
				if k == "serverids" || k == "targetserverids" {
					for _, id := range strings.Split(text, ",") {
						if id = strings.TrimSpace(id); id != "" {
							servers[id] = true
						}
					}
				}
			}
			if k == "serverids" || k == "targetserverids" {
				if list, ok := item.([]any); ok {
					for _, id := range list {
						if text, ok := id.(string); ok && text != "" {
							servers[text] = true
						}
					}
				}
			}
			collectReferenceIDs(item, credentials, servers)
		}
	case []any:
		for _, item := range value {
			collectReferenceIDs(item, credentials, servers)
		}
	}
}
func referenceIDs(v any) (map[string]bool, map[string]bool, error) {
	mapped, err := snapshotMap(struct{ Value any }{v})
	if err != nil {
		return nil, nil, err
	}
	credentials, servers := map[string]bool{}, map[string]bool{}
	collectReferenceIDs(mapped, credentials, servers)
	return credentials, servers, nil
}
func bindingIdentity(server *target.Server) string {
	data, _ := json.Marshal(struct {
		Host               string
		Port               int
		User, CredentialID string
	}{server.Host, server.Port, server.User, server.CredentialID})
	return string(data)
}
func (b *nodeRetryBindings) capture(ctx context.Context, id string) (runretry.Snapshot, error) {
	if !b.real || b.loader == nil || b.runners == nil || b.targets == nil {
		return runretry.Snapshot{}, runretry.ErrUnavailable
	}
	r, err := b.runs.Get(ctx, id)
	if err != nil {
		return runretry.Snapshot{}, err
	}
	cfg, source, err := b.loader.Get(ctx, r.ProjectID, r.Trigger.Branch)
	if err != nil {
		return runretry.Snapshot{}, err
	}
	p, err := b.projects.Get(ctx, r.ProjectID)
	if err != nil {
		return runretry.Snapshot{}, err
	}
	settings, err := b.settings.Get(ctx, r.ProjectID)
	if err != nil {
		return runretry.Snapshot{}, err
	}
	projectMap, err := snapshotMap(p)
	if err != nil {
		return runretry.Snapshot{}, err
	}
	settingsMap, err := snapshotMap(settings)
	if err != nil {
		return runretry.Snapshot{}, err
	}
	config, err := b.runners.Get(ctx, r.ProjectID)
	if err != nil {
		return runretry.Snapshot{}, err
	}
	kind := "stored"
	if source.FromRepo {
		kind = "repo"
	}
	snap := runretry.Snapshot{SchemaVersion: runretry.SchemaVersion, ProjectID: r.ProjectID, Spec: cfg.Spec,
		Project: projectMap, Settings: settingsMap, RequestedRef: r.Trigger.Branch,
		Trigger: runretry.Trigger{Type: r.Trigger.Type, Branch: r.Trigger.Branch, Commit: r.Trigger.Commit, Actor: r.Trigger.Actor,
			Environment: r.Trigger.ResolvedEnvironment, TargetServerIDs: r.Trigger.ResolvedTargetServerIDs, Params: r.Trigger.Params,
			ChainSourceRunID: r.Trigger.ChainSourceRunID, ChainDepth: r.Trigger.ChainDepth},
		Source:     runretry.ConfigSource{Kind: kind, Ref: source.Ref, File: source.File, Fallback: source.FallbackReason},
		RunnerRefs: map[string]string{"serverId": config.RunnerServerID}}
	if len(r.ResolvedCommitSHA) == 40 && !r.ResolvedCommitConflict {
		snap.CommitSHA = r.ResolvedCommitSHA
	}
	credentials, servers, err := referenceIDs(struct {
		Project  *project.Project
		Settings *pipeline.Settings
		Spec     pipeline.Spec
	}{p, settings, cfg.Spec})
	if err != nil {
		return runretry.Snapshot{}, err
	}
	for _, server := range r.Trigger.ResolvedTargetServerIDs {
		servers[server] = true
	}
	if config.RunnerServerID != "" {
		servers[config.RunnerServerID] = true
	}
	for _, server := range sortedReferenceIDs(servers) {
		actual, err := b.targets.Get(ctx, server)
		if errors.Is(err, target.ErrNotFound) {
			snap.RunnerRefs["server/"+server] = "missing"
			continue
		}
		if err != nil {
			return runretry.Snapshot{}, err
		}
		snap.RunnerRefs["server/"+server] = bindingIdentity(actual)
		if actual.CredentialID != "" {
			credentials[actual.CredentialID] = true
		}
	}
	q := configstate.Queryer(ctx, b.db)
	for _, id := range sortedReferenceIDs(credentials) {
		var kind string
		err := q.QueryRowContext(ctx, "SELECT type FROM credentials WHERE id=?", id).Scan(&kind)
		if errors.Is(err, sql.ErrNoRows) {
			snap.RunnerRefs["credential/"+id] = "missing"
			continue
		}
		if err != nil {
			return runretry.Snapshot{}, err
		}
		snap.RunnerRefs["credential/"+id] = kind
	}
	return snap, nil
}
func sortedReferenceIDs(set map[string]bool) []string {
	ids := make([]string, 0, len(set))
	for id := range set {
		if id != "" {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	return ids
}
func (b *nodeRetryBindings) preflight(ctx context.Context, snap runretry.Snapshot, plan *runretry.Plan) ([]runretry.Reason, error) {
	if !b.real {
		return []runretry.Reason{{Code: "executor_unavailable"}}, nil
	}
	var settings pipeline.Settings
	data, err := json.Marshal(snap.Settings)
	if err != nil {
		return nil, runretry.ErrUnavailable
	}
	if json.Unmarshal(data, &settings) != nil {
		return nil, runretry.ErrUnavailable
	}
	credentials, servers := map[string]bool{}, map[string]bool{}
	active := false
	deploying := false
	for _, n := range plan.Nodes {
		if n.Decision.Action != "execute" && n.Decision.Action != "control" {
			continue
		}
		active = true
		if n.Node.Type == "deploy_ssh" || n.Node.Type == "deploy_frontend" {
			deploying = true
		}
		if snap.Compiled == nil {
			return []runretry.Reason{{Code: "snapshot_unavailable"}}, nil
		}
		for _, stage := range snap.Compiled.Stages {
			if stage.ID != n.Node.ExecutionStageID {
				continue
			}
			if n.Node.ControlKey != "" {
				c, s, e := referenceIDs(struct {
					Services []pipeline.ServiceSpec
					Post     []pipeline.PostStep
				}{stage.Services, stage.Post})
				if e != nil {
					return nil, e
				}
				for id := range c {
					credentials[id] = true
				}
				for id := range s {
					servers[id] = true
				}
			} else {
				for _, job := range stage.Jobs {
					if job.ID == n.Node.ExecutionJobID {
						c, s, e := referenceIDs(job.Config)
						if e != nil {
							return nil, e
						}
						for id := range c {
							credentials[id] = true
						}
						for id := range s {
							servers[id] = true
						}
					}
				}
			}
		}
	}
	if !active {
		return nil, nil
	}
	if id, ok := snap.Project["CredentialID"].(string); ok && id != "" {
		credentials[id] = true
	}
	c, _, err := referenceIDs(settings.Build)
	if err != nil {
		return nil, err
	}
	for id := range c {
		credentials[id] = true
	}
	for _, environment := range settings.Environments {
		if environment.Name != snap.Trigger.Environment {
			continue
		}
		c, _, err := referenceIDs(environment)
		if err != nil {
			return nil, err
		}
		for id := range c {
			credentials[id] = true
		}
		if deploying {
			for _, id := range environment.TargetServerIDs {
				servers[id] = true
			}
		}
	}
	if deploying {
		for _, id := range snap.Trigger.TargetServerIDs {
			servers[id] = true
		}
	}
	if id := snap.RunnerRefs["serverId"]; id != "" {
		servers[id] = true
	}
	q := configstate.Queryer(ctx, b.db)
	codes := map[string]bool{}
	for _, id := range sortedReferenceIDs(servers) {
		actual := &target.Server{ID: id}
		err := q.QueryRowContext(ctx, "SELECT host,port,user,credential_id FROM servers WHERE id=?", id).Scan(&actual.Host, &actual.Port, &actual.User, &actual.CredentialID)
		if errors.Is(err, sql.ErrNoRows) {
			codes["target_missing"] = true
			continue
		}
		if err != nil {
			return nil, err
		}
		frozen := snap.RunnerRefs["server/"+id]
		if frozen == "" || frozen == "missing" {
			codes["binding_evidence_missing"] = true
		} else if frozen != bindingIdentity(actual) {
			codes["target_binding_changed"] = true
		}
		if actual.CredentialID != "" {
			credentials[actual.CredentialID] = true
		}
	}
	for _, id := range sortedReferenceIDs(credentials) {
		var current string
		err := q.QueryRowContext(ctx, "SELECT type FROM credentials WHERE id=?", id).Scan(&current)
		if errors.Is(err, sql.ErrNoRows) {
			codes["credential_missing"] = true
			continue
		}
		if err != nil {
			return nil, err
		}
		frozen := snap.RunnerRefs["credential/"+id]
		if frozen == "" || frozen == "missing" {
			codes["binding_evidence_missing"] = true
		} else if frozen != current {
			codes["credential_type_changed"] = true
		}
	}
	reasons := []runretry.Reason{}
	for _, code := range sortedReferenceIDs(codes) {
		reasons = append(reasons, runretry.Reason{Code: code})
	}
	return reasons, nil
}
