package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/huangchengsir/pipewright/internal/build"
	"github.com/huangchengsir/pipewright/internal/configstate"
	"github.com/huangchengsir/pipewright/internal/httpapi"
	"github.com/huangchengsir/pipewright/internal/kubernetes"
	"github.com/huangchengsir/pipewright/internal/notify"
	"github.com/huangchengsir/pipewright/internal/pipeline"
	"github.com/huangchengsir/pipewright/internal/pipelineyaml"
	"github.com/huangchengsir/pipewright/internal/previewenv"
	"github.com/huangchengsir/pipewright/internal/project"
	"github.com/huangchengsir/pipewright/internal/run"
	"github.com/huangchengsir/pipewright/internal/runner"
	"github.com/huangchengsir/pipewright/internal/runretry"
	"github.com/huangchengsir/pipewright/internal/target"
	"github.com/huangchengsir/pipewright/internal/tenancy"
	"github.com/huangchengsir/pipewright/internal/vault"
	"maps"
	"sort"
	"strconv"
	"strings"
)

// runAccessCompiler is controller wiring, never a request DTO or public tool.
// Preparation can perform a single source read. Approve only verifies and writes
// the resulting frozen manifest inside run admission's transaction.
type runAccessCompiler struct {
	db       *sql.DB
	projects interface {
		Get(context.Context, string) (*project.Project, error)
	}
	pipelines interface {
		Get(context.Context, string) (*pipeline.Config, error)
	}
	settings interface {
		Get(context.Context, string) (*pipeline.Settings, error)
	}
	runners interface {
		Get(context.Context, string) (*runner.Config, error)
	}
	targets interface {
		Get(context.Context, string) (*target.Server, error)
	}
	vault      vault.Vault
	source     httpapi.PipelineSnapshotReader
	recovery   *runretry.Service
	kubernetes *kubernetes.Service
	pacGlobal  bool
}
type preparedRunAccess struct {
	snapshot    runretry.Snapshot
	revision    int64
	trigger     string
	permissions []tenancy.ExecutionPermission
}

func triggerIdentity(t run.Trigger) string { data, _ := json.Marshal(t); return string(data) }
func (c *runAccessCompiler) Prepare(ctx context.Context, projectID string, trigger run.Trigger) (*run.PreparedExecution, error) {
	return c.prepare(ctx, projectID, trigger, false)
}

