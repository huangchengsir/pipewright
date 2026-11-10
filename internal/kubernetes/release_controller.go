package kubernetes

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/huangchengsir/pipewright/internal/kubeprotocol"
	"github.com/huangchengsir/pipewright/internal/pipeline"
)

var ErrReleasePaused = errors.New("kubernetes_release_waiting_action")

type releaseAPI interface {
	Read(context.Context, string, string) ([]byte, error)
	DryRun(context.Context, kubeprotocol.Request) (map[string]any, error)
	ExecuteAction(context.Context, string, string, int64, ReleaseActionIntent, string) (ReleaseAction, error)
	Close(context.Context) (ReleaseRuntimeLease, error)
}
type ReleaseController struct {
	store   *Service
	runtime *Runtime
	preview func(context.Context, ReleaseAuthorization, string) (releaseAPI, error)
	writer  func(context.Context, string, string) (releaseAPI, error)
	poll    time.Duration
}

func NewReleaseController(s *Service, r *Runtime) *ReleaseController {
	c := &ReleaseController{store: s, runtime: r, poll: 2 * time.Second}
	c.preview = func(ctx context.Context, a ReleaseAuthorization, owner string) (releaseAPI, error) {
		return r.OpenReleasePreview(ctx, a, owner)
	}
	c.writer = func(ctx context.Context, id, owner string) (releaseAPI, error) {
		return r.OpenReleaseWriter(ctx, id, owner)
	}
	return c
}
func resourceAPI(id ReleaseResourceID) ReleaseAPIResource {
	return ReleaseAPIResource{Group: id.Group, Version: id.Version, Name: id.Resource, Kind: id.Kind, Namespaced: id.Namespace != ""}
}
func releaseObjectCopy(v map[string]any) map[string]any {
	b, _ := json.Marshal(v)
	var out map[string]any
	json.Unmarshal(b, &out)
	clear(b)
	return out
}
func releaseCanonicalObject(v map[string]any) map[string]any {
	out := releaseObjectCopy(v)
	delete(out, "status")
	m := releaseMap(out["metadata"])
	for _, key := range []string{"uid", "resourceVersion", "generation", "creationTimestamp", "deletionTimestamp", "managedFields", "selfLink"} {
		delete(m, key)
	}
	ann := releaseMap(m["annotations"])
	delete(ann, ReleaseActionAnnotation)
	delete(ann, OperationAnnotation)
	if len(ann) == 0 {
		delete(m, "annotations")
	}
	return out
}
func validateReleaseWorkloadStrategy(resource ReleaseResource) error {
	spec := releaseMap(resource.Object["spec"])
	if resource.ID.Kind == "Deployment" && releaseBool(spec["paused"]) {
		return ErrInvalid
	}
	if resource.ID.Kind == "StatefulSet" || resource.ID.Kind == "DaemonSet" {
		strategy := releaseMap(spec["updateStrategy"])
		if strategy["type"] != nil && strategy["type"] != "RollingUpdate" {
			return ErrInvalid
		}
		if resource.ID.Kind == "StatefulSet" && releaseInt(releaseMap(strategy["rollingUpdate"])["partition"]) != 0 {
			return ErrInvalid
		}
	}
	return nil
}
func readReleaseObject(ctx context.Context, api releaseAPI, id ReleaseResourceID) (map[string]any, error) {
	path, e := resourceAPI(id).Path(id.Namespace, id.Name)
	if e != nil {
		return nil, e
	}
	raw, e := api.Read(ctx, path, "")
	if e != nil {
		return nil, e
	}
	defer clear(raw)
	if len(raw) > MaxReleaseObjectBytes {
		return nil, ErrPrivateLimit
	}
	var out map[string]any
	if json.Unmarshal(raw, &out) != nil {
		return nil, ErrInvalid
	}
	return out, nil
}
func releaseRequestedAccess(id ReleaseResourceID, verb string) ReleaseAccess {
	scope := ReleaseAccess{ID: id, Actions: []string{"get", verb}}
	switch id.Kind {
	case "Deployment":
		scope.Actions = append(scope.Actions, "related-replicasets", "related-pods")
	case "DaemonSet":
		scope.Actions = append(scope.Actions, "related-pods")
		if releaseDaemonID(id) {
			scope.Actions = append(scope.Actions, releaseDaemonRevisionAction)
		}
	case "StatefulSet", "Job", "CronJob":
		scope.Actions = append(scope.Actions, "related-pods")
	case "Service":
		scope.Actions = append(scope.Actions, "related-endpointslices")
	}
	return scope
}

