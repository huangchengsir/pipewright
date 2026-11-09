package kubernetes

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"github.com/huangchengsir/pipewright/internal/kubeprobe"
	"github.com/huangchengsir/pipewright/internal/kubeprotocol"
	"github.com/huangchengsir/pipewright/internal/pipeline"
	"net"
	"net/url"
	"sort"
	"strings"
)

const ReleaseRefreshAnnotation = "pipewright.io/config-refresh"

func mergeReleaseScopes(groups ...[]ReleaseAccess) []ReleaseAccess {
	byID := map[ReleaseResourceID]ReleaseAccess{}
	for _, group := range groups {
		for _, scope := range group {
			value := byID[scope.ID]
			value.ID = scope.ID
			value.Actions = uniqueReleaseActions(append(value.Actions, scope.Actions...))
			value.Containers = uniqueReleaseActions(append(value.Containers, scope.Containers...))
			value.Fields = uniqueReleaseActions(append(value.Fields, scope.Fields...))
			byID[scope.ID] = value
		}
	}
	out := []ReleaseAccess{}
	for _, scope := range byID {
		out = append(out, scope)
	}
	return canonicalReleaseAccess(out)
}
func checkResourceID(ref pipeline.KubernetesCheckResource, namespace string) ReleaseResourceID {
	if ref.Namespace != "" {
		namespace = ref.Namespace
	}
	return ReleaseResourceID{Group: ref.Group, Version: ref.Version, Resource: ref.Resource, Kind: ref.Kind, Namespace: namespace, Name: ref.Name}
}
func (c *ReleaseController) appendReleaseChecks(ctx context.Context, api releaseAPI, plan *ReleasePlan, catalog ReleaseAuthorization, config []byte) ([]ReleaseAccess, error) {
	var cfg pipeline.KubernetesReleaseConfig
	if json.Unmarshal(config, &cfg) != nil {
		return nil, ErrInvalid
	}
	extra := []ReleaseAccess{}
	checks := map[string]pipeline.KubernetesReleaseCheck{}
	for _, check := range cfg.Checks {
		if (check.Kind == "storage" || check.Kind == "network-deny") && check.WaitSeconds != 0 {
			return nil, ErrInvalid
		}
		namespace := check.Namespace
		if namespace == "" {
			namespace = cfg.Namespace
		}
		key := namespace + "/" + check.Name
		if _, ok := checks[key]; ok {
			return nil, ErrInvalid
		}
		checks[key] = check
	}
	// Positive network checks are scheduled before their explicitly paired negative.
	ordered := append([]pipeline.KubernetesReleaseCheck(nil), cfg.Checks...)
	sort.SliceStable(ordered, func(i, j int) bool { return ordered[i].Kind != "network-deny" && ordered[j].Kind == "network-deny" })
	for _, check := range ordered {
		namespace := check.Namespace
		if namespace == "" {
			namespace = cfg.Namespace
		}
		if check.Kind == "workload" || check.Kind == "condition" {
			count := 0
			for index := range plan.Resources {
				resource := &plan.Resources[index]
				if check.Resource != nil && checkResourceID(*check.Resource, namespace) != resource.ID {
					continue
				}
				if check.Kind == "workload" && resource.Health != "workload" && resource.ID.Kind != "CronJob" {
					continue
				}
				if resource.ID.Name != check.Name || resource.ID.Namespace != namespace {
					continue
				}
				count++
				if check.Kind == "workload" && resource.ID.Kind == "CronJob" {
					access, e := c.appendCronJobRun(ctx, api, plan, *resource, check.Target, check.Restart)
					if e != nil {
						return nil, e
					}
					extra = append(extra, access)
					continue
				}
				if check.Kind == "workload" && resource.Health != "workload" && resource.ID.Kind != "CronJob" {
					return nil, ErrInvalid
				}
				if check.Kind == "condition" {
					predicate := ReleasePredicate{Predicate: check.Predicate, Expected: check.Expected}
					exists := false
					for _, previous := range resource.Predicates {
						exists = exists || previous == predicate
					}
					if !exists {
						resource.Predicates = append(resource.Predicates, predicate)
					}
				}
				if check.Restart {
					if e := restartReleaseResource(resource); e != nil {
						return nil, e
					}
					access := releaseRequestedAccess(resource.ID, "patch")
					access.Containers = releaseContainerNames(*resource)
					access.Fields = []string{"/spec/template/metadata/annotations/pipewright.io~1config-refresh"}
					extra = append(extra, access)
				}
			}
			if count > 1 {
				return nil, ErrInvalid
			}
			if count == 1 {
				continue
			}
			if check.Resource == nil {
				return nil, ErrInvalid
			}
			id := checkResourceID(*check.Resource, namespace)
			if id.Namespace != namespace {
				return nil, ErrInvalid
			}
			if id.Name != check.Name || !validReleaseAccess(ReleaseAccess{ID: id, Actions: []string{"get"}}) {
				return nil, ErrInvalid
			}
			object, e := readReleaseObject(ctx, api, id)
			if e != nil {
				return nil, e
			}
			adapter := ReleaseAdapterFor(id.Group, id.Kind)
			if check.Kind == "workload" && adapter.Health != "workload" && id.Kind != "CronJob" {
				return nil, ErrInvalid
			}
			resource := releaseResourceFromObject(id, releaseCanonicalObject(object), adapter.Health)
			resource.ExpectedUID = releaseString(releaseMap(object["metadata"])["uid"])
			if check.Kind == "condition" {
				resource.Predicates = []ReleasePredicate{{Predicate: check.Predicate, Expected: check.Expected}}
			}
			if adapter.Health == "custom" {
				raw, e := api.Read(ctx, releaseSchemaPath(id), "")
				if e != nil {
					return nil, e
				}
				if !releaseSchemaAvailable(raw, id) {
					clear(raw)
					return nil, ErrDiscovery
				}
				resource.SchemaDigest = releaseByteDigest(raw)
				clear(raw)
			}
			if check.Kind == "workload" && id.Kind == "CronJob" {
				access, e := c.appendCronJobRun(ctx, api, plan, resource, check.Target, check.Restart)
				if e != nil {
					return nil, e
				}
				extra = append(extra, access, ReleaseAccess{ID: id, Actions: []string{"get"}})
				plan.Verify = append(plan.Verify, resource)
				continue
			}
			access := releaseRequestedAccess(id, "get")
			access.Actions = uniqueReleaseActions(access.Actions)
			if check.Restart {
				if e = restartReleaseResource(&resource); e != nil {
					return nil, e
				}
				resource.ExpectedUID = ""
				resource.Supplemental = true
				plan.Resources = append(plan.Resources, resource)
				plan.Baseline = append(plan.Baseline, releaseResourceFromObject(id, object, adapter.Health))
				access = releaseRequestedAccess(id, "patch")
				access.Containers = releaseContainerNames(resource)
				access.Fields = []string{"/spec/template/metadata/annotations/pipewright.io~1config-refresh"}
			} else {
				plan.Verify = append(plan.Verify, resource)
			}
			extra = append(extra, access)
			continue
		}
		if !validName(check.Name, 63) || !validName(namespace, 63) || check.Target == "" {
			return nil, ErrInvalid
		}
		id := ReleaseResourceID{Group: "batch", Version: "v1", Resource: "jobs", Kind: "Job", Namespace: namespace, Name: check.Name}
		for _, resource := range plan.Resources {
			if resource.ID == id {
				return nil, ErrInvalid
			}
		}
		if _, e := readReleaseObject(ctx, api, id); e == nil {
			return nil, ErrInUse
		} else if !errors.Is(e, ErrNotFound) {
			return nil, e
		}
		spec := kubeprobe.Spec{WaitSeconds: check.WaitSeconds, Kind: check.Kind, Target: check.Target, Expected: check.Expected, Protocol: check.Protocol, Server: check.Server, TimeoutSeconds: 5}
		if check.WaitSeconds < 0 || check.WaitSeconds > 600 || check.WaitSeconds >= plan.TimeoutSeconds {
			return nil, ErrInvalid
		}
		if check.Kind == "network-deny" {
			spec.TimeoutSeconds = 3
			control, ok := checks[namespace+"/"+check.PositiveControl]
			if !ok || control.Kind != "network-allow" || control.Target != check.Target {
				return nil, ErrInvalid
			}
			host, _, e := net.SplitHostPort(check.Target)
			if e != nil || net.ParseIP(host) == nil {
				return nil, ErrInvalid
			}
		}
		if check.Kind == "http" {
			target, e := url.Parse(check.Target)
			if e != nil || target.User != nil || target.Host == "" || target.Fragment != "" || target.Scheme != "http" && target.Scheme != "https" {
				return nil, ErrInvalid
			}
		}
		if check.Kind == "dns" {
			host, _, e := net.SplitHostPort(check.Server)
			if e != nil || net.ParseIP(host) == nil || net.ParseIP(check.Expected) == nil || check.Protocol != "udp" && check.Protocol != "tcp" {
				return nil, ErrInvalid
			}
		}
		if check.Kind == "tcp" || check.Kind == "network-allow" {
			host, port, e := net.SplitHostPort(check.Target)
			if e != nil || host == "" || port == "" {
				return nil, ErrInvalid
			}
		}
		if check.Kind == "storage" {
			if !validDeploymentName(check.Target) {
				return nil, ErrInvalid
			}
			claimID := ReleaseResourceID{Version: "v1", Resource: "persistentvolumeclaims", Kind: "PersistentVolumeClaim", Namespace: namespace, Name: check.Target}
			declared := false
			for _, resource := range plan.Resources {
				if resource.ID == claimID {
					declared = true
					if releaseMap(resource.Object["spec"])["volumeMode"] == "Block" {
						return nil, ErrInvalid
					}
				}
			}
			if !declared {
				claim, e := readReleaseObject(ctx, api, claimID)
				if e != nil {
					return nil, e
				}
				if releaseMap(claim["spec"])["volumeMode"] == "Block" || releaseMap(claim["metadata"])["deletionTimestamp"] != nil {
					return nil, ErrInvalid
				}
				frozen := releaseResourceFromObject(claimID, releaseCanonicalObject(claim), "pvc")
				frozen.ExpectedUID = releaseString(releaseMap(claim["metadata"])["uid"])
				plan.Verify = append(plan.Verify, frozen)
			}
			extra = append(extra, ReleaseAccess{ID: claimID, Actions: []string{"get", "probe"}})
			spec.Target = "/data/pw-check-" + uuid.NewString()
			spec.Expected = uuid.NewString()
		}
		body, _ := json.Marshal(spec)
		labels := map[string]any{"pipewright.io/probe": "true"}
		for key, value := range check.Labels {
			if strings.HasPrefix(key, "pipewright.io/") {
				return nil, ErrInvalid
			}
			labels[key] = value
		}
		account := check.ServiceAccount
		if account == "" {
			account = "default"
		}
		pod := map[string]any{"restartPolicy": "Never", "automountServiceAccountToken": false, "serviceAccountName": account, "securityContext": map[string]any{"runAsNonRoot": true, "runAsUser": 1000, "runAsGroup": 1000, "fsGroup": 1000, "seccompProfile": map[string]any{"type": "RuntimeDefault"}}, "nodeSelector": map[string]any{"kubernetes.io/os": "linux", "kubernetes.io/arch": catalog.Profile.Profile.Runtime.Architecture}, "containers": []any{map[string]any{"name": "probe", "image": catalog.Profile.Profile.Runtime.Image, "imagePullPolicy": "IfNotPresent", "command": []any{catalog.Profile.Profile.HelperPath, "probe", string(body)}, "securityContext": map[string]any{"allowPrivilegeEscalation": false, "readOnlyRootFilesystem": true, "capabilities": map[string]any{"drop": []any{"ALL"}}}, "resources": map[string]any{"requests": map[string]any{"cpu": "10m", "memory": "16Mi"}, "limits": map[string]any{"cpu": "100m", "memory": "64Mi"}}}}}
		if len(check.ImagePullSecrets) > 0 {
			secrets := []any{}
			for _, name := range check.ImagePullSecrets {
				secrets = append(secrets, map[string]any{"name": name})
			}
			pod["imagePullSecrets"] = secrets
		}
		if check.Kind == "storage" {
			pod["volumes"] = []any{map[string]any{"name": "data", "persistentVolumeClaim": map[string]any{"claimName": check.Target}}}
			releaseMap(releaseItems(pod["containers"])[0])["volumeMounts"] = []any{map[string]any{"name": "data", "mountPath": "/data"}}
		}
		object := map[string]any{"apiVersion": "batch/v1", "kind": "Job", "metadata": map[string]any{"name": check.Name, "namespace": namespace, "labels": labels}, "spec": map[string]any{"backoffLimit": 0, "activeDeadlineSeconds": min(max(120, check.WaitSeconds+10), plan.TimeoutSeconds), "ttlSecondsAfterFinished": 3600, "template": map[string]any{"metadata": map[string]any{"labels": labels}, "spec": pod}}}
		resource := releaseResourceFromObject(id, object, "workload")
		resource.Phase = 4
		resource.Supplemental = true
		resource.Probe = true
		resource.PositiveControl = check.PositiveControl
		plan.Resources = append(plan.Resources, resource)
		plan.Deletes = append(plan.Deletes, id)
		extra = append(extra, ReleaseAccess{ID: id, Actions: []string{"get", "create", "delete", "probe", "related-pods"}, Containers: []string{"probe"}})
	}
	return extra, nil
}
func restartReleaseResource(resource *ReleaseResource) error {
	if resource.ID.Kind != "Deployment" && resource.ID.Kind != "StatefulSet" && resource.ID.Kind != "DaemonSet" {
		return ErrInvalid
	}
	template := releaseMap(releaseMap(resource.Object["spec"])["template"])
	meta := releaseMap(template["metadata"])
	if meta == nil {
		meta = map[string]any{}
		template["metadata"] = meta
	}
	annotations := releaseMap(meta["annotations"])
	if annotations == nil {
		annotations = map[string]any{}
		meta["annotations"] = annotations
	}
	annotations[ReleaseRefreshAnnotation] = uuid.NewString()
	resource.Supplemental = true
	resource.Expected = nil
	body, _ := json.Marshal(resource.Object)
	resource.Digest = releaseByteDigest(body)
	return validateReleaseWorkloadStrategy(*resource)
}
func (c *ReleaseController) dryRunReleaseResources(ctx context.Context, owner string, plan *ReleasePlan) (resultErr error) {
	if e := c.prepareDaemonRevisionProof(ctx, plan); e != nil {
		return e
	}
	api, e := c.preview(ctx, plan.Authorization, owner)
	if e != nil {
		return e
	}
	defer func() { _, e := api.Close(context.WithoutCancel(ctx)); resultErr = errors.Join(resultErr, e) }()
	if plan.Helm != nil && plan.MutationVersion == 1 {
		if session, ok := api.(*ReleaseSession); ok {
			if e := session.requireHelmCapabilities(ctx, plan.Helm.Hooks); e != nil {
				return e
			}
		}
	}
	if e := checkDaemonRevisionRead(ctx, api, *plan); e != nil {
		return e
	}
	batches := [][]ReleaseResource{plan.Resources}
	if plan.Helm != nil {
		batches = append([][]ReleaseResource{plan.Helm.Rendered}, batches...)
	}
	if plan.Mode == "progressive" {
		batches = plan.Batches
	}
	for batchIndex, batch := range batches {
		for index, resource := range batch {
			if resource.RequiredCRD != nil || resource.Delete || resource.VerifyOnly {
				continue
			}
			method := "POST"
			name := ""
			for _, baseline := range plan.Baseline {
				if baseline.ID == resource.ID {
					method = "PATCH"
					name = resource.ID.Name
					break
				}
			}
			path, e := resourceAPI(resource.ID).Path(resource.ID.Namespace, name)
			if e != nil {
				return e
			}
			var baselineObject map[string]any
			for _, baseline := range plan.Baseline {
				if baseline.ID == resource.ID {
					baselineObject = baseline.Object
					break
				}
			}
			if plan.MutationVersion == 1 && plan.CoreDNS != nil && method == "PATCH" && resource.Mutation == nil {
				mutation, e := releaseCoreDNSMutation(*plan, resource, baselineObject)
				if e != nil {
					return e
				}
				resource.Mutation = mutation
			}
			if plan.MutationVersion == 1 && method == "PATCH" && resource.Mutation == nil && c.store != nil {
				mutation, e := c.store.ownedPostMutationForHelm(ctx, plan.Authorization, resource, baselineObject, plan.Helm)
				if e != nil {
					return e
				}
				resource.Mutation = mutation
			}
			if plan.MutationVersion == 1 && plan.Mode == "progressive" && method == "POST" && resource.Mutation == nil {
				mutation, e := releaseProgressiveMutation(*plan, batchIndex, resource)
				if e != nil {
					return e
				}
				resource.Mutation = mutation
			}
			if !validReleaseMutation(resource) {
				return ErrInvalid
			}
			object := releaseMutationObject(resource)
			contentType := ""
			if resource.Mutation != nil && resource.Mutation.Strategy == releasePriorCreateMerge {
				// The future object is not created during preview. Validate admission by
				// POST now; its frozen merge is dry-run again against the real UID at use.
				object = releaseObjectCopy(resource.Object)
			} else if resource.Mutation != nil {
				contentType = resource.Mutation.ContentType
				meta := releaseMap(object["metadata"])
				meta["uid"], meta["resourceVersion"] = releaseMap(baselineObject["metadata"])["uid"], releaseMap(baselineObject["metadata"])["resourceVersion"]
				if meta["uid"] != resource.Mutation.UID {
					return ErrSuperseded
				}
			}
			body, _ := json.Marshal(object)
			admitted, e := api.DryRun(ctx, kubeprotocol.Request{Method: method, Path: path, Body: body, ContentType: contentType})
			clear(body)
			if e != nil {
				return e
			}
			if !releaseJobGeneratedLabelsMatch(resource, admitted) || !releaseApprovedFieldsMatch(resource.ID, releaseComparableActionBody(resource.Object), releaseComparableActionBody(admitted)) {
				return errReleaseAdmissionMismatch
			}
			resource.Expected = releaseAdmittedExpected(resource, admitted)
			batch[index] = resource
		}
		if plan.Helm != nil && batchIndex == 0 {
			plan.Helm.Rendered = batch
		} else if plan.Mode == "progressive" {
			plan.Batches[batchIndex] = batch
		} else {
			plan.Resources = batch
		}
	}
	if plan.Mode == "progressive" {
		plan.Resources = plan.Batches[len(plan.Batches)-1]
	}
	if e := c.prepareCoreDNSFinalization(ctx, api, plan); e != nil {
		return e
	}
	return c.authorizeReleaseAdministrativeDeltas(ctx, plan)
}