func (c *runAccessCompiler) prepare(ctx context.Context, projectID string, trigger run.Trigger, storedOnly bool) (*run.PreparedExecution, error) {
	if c.db == nil || c.projects == nil || c.pipelines == nil || c.settings == nil || c.source == nil || c.recovery == nil {
		return nil, tenancy.ErrForbidden
	}
	originalTrigger := triggerIdentity(trigger)
	preparation, err := tenancy.PrepareRunContext(ctx, c.db, projectID)
	if err != nil {
		return nil, err
	}
	// These reads may lazily create legacy defaults, so they precede the fixed
	// read transaction. Their ordinary write authorization remains in force.
	if _, err = c.pipelines.Get(preparation, projectID); err != nil {
		return nil, err
	}
	if _, err = c.settings.Get(preparation, projectID); err != nil {
		return nil, err
	}
	var proj *project.Project
	var cfg *pipeline.Config
	var settings *pipeline.Settings
	var revision int64
	err = configstate.WithReadTxContext(preparation, c.db, func(txctx context.Context, q configstate.DBTX) error {
		var err error
		revision, err = configstate.ReadRevision(txctx, q, projectID)
		if err != nil {
			return err
		}
		proj, err = c.projects.Get(txctx, projectID)
		if err != nil {
			return err
		}
		cfg, err = pipeline.LoadExisting(txctx, c.db, projectID)
		if err != nil {
			return err
		}
		settings, err = pipeline.LoadExistingSettings(txctx, c.db, projectID)
		return err
	})
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(trigger.Branch) == "" {
		trigger.Branch = proj.DefaultBranch
	}
	if trigger.Type == "" {
		trigger.Type = run.TriggerManual
	}
	definitions, err := run.NewParameterService(c.db).Get(preparation, projectID)
	if err != nil {
		return nil, err
	}
	resolved, err := run.ResolveParams(definitions.Defs, trigger.Params)
	if err != nil {
		return nil, err
	}
	defaults, defaultErr := run.ResolveParams(definitions.Defs, nil)
	if defaultErr != nil || !maps.Equal(resolved, defaults) {
		if err = tenancy.Check(preparation, c.db, "pipeline.edit", "project", projectID); err != nil {
			return nil, err
		}
	}
	trigger.Params = resolved
	// Caller-supplied targets must be exactly the saved environment defaults, or
	// require configuration authority. Environment execution is checked separately.
	expectedTargets := map[string]bool{}
	for _, env := range settings.Environments {
		if env.Name == trigger.ResolvedEnvironment {
			for _, id := range env.TargetServerIDs {
				expectedTargets[id] = true
			}
		}
	}
	suppliedTargets := map[string]bool{}
	for _, id := range trigger.ResolvedTargetServerIDs {
		suppliedTargets[id] = true
	}
	if len(suppliedTargets) > 0 && !maps.Equal(expectedTargets, suppliedTargets) {
		if err = tenancy.Check(preparation, c.db, "pipeline.edit", "project", projectID); err != nil {
			return nil, err
		}
	}
	if err = tenancy.Check(preparation, c.db, "credential.use", "credential", proj.CredentialID); err != nil {
		return nil, err
	}
	auth, release, err := vault.AcquireGitAuth(preparation, c.vault, proj.CredentialID)
	if err != nil {
		return nil, err
	}
	var source httpapi.PipelineSourceSnapshot
	var sourceErr error
	if storedOnly {
		if proj.PacEnabled || c.pacGlobal {
			release()
			return nil, tenancy.ErrConflict
		}
		source.CommitSHA, sourceErr = c.source.PipelineBranchCommit(preparation, proj.RepoURL, auth.Username, auth.Token, trigger.Branch)
	} else {
		source, sourceErr = c.source.PipelineSnapshot(preparation, proj.RepoURL, auth.Username, auth.Token, trigger.Branch)
	}
	release()
	auth.Username = ""
	auth.Token = ""
	if sourceErr != nil {
		return nil, sourceErr
	}
	if len(source.CommitSHA) != 40 {
		return nil, tenancy.ErrInvalidInput
	}
	if trigger.Commit != "" && !strings.HasPrefix(strings.ToLower(source.CommitSHA), strings.ToLower(trigger.Commit)) {
		return nil, tenancy.ErrConflict
	}
	configSource := runretry.ConfigSource{Kind: "stored", Ref: source.CommitSHA, Fallback: "disabled", HistoryVersionID: cfg.HistoryVersionID}
	if proj.PacEnabled || c.pacGlobal {
		configSource.Fallback = "no_file"
		if source.Found {
			parsed, parseErr := pipelineyaml.Parse([]byte(source.YAML))
			if parseErr == nil {
				cfg = &parsed
				configSource = runretry.ConfigSource{Kind: "repo", Ref: source.CommitSHA, File: ".pipewright.yml"}
			} else {
				configSource.Fallback = "invalid_yaml"
			}
		}
	}
	projectMap, err := snapshotMap(proj)
	if err != nil {
		return nil, err
	}
	settingsMap, err := snapshotMap(settings)
	if err != nil {
		return nil, err
	}
	snapshot := runretry.Snapshot{SchemaVersion: runretry.SchemaVersion, ScriptEntrypointMode: runretry.ScriptEntrypointClearV1, ProjectID: projectID, Project: projectMap, Settings: settingsMap, Spec: cfg.Spec, CommitSHA: source.CommitSHA, RequestedRef: trigger.Branch, Source: configSource, RunnerRefs: map[string]string{}, Trigger: runretry.Trigger{Type: trigger.Type, Branch: trigger.Branch, Commit: trigger.Commit, Actor: trigger.Actor, Environment: trigger.ResolvedEnvironment, TargetServerIDs: trigger.ResolvedTargetServerIDs, Params: trigger.Params, ChainSourceRunID: trigger.ChainSourceRunID, ChainDepth: trigger.ChainDepth}}
	if c.runners != nil {
		remote, e := c.runners.Get(preparation, projectID)
		if e != nil {
			return nil, e
		}
		snapshot.RunnerRefs["serverId"] = remote.RunnerServerID
	}
	permissions, err := c.planPermissions(preparation, proj, settings, &snapshot, trigger)
	if err != nil {
		return nil, err
	}
	if err = build.FreezeKubernetesPolicy(preparation, c.kubernetes, &snapshot); err != nil {
		return nil, err
	}
	snapshot, err = runretry.FreezeSnapshot(snapshot)
	if err != nil {
		return nil, err
	}
	return &run.PreparedExecution{Value: preparedRunAccess{snapshot: snapshot, revision: revision, trigger: originalTrigger, permissions: permissions}}, nil
}
func (c *runAccessCompiler) Approve(ctx context.Context, runID, projectID string, trigger run.Trigger, prepared *run.PreparedExecution) (string, []tenancy.ExecutionPermission, error) {
	if prepared == nil {
		return "", nil, tenancy.ErrForbidden
	}
	value, ok := prepared.Value.(preparedRunAccess)
	if !ok || value.snapshot.ProjectID != projectID || value.trigger != triggerIdentity(trigger) {
		return "", nil, tenancy.ErrConflict
	}
	revision, err := configstate.ReadRevision(ctx, configstate.Queryer(ctx, c.db), projectID)
	if err != nil || revision != value.revision {
		return "", nil, tenancy.ErrConflict
	}
	if err = c.recovery.SaveManifest(ctx, runID, value.snapshot); err != nil {
		return "", nil, err
	}
	digest, err := runretry.SnapshotDigest(value.snapshot)
	return digest, value.permissions, err
}
func (c *runAccessCompiler) Digest(ctx context.Context, runID string) (string, error) {
	snapshot, err := c.recovery.FrozenSnapshotForRelease(ctx, runID)
	if err != nil {
		return "", err
	}
	return runretry.SnapshotDigest(snapshot)
}
func (c *runAccessCompiler) Verify(ctx context.Context, runID string) error {
	if err := tenancy.CheckRunBoundary(ctx, c.db, runID); err != nil {
		return err
	}
	return c.VerifyPrepared(ctx, runID)
}

