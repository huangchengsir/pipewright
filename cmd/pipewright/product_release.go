package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/huangchengsir/pipewright/internal/configstate"
	"github.com/huangchengsir/pipewright/internal/dagrun"
	"github.com/huangchengsir/pipewright/internal/deployattempt"
	"github.com/huangchengsir/pipewright/internal/pipeline"
	"github.com/huangchengsir/pipewright/internal/productrelease"
	"github.com/huangchengsir/pipewright/internal/project"
	"github.com/huangchengsir/pipewright/internal/run"
	"github.com/huangchengsir/pipewright/internal/runretry"
	"github.com/huangchengsir/pipewright/internal/store"
	"github.com/huangchengsir/pipewright/internal/target"
	"github.com/huangchengsir/pipewright/internal/tenancy"
	"github.com/huangchengsir/pipewright/internal/workspace"
)

type productReleaseController struct {
	db        *sql.DB
	workspace *workspace.Service
	compiler  *runAccessCompiler
	runs      run.Service
	targets   target.LimitedExecutor
	attempts  *deployattempt.Store
	ready     bool
}

type productReleaseSnapshot struct {
	Principal          productrelease.Principal
	ProductID          string
	PlanID             string
	PlanIndex          int
	PlanCount          int
	CreatedAt          int64
	Plan               productrelease.ItemPlan
	Snapshot           runretry.Snapshot
	Permissions        []tenancy.ExecutionPermission
	Revision           int64
	AssignmentRevision int64
	StageID            string
	Targets            map[string]string
}

const snapshotDomain = "pipewright-product-release-preview-v1\n"
const productPreviewLifetime = 30 * time.Minute

func (c *productReleaseController) Principal(ctx context.Context) (productrelease.Principal, error) {
	p, on, err := tenancy.CurrentPrincipal(ctx, c.db)
	if err != nil {
		return productrelease.Principal{}, err
	}
	if on {
		if p.ID == "" || p.TenantID == "" {
			return productrelease.Principal{}, tenancy.ErrForbidden
		}
		return productrelease.Principal{TenantID: p.TenantID, ActorID: p.ID}, nil
	}
	if !productrelease.HasLegacySession(ctx) {
		return productrelease.Principal{}, tenancy.ErrForbidden
	}
	return productrelease.Principal{TenantID: "__single__", ActorID: "instance-owner"}, nil
}

func (c *productReleaseController) Authorize(ctx context.Context, product string, items []productrelease.ItemPlan, dispatch bool) error {
	if dispatch {
		if !c.ready {
			return productrelease.ErrUnavailable
		}
		if q := configstate.Queryer(ctx, c.db); q != c.db {
			if err := configstate.CheckMaintenanceAdmission(ctx, q); err != nil {
				return err
			}
			if err := tenancy.LockAuthorization(ctx, c.db); err != nil {
				return err
			}
			// A legacy instance still needs a common lock with preview cleanup.
			if err := configstate.LockEpochs(ctx, q, c.db, configstate.EpochPolicy, configstate.EpochSecrets, configstate.EpochExecutor); err != nil {
				return err
			}
		}
	}
	if _, err := c.workspace.GetProduct(ctx, product); err != nil {
		return releaseResourceError(err)
	}
	for n, i := range items {
		if !dispatch {
			if err := tenancy.Check(ctx, c.db, "project.read", "project", i.ApplicationID); err != nil {
				return err
			}
			if err := tenancy.CheckEnvironmentName(ctx, c.db, "environment.read", i.ApplicationID, i.Environment); err != nil {
				return err
			}
			for _, id := range i.TargetIDs {
				if err := tenancy.Check(ctx, c.db, "server.inspect", "server", id); err != nil {
					return err
				}
			}
			continue
		}
		frozen, err := c.loadSnapshot(ctx, i)
		if err != nil {
			return err
		}
		if frozen.ProductID != product || frozen.PlanID != items[0].SnapshotRef || frozen.PlanIndex != n || frozen.PlanCount != len(items) {
			return productrelease.ErrConflict
		}
		app, err := c.application(ctx, product, i.ApplicationID)
		if err != nil {
			return err
		}
		if app.Revision != frozen.AssignmentRevision {
			return productrelease.ErrConflict
		}
		if err = c.authorizeDeployment(ctx, i.ApplicationID, i.Environment); err != nil {
			return err
		}
	}
	return nil
}

func releaseResourceError(err error) error {
	if errors.Is(err, workspace.ErrNotFound) || errors.Is(err, project.ErrNotFound) {
		return productrelease.ErrNotFound
	}
	return err
}