// PrepareManifest performs bounded real discovery and read-only baseline capture
// before creating the immutable approved plan. Raw fields remain inside the
// sealed domain boundary; callers receive only a ReleaseRecord projection.
func (c *ReleaseController) PrepareManifest(ctx context.Context, run, node, owner string, auth ReleaseAuthorization, files []ReleaseInputFile, vars map[string]ReleaseVariableValue, config json.RawMessage, timeout, observe int) (ReleaseRecord, error) {
	return c.prepareManifest(ctx, run, node, owner, auth, files, vars, config, timeout, observe, nil, nil, nil)
}
func (c *ReleaseController) prepareManifest(ctx context.Context, run, node, owner string, auth ReleaseAuthorization, files []ReleaseInputFile, vars map[string]ReleaseVariableValue, config json.RawMessage, timeout, observe int, helm *ReleaseHelmPlan, chart, values []byte) (out ReleaseRecord, resultErr error) {
	return c.prepareManifestWithCommit(ctx, run, node, owner, auth, files, vars, config, timeout, observe, helm, chart, values, nil, nil)
}
func (c *ReleaseController) prepareManifestWithCommit(ctx context.Context, run, node, owner string, auth ReleaseAuthorization, files []ReleaseInputFile, vars map[string]ReleaseVariableValue, config json.RawMessage, timeout, observe int, helm *ReleaseHelmPlan, chart, values []byte, customize func(*ReleasePlan, *ReleaseAuthorizationRequest) error, commit func(context.Context, ReleasePlan) (ReleaseRecord, error)) (out ReleaseRecord, resultErr error) {
	phase := "runtime"
	defer func() { resultErr = WithReleasePreparationPhase(resultErr, phase) }()
	api, e := c.preview(ctx, auth, owner)
	if e != nil {
		return out, e
	}
	defer func() {
		if api != nil {
			_, e := api.Close(context.WithoutCancel(ctx))
			resultErr = errors.Join(resultErr, e)
		}
	}()
	phase = "discovery"
	d := NewReleaseDiscovery()
	seen := map[string]bool{}
	for _, scope := range auth.Request.Resources {
		path := "/api/" + scope.ID.Version
		if scope.ID.Group != "" {
			path = "/apis/" + scope.ID.Group + "/" + scope.ID.Version
		}
		if seen[path] {
			continue
		}
		seen[path] = true
		raw, e := api.Read(ctx, path, "")
		if errors.Is(e, ErrNotFound) {
			continue
		}
		if e != nil {
			return out, e
		}
		e = d.Add(raw)
		clear(raw)
		if e != nil {
			return out, e
		}
	}
	var sourceConfig struct {
		Namespace string `json:"namespace"`
	}
	_ = json.Unmarshal(config, &sourceConfig)
	defaultNamespace := sourceConfig.Namespace
	if defaultNamespace == "" {
		for _, scope := range auth.Request.Resources {
			if scope.ID.Namespace != "" {
				defaultNamespace = scope.ID.Namespace
				break
			}
		}
	}
	if e = d.addProposedCRDs(files, vars, helm == nil); e != nil {
		return out, e
	}
	crdFiles := []ReleaseInputFile{}
	helmCRDs := map[ReleaseResourceID]bool{}
	if helm != nil && len(chart) > 0 {
		crdFiles, e = ReadReleaseChartCRDs(chart)
		if e != nil {
			return out, e
		}
		files = append(append([]ReleaseInputFile(nil), crdFiles...), files...)
		if e = d.addProposedCRDs(crdFiles, nil, false); e != nil {
			return out, e
		}
		if len(crdFiles) > 0 {
			definitions, e := freezeReleaseManifest(crdFiles, nil, d, "", false)
			if e != nil {
				return out, e
			}
			for _, definition := range definitions.Resources {
				if definition.ID.Group != "apiextensions.k8s.io" || definition.ID.Kind != "CustomResourceDefinition" {
					return out, ErrManifest
				}
				helmCRDs[definition.ID] = true
			}
		}
	}
	phase = "manifest"
	manifest, e := freezeReleaseManifest(files, vars, d, defaultNamespace, helm == nil)
	if e != nil {
		return out, e
	}
	plan := ReleasePlan{MutationVersion: 1, Version: 2, Mode: "manifest", Resources: manifest.Resources, Input: &manifest, Config: append([]byte(nil), config...), TimeoutSeconds: timeout, ObserveSeconds: observe}
	if helm != nil {
		plan.Mode = "helm"
		plan.Helm = helm
		plan.Chart = append([]byte(nil), chart...)
		plan.Values = append([]byte(nil), values...)
		for index := range plan.Resources {
			resource := &plan.Resources[index]
			if helmCRDs[resource.ID] {
				resource.HelmCRD = true
				resource.Supplemental = true
				continue
			}
			meta := releaseMap(resource.Object["metadata"])
			ann := releaseMap(meta["annotations"])
			if ann["helm.sh/hook"] != nil {
				// Helm executes hooks directly from h.Manifest, without the ordinary
				// resource setMetadataVisitor. Freeze the exact rendered metadata,
				// including any explicit ownership fields; never invent or overwrite it.
				if !validReleaseHelmHook(*resource) {
					return out, ErrDenied
				}
			} else {
				if ann == nil {
					ann = map[string]any{}
					meta["annotations"] = ann
				}
				ann["meta.helm.sh/release-name"] = helm.Name
				ann["meta.helm.sh/release-namespace"] = helm.Namespace
				labels := releaseMap(meta["labels"])
				if labels == nil {
					labels = map[string]any{}
					meta["labels"] = labels
				}
				labels["app.kubernetes.io/managed-by"] = "Helm"
			}
			body, _ := json.Marshal(resource.Object)
			resource.Digest = releaseByteDigest(body)
		}
	}
	if helm != nil {
		for _, resource := range plan.Resources {
			if resource.HelmCRD {
				continue
			}
			copy := resource
			copy.Object = releaseObjectCopy(resource.Object)
			helm.Rendered = append(helm.Rendered, copy)
		}
	}
	if e = c.appendHelmDeletions(ctx, api, d, &plan); e != nil {
		return out, e
	}
	var predicateConfig struct {
		Checks []struct {
			Kind, Name, Namespace, Predicate, Expected string
			Resource                                   *pipeline.KubernetesCheckResource
		}
	}
	_ = json.Unmarshal(config, &predicateConfig)
	for index := range plan.Resources {
		resource := &plan.Resources[index]
		for _, check := range predicateConfig.Checks {
			namespace := check.Namespace
			if namespace == "" {
				namespace = defaultNamespace
			}
			if check.Resource != nil && checkResourceID(*check.Resource, namespace) != resource.ID {
				continue
			}
			if check.Kind == "condition" && check.Name == resource.ID.Name && namespace == resource.ID.Namespace {
				if (!strings.HasPrefix(check.Predicate, "condition:") && !strings.HasPrefix(check.Predicate, "/status/")) || len(check.Predicate) > 256 || check.Expected == "" {
					return out, ErrInvalid
				}
				resource.Predicates = append(resource.Predicates, ReleasePredicate{Predicate: check.Predicate, Expected: check.Expected})
			}
		}
		if resource.Health == "custom" {
			if len(resource.Predicates) == 0 {
				return out, ErrDiscovery
			}
			if resource.RequiredCRD == nil {
				raw, e := api.Read(ctx, releaseSchemaPath(resource.ID), "")
				if e != nil {
					return out, e
				}
				if !releaseSchemaAvailable(raw, resource.ID) {
					clear(raw)
					return out, ErrDiscovery
				}
				resource.SchemaDigest = releaseByteDigest(raw)
				clear(raw)
			}
		}
	}
	maintenanceAccess, e := c.prepareCoreDNSPlan(ctx, api, &plan, auth, config)
	if e != nil {
		return out, e
	}
	extra, e := c.appendReleaseChecks(ctx, api, &plan, auth, config)
	if e != nil {
		return out, e
	}
	request := auth.Request
	request.Resources = nil
	for _, resource := range plan.Resources {
		if e = validateReleaseWorkloadStrategy(resource); e != nil {
			return out, e
		}
		phase = "baseline"
		baseline, e := readReleaseObject(ctx, api, resource.ID)
		if resource.Ephemeral && e == nil {
			return out, ErrInUse
		}
		verb := "patch"
		if errors.Is(e, ErrNotFound) {
			verb = "create"
		} else if e != nil {
			return out, e
		} else {
			b, _ := json.Marshal(baseline)
			original := ReleaseResource{ID: resource.ID, Object: baseline, Digest: releaseByteDigest(b), Health: resource.Health}
			c.captureRollbackImages(ctx, api, &original)
			plan.Baseline = append(plan.Baseline, original)
		}
		if resource.HelmCRD && baseline != nil {
			verify := releaseResourceFromObject(resource.ID, releaseCanonicalObject(baseline), "crd")
			verify.ExpectedUID = releaseString(releaseMap(baseline["metadata"])["uid"])
			plan.Verify = append(plan.Verify, verify)
			for i := range plan.Resources {
				if plan.Resources[i].ID == resource.ID {
					plan.Resources[i].VerifyOnly = true
					plan.Resources[i].ExpectedUID = releaseString(releaseMap(baseline["metadata"])["uid"])
					plan.Resources[i].Object = releaseCanonicalObject(baseline)
					b, _ := json.Marshal(plan.Resources[i].Object)
					plan.Resources[i].Digest = releaseByteDigest(b)
				}
			}
		}
		if resource.Delete {
			verb = "delete"
		}
		access := releaseRequestedAccess(resource.ID, verb)
		if helm != nil && !resource.HelmCRD && !resource.Delete && !resource.VerifyOnly {
			access.Actions = uniqueReleaseActions(append(access.Actions, "patch"))
		}
		if resource.VerifyOnly || resource.HelmCRD && baseline != nil {
			access.Actions = []string{"get"}
		}
		if plan.CoreDNS != nil {
			access.Actions = uniqueReleaseActions(append(access.Actions, "coredns"))
		}
		if resource.Ephemeral {
			access.Actions = uniqueReleaseActions(append(access.Actions, "delete"))
		}
		access.Actions = uniqueReleaseActions(append(access.Actions, releaseAdministrativeActions(resource)...))
		if helm != nil && releaseMap(releaseMap(resource.Object["metadata"])["annotations"])["helm.sh/hook"] != nil {
			access.Actions = uniqueReleaseActions(append(access.Actions, "create", "delete", "watch", "helm-hook"))
			plan.Deletes = append(plan.Deletes, resource.ID)
		}
		access.Containers = releaseContainerNames(resource)
		request.Resources = append(request.Resources, access)
	}
	if helm != nil {
		request.Resources = append(request.Resources, ReleaseAccess{ID: helm.StorageAnchor, Actions: []string{"get", "create", "update", "patch", "helm-storage"}})
	}
	request.Resources = mergeReleaseScopes(request.Resources, extra, maintenanceAccess)
	if customize != nil {
		if e = customize(&plan, &request); e != nil {
			return out, e
		}
	}
	phase = "authorization"
	plan.Authorization, e = c.store.AuthorizeRelease(ctx, request)
	if e != nil {
		return out, e
	}
	if _, e = api.Close(context.WithoutCancel(ctx)); e != nil {
		return out, e
	}
	api = nil
	phase = "dry_run"
	if e = c.dryRunReleaseResources(ctx, owner, &plan); e != nil {
		return out, e
	}
	phase = "plan"
	if commit != nil {
		return commit(ctx, plan)
	}
	return c.store.CreateRelease(ctx, run, node, owner, plan)
}
func (c *ReleaseController) related(ctx context.Context, api releaseAPI, resource ReleaseResource) ([]map[string]any, error) {
	type collection struct{ path, version, kind string }
	paths := []collection{}
	ns := url.PathEscape(resource.ID.Namespace)
	switch resource.ID.Kind {
	case "Deployment":
		paths = append(paths, collection{"/apis/apps/v1/namespaces/" + ns + "/replicasets", "apps/v1", "ReplicaSet"}, collection{"/api/v1/namespaces/" + ns + "/pods", "v1", "Pod"})
	case "StatefulSet", "DaemonSet", "Job":
		paths = append(paths, collection{"/api/v1/namespaces/" + ns + "/pods", "v1", "Pod"})
		if resource.ID.Kind == "DaemonSet" && resource.DaemonSetProofVersion == 1 {
			paths = append(paths, collection{"/apis/apps/v1/namespaces/" + ns + "/controllerrevisions", "apps/v1", "ControllerRevision"})
		}
	case "Service":
		paths = append(paths, collection{"/apis/discovery.k8s.io/v1/namespaces/" + ns + "/endpointslices", "discovery.k8s.io/v1", "EndpointSlice"})
	}
	out := []map[string]any{}
	for _, path := range paths {
		raw, e := api.Read(ctx, path.path, "limit=200")
		if e != nil {
			return nil, withReleaseObservationStage(e, "related_list_read")
		}
		var list map[string]any
		if json.Unmarshal(raw, &list) != nil || kubeprotocol.BindTypedList(list, path.version, path.kind) != nil {
			clear(raw)
			return nil, withReleaseObservationStage(ErrInvalid, "related_list_decode")
		}
		clear(raw)
		for _, item := range releaseItems(list["items"]) {
			out = append(out, releaseMap(item))
		}
		if len(out) > 4096 {
			return nil, withReleaseObservationStage(ErrPrivateLimit, "related_list_decode")
		}
	}
	if len(paths) == 0 {
		return out, nil
	}
	anchor, e := readReleaseObject(ctx, api, resource.ID)
	if e != nil {
		return nil, withReleaseObservationStage(e, "related_anchor_read")
	}
	scope := ReleaseAccess{ID: resource.ID, Actions: []string{"related-pods", "related-replicasets", "related-endpointslices", "related-jobs"}}
	if resource.ID.Kind == "DaemonSet" && resource.DaemonSetProofVersion == 1 {
		scope.Actions = append(scope.Actions, releaseDaemonRevisionAction)
	}
	filtered := []map[string]any{}
	kinds := []string{}
	switch resource.ID.Kind {
	case "Deployment":
		kinds = []string{"ReplicaSet", "Pod"}
	case "StatefulSet", "DaemonSet", "Job":
		kinds = []string{"Pod"}
		if resource.ID.Kind == "DaemonSet" && resource.DaemonSetProofVersion == 1 {
			kinds = append(kinds, "ControllerRevision")
		}
	case "Service":
		kinds = []string{"EndpointSlice"}
	}
	for _, kind := range kinds {
		items, e := FilterReleaseRelatedObjects(scope, anchor, kind, out)
		if e != nil {
			return nil, withReleaseObservationStage(e, "related_filter")
		}
		filtered = append(filtered, items...)
	}
	return filtered, nil
}
func (c *ReleaseController) observeResource(ctx context.Context, api releaseAPI, resource ReleaseResource, seconds int) (receipt ReleaseActionReceipt, resultErr error) {
	stage := "resource_read"
	defer func() { releaseObservationFailure(ctx, stage, resultErr) }()
	receipt = ReleaseActionReceipt{Resource: resource.ID}
	window := ReleaseHealthWindow{}
	for {
		stage = "resource_read"
		actual, e := readReleaseObject(ctx, api, resource.ID)
		if e != nil {
			return receipt, e
		}
		stage = "related_list_read"
		related, e := c.related(ctx, api, resource)
		if e != nil {
			return receipt, e
		}
		stage = "health_evaluation"
		health := EvaluateReleaseHealth(resource, actual, related)
		meta := releaseMap(actual["metadata"])
		receipt.UID = releaseString(meta["uid"])
		receipt.ResourceVersion = releaseString(meta["resourceVersion"])
		receipt.Object = actual
		receipt.Health = health
		if health.State == "failed" {
			return receipt, ErrRollout
		}
		if health.State == "unverified" {
			return receipt, ErrRollout
		}
		duration := seconds
		if health.State == "declared" || resource.ID.Kind == "Job" {
			duration = 0
		}
		if window.Observe(health, time.Now(), duration) {
			return receipt, nil
		}
		stage = "observation_wait"
		select {
		case <-ctx.Done():
			return receipt, ctx.Err()
		case <-time.After(c.poll):
		}
	}
}
func (c *ReleaseController) Run(ctx context.Context, id, owner string, log PhaseLog) (out ReleaseRecord, resultErr error) {
	r, e := c.store.GetRelease(ctx, id)
	if e != nil {
		return out, e
	}
	if r.OwnerEpoch != owner || r.OwnerFenced || r.State != "running" {
		return r, ErrOwner
	}
	if e = c.store.AcquireReleaseGates(ctx, id, owner); e != nil {
		return r, e
	}
	work, cancel := context.WithTimeout(ctx, time.Duration(r.Plan.TimeoutSeconds)*time.Second)
	work = context.WithValue(work, releaseHealthLogKey{}, log)
	defer cancel()
	api, e := c.writer(work, id, owner)
	if e != nil {
		fresh, readErr := c.store.GetRelease(context.WithoutCancel(ctx), id)
		if readErr != nil {
			return r, errors.Join(e, readErr)
		}
		facts, factsErr := c.store.ListReleaseActions(context.WithoutCancel(ctx), id)
		if factsErr != nil {
			return fresh, errors.Join(e, factsErr)
		}
		settled, settleErr := c.store.SetReleaseTerminal(context.WithoutCancel(ctx), id, owner, fresh.Revision, releaseFailureState(facts), "runtime_preflight_failed")
		if settleErr == nil && settled.Cleanup == CleanupVerified {
			settleErr = c.store.ReleaseGates(context.WithoutCancel(ctx), id, owner)
		}
		return settled, errors.Join(e, settleErr)
	}
	state, reason := "failed", "release_failed"
	defer func() {
		clean, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		defer cancel()
		if resultErr != nil && !errors.Is(resultErr, ErrReleasePaused) {
			facts, factsErr := c.store.ListReleaseActions(clean, id)
			unknown := factsErr != nil
			for _, fact := range facts {
				unknown = unknown || fact.State == "outcome_unknown" || fact.State == "submitted"
			}
			if !unknown {
				cleanupResources := r.Plan.Resources
				if r.Plan.CoreDNS == nil {
					cleanupResources = nil
					for _, resource := range r.Plan.Resources {
						if resource.Probe {
							cleanupResources = append(cleanupResources, resource)
						}
					}
				}
				resultErr = errors.Join(resultErr, c.cleanupReleaseProbes(clean, api, r, cleanupResources))
			}
		}
		phase(log, "cleanup", "cleanup_started")
		_, cleanupErr := api.Close(clean)
		fresh, e := c.store.GetRelease(clean, id)
		if cleanupErr == nil && e == nil && fresh.Cleanup == CleanupVerified {
			phase(log, "cleanup", "cleanup_verified")
		} else {
			phase(log, "cleanup", "cleanup_unverified")
		}
		if e != nil {
			resultErr = errors.Join(resultErr, cleanupErr, e)
			return
		}
		if errors.Is(resultErr, ErrReleasePaused) {
			out = fresh
			resultErr = errors.Join(resultErr, cleanupErr)
			return
		}
		if resultErr != nil && !errors.Is(resultErr, ErrReleasePaused) {
			facts, factsErr := c.store.ListReleaseActions(clean, id)
			if factsErr != nil {
				resultErr = errors.Join(resultErr, factsErr)
				return
			}
			applied, unknown := false, false
			for _, fact := range facts {
				applied = applied || fact.State == "verified"
				unknown = unknown || fact.State == "outcome_unknown" || fact.State == "submitted"
			}
			if unknown {
				state = "outcome_unknown"
			} else if applied {
				state = "partial_failed"
			} else {
				state = "failed"
			}
		}
		out, e = c.store.SetReleaseTerminal(clean, id, owner, fresh.Revision, state, reason)
		resultErr = errors.Join(resultErr, cleanupErr, e)
		if e == nil && cleanupErr == nil {
			resultErr = errors.Join(resultErr, c.store.ReleaseGates(clean, id, owner))
		}
	}()
	if r.Plan.Mode == "progressive" || r.Plan.RollbackOf != "" && r.Plan.Progressive != nil {
		if e := c.verifyProgressivePreconditions(work, api, r.Plan); e != nil {
			return r, e
		}
	}
	if r.Plan.Mode == "helm" {
		if e := c.runHelmRelease(work, api, r); e != nil {
			return r, e
		}
		state = "ready"
		reason = "release_ready"
		if r.Plan.RollbackOf != "" {
			state = "rolled_back"
			reason = "rollback_ready"
		}
		return r, nil
	}
	resources := r.Plan.Resources
	if r.Plan.Mode == "progressive" {
		if r.Cursor >= len(r.Plan.Batches) {
			return r, ErrInvalid
		}
		resources = r.Plan.Batches[r.Cursor]
	}
	actions, e := c.store.ListReleaseActions(work, id)
	if e != nil {
		return r, e
	}
	byStep := map[string]ReleaseAction{}
	for _, a := range actions {
		byStep[a.Intent.StepKey] = a
	}
	submitted := []ReleaseAction{}
	// A crash after the terminal write must not re-observe/reapply the superseded
	// canary body or replay any completed probe. Resume from its sealed receipt.
	if r.Plan.CoreDNS != nil && r.Plan.CoreDNS.Finalize != nil {
		if _, exists := byStep[releaseCoreDNSFinalizeStep]; exists {
			if e := c.finishCoreDNSFinalization(work, api, r); e != nil {
				return r, e
			}
			if e := c.recordReleaseVerifications(work, api, r); e != nil {
				return r, e
			}
			if e := c.cleanupReleaseProbes(work, api, r, resources); e != nil {
				return r, e
			}
			state, reason = "ready", "release_ready"
			if r.Plan.RollbackOf != "" {
				state, reason = "rolled_back", "rollback_complete"
			}
			return r, nil
		}
	}

	for index, resource := range resources {
		if r.Plan.CoreDNS != nil && (index == r.Plan.CoreDNS.LiveIndex || resource.Ephemeral && resource.ID.Group == "apps" && resource.ID.Kind == "Deployment") {
			if e := c.verifyCoreDNSPriorityClass(work, api, r); e != nil {
				return r, e
			}
		}
		if r.Plan.CoreDNS != nil && index == r.Plan.CoreDNS.LiveIndex {
			if e := c.verifyCoreDNSPopulation(work, api, r.Plan); e != nil {
				return r, e
			}
			for _, candidate := range resources[:index] {
				if _, e := c.observeResource(work, api, candidate, r.Plan.ObserveSeconds); e != nil {
					return r, e
				}
			}
		}
		if resource.Probe {
			if e := c.ensureStorageProbeConsumers(work, api, r, resources, byStep); e != nil {
				return r, e
			}
			prerequisites := resources
			if r.Plan.CoreDNS != nil {
				prerequisites = resources[:index]
			}
			if e := c.observePrimaryResources(work, api, r, prerequisites); e != nil {
				return r, e
			}
			if e := c.observePositiveControl(work, api, r, resource, resources); e != nil {
				return r, e
			}
		}
		phase(log, "preflight", "resource_identity_check")
		step := "batch-" + strconvI(r.Cursor) + "-resource-" + strconvI(index)
		if previous, ok := byStep[step]; ok {
			if previous.State == "outcome_unknown" {
				state = "outcome_unknown"
				return r, ErrOutcomeUnknown
			}
			if previous.State == "rejected" {
				return r, ErrDenied
			}
			submitted = append(submitted, previous)
			continue
		}
		if resource.Health == "custom" {
			if e := c.verifyCustomPrerequisites(work, api, r, resource); e != nil {
				return r, e
			}
		}
		current, e := readReleaseObject(work, api, resource.ID)
		if e == nil && releaseMap(current["metadata"])["deletionTimestamp"] != nil {
			return r, ErrSuperseded
		}
		verb := "patch"
		var baseline *ReleaseResource
		for i := range r.Plan.Baseline {
			if r.Plan.Baseline[i].ID == resource.ID {
				baseline = &r.Plan.Baseline[i]
				break
			}
		}
		if r.Plan.Mode == "progressive" && r.Cursor > 0 && !resource.Probe {
			previousStep := "batch-" + strconvI(r.Cursor-1) + "-resource-" + strconvI(index)
			previous, ok := byStep[previousStep]
			if !ok || previous.State != "verified" || previous.Receipt == nil || previous.Intent.Resource != resource.ID {
				return r, ErrOutcomeUnknown
			}
			b := releaseResourceFromObject(resource.ID, previous.Receipt.Object, resource.Health)
			baseline = &b
		}
		if errors.Is(e, ErrNotFound) {
			if baseline != nil {
				return r, ErrSuperseded
			}
			verb = "create"
		} else if e != nil {
			return r, e
		} else if baseline == nil {
			return r, ErrSuperseded
		} else if releaseString(releaseMap(current["metadata"])["uid"]) != releaseString(releaseMap(baseline.Object["metadata"])["uid"]) || !releaseResourceBaselineMatches(resource, baseline.Object, current) {
			return r, ErrSuperseded
		}
		if resource.Delete {
			if verb != "patch" {
				return r, ErrSuperseded
			}
			if e := c.observePrimaryResources(work, api, r, resources[:index]); e != nil {
				return r, e
			}
			verb = "delete"
		}
		if e := releaseMutationCurrent(r, resource, current, actions); e != nil {
			return r, e
		}
		obj := releaseMutationObject(resource)
		meta := releaseMap(obj["metadata"])
		intent := ReleaseActionIntent{MutationVersion: r.Plan.MutationVersion, Version: 2, ParentID: r.PlanID, StepKey: step, Resource: resource.ID, Verb: verb, AuthorizationFingerprint: r.Plan.Authorization.Fingerprint}
		if verb != "create" {
			intent.ExpectedUID = releaseString(releaseMap(current["metadata"])["uid"])
			intent.ExpectedRV = releaseString(releaseMap(current["metadata"])["resourceVersion"])
			meta["uid"] = intent.ExpectedUID
			meta["resourceVersion"] = intent.ExpectedRV
		}
		if resource.Mutation != nil {
			intent.ContentType = resource.Mutation.ContentType
		}
		intent.Body, _ = json.Marshal(obj)
		if resource.Delete {
			intent.Body, _ = json.Marshal(map[string]any{"apiVersion": "v1", "kind": "DeleteOptions", "propagationPolicy": "Foreground", "preconditions": map[string]any{"uid": intent.ExpectedUID, "resourceVersion": intent.ExpectedRV}})
		}
		path, e := resourceAPI(resource.ID).Path(resource.ID.Namespace, func() string {
			if verb == "create" {
				return ""
			}
			return resource.ID.Name
		}())
		if e != nil {
			return r, e
		}
		if resource.RequiredCRD != nil || resource.Mutation != nil {
			method := "PATCH"
			if verb == "create" {
				method = "POST"
			}
			admitted, e := api.DryRun(work, kubeprotocol.Request{Method: method, Path: path, Body: intent.Body, ContentType: intent.ContentType})
			if e != nil {
				return r, e
			}
			if resource.Mutation != nil {
				if !releaseResourceExpectedMatches(resource, admitted) {
					return r, errReleaseAdmissionMismatch
				}
			} else if !ReleaseObjectMatches(resource.Object, admitted) {
				return r, ErrDenied
			}
		}
		fresh, e := c.store.GetRelease(work, id)
		if e != nil {
			return r, e
		}
		phase(log, "dispatch", "resource_write_fenced")
		action, e := api.ExecuteAction(work, id, owner, fresh.Revision, intent, path)
		clear(intent.Body)
		if e != nil {
			if errors.Is(e, ErrOutcomeUnknown) {
				state = "outcome_unknown"
			} else if len(submitted) > 0 {
				state = "partial_failed"
			}
			return r, e
		}
		submitted = append(submitted, action)
		if r.Plan.CoreDNS != nil && resource.Probe {
			receipt, observeErr := c.observeResource(work, api, resource, 0)
			if receipt.Object != nil {
				if _, e = persistReleaseActionHealth(work, c.store, action.ID, owner, action.Revision, receipt); e != nil {
					return r, errors.Join(observeErr, e)
				}
			}
			if observeErr != nil {
				return r, observeErr
			}
		}
		if r.Plan.Progressive != nil && resource.ID == r.Plan.Progressive.Candidate && !resource.Delete {
			receipt, observeErr := c.observeResource(work, api, resource, r.Plan.ObserveSeconds)
			if receipt.Object != nil {
				if _, e = persistReleaseActionHealth(work, c.store, action.ID, owner, action.Revision, receipt); e != nil {
					return r, errors.Join(observeErr, e)
				}
			}
			if observeErr != nil {
				return r, observeErr
			}
		}
		// CRDs must establish before dependent resources can be discovered/dry-run.
		if resource.Health == "crd" {
			receipt, observeErr := c.observeResource(work, api, resource, 0)
			if receipt.Object != nil {
				if _, e = persistReleaseActionHealth(work, c.store, action.ID, owner, action.Revision, receipt); e != nil {
					return r, errors.Join(observeErr, e)
				}
			}
			if observeErr != nil {
				return r, observeErr
			}
		}
	}
	for i, resource := range resources {
		phase(log, "observe", "resource_health_check")
		action, e := c.store.GetReleaseAction(work, submitted[i].ID)
		if e != nil {
			return r, e
		}
		if action.State != "verified" {
			return r, ErrOutcomeUnknown
		}
		if resource.Delete {
			if e = c.observeReleaseDeletion(work, api, r, action); e != nil {
				return r, e
			}
			continue
		}
		receipt, observeErr := c.observeResource(work, api, resource, r.Plan.ObserveSeconds)
		if receipt.Object != nil {
			if _, e = persistReleaseActionHealth(work, c.store, action.ID, owner, action.Revision, receipt); e != nil {
				return r, errors.Join(observeErr, e)
			}
		}
		if observeErr != nil {
			return r, observeErr
		}
	}
	if e := c.recordReleaseVerifications(work, api, r); e != nil {
		return r, e
	}
	if r.Plan.CoreDNS != nil {
		if e := c.verifyCoreDNSPopulation(work, api, r.Plan); e != nil {
			return r, e
		}
	}
	if e := c.finishCoreDNSFinalization(work, api, r); e != nil {
		return r, e
	}
	if e := c.cleanupReleaseProbes(work, api, r, resources); e != nil {
		return r, e
	}
	if r.Plan.Mode == "progressive" {
		if e := c.verifyProgressiveService(work, api, r.Plan); e != nil {
			return r, e
		}
	}
	if r.Plan.Mode == "progressive" && r.Cursor+1 < len(r.Plan.Batches) {
		phase(log, "pause", "batch_ready_waiting_action")
		return r, ErrReleasePaused
	}
	state = "ready"
	reason = "release_ready"
	if r.Plan.RollbackOf != "" {
		if r.Plan.Progressive != nil {
			if e := c.observeRollbackService(work, api, r.Plan); e != nil {
				return r, e
			}
		}
		state = "rolled_back"
		reason = "rollback_complete"
		if len(r.Plan.Retained) > 0 {
			state = "partial_failed"
			reason = "rollback_partial"
			return r, ErrRollout
		}
	}
	phase(log, "complete", "release_ready")
	return r, nil
}
func strconvI(v int) string {
	if v == 0 {
		return "0"
	}
	digits := []byte{}
	for v > 0 {
		digits = append([]byte{byte('0' + v%10)}, digits...)
		v /= 10
	}
	return string(digits)
}