// VerifyPrepared rechecks saved execution input revisions for a legacy
// single-administrator product run, and also supplements multi-tenant grants.
func (c *runAccessCompiler) VerifyPrepared(ctx context.Context, runID string) error {
	snapshot, err := c.recovery.FrozenSnapshotForRelease(ctx, runID)
	if err != nil {
		return err
	}
	// IDs alone do not freeze an execution target: host/user/credential changes
	// after approval must never redirect an already-approved command.
	for key, expected := range snapshot.RunnerRefs {
		if strings.HasPrefix(key, "product-target/") {
			t, ok := c.targets.(interface {
				Capture(context.Context, string) (target.ConnectionSnapshot, error)
			})
			if !ok {
				return tenancy.ErrForbidden
			}
			server, err := t.Capture(ctx, strings.TrimPrefix(key, "product-target/"))
			if err != nil || server.Fingerprint() != expected {
				return tenancy.ErrConflict
			}
			continue
		}
		if strings.HasPrefix(key, "product-epoch/") {
			name := strings.TrimPrefix(key, "product-epoch/")
			switch name {
			case configstate.EpochPolicy, configstate.EpochSecrets, configstate.EpochExecutor:
			default:
				return tenancy.ErrConflict
			}
			current, e := configstate.ReadEpoch(ctx, configstate.Queryer(ctx, c.db), name)
			if e != nil || strconv.FormatInt(current, 10) != expected {
				return tenancy.ErrConflict
			}
			continue
		}
		if strings.HasPrefix(key, "preview/") {
			version, e := previewenv.ExecutionVersion(ctx, c.db, strings.TrimPrefix(key, "preview/"))
			if e != nil {
				return e
			}
			if version != expected {
				return tenancy.ErrConflict
			}
			continue
		}
		if strings.HasPrefix(key, "notification/") {
			version, e := notify.DeliveryVersion(ctx, c.db, strings.TrimPrefix(key, "notification/"))
			if e != nil {
				return e
			}
			if version != expected {
				return tenancy.ErrConflict
			}
			continue
		}
		if !strings.HasPrefix(key, "server/") {
			continue
		}
		if c.targets == nil {
			return tenancy.ErrForbidden
		}
		actual, err := c.targets.Get(ctx, strings.TrimPrefix(key, "server/"))
		if err != nil {
			return err
		}
		if bindingIdentity(actual) != expected {
			return tenancy.ErrConflict
		}
	}
	return nil
}