func (c *productReleaseController) application(ctx context.Context, product, id string) (workspace.Application, error) {
	items, err := c.workspace.ListApplications(ctx, workspace.ApplicationFilter{ProductID: product, ApplicationID: id, IdentityOnly: true, PageSize: 1})
	if err != nil {
		return workspace.Application{}, releaseResourceError(err)
	}
	if items.Total != 1 || len(items.Items) != 1 {
		return workspace.Application{}, productrelease.ErrNotFound
	}
	return items.Items[0], nil
}

func (c *productReleaseController) authorizeDeployment(ctx context.Context, app, env string) error {
	if err := run.AuthorizeManualStart(ctx, c.runs, app); err != nil {
		return err
	}
	if err := tenancy.CheckEnvironmentName(ctx, c.db, "environment.read", app, env); err != nil {
		return err
	}
	return tenancy.CheckEnvironmentName(ctx, c.db, "deployment.execute", app, env)
}

func (c *productReleaseController) authorizedTarget(ctx context.Context, id string) (*target.Server, error) {
	if err := tenancy.Check(ctx, c.db, "server.use", "server", id); err != nil {
		return nil, err
	}
	server, err := c.compiler.targets.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if err = tenancy.Check(ctx, c.db, "credential.use", "credential", server.CredentialID); err != nil {
		return nil, err
	}
	return server, nil
}
func configuredEnvironment(settings *pipeline.Settings, name string) (*pipeline.Environment, error) {
	for i := range settings.Environments {
		env := &settings.Environments[i]
		if env.Name == name && len(env.TargetServerIDs) > 0 {
			return env, nil
		}
	}
	return nil, productrelease.ErrUnsupported
}

func (c *productReleaseController) Options(ctx context.Context, product string, page int) (productrelease.Options, error) {
	out := productrelease.Options{Applications: []productrelease.ApplicationOption{}}
	apps, err := c.workspace.ListApplications(ctx, workspace.ApplicationFilter{ProductID: product, IdentityOnly: true, Page: page, PageSize: 50})
	if err != nil {
		return out, releaseResourceError(err)
	}
	out.HasMore = apps.Page*apps.PageSize < apps.Total
	for _, a := range apps.Items {
		item := productrelease.ApplicationOption{ApplicationID: a.Project.ID, Name: a.Project.Name, DefaultBranch: a.Project.DefaultBranch, Environments: []productrelease.EnvironmentOption{}, Reason: "no_verifiable_deployment"}
		if c.ready && !c.compiler.pacGlobal && !a.Project.PacEnabled &&
			tenancy.Check(ctx, c.db, "credential.use", "credential", a.Project.CredentialID) == nil {
			prep, err := tenancy.PrepareRunContext(ctx, c.db, a.Project.ID)
			if err == nil {
				cfg, e := pipeline.LoadExisting(prep, c.db, a.Project.ID)
				settings, se := pipeline.LoadExistingSettings(prep, c.db, a.Project.ID)
				if e == nil && se == nil {
					for _, env := range settings.Environments {
						// Branch-conditional DAGs are checked again for the
						// exact branch at preview and dispatch.
						if !canPreviewBranch(cfg.Spec, a.Project.DefaultBranch, env.TargetServerIDs) {
							continue
						}
						if c.authorizeDeployment(ctx, a.Project.ID, env.Name) != nil {
							continue
						}
						opt := productrelease.EnvironmentOption{Name: env.Name, TargetIDs: slices.Clone(env.TargetServerIDs)}
						for _, id := range opt.TargetIDs {
							srv, e := c.authorizedTarget(ctx, id)
							if e != nil {
								opt.Targets = nil
								break
							}
							opt.Targets = append(opt.Targets, productrelease.TargetOption{ID: id, Name: srv.Name})
						}
						if len(opt.Targets) == len(opt.TargetIDs) {
							item.Environments = append(item.Environments, opt)
						}
					}
				}
			}
		} else if !c.ready {
			item.Reason = "runtime_unavailable"
		}
		if len(item.Environments) > 0 {
			item.CanRelease = true
			item.Reason = ""
			out.CanRelease = true
		}
		out.Applications = append(out.Applications, item)
	}
	return out, nil
}

type productReleaseStage struct {
	deployIndex int
	deployID    string
	pushStage   string
	pushJob     string
}

