package kubernetes

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/huangchengsir/pipewright/internal/kubeprotocol"
	"strings"
	"time"
)

func (s *ReleaseSession) permitHelmPending(ctx context.Context, parent ReleaseRecord, pending kubeprotocol.PendingWrite, index int) (ReleaseAction, error) {
	request := pending.Request
	rawRequest, digestErr := json.Marshal(request)
	validDigest := digestErr == nil && releaseByteDigest(rawRequest) == pending.Digest
	clear(rawRequest)
	if !validDigest || !validID(pending.ID) {
		return ReleaseAction{}, ErrDenied
	}
	verb := ""
	switch request.Method {
	case "POST":
		verb = "create"
	case "PUT":
		verb = "update"
	case "PATCH":
		if request.ContentType != "application/apply-patch+yaml" && request.ContentType != "" {
			return ReleaseAction{}, ErrDenied
		}
		verb = "patch"
	case "DELETE":
		verb = "delete"
	default:
		return ReleaseAction{}, ErrDenied
	}
	var object map[string]any
	if json.Unmarshal(request.Body, &object) != nil {
		return ReleaseAction{}, ErrInvalid
	}
	suppliedRV := releaseString(releaseMap(object["metadata"])["resourceVersion"])
	object = releaseCanonicalObject(object)
	if normalizeReleaseSecret(object) != nil {
		return ReleaseAction{}, ErrInvalid
	}
	meta := releaseMap(object["metadata"])
	var id ReleaseResourceID
	found := false
	candidates := append([]ReleaseResource(nil), parent.Plan.Resources...)
	if parent.Plan.Helm != nil {
		anchor := parent.Plan.Helm.StorageAnchor
		name := releaseString(meta["name"])
		anchor.Name = name
		if releaseHelmStorage(parent.Plan, anchor) {
			candidates = append(candidates, ReleaseResource{ID: anchor})
		}
	}
	for _, candidate := range candidates {
		expectedName := candidate.ID.Name
		if verb == "create" {
			expectedName = ""
		}
		path, e := resourceAPI(candidate.ID).Path(candidate.ID.Namespace, expectedName)
		if e == nil && path == request.Path && (verb == "delete" || meta["name"] == candidate.ID.Name) {
			id = candidate.ID
			found = true
			break
		}
	}
	if !found {
		return ReleaseAction{}, ErrDenied
	}
	var mutationResource *ReleaseResource
	if verb == "create" || verb == "patch" {
		var mutationErr error
		mutationResource, mutationErr = releaseHelmPendingMutation(parent, id, object)
		if mutationErr != nil {
			return ReleaseAction{}, mutationErr
		}
		if mutationResource != nil && verb != "patch" {
			return ReleaseAction{}, ErrDenied
		}
	}
	current, e := readReleaseObject(ctx, s, id)
	var baseline map[string]any
	for _, resource := range parent.Plan.Baseline {
		if resource.ID == id {
			baseline = resource.Object
		}
	}
	if parent.Plan.Helm != nil {
		for _, resource := range parent.Plan.Helm.Storage {
			if resource.ID == id {
				baseline = resource.Object
			}
		}
	}
	facts, readErr := s.runtime.store.ListReleaseActions(ctx, parent.ID)
	if readErr != nil {
		return ReleaseAction{}, readErr
	}
	for _, fact := range facts {
		if fact.State == "outcome_unknown" || fact.State == "submitted" {
			return ReleaseAction{}, ErrOutcomeUnknown
		}
		if fact.Intent.Resource == id && fact.State == "verified" && fact.Receipt != nil {
			baseline = fact.Receipt.Object
		}
	}
	createOnly := false
	if errors.Is(e, ErrNotFound) {
		if baseline == nil && verb == "patch" && (request.ContentType == "application/apply-patch+yaml" || request.ContentType == "") && suppliedRV == "" {
			verb = "create"
			createOnly = true
		}
		if baseline != nil || verb != "create" {
			return ReleaseAction{}, ErrSuperseded
		}
	} else if e != nil {
		return ReleaseAction{}, e
	} else {
		if baseline == nil || verb == "create" || releaseMap(current["metadata"])["uid"] != releaseMap(baseline["metadata"])["uid"] || !ReleaseObjectMatches(releaseCanonicalObject(baseline), releaseCanonicalObject(current)) {
			return ReleaseAction{}, ErrSuperseded
		}
	}
	if verb == "delete" {
		for _, resource := range parent.Plan.Resources {
			if resource.ID != id || releaseMap(releaseMap(resource.Object["metadata"])["annotations"])["helm.sh/hook"] == nil {
				continue
			}
			for i := len(facts) - 1; i >= 0; i-- {
				fact := facts[i]
				if fact.Intent.Resource != id || fact.Intent.Verb == "delete" || fact.State != "verified" {
					continue
				}
				related := []map[string]any{}
				if resource.ID.Kind == "Job" {
					raw, e := s.Read(ctx, "/api/v1/namespaces/"+resource.ID.Namespace+"/pods", "limit=200")
					if e != nil {
						return ReleaseAction{}, e
					}
					var list struct {
						Items []map[string]any `json:"items"`
					}
					if json.Unmarshal(raw, &list) != nil {
						clear(raw)
						return ReleaseAction{}, ErrInvalid
					}
					clear(raw)
					related = list.Items
				}
				health := EvaluateReleaseHealth(resource, current, related)
				if health.State != "ready" && health.State != "failed" {
					return ReleaseAction{}, ErrRollout
				}
				receipt := ReleaseActionReceipt{Resource: id, UID: releaseString(releaseMap(current["metadata"])["uid"]), ResourceVersion: releaseString(releaseMap(current["metadata"])["resourceVersion"]), Object: current, Health: health}
				if _, e := persistReleaseActionHealth(ctx, s.runtime.store, fact.ID, parent.OwnerEpoch, fact.Revision, receipt); e != nil {
					return ReleaseAction{}, e
				}
				break
			}
		}
	}
	intent := ReleaseActionIntent{MutationVersion: parent.Plan.MutationVersion, Version: 2, ParentID: parent.PlanID, StepKey: "helm-" + strconvI(index), Resource: id, Verb: verb, AuthorizationFingerprint: parent.Plan.Authorization.Fingerprint}
	if verb != "create" {
		intent.ExpectedUID = releaseString(releaseMap(current["metadata"])["uid"])
		intent.ExpectedRV = releaseString(releaseMap(current["metadata"])["resourceVersion"])
	}
	if verb == "delete" {
		object = map[string]any{"apiVersion": "v1", "kind": "DeleteOptions", "propagationPolicy": "Foreground", "preconditions": map[string]any{"uid": intent.ExpectedUID, "resourceVersion": intent.ExpectedRV}}
	} else {
		if meta == nil {
			return ReleaseAction{}, ErrInvalid
		}
		if id.Namespace != "" && meta["namespace"] == nil {
			meta["namespace"] = id.Namespace
		}
		if verb != "create" {
			if suppliedRV != "" && suppliedRV != intent.ExpectedRV {
				return ReleaseAction{}, ErrSuperseded
			}
			meta["uid"] = intent.ExpectedUID
			meta["resourceVersion"] = intent.ExpectedRV
		}
	}
	if mutationResource != nil {
		if e := releaseMutationCurrent(parent, *mutationResource, current, facts); e != nil {
			return ReleaseAction{}, e
		}
		object = releaseMutationObject(*mutationResource)
		meta = releaseMap(object["metadata"])
		meta["uid"], meta["resourceVersion"] = intent.ExpectedUID, intent.ExpectedRV
		intent.ContentType = mutationResource.Mutation.ContentType
		body, _ := json.Marshal(object)
		admitted, e := s.DryRun(ctx, kubeprotocol.Request{Method: "PATCH", Path: request.Path, ContentType: intent.ContentType, Body: body})
		clear(body)
		if e != nil {
			return ReleaseAction{}, e
		}
		if !releaseResourceExpectedMatches(*mutationResource, admitted) {
			return ReleaseAction{}, errReleaseAdmissionMismatch
		}
	}
	intent.Body, _ = json.Marshal(object)
	defer clear(intent.Body)
	fresh, e := s.runtime.store.GetRelease(ctx, parent.ID)
	if e != nil {
		return ReleaseAction{}, e
	}
	action, e := s.runtime.store.PrepareReleaseAction(ctx, parent.ID, parent.OwnerEpoch, fresh.Revision, intent)
	if e != nil {
		return action, e
	}
	stream := "permit-" + action.ID
	if e = s.Upload(ctx, stream, action.Intent.Body); e != nil {
		return action, ErrOutcomeUnknown
	}
	if e = s.checkIdentity(ctx, true); e != nil {
		return action, ErrOutcomeUnknown
	}
	if e = s.runtime.store.ValidateReleaseActionPermit(ctx, parent.ID, action.ID, parent.OwnerEpoch); e != nil {
		return action, ErrOutcomeUnknown
	}
	permit := &kubeprotocol.Permit{RequestID: pending.ID, ActionID: action.ID, Digest: pending.Digest, Allow: true, Expires: time.Now().Add(5 * time.Second).UnixMilli(), BodyStream: stream, CreateOnly: createOnly}
	if intent.ContentType != "" {
		permit.ContentType = intent.ContentType
		permit.FieldManager = releaseFieldManager
	}
	if parent.Plan.MutationVersion == 1 && (createOnly || request.Method == "POST") {
		permit.FieldManager = releaseFieldManager
	}
	result, e := s.RPC(ctx, kubeprotocol.Frame{Version: 2, Command: "permit", Permit: permit})
	if e != nil || result.Code != "ok" {
		return action, ErrOutcomeUnknown
	}
	return action, nil
}
func (s *ReleaseSession) RunReleaseHelm(ctx context.Context, parent ReleaseRecord) error {
	helm := parent.Plan.Helm
	if helm == nil {
		return ErrInvalid
	}
	existing, e := s.runtime.store.ListReleaseActions(ctx, parent.ID)
	if e != nil {
		return e
	}
	for _, fact := range existing {
		safe := false
		for _, resource := range parent.Plan.Resources {
			if resource.HelmCRD && fact.Intent.Resource == resource.ID && fact.State == "verified" {
				safe = true
			}
		}
		if !safe {
			return ErrOutcomeUnknown
		}
	}
	if helm.Action != "rollback" {
		if e = s.Upload(ctx, "chart", parent.Plan.Chart); e != nil {
			return e
		}
	}
	task := kubeprotocol.HelmTask{Action: helm.Action, Revision: helm.TargetRevision, Name: helm.Name, Namespace: helm.Namespace, ChartStream: "chart", Hooks: helm.Hooks, TimeoutSeconds: parent.Plan.TimeoutSeconds}
	if len(parent.Plan.Values) > 0 {
		if e = s.Upload(ctx, "values", parent.Plan.Values); e != nil {
			return e
		}
		task.ValuesStream = "values"
	}
	started, e := s.RPC(ctx, kubeprotocol.Frame{Version: 2, Command: "helm-start", Helm: &task})
	if e != nil || started.Code != "ok" {
		return ErrTool
	}
	actions := map[string]ReleaseAction{}
	index := 0
	for {
		reply, e := s.RPC(ctx, kubeprotocol.Frame{Version: 2, Command: "poll"})
		if e != nil || reply.Code != "ok" {
			return ErrOutcomeUnknown
		}
		if reply.Result != nil {
			result := reply.Result
			action, ok := actions[result.ActionID]
			if !ok {
				clear(result.Body)
				return ErrOutcomeUnknown
			}
			receipt := ReleaseActionReceipt{Resource: action.Intent.Resource, Health: ReleaseHealth{State: "pending", Reason: "resource_not_observed"}}
			state := "outcome_unknown"
			if result.Code == "" && result.Status >= 200 && result.Status < 300 {
				state = "verified"
				if action.Intent.Verb == "delete" {
					receipt.UID = action.Intent.ExpectedUID
					receipt.ResourceVersion = action.Intent.ExpectedRV
					receipt.Health = ReleaseHealth{State: "declared", Reason: "delete_accepted"}
				} else if json.Unmarshal(result.Body, &receipt.Object) == nil {
					m := releaseMap(receipt.Object["metadata"])
					receipt.UID = releaseString(m["uid"])
					receipt.ResourceVersion = releaseString(m["resourceVersion"])
				} else {
					state = "outcome_unknown"
				}
			} else if result.Code == "" && releaseDefinitiveAPIRejection(result.Status) {
				state = "rejected"
			}
			clear(result.Body)
			_, e = s.runtime.store.RecordReleaseAction(ctx, action.ID, parent.OwnerEpoch, action.Revision, state, receipt)
			if e != nil {
				return e
			}
			delete(actions, result.ActionID)
			if state == "outcome_unknown" {
				return ErrOutcomeUnknown
			}
			if state == "rejected" {
				if parent.Plan.MutationVersion == 1 {
					return &releaseAPIStatusError{status: result.Status}
				}
				return ErrDenied
			}
		}
		if reply.Pending != nil {
			action, e := s.permitHelmPending(ctx, parent, *reply.Pending, index)
			if e != nil {
				_, _ = s.RPC(context.WithoutCancel(ctx), kubeprotocol.Frame{Version: 2, Command: "permit", Permit: &kubeprotocol.Permit{RequestID: reply.Pending.ID, ActionID: "denied-" + reply.Pending.ID, Digest: reply.Pending.Digest, Allow: false, Expires: time.Now().Add(time.Second).UnixMilli()}})
				return e
			}
			actions[action.ID] = action
			index++
		}
		status, e := s.RPC(ctx, kubeprotocol.Frame{Version: 2, Command: "helm-poll", StreamID: started.TaskID})
		if e != nil || status.Code != "ok" {
			return ErrOutcomeUnknown
		}
		if status.Done && len(actions) == 0 && reply.Pending == nil && reply.Result == nil {
			if status.ExitCode != 0 {
				return ErrTool
			}
			if status.Revision != helm.Revision {
				return ErrConflict
			}
			return nil
		}
		select {
		case <-ctx.Done():
			if len(actions) > 0 {
				return ErrOutcomeUnknown
			}
			return ctx.Err()
		case <-time.After(50 * time.Millisecond):
		}
	}
}
func (c *ReleaseController) runHelmRelease(ctx context.Context, api releaseAPI, parent ReleaseRecord) error {
	session, ok := api.(*ReleaseSession)
	if !ok {
		return ErrTool
	}
	if parent.Plan.MutationVersion == 1 {
		if e := session.requireHelmCapabilities(ctx, parent.Plan.Helm.Hooks); e != nil {
			return e
		}
	}
	for index, resource := range parent.Plan.Resources {
		if !resource.HelmCRD {
			continue
		}
		if resource.VerifyOnly {
			if _, e := c.observeResource(ctx, api, resource, parent.Plan.ObserveSeconds); e != nil {
				return e
			}
			continue
		}
		if _, e := c.executeFrozenResource(ctx, api, parent, resource, "crd-"+strconvI(index)); e != nil {
			return e
		}
		if _, e := c.observeResource(ctx, api, resource, parent.Plan.ObserveSeconds); e != nil {
			return e
		}
	}
	for _, resource := range parent.Plan.Resources {
		if resource.Health == "custom" {
			if e := c.verifyCustomPrerequisites(ctx, api, parent, resource); e != nil {
				return e
			}
		}
	}
	if e := session.RunReleaseHelm(ctx, parent); e != nil {
		return e
	}
	if e := c.runHelmSupplementalChecks(ctx, api, parent); e != nil {
		return e
	}
	facts, e := c.store.ListReleaseActions(ctx, parent.ID)
	if e != nil {
		return e
	}
	byID := map[ReleaseResourceID]ReleaseAction{}
	completedHooks := map[ReleaseResourceID]bool{}
	for _, fact := range facts {
		if fact.State != "verified" {
			return ErrOutcomeUnknown
		}
		byID[fact.Intent.Resource] = fact
		if fact.Intent.Verb != "delete" && fact.Receipt != nil && fact.Receipt.Health.State == "ready" {
			completedHooks[fact.Intent.Resource] = true
		}
	}
	for _, resource := range parent.Plan.Resources {
		if resource.Delete {
			last, ok := byID[resource.ID]
			if !ok || last.Intent.Verb != "delete" {
				return ErrRuntimeUnverified
			}
			if e := c.observeReleaseDeletion(ctx, api, parent, last); e != nil {
				return e
			}
			continue
		}
		if last, ok := byID[resource.ID]; ok && last.Intent.Verb == "delete" && completedHooks[resource.ID] {
			if _, e := readReleaseObject(ctx, api, resource.ID); !errors.Is(e, ErrNotFound) {
				return ErrRuntimeUnverified
			}
			if last.Receipt != nil && !last.Receipt.Deleted {
				if _, e := c.store.RecordReleaseDeletion(ctx, last.ID, parent.OwnerEpoch, last.Revision); e != nil {
					return e
				}
			}
			continue
		}
		receipt, observeErr := c.observeResource(ctx, api, resource, parent.Plan.ObserveSeconds)
		if action, ok := byID[resource.ID]; ok && receipt.Object != nil {
			if _, e = persistReleaseActionHealth(ctx, c.store, action.ID, parent.OwnerEpoch, action.Revision, receipt); e != nil {
				return errors.Join(observeErr, e)
			}
		}
		if observeErr != nil {
			return observeErr
		}
	}
	storage := parent.Plan.Helm.StorageAnchor
	storage.Name += ".v" + strconvI(parent.Plan.Helm.Revision)
	actual, e := readReleaseObject(ctx, api, storage)
	if e != nil {
		return e
	}
	release, e := decodeHelmRelease(actual)
	if e != nil || releaseMap(release["info"])["status"] != "deployed" || !releaseHelmStorageAllowed(parent.Plan, storage, actual) {
		return ErrRollout
	}
	if e := c.recordReleaseVerifications(ctx, api, parent); e != nil {
		return e
	}
	if e := c.cleanupReleaseProbes(ctx, api, parent, parent.Plan.Resources); e != nil {
		return e
	}
	if !strings.HasPrefix(storage.Name, parent.Plan.Helm.StorageAnchor.Name) {
		return ErrInvalid
	}
	return nil
}