// CronJob configuration does not imply a successful scheduled execution. An
// explicit check freezes a separately named Job, with its own create/read/Pod
// grant and UID-bound completion facts. It is never a namespace-wide Job read.
func (c *ReleaseController) appendCronJobRun(ctx context.Context, api releaseAPI, plan *ReleasePlan, cron ReleaseResource, name string, restart bool) (ReleaseAccess, error) {
	if restart || !validName(name, 63) || cron.ID.Group != "batch" || cron.ID.Kind != "CronJob" {
		return ReleaseAccess{}, ErrInvalid
	}
	id := ReleaseResourceID{Group: "batch", Version: "v1", Resource: "jobs", Kind: "Job", Namespace: cron.ID.Namespace, Name: name}
	for _, resource := range plan.Resources {
		if resource.ID == id {
			return ReleaseAccess{}, ErrInUse
		}
	}
	if _, e := readReleaseObject(ctx, api, id); e == nil {
		return ReleaseAccess{}, ErrInUse
	} else if !errors.Is(e, ErrNotFound) {
		return ReleaseAccess{}, e
	}
	template := releaseMap(releaseMap(cron.Object["spec"])["jobTemplate"])
	spec := releaseMap(template["spec"])
	if spec == nil {
		return ReleaseAccess{}, ErrInvalid
	}
	meta := releaseObjectCopy(releaseMap(template["metadata"]))
	if meta == nil {
		meta = map[string]any{}
	}
	meta["name"] = name
	meta["namespace"] = cron.ID.Namespace
	for _, key := range []string{"uid", "resourceVersion", "generation", "creationTimestamp", "ownerReferences", "finalizers", "generateName"} {
		delete(meta, key)
	}
	annotations := releaseMap(meta["annotations"])
	if annotations == nil {
		annotations = map[string]any{}
		meta["annotations"] = annotations
	}
	delete(annotations, ReleaseActionAnnotation)
	delete(annotations, OperationAnnotation)
	annotations["cronjob.kubernetes.io/instantiate"] = "manual"
	object := map[string]any{"apiVersion": "batch/v1", "kind": "Job", "metadata": meta, "spec": releaseObjectCopy(spec)}
	resource := releaseResourceFromObject(id, object, "workload")
	resource.Supplemental = true
	resource.Phase = 3
	plan.Resources = append(plan.Resources, resource)
	access := releaseRequestedAccess(id, "create")
	access.Containers = releaseContainerNames(resource)
	return access, nil
}