// Supports only the regular registry image path with one uniquely verifiable
// deployment stage; unknown, scripted, K8s and multi-stage deployments retain
// their existing single-application entry point.
func releaseStage(spec pipeline.Spec, branch string, targets []string) (productReleaseStage, error) {
	if len(targets) == 0 || len(targets) > 100 || !dagrun.HasFixedGitSource(spec.Stages) {
		return productReleaseStage{}, productrelease.ErrUnsupported
	}
	set := map[string]bool{}
	for _, id := range targets {
		if id == "" || id != strings.TrimSpace(id) || set[id] {
			return productReleaseStage{}, productrelease.ErrUnsupported
		}
		set[id] = true
	}
	stageID, buildStage, pushStage, buildJob, pushJob := "", "", "", "", ""
	deployIndex := -1
	var pushInputs []string
	source := false
	for index, stage := range spec.Stages {
		if !stage.When.Matches(branch, run.TriggerManual) {
			continue
		}
		if stage.AllowFailure || len(stage.Matrix) != 0 || len(stage.Post) != 0 || len(stage.Services) != 0 {
			return productReleaseStage{}, productrelease.ErrUnsupported
		}
		for _, job := range stage.Jobs {
			switch job.Type {
			case "git_source":
				source = true
			case "build_image":
				if buildStage != "" {
					return productReleaseStage{}, productrelease.ErrUnsupported
				}
				buildStage, buildJob = stage.ID, job.ID
			case "push_image":
				if pushStage != "" {
					return productReleaseStage{}, productrelease.ErrUnsupported
				}
				pushStage, pushJob, pushInputs = stage.ID, job.ID, job.Needs
			case "deploy_ssh":
				if stage.Kind != pipeline.KindDeploy || stageID != "" || len(stage.Jobs) != 1 || job.Config["artifactType"] != "image" {
					return productReleaseStage{}, productrelease.ErrUnsupported
				}
				if local := job.Config["localDocker"]; local != nil && local != false && local != "" && local != "false" && local != "0" {
					return productReleaseStage{}, productrelease.ErrUnsupported
				}
				for _, key := range []string{"serverId", "restartCommand", "artifactSource", "runArgs", "localDockerPrepareCommand", "localDockerBuildContext", "localDockerDockerfile", "localDockerBuildArgs", "localDockerRunArgs"} {
					if value, ok := job.Config[key]; ok && value != nil && value != "" {
						return productReleaseStage{}, productrelease.ErrUnsupported
					}
				}
				if strategy := job.Config["strategy"]; strategy != nil && strategy != "" && strategy != "rolling" {
					return productReleaseStage{}, productrelease.ErrUnsupported
				}
				stageID, deployIndex = stage.ID, index
			default:
				return productReleaseStage{}, productrelease.ErrUnsupported
			}
		}
	}
	if stageID == "" || !source || buildStage == "" || pushStage != buildStage || !slices.Contains(pushInputs, buildJob) ||
		!slices.Contains(pipeline.StageDependencies(spec.Stages)[stageID], pushStage) {
		return productReleaseStage{}, productrelease.ErrUnsupported
	}
	return productReleaseStage{deployIndex: deployIndex, deployID: stageID, pushStage: pushStage, pushJob: pushJob}, nil
}

func canPreviewBranch(spec pipeline.Spec, defaultBranch string, servers []string) bool {
	candidates := []string{defaultBranch}
	for _, stage := range spec.Stages {
		for _, name := range stage.When.Branches {
			// Options do no repository request: a candidate only establishes
			// that a branch rule could apply. Preview validates the real branch.
			candidate := strings.ReplaceAll(name, "*", "pipewright-release")
			if candidate != "" && !slices.Contains(candidates, candidate) {
				candidates = append(candidates, candidate)
			}
		}
	}
	for _, b := range candidates {
		if _, err := releaseStage(spec, b, servers); err == nil {
			return true
		}
	}
	return false
}