func (c *runAccessCompiler) planPermissions(ctx context.Context, proj *project.Project, settings *pipeline.Settings, snap *runretry.Snapshot, trigger run.Trigger) ([]tenancy.ExecutionPermission, error) {
	set := map[string]tenancy.ExecutionPermission{}
	add := func(action, id, environment string) {
		if id != "" {
			p := tenancy.ExecutionPermission{Action: action, ResourceID: id, EnvironmentID: environment}
			set[action+"/"+id+"/"+environment] = p
		}
	}
	add("credential.use", proj.CredentialID, "")
	active := *settings
	active.Environments = nil
	for _, env := range settings.Environments {
		if env.Name == trigger.ResolvedEnvironment {
			active.Environments = append(active.Environments, env)
		}
	}
	refs := struct {
		Settings pipeline.Settings
		Spec     pipeline.Spec
		Runner   map[string]string
	}{active, snap.Spec, snap.RunnerRefs}
	encoded, err := json.Marshal(refs)
	if err != nil {
		return nil, err
	}
	var values any
	if err = json.Unmarshal(encoded, &values); err != nil {
		return nil, err
	}
	var walk func(any) error
	walk = func(value any) error {
		switch item := value.(type) {
		case map[string]any:
			for key, value := range item {
				lower := strings.ToLower(key)
				if text, ok := value.(string); ok {
					switch lower {
					case "credentialid", "registrycredentialid", "gitcredentialid":
						if strings.HasPrefix(text, "kcred_") {
							add("kubernetes_credential.use", text, "")
						} else {
							add("credential.use", text, "")
						}
					case "serverid", "runnerserverid":
						add("server.use", text, "")
					case "connectionid":
						add("kubernetes.use", text, "")
					case "serverids", "targetserverids":
						for _, id := range strings.Split(text, ",") {
							add("server.use", strings.TrimSpace(id), "")
						}
					case "executor":
						var nested any
						if json.Unmarshal([]byte(text), &nested) != nil {
							return tenancy.ErrInvalidInput
						}
						if err := walk(nested); err != nil {
							return err
						}
					}
				}
				if lower == "serverids" || lower == "targetserverids" {
					if ids, ok := value.([]any); ok {
						for _, id := range ids {
							if text, ok := id.(string); ok {
								add("server.use", text, "")
							}
						}
					}
				}
				if err := walk(value); err != nil {
					return err
				}
			}
		case []any:
			for _, child := range item {
				if err := walk(child); err != nil {
					return err
				}
			}
		}
		return nil
	}

	if err = walk(values); err != nil {
		return nil, err
	}
	for _, id := range trigger.ResolvedTargetServerIDs {
		add("server.use", id, "")
	}
	deploy := false
	for _, stage := range snap.Spec.Stages {
		if !stage.When.Matches(trigger.Branch, trigger.Type) {
			continue
		}
		for _, job := range stage.Jobs {
			if job.Type == "notify" || stage.Kind == pipeline.KindNotify {
				if ref, ok := job.Config["channel"].(string); ok && strings.TrimSpace(ref) != "" {
					id, _, e := notify.ResolveAllowedChannel(ctx, c.db, ref)
					if e != nil {
						return nil, e
					}
					add("notification.send", id, "")
					version, e := notify.DeliveryVersion(ctx, c.db, id)
					if e != nil {
						return nil, e
					}
					snap.RunnerRefs["notification/"+id] = version
				}
			}
			if stage.Kind == pipeline.KindDeploy || strings.HasPrefix(job.Type, "deploy_") {
				deploy = true
			}
		}
	}
	if deploy {
		if trigger.ResolvedEnvironment == "" {
			return nil, tenancy.ErrInvalidInput
		}
		enabled, e := tenancy.Enabled(ctx, c.db)
		if e != nil {
			return nil, e
		}
		var env string
		if enabled {
			if err = configstate.Queryer(ctx, c.db).QueryRowContext(ctx, `SELECT id FROM access_environments WHERE project_id=? AND name=?`, proj.ID, trigger.ResolvedEnvironment).Scan(&env); err != nil {
				return nil, tenancy.ErrForbidden
			}
			add("deployment.execute", env, env)
		}
		for _, stage := range snap.Spec.Stages {
			for _, job := range stage.Jobs {
				if job.Type == pipeline.KubernetesJobType {
					if connection, ok := job.Config["connectionId"].(string); ok {
						add("kubernetes.deploy", connection, env)
					}
				}
			}
		}
	}
	if deploy && previewenv.PRNumber(trigger.Branch) > 0 {
		inputs, e := previewenv.PlanInputs(ctx, c.db, proj.ID, previewenv.PRNumber(trigger.Branch))
		if e != nil {
			return nil, e
		}
		snap.RunnerRefs["preview/"+proj.ID] = inputs.Version
		if inputs.Enabled {
			add("preview.provision", proj.ID, "")
			add("dns.use", inputs.ProviderID, "")
			add("credential.use", inputs.CredentialID, "")
			add("preview.reclaim", inputs.ExistingPreviewID, "")
			for _, serverID := range trigger.ResolvedTargetServerIDs {
				add("server.use", serverID, "")
				add("proxy.read", serverID, "")
				add("proxy.manage", serverID, "")
				add("proxy.install", serverID, "")
			}
		}
	}
	for _, permission := range set {
		if permission.Action == "server.use" {
			if err = tenancy.Check(ctx, c.db, "server.use", "server", permission.ResourceID); err != nil {
				return nil, err
			}
			if c.targets == nil {
				return nil, tenancy.ErrForbidden
			}
			server, e := c.targets.Get(ctx, permission.ResourceID)
			if e != nil {
				return nil, e
			}
			snap.RunnerRefs["server/"+permission.ResourceID] = bindingIdentity(server)
			add("credential.use", server.CredentialID, "")
		}
	}
	keys := make([]string, 0, len(set))
	for key := range set {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	out := make([]tenancy.ExecutionPermission, 0, len(keys))
	for _, key := range keys {
		permission := set[key]
		if permission.EnvironmentID == "" {
			kind := strings.TrimSuffix(permission.Action, ".use")
			if kind == "server" || kind == "credential" || kind == "kubernetes" || kind == "kubernetes_credential" {
				if err = tenancy.Check(ctx, c.db, permission.Action, kind, permission.ResourceID); err != nil {
					return nil, err
				}
			}
		}
		out = append(out, permission)
	}
	if len(out) > 255 {
		return nil, errors.New("execution permission limit")
	}
	return out, nil
}