// PreviewRelease is the only public projection of a private plan.
type ReleaseChangePreview struct {
	Resource ReleaseResourceID    `json:"resource"`
	Action   string               `json:"action"`
	Images   []ReleaseImageUpdate `json:"images,omitempty"`
	Health   string               `json:"health"`
}
type ReleasePreview struct {
	Changes         []ReleaseChangePreview     `json:"changes,omitempty"`
	Batches         []int64                    `json:"batchReplicas,omitempty"`
	RollbackOf      string                     `json:"rollbackOf,omitempty"`
	Retained        []ReleaseRollbackRetention `json:"retained,omitempty"`
	Deletes         []ReleaseResourceID        `json:"deletes,omitempty"`
	CoreDNSCanary   string                     `json:"coreDNSCanary,omitempty"`
	CoreDNSFinalize bool                       `json:"coreDNSFinalize,omitempty"`
	HelmRelease     string                     `json:"helmRelease,omitempty"`
	HelmRevision    int                        `json:"helmRevision,omitempty"`
	HelmHooks       string                     `json:"helmHooks,omitempty"`
	ID              string                     `json:"id"`
	Revision        int64                      `json:"revision"`
	PlanID          string                     `json:"planId"`
	Mode            string                     `json:"mode"`
	Resources       []ReleaseResourceID        `json:"resources"`
	RequireApproval bool                       `json:"requireApproval"`
	TimeoutSeconds  int                        `json:"timeoutSeconds"`
	ObserveSeconds  int                        `json:"observeSeconds"`
}