func (c *productReleaseController) Preview(ctx context.Context, product string, items []productrelease.PreviewItem) (productrelease.Plan, error) {
	if !c.ready {
		return productrelease.Plan{}, productrelease.ErrUnavailable
	}
	p, err := c.Principal(ctx)
	if err != nil {
		return productrelease.Plan{}, err
	}
	out := productrelease.Plan{ProductID: product, Items: make([]productrelease.ItemPlan, 0, len(items))}
	bundles := make([]productReleaseSnapshot, 0, len(items))
	for _, i := range items {
		b, err := c.prepareRelease(ctx, p, product, i)
		if err != nil {
			return productrelease.Plan{}, err
		}
		out.Items = append(out.Items, b.Plan)
		bundles = append(bundles, b)
	}
	for i := range bundles {
		bundles[i].PlanID, bundles[i].PlanIndex, bundles[i].PlanCount = out.Items[0].SnapshotRef, i, len(bundles)
		bundles[i].CreatedAt = time.Now().UTC().UnixMilli()
	}
	if err = configstate.WithWriteTxContext(ctx, c.db, func(ctx context.Context, q configstate.DBTX) error {
		if err := configstate.CheckMaintenanceAdmission(ctx, q); err != nil {
			return err
		}
		if err := tenancy.LockAuthorization(ctx, c.db); err != nil {
			return err
		}
		if err := configstate.LockEpochs(ctx, q, c.db, configstate.EpochPolicy, configstate.EpochSecrets, configstate.EpochExecutor); err != nil {
			return err
		}
		if err := c.cleanupPreviews(ctx, q, p, time.Now().Add(-productPreviewLifetime).UnixMilli()); err != nil {
			return err
		}
		for _, b := range bundles {
			if _, err := configstate.LockProject(ctx, q, c.db, b.Plan.ApplicationID); err != nil {
				return err
			}
			if err := c.validateSnapshot(ctx, b); err != nil {
				return err
			}
			binary, err := json.Marshal(b)
			if err != nil {
				return err
			}
			sealed, err := c.compiler.vault.SealSecret(append([]byte(snapshotDomain), binary...))
			if err != nil {
				return err
			}
			_, err = q.ExecContext(ctx, `INSERT INTO product_release_previews(id,tenant_id,actor_id,product_id,plan_id,created_at,ciphertext) VALUES(?,?,?,?,?,?,`+store.BinaryPlaceholder(store.DialectOf(c.db))+`)`, b.Plan.SnapshotRef, p.TenantID, p.ActorID, b.ProductID, b.PlanID, b.CreatedAt, sealed)
			if err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		return productrelease.Plan{}, err
	}
	return out, nil
}

func (c *productReleaseController) prepareRelease(ctx context.Context, p productrelease.Principal, product string, i productrelease.PreviewItem) (productReleaseSnapshot, error) {
	a, err := c.application(ctx, product, i.ApplicationID)
	if err != nil {
		return productReleaseSnapshot{}, err
	}
	branch := strings.TrimSpace(i.Branch)
	if branch == "" {
		branch = a.Project.DefaultBranch
	}
	if branch == "" || strings.HasPrefix(branch, "refs/") || strings.ContainsAny(branch, "\r\n\x00") || c.compiler.pacGlobal || a.Project.PacEnabled {
		return productReleaseSnapshot{}, productrelease.ErrUnsupported
	}
	if err = c.authorizeDeployment(ctx, i.ApplicationID, i.Environment); err != nil {
		return productReleaseSnapshot{}, err
	}
	prep, err := tenancy.PrepareRunContext(ctx, c.db, i.ApplicationID)
	if err != nil {
		return productReleaseSnapshot{}, err
	}
	settings, err := pipeline.LoadExistingSettings(prep, c.db, i.ApplicationID)
	if err != nil {
		return productReleaseSnapshot{}, releaseResourceError(err)
	}
	if settings == nil {
		return productReleaseSnapshot{}, productrelease.ErrUnsupported
	}
	env, err := configuredEnvironment(settings, i.Environment)
	if err != nil {
		return productReleaseSnapshot{}, err
	}
	for _, id := range env.TargetServerIDs {
		if _, err := c.authorizedTarget(ctx, id); err != nil {
			return productReleaseSnapshot{}, err
		}
	}
	epochs := map[string]string{}
	q := configstate.Queryer(ctx, c.db)
	for _, name := range []string{configstate.EpochPolicy, configstate.EpochSecrets, configstate.EpochExecutor} {
		epoch, err := configstate.ReadEpoch(ctx, q, name)
		if err != nil {
			return productReleaseSnapshot{}, err
		}
		epochs["product-epoch/"+name] = strconv.FormatInt(epoch, 10)
	}
	in := run.Trigger{Type: run.TriggerManual, Actor: "product-release:" + p.ActorID, Branch: branch, Commit: i.CommitSHA, ResolvedEnvironment: i.Environment, ResolvedTargetServerIDs: slices.Clone(env.TargetServerIDs)}
	prepared, err := c.compiler.prepare(ctx, i.ApplicationID, in, true)
	if errors.Is(err, tenancy.ErrConflict) {
		return productReleaseSnapshot{}, productrelease.ErrConflict
	}
	if err != nil {
		return productReleaseSnapshot{}, err
	}
	value, ok := prepared.Value.(preparedRunAccess)
	if !ok || value.snapshot.Source.Kind != "stored" || value.snapshot.Source.Fallback != "disabled" || value.snapshot.RunnerRefs["serverId"] != "" {
		return productReleaseSnapshot{}, productrelease.ErrUnsupported
	}
	var fixed pipeline.Settings
	raw, err := json.Marshal(value.snapshot.Settings)
	if err != nil || json.Unmarshal(raw, &fixed) != nil {
		return productReleaseSnapshot{}, productrelease.ErrConflict
	}
	fixedEnv, err := configuredEnvironment(&fixed, i.Environment)
	if err != nil || !slices.Equal(fixedEnv.TargetServerIDs, in.ResolvedTargetServerIDs) {
		return productReleaseSnapshot{}, productrelease.ErrConflict
	}
	stage, err := releaseStage(value.snapshot.Spec, branch, in.ResolvedTargetServerIDs)
	if err != nil {
		return productReleaseSnapshot{}, err
	}
	// Product releases have exactly one push job on the deployment path.
	// Seal that source into this release so the SSH step cannot select a local build image.
	selector, err := json.Marshal(map[string]any{"stageId": stage.pushStage, "jobId": stage.pushJob, "declarationIndex": 0})
	if err != nil {
		return productReleaseSnapshot{}, err
	}
	value.snapshot.Spec.Stages[stage.deployIndex].Jobs[0].Config["artifactSource"] = string(selector)
	value.snapshot.Compiled = nil
	value.snapshot, err = runretry.FreezeSnapshot(value.snapshot)
	if err != nil {
		return productReleaseSnapshot{}, err
	}
	b := productReleaseSnapshot{Principal: p, ProductID: product, Snapshot: value.snapshot, Permissions: value.permissions, Revision: value.revision, AssignmentRevision: a.Revision, StageID: stage.deployID, Targets: map[string]string{}}
	for key, v := range epochs {
		b.Snapshot.RunnerRefs[key] = v
	}
	for _, id := range in.ResolvedTargetServerIDs {
		if err := tenancy.Check(ctx, c.db, "server.use", "server", id); err != nil {
			return productReleaseSnapshot{}, err
		}
		s, err := c.targets.Capture(ctx, id)
		if err != nil {
			return productReleaseSnapshot{}, err
		}
		b.Targets[id] = s.Fingerprint()
		b.Snapshot.RunnerRefs["product-target/"+id] = b.Targets[id]
	}
	b.Snapshot.Trigger.Commit = b.Snapshot.CommitSHA
	digest, err := runretry.SnapshotDigest(b.Snapshot)
	if err != nil {
		return productReleaseSnapshot{}, err
	}
	b.Plan = productrelease.ItemPlan{ApplicationID: i.ApplicationID, CommitSHA: b.Snapshot.CommitSHA, Environment: i.Environment, TargetIDs: in.ResolvedTargetServerIDs, ConfigDigest: digest, SnapshotRef: uuid.NewString()}
	return b, nil
}

func (c *productReleaseController) loadSnapshot(ctx context.Context, item productrelease.ItemPlan) (productReleaseSnapshot, error) {
	var b productReleaseSnapshot
	p, err := c.Principal(ctx)
	if err != nil {
		return b, err
	}
	var ciphertext []byte
	var product string
	err = configstate.Queryer(ctx, c.db).QueryRowContext(ctx, `SELECT product_id,ciphertext FROM product_release_previews WHERE id=? AND tenant_id=? AND actor_id=?`, item.SnapshotRef, p.TenantID, p.ActorID).Scan(&product, &ciphertext)
	if errors.Is(err, sql.ErrNoRows) {
		return b, productrelease.ErrConflict
	}
	if err != nil {
		return b, err
	}
	plain, err := c.compiler.vault.OpenSecret(ciphertext)
	if err != nil || !bytes.HasPrefix(plain, []byte(snapshotDomain)) || json.Unmarshal(plain[len(snapshotDomain):], &b) != nil {
		return productReleaseSnapshot{}, productrelease.ErrConflict
	}
	if b.Principal != p || b.ProductID != product || productrelease.ItemDigest(b.Plan) != productrelease.ItemDigest(item) ||
		b.Snapshot.ProjectID != item.ApplicationID || b.Snapshot.CommitSHA != item.CommitSHA || b.Snapshot.Trigger.Commit != item.CommitSHA ||
		b.Snapshot.Trigger.Environment != item.Environment || !slices.Equal(b.Snapshot.Trigger.TargetServerIDs, item.TargetIDs) {
		return productReleaseSnapshot{}, productrelease.ErrConflict
	}
	digest, err := runretry.SnapshotDigest(b.Snapshot)
	if err != nil || digest != item.ConfigDigest {
		return productReleaseSnapshot{}, productrelease.ErrConflict
	}
	return b, nil
}

func (c *productReleaseController) validateSnapshot(ctx context.Context, b productReleaseSnapshot) error {
	p, err := c.Principal(ctx)
	if err != nil {
		return err
	}
	if p != b.Principal {
		return tenancy.ErrForbidden
	}
	a, err := c.application(ctx, b.ProductID, b.Plan.ApplicationID)
	if err != nil {
		return err
	}
	if a.Revision != b.AssignmentRevision {
		return productrelease.ErrConflict
	}
	if err = c.authorizeDeployment(ctx, b.Plan.ApplicationID, b.Plan.Environment); err != nil {
		return err
	}
	q := configstate.Queryer(ctx, c.db)
	rev, err := configstate.ReadRevision(ctx, q, b.Plan.ApplicationID)
	if err != nil {
		return err
	}
	if rev != b.Revision {
		return productrelease.ErrConflict
	}
	for _, name := range []string{configstate.EpochPolicy, configstate.EpochSecrets, configstate.EpochExecutor} {
		current, err := configstate.ReadEpoch(ctx, q, name)
		if err != nil {
			return err
		}
		if b.Snapshot.RunnerRefs["product-epoch/"+name] != strconv.FormatInt(current, 10) {
			return productrelease.ErrConflict
		}
	}
	if err = tenancy.Check(ctx, c.db, "credential.use", "credential", a.Project.CredentialID); err != nil {
		return err
	}
	for _, id := range b.Plan.TargetIDs {
		if err = tenancy.Check(ctx, c.db, "server.use", "server", id); err != nil {
			return err
		}
		snap, err := c.targets.Capture(ctx, id)
		if err != nil {
			return err
		}
		if snap.Fingerprint() != b.Targets[id] {
			return productrelease.ErrConflict
		}
		if err = tenancy.Check(ctx, c.db, "credential.use", "credential", snap.Server.CredentialID); err != nil {
			return err
		}
	}
	return nil
}

func (c *productReleaseController) ValidateFrozen(ctx context.Context, i productrelease.ItemPlan) error {
	if !c.ready {
		return productrelease.ErrUnavailable
	}
	b, err := c.loadSnapshot(ctx, i)
	if err != nil {
		return err
	}
	return c.validateSnapshot(ctx, b)
}
func releaseTriggerIdentity(i productrelease.Identity) run.Trigger {
	return run.Trigger{IdempotencyScope: i.Scope, IdempotencyKey: i.Key, IdempotencyFingerprint: i.Fingerprint}
}
func (c *productReleaseController) StartFrozen(ctx context.Context, i productrelease.ItemPlan, identity productrelease.Identity) (productrelease.Receipt, error) {
	if !c.ready {
		return productrelease.Receipt{}, errors.Join(productrelease.ErrDispatchRejected, productrelease.ErrUnavailable)
	}
	b, err := c.loadSnapshot(ctx, i)
	if err != nil {
		return productrelease.Receipt{}, errors.Join(productrelease.ErrDispatchRejected, err)
	}
	if err = c.validateSnapshot(ctx, b); err != nil {
		return productrelease.Receipt{}, errors.Join(productrelease.ErrDispatchRejected, err)
	}
	in := releaseTriggerIdentity(identity)
	in.Type, in.Actor, in.Branch, in.Commit = run.TriggerManual, b.Snapshot.Trigger.Actor, b.Snapshot.Trigger.Branch, i.CommitSHA
	in.ResolvedEnvironment, in.ResolvedTargetServerIDs, in.Params = i.Environment, slices.Clone(i.TargetIDs), b.Snapshot.Trigger.Params
	fixed := &run.PreparedExecution{Value: preparedRunAccess{snapshot: b.Snapshot, revision: b.Revision, trigger: triggerIdentity(in), permissions: b.Permissions}}
	rn, err := run.CreatePrepared(ctx, c.runs, i.ApplicationID, in, fixed, func(locked context.Context) error {
		if err := c.validateSnapshot(locked, b); err != nil {
			return errors.Join(productrelease.ErrDispatchRejected, err)
		}
		return nil
	})
	if rn != nil && rn.DispatchState == "rejected" {
		return productrelease.Receipt{RunID: rn.ID, State: "failed"}, nil
	}
	if err != nil {
		return productrelease.Receipt{}, err
	}
	return c.receipt(ctx, b, rn)
}

func (c *productReleaseController) Lookup(ctx context.Context, i productrelease.ItemPlan, identity productrelease.Identity) (productrelease.Receipt, bool, error) {
	b, err := c.loadSnapshot(ctx, i)
	if err != nil {
		return productrelease.Receipt{}, false, err
	}
	lookup, ok := c.runs.(interface {
		LookupIdempotentRun(context.Context, run.Trigger) (*run.Run, bool, error)
	})
	if !ok {
		return productrelease.Receipt{}, false, productrelease.ErrUnavailable
	}
	rn, found, err := lookup.LookupIdempotentRun(ctx, releaseTriggerIdentity(identity))
	if err != nil || !found {
		return productrelease.Receipt{}, found, err
	}
	out, err := c.receipt(ctx, b, rn)
	return out, true, err
}

func (c *productReleaseController) Observe(ctx context.Context, i productrelease.ItemPlan, id string) (productrelease.Receipt, error) {
	b, err := c.loadSnapshot(ctx, i)
	if err != nil {
		return productrelease.Receipt{}, err
	}
	rn, err := c.runs.Get(ctx, id)
	if err != nil {
		return productrelease.Receipt{}, err
	}
	return c.receipt(ctx, b, rn)
}

func (c *productReleaseController) receipt(ctx context.Context, b productReleaseSnapshot, rn *run.Run) (productrelease.Receipt, error) {
	if rn == nil || rn.ProjectID != b.Plan.ApplicationID ||
		rn.Trigger.Actor != b.Snapshot.Trigger.Actor || rn.Trigger.Branch != b.Snapshot.Trigger.Branch ||
		rn.Trigger.ResolvedEnvironment != b.Plan.Environment || !slices.Equal(rn.Trigger.ResolvedTargetServerIDs, b.Plan.TargetIDs) {
		return productrelease.Receipt{}, productrelease.ErrConflict
	}
	// After checkout SetCommit stores an abbreviated display SHA separately
	// from the full resolved SHA. Only the latter can validate that abbreviation.
	if rn.Trigger.Commit != b.Plan.CommitSHA &&
		(rn.ResolvedCommitConflict || rn.ResolvedCommitSHA != b.Plan.CommitSHA ||
			len(rn.Trigger.Commit) != 7 || !strings.HasPrefix(b.Plan.CommitSHA, rn.Trigger.Commit)) {
		return productrelease.Receipt{}, productrelease.ErrConflict
	}
	out := productrelease.Receipt{RunID: rn.ID, State: "unknown"}
	if rn.DispatchState == "rejected" {
		out.State = "failed"
		return out, nil
	}
	if rn.Status == run.StatusQueued && rn.DispatchState != "accepted" {
		return out, nil
	}
	switch rn.Status {
	case run.StatusQueued, run.StatusRunning:
		out.State = "running"
	case run.StatusWaitingApproval:
		out.State = "waiting_approval"
	case run.StatusFailed, run.StatusPartialFailed, run.StatusRolledBack:
		attempts, err := c.attempts.ListByRun(ctx, rn.ID)
		if err != nil {
			return out, err
		}
		for _, a := range attempts {
			if !sameProductDeployStage(b, rn.ID, a) {
				continue
			}
			for _, t := range a.Targets {
				if t.Outcome == deployattempt.Started || t.Outcome == deployattempt.ActivationStarted || t.Outcome == deployattempt.Activated || t.Outcome == deployattempt.OutcomeUnknown {
					return out, nil
				}
			}
		}
		out.State = "failed"
	case run.StatusSuccess:
		// The run has to have used this exact encrypted execution manifest and
		// an independently recorded actual source checkout before a receipt helps.
		if rn.ResolvedCommitConflict || rn.ResolvedCommitSHA != b.Plan.CommitSHA {
			return out, nil
		}
		digest, err := c.compiler.Digest(ctx, rn.ID)
		if err != nil || digest != b.Plan.ConfigDigest {
			return out, nil
		}
		attempts, err := c.attempts.ListByRun(ctx, rn.ID)
		if err != nil {
			return out, err
		}
		if !verifiedProductDeployment(b, rn.ID, attempts) {
			return out, nil
		}
		matching := productDeploymentAttempt(b, rn.ID, attempts)
		if match, err := c.artifactMatches(ctx, rn.ID, matching.Targets); err != nil || !match {
			return out, err
		}
		out.State, out.PlanDigest, out.VerifiedDeployment = "succeeded", productrelease.ItemDigest(b.Plan), true
	}
	return out, nil
}

func sameProductDeployStage(b productReleaseSnapshot, id string, a deployattempt.Attempt) bool {
	return a.Source.RunID == id && a.Source.StageID == b.StageID && a.Source.Initiator == "stage"
}

func productDeploymentAttempt(b productReleaseSnapshot, id string, attempts []deployattempt.Attempt) *deployattempt.Attempt {
	var result *deployattempt.Attempt
	for i := range attempts {
		if !sameProductDeployStage(b, id, attempts[i]) {
			continue
		}
		if result != nil {
			return nil
		}
		result = &attempts[i]
	}
	return result
}

func verifiedProductDeployment(b productReleaseSnapshot, id string, attempts []deployattempt.Attempt) bool {
	a := productDeploymentAttempt(b, id, attempts)
	if a == nil {
		return false
	}
	source := a.Source
	if source.RunID != id || source.StageID != b.StageID || source.Initiator != "stage" || source.ProjectID != b.Plan.ApplicationID ||
		source.Environment != b.Plan.Environment || source.ResolvedFullSHA != b.Plan.CommitSHA || source.ConfigRevision != b.Revision ||
		len(a.Targets) != len(b.Plan.TargetIDs) {
		return false
	}
	seen := map[string]bool{}
	for _, t := range a.Targets {
		if t.Source != source || t.AttemptID != a.ID || seen[t.ServerID] || b.Targets[t.ServerID] == "" ||
			t.ConnectionFingerprint != b.Targets[t.ServerID] || t.Outcome != deployattempt.Succeeded || len(t.Receipts) == 0 {
			return false
		}
		seen[t.ServerID] = true
		r := deployattempt.Receipt{}
		for j := len(t.Receipts) - 1; j >= 0; j-- {
			if t.Receipts[j].Kind != deployattempt.IdentityObserved {
				r = t.Receipts[j]
				break
			}
		}
		if r.TargetID != t.ID || r.Kind != deployattempt.Succeeded || (r.HealthGate != "passed" && r.HealthGate != "not_run") ||
			!verifiedProductImage(t.ExpectedImage, r.ActualImage) {
			return false
		}
	}
	return true
}

func (c *productReleaseController) AuthorizeRunRead(ctx context.Context, id string) error {
	return tenancy.Check(ctx, c.db, "run.read", "run", id)
}

func (c *productReleaseController) ValidateConfirmation(ctx context.Context, p productrelease.Plan) error {
	if len(p.Items) == 0 {
		return productrelease.ErrInvalid
	}
	first, err := c.loadSnapshot(ctx, p.Items[0])
	if err != nil {
		return err
	}
	now := time.Now().UTC().UnixMilli()
	if first.CreatedAt <= 0 || first.CreatedAt > now || now-first.CreatedAt > productPreviewLifetime.Milliseconds() {
		return productrelease.ErrConflict
	}
	return nil
}

func (c *productReleaseController) artifactMatches(ctx context.Context, runID string, targets []deployattempt.Target) (bool, error) {
	if len(targets) == 0 {
		return false, nil
	}
	first := targets[0]
	read, err := tenancy.QueryPredicate(ctx, c.db, "run.read", "r.id")
	if err != nil {
		return false, err
	}
	var metadata string
	err = configstate.Queryer(ctx, c.db).QueryRowContext(ctx, `SELECT a.metadata_json FROM run_artifacts a JOIN pipeline_runs r ON r.id=a.run_id
		WHERE a.run_id=? AND a.id=? AND a.type='image' AND (`+read.SQL+`)`, append([]any{runID, first.ArtifactID}, read.Args...)...).Scan(&metadata)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	var image struct {
		Pushed     bool   `json:"pushed"`
		Digest     string `json:"digest"`
		Repository string `json:"digestRepository"`
	}
	if json.Unmarshal([]byte(metadata), &image) != nil || !image.Pushed || image.Digest == "" || image.Repository == "" {
		return false, nil
	}
	for _, t := range targets {
		if t.ArtifactID != first.ArtifactID || t.ExpectedImage.Digest != image.Digest || t.ExpectedImage.Repository != image.Repository {
			return false, nil
		}
	}
	return true, nil
}

// cleanupPreviews runs under the same epoch locks as confirmation. The fixed
// cap bounds a request's work; retained confirmed snapshots never expire.
func (c *productReleaseController) cleanupPreviews(ctx context.Context, q configstate.DBTX, p productrelease.Principal, cutoff int64) error {
	const orphan = `NOT EXISTS (SELECT 1 FROM product_release_batches b WHERE b.tenant_id=product_release_previews.tenant_id
		AND b.actor_id=product_release_previews.actor_id AND b.request_id=product_release_previews.plan_id)`
	args := []any{p.TenantID, p.ActorID, cutoff}
	rows, err := q.QueryContext(ctx, `SELECT id FROM product_release_previews WHERE tenant_id=? AND actor_id=? AND created_at<? AND `+orphan+
		` ORDER BY created_at,id`+store.Limit(store.DialectOf(c.db), "200"), args...)
	if err != nil {
		return err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, id := range ids {
		if _, err = q.ExecContext(ctx, `DELETE FROM product_release_previews WHERE id=? AND tenant_id=? AND actor_id=? AND created_at<? AND `+orphan, append([]any{id}, args...)...); err != nil {
			return err
		}
	}
	return nil
}

func verifiedProductImage(expected deployattempt.ExpectedImage, actual deployattempt.ActualImage) bool {
	if expected.IdentitySource != "" || expected.Repository == "" || !productImageDigest(expected.Digest) ||
		!productImageDigest(actual.ConfigID) || !deployattempt.ValidContainerID(actual.ContainerID) ||
		actual.Platform.OS == "" || actual.Platform.Architecture == "" {
		return false
	}
	switch expected.DigestKind {
	case deployattempt.DigestManifest, deployattempt.DigestIndex, deployattempt.DigestRepositoryUnknown:
	default:
		return false
	}
	// SSH verifies the exact pulled digest before activation and then matches
	// the activated config ID. No repository alias or platform type is inferred.
	return expected.Reference == expected.Repository+"@"+expected.Digest && slices.Contains(actual.RepoDigests, expected.Reference) &&
		(expected.Platform.OS == "" || expected.Platform.OS == actual.Platform.OS) &&
		(expected.Platform.Architecture == "" || expected.Platform.Architecture == actual.Platform.Architecture) &&
		(expected.Platform.Variant == "" || expected.Platform.Variant == actual.Platform.Variant)
}
func productImageDigest(digest string) bool {
	payload, ok := strings.CutPrefix(digest, "sha256:")
	if !ok || len(payload) != 64 || payload != strings.ToLower(payload) {
		return false
	}
	_, err := hex.DecodeString(payload)
	return err == nil
}