func PreviewRelease(r ReleaseRecord) ReleasePreview {
	out := ReleasePreview{RollbackOf: r.Plan.RollbackOf, Retained: r.Plan.Retained, Deletes: r.Plan.Deletes, ID: r.ID, Revision: r.Revision, PlanID: r.PlanID, Mode: r.Plan.Mode, Resources: []ReleaseResourceID{}, RequireApproval: r.Plan.Authorization.RequireApproval || r.Plan.ForceApproval, TimeoutSeconds: r.Plan.TimeoutSeconds, ObserveSeconds: r.Plan.ObserveSeconds}
	for _, resource := range r.Plan.Resources {
		action := "create"
		for _, baseline := range r.Plan.Baseline {
			if baseline.ID == resource.ID {
				action = "patch"
			}
		}
		if resource.Delete {
			action = "delete"
		}
		if resource.VerifyOnly {
			action = "get"
		}
		change := ReleaseChangePreview{Resource: resource.ID, Action: action, Health: resource.Health}
		if r.Plan.Mode == "image" || r.Plan.Mode == "progressive" || resource.ImageOnly {
			pod := releaseMap(releaseMap(releaseMap(resource.Object["spec"])["template"])["spec"])
			for _, value := range releaseItems(pod["containers"]) {
				container := releaseMap(value)
				change.Images = append(change.Images, ReleaseImageUpdate{Container: releaseString(container["name"]), Image: releaseString(container["image"])})
			}
		}
		out.Changes = append(out.Changes, change)
	}
	if r.Plan.CoreDNS != nil {
		out.CoreDNSCanary = r.Plan.CoreDNS.Canary
		if r.Plan.CoreDNS.Finalize != nil {
			out.CoreDNSFinalize = true
		}

	}
	if r.Plan.Helm != nil {
		out.HelmRelease = r.Plan.Helm.Name
		out.HelmRevision = r.Plan.Helm.Revision
		out.HelmHooks = "disabled"
		if r.Plan.Helm.Hooks {
			out.HelmHooks = "approved-jobs"
		}
	}
	if r.Plan.Progressive != nil {
		for _, batch := range r.Plan.Batches {
			for _, resource := range batch {
				if resource.ID == r.Plan.Progressive.Candidate {
					out.Batches = append(out.Batches, releaseInt(releaseMap(resource.Object["spec"])["replicas"]))
				}
			}
		}
	}
	for _, resource := range r.Plan.Resources {
		out.Resources = append(out.Resources, resource.ID)
	}
	sort.Slice(out.Resources, func(i, j int) bool { return out.Resources[i].Key() < out.Resources[j].Key() })
	return out
}

func (c *ReleaseController) Capabilities(ctx context.Context, a ReleaseAuthorization, owner string) (out []ReleaseCapability, resultErr error) {
	api, e := c.preview(ctx, a, owner)
	if e != nil {
		return nil, e
	}
	defer func() { _, e := api.Close(context.WithoutCancel(ctx)); resultErr = errors.Join(resultErr, e) }()
	d := NewReleaseDiscovery()
	seen := map[string]bool{}
	for _, scope := range a.Request.Resources {
		path := "/api/" + scope.ID.Version
		if scope.ID.Group != "" {
			path = "/apis/" + scope.ID.Group + "/" + scope.ID.Version
		}
		if seen[path] {
			continue
		}
		seen[path] = true
		raw, e := api.Read(ctx, path, "")
		if e != nil {
			return nil, e
		}
		e = d.Add(raw)
		clear(raw)
		if e != nil {
			return nil, e
		}
	}
	return d.Capabilities(func(r ReleaseAPIResource) bool {
		for _, scope := range a.Request.Resources {
			if scope.ID.Group == r.Group && scope.ID.Version == r.Version && scope.ID.Resource == r.Name && scope.ID.Kind == r.Kind {
				return true
			}
		}
		return false
	}), nil
}

func (c *ReleaseController) VerifyReuse(ctx context.Context, id, planID string) (out ReleaseRecord, resultErr error) {
	r, e := c.store.GetRelease(ctx, id)
	if e != nil {
		return r, e
	}
	if !r.Successful() || r.PlanID != planID {
		return r, ErrConflict
	}
	if e = c.store.ValidateReleaseAuthorization(ctx, r.Plan.Authorization); e != nil {
		return r, e
	}
	readAuth, e := c.store.AuthorizeRelease(ctx, releaseReadRequest(r.Plan.Authorization))
	if e != nil {
		return r, e
	}
	api, e := c.preview(ctx, readAuth, "reuse_"+id)
	if e != nil {
		return r, e
	}
	defer func() { _, e := api.Close(context.WithoutCancel(ctx)); resultErr = errors.Join(resultErr, e) }()
	facts, e := c.store.ListReleaseActions(ctx, id)
	if e != nil {
		return r, e
	}
	for _, fact := range facts {
		if fact.State == "outcome_unknown" || fact.State == "submitted" {
			return r, ErrOutcomeUnknown
		}
	}
	resources := r.Plan.Resources
	if r.Plan.Mode == "progressive" && len(r.Plan.Batches) > 0 {
		resources = r.Plan.Batches[len(r.Plan.Batches)-1]
	}
	resources = append([]ReleaseResource(nil), resources...)
	if r.Plan.CoreDNS != nil && r.Plan.CoreDNS.Finalize != nil {
		for i := range resources {
			if resources[i].ID == r.Plan.CoreDNS.Finalize.ID {
				resources[i] = *r.Plan.CoreDNS.Finalize
			}
		}
	}
	resources = append(resources, r.Plan.Verify...)
	for _, resource := range resources {
		if resource.Delete || resource.Probe || resource.Ephemeral {
			if e = verifyReleaseSavedDeletion(ctx, api, r, facts, resource); e != nil {
				return r, e
			}
			continue
		}
		actual, e := readReleaseObject(ctx, api, resource.ID)
		if e != nil {
			return r, e
		}
		meta := releaseMap(actual["metadata"])
		if meta["deletionTimestamp"] != nil {
			return r, ErrSuperseded
		}
		uid := releaseString(meta["uid"])
		if uid == "" {
			return r, ErrSuperseded
		}
		verified := false
		for _, fact := range facts {
			if !releaseReuseFact(r, fact) || fact.Intent.Verb == "delete" || fact.Receipt == nil {
				continue
			}
			if fact.Intent.Resource == resource.ID && fact.Receipt.Resource == resource.ID && fact.Receipt.UID == uid && releaseMap(meta["annotations"])[ReleaseActionAnnotation] == fact.ID && fact.Receipt.ObservedAt > 0 && (fact.Receipt.Health.State == "ready" || fact.Receipt.Health.State == "declared") {
				planned, ok := releasePlannedActionResource(r.Plan, fact)
				if !ok || planned.ID != resource.ID {
					continue
				}
				if r.Plan.CoreDNS != nil && r.Plan.CoreDNS.Finalize != nil && resource.ID == r.Plan.CoreDNS.Finalize.ID && fact.Intent.StepKey != releaseCoreDNSFinalizeStep {
					continue
				}
				resource.ExpectedUID = uid
				verified = true
				break
			}
			// Read-only dependencies are anchored by separately sealed check evidence,
			// never by a same-name current object or a plan expectation alone.
			for _, check := range fact.Receipt.Checks {
				if resource.ExpectedUID != "" && check.Resource == resource.ID && check.UID == resource.ExpectedUID && check.UID == uid && check.ObservedAt > 0 && (check.Health.State == "ready" || check.Health.State == "declared") {
					verified = true
					break
				}
			}
			if verified {
				break
			}
		}
		if !verified {
			return r, ErrSuperseded
		}
		if _, e = c.observeResource(ctx, api, resource, 0); e != nil {
			return r, e
		}
	}
	if r.Plan.CoreDNS != nil {
		if e = c.verifyCoreDNSPopulation(ctx, api, r.Plan); e != nil {
			return r, e
		}
	}
	return r, nil
}
func releaseReuseFact(parent ReleaseRecord, fact ReleaseAction) bool {
	return fact.ReleaseID == parent.ID && fact.Intent.ParentID == parent.PlanID && fact.Intent.AuthorizationFingerprint == parent.Plan.Authorization.Fingerprint && fact.State == "verified"
}
func verifyReleaseSavedDeletion(ctx context.Context, api releaseAPI, parent ReleaseRecord, facts []ReleaseAction, resource ReleaseResource) error {
	deleted := false
	for _, cleanup := range facts {
		if !releaseReuseFact(parent, cleanup) || cleanup.Intent.Resource != resource.ID || cleanup.Intent.Verb != "delete" || cleanup.Receipt == nil || !cleanup.Receipt.Deleted || cleanup.Receipt.Resource != resource.ID || cleanup.Intent.ExpectedUID == "" || cleanup.Receipt.UID != cleanup.Intent.ExpectedUID {
			continue
		}
		if resource.Delete {
			deleted = true
			break
		}
		for _, created := range facts {
			if !releaseReuseFact(parent, created) || created.Intent.Resource != resource.ID || created.Intent.Verb == "delete" || created.Receipt == nil || created.Receipt.UID != cleanup.Intent.ExpectedUID || created.Receipt.Resource != resource.ID {
				continue
			}
			if created.Receipt.Health.State == "ready" || resource.Ephemeral && created.Receipt.Health.State == "declared" {
				deleted = true
				break
			}
		}
		if deleted {
			break
		}
	}
	if !deleted {
		return ErrConflict
	}
	if _, e := readReleaseObject(ctx, api, resource.ID); !errors.Is(e, ErrNotFound) {
		if e == nil {
			return ErrSuperseded
		}
		return e
	}
	return nil
}

func releaseFailureState(facts []ReleaseAction) string {
	state := "failed"
	for _, fact := range facts {
		if fact.State == "verified" {
			state = "partial_failed"
		}
		if fact.State == "outcome_unknown" || fact.State == "submitted" {
			return "outcome_unknown"
		}
	}
	return state
}
