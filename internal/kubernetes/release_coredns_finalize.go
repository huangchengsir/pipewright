package kubernetes

import (
	"context"
	"encoding/json"
	"github.com/huangchengsir/pipewright/internal/kubeprotocol"
	"github.com/huangchengsir/pipewright/internal/pipeline"
)

const releaseCoreDNSFinalizeStep = "coredns-finalize-v1"

func coreDNSCanarySuffix(canary string, port int) string {
	return "\n" + canary + ":" + strconvI(port) + " {\n hosts {\n  192.0.2.1 " + canary + "\n }\n}\n"
}

// A new plan approves both the temporary reload-proof Corefile and its exact
// final target. Historical plans omit Finalize and retain their original bytes
// and action semantics. Only this plan's suffix may differ between the bodies.
func validCoreDNSFinalization(plan ReleasePlan) bool {
	if plan.CoreDNS == nil || plan.CoreDNS.Finalize == nil {
		return true
	}
	m := plan.CoreDNS
	if plan.Mode != "coredns" || plan.MutationVersion != 1 || m.LiveIndex < 0 || m.LiveIndex >= len(plan.Resources) {
		return false
	}
	live, final := plan.Resources[m.LiveIndex], *m.Finalize
	var cfg pipeline.KubernetesReleaseConfig
	if json.Unmarshal(plan.Config, &cfg) != nil || cfg.CoreDNS == nil || cfg.CoreDNS.Key == "" || m.Canary == "" {
		return false
	}
	key := cfg.CoreDNS.Key
	if final.ID != live.ID || final.ID.Kind != "ConfigMap" || final.ID.Group != "" || final.ExpectedUID == "" || final.Expected == nil || final.Probe || final.Ephemeral || final.Delete || final.VerifyOnly || final.Mutation == nil || final.Mutation.Strategy != releaseCoreDNSKeyMerge || final.Mutation.UID != final.ExpectedUID || !validReleaseMutation(final) {
		return false
	}
	target := releaseString(releaseMap(final.Object["data"])[key])
	if target == "" || releaseMap(live.Object["data"])[key] != target+coreDNSCanarySuffix(m.Canary, cfg.CoreDNS.DNSPort) {
		return false
	}
	expected := releaseObjectCopy(live.Object)
	releaseMap(expected["data"])[key] = target
	if !releaseSameValue(expected, final.Object) {
		return false
	}
	body, e := json.Marshal(final.Object)
	if e != nil || len(body) > MaxReleaseObjectBytes || final.Digest != releaseByteDigest(body) {
		return false
	}
	for _, baseline := range plan.Baseline {
		if baseline.ID != final.ID {
			continue
		}
		mutation, e := releaseCoreDNSMutation(plan, final, baseline.Object)
		return e == nil && mutation != nil && mutation.UID == final.ExpectedUID && releaseSameValue(mutation.Patch, final.Mutation.Patch)
	}
	return false
}

func (c *ReleaseController) prepareCoreDNSFinalization(ctx context.Context, api releaseAPI, plan *ReleasePlan) error {
	if plan.CoreDNS == nil || plan.CoreDNS.Finalize == nil {
		return nil
	}
	final := *plan.CoreDNS.Finalize
	var baseline map[string]any
	for _, resource := range plan.Baseline {
		if resource.ID == final.ID {
			baseline = resource.Object
		}
	}
	mutation, e := releaseCoreDNSMutation(*plan, final, baseline)
	if e != nil || mutation == nil {
		if e != nil {
			return e
		}
		return ErrInvalid
	}
	final.Mutation = mutation
	object := releaseMutationObject(final)
	meta := releaseMap(object["metadata"])
	meta["uid"], meta["resourceVersion"] = releaseMap(baseline["metadata"])["uid"], releaseMap(baseline["metadata"])["resourceVersion"]
	body, e := json.Marshal(object)
	if e != nil {
		return e
	}
	defer clear(body)
	path, e := resourceAPI(final.ID).Path(final.ID.Namespace, final.ID.Name)
	if e != nil {
		return e
	}
	admitted, e := api.DryRun(ctx, kubeprotocol.Request{Method: "PATCH", Path: path, Body: body, ContentType: mutation.ContentType})
	if e != nil {
		return e
	}
	if !releaseApprovedFieldsMatch(final.ID, final.Object, admitted) {
		return errReleaseAdmissionMismatch
	}
	final.Expected = releaseAdmittedExpected(final, admitted)
	plan.CoreDNS.Finalize = &final
	if !validCoreDNSFinalization(*plan) {
		return ErrInvalid
	}
	return nil
}

func coreDNSLiveFact(parent ReleaseRecord, facts []ReleaseAction) (ReleaseAction, error) {
	m := parent.Plan.CoreDNS
	if m == nil || m.Finalize == nil || !validCoreDNSFinalization(parent.Plan) {
		return ReleaseAction{}, ErrInvalid
	}
	step := "batch-" + strconvI(parent.Cursor) + "-resource-" + strconvI(m.LiveIndex)
	for _, fact := range facts {
		if fact.Intent.StepKey != step {
			continue
		}
		if fact.State != "verified" || fact.Receipt == nil {
			return fact, ErrOutcomeUnknown
		}
		if fact.Intent.ParentID != parent.PlanID || fact.Intent.Resource != m.Finalize.ID || fact.Intent.Verb != "patch" || fact.Receipt.Resource != m.Finalize.ID || fact.Receipt.UID != m.Finalize.ExpectedUID || fact.Receipt.ResourceVersion == "" {
			return fact, ErrSuperseded
		}
		return fact, nil
	}
	return ReleaseAction{}, ErrOutcomeUnknown
}

func (s *Service) validateCoreDNSFinalizeIntent(ctx context.Context, parent ReleaseRecord, intent ReleaseActionIntent) error {
	if intent.StepKey != releaseCoreDNSFinalizeStep || intent.Verb != "patch" {
		return ErrDenied
	}
	facts, e := s.ListReleaseActions(ctx, parent.ID)
	if e != nil {
		return e
	}
	previous, e := coreDNSLiveFact(parent, facts)
	if e != nil {
		return e
	}
	if intent.Resource != parent.Plan.CoreDNS.Finalize.ID || intent.ExpectedUID != previous.Receipt.UID || intent.ExpectedRV != previous.Receipt.ResourceVersion {
		return ErrSuperseded
	}
	// A terminal write can never bypass any frozen candidate/original-Pod probe.
	for index, resource := range parent.Plan.Resources {
		if !resource.Probe {
			continue
		}
		found := false
		for _, fact := range facts {
			if fact.Intent.StepKey == "batch-"+strconvI(parent.Cursor)+"-resource-"+strconvI(index) && fact.Intent.ParentID == parent.PlanID && fact.Intent.Resource == resource.ID && fact.State == "verified" && fact.Receipt != nil && fact.Receipt.Resource == resource.ID && fact.Receipt.UID != "" && fact.Receipt.ObservedAt > 0 && fact.Receipt.Health.State == "ready" {
				found = true
			}
		}
		if !found {
			return ErrOutcomeUnknown
		}
	}
	return nil
}

// Finalization is a normal approved, journalled CAS action, never best-effort
// defer cleanup. A known receipt is reusable after a crash; an unsettled action
// fences dispatch even if current bytes happen to resemble the desired target.
func (c *ReleaseController) finishCoreDNSFinalization(ctx context.Context, api releaseAPI, parent ReleaseRecord) error {
	if parent.Plan.CoreDNS == nil || parent.Plan.CoreDNS.Finalize == nil {
		return nil
	}
	final := *parent.Plan.CoreDNS.Finalize
	if !validCoreDNSFinalization(parent.Plan) {
		return ErrInvalid
	}
	facts, e := c.store.ListReleaseActions(ctx, parent.ID)
	if e != nil {
		return e
	}
	var completed *ReleaseAction
	for i := range facts {
		if facts[i].Intent.StepKey != releaseCoreDNSFinalizeStep {
			continue
		}
		if facts[i].State != "verified" || facts[i].Receipt == nil {
			return ErrOutcomeUnknown
		}
		completed = &facts[i]
	}
	current, e := readReleaseObject(ctx, api, final.ID)
	if e != nil {
		return e
	}
	var prior ReleaseAction
	if completed != nil {
		prior = *completed
	} else {
		prior, e = coreDNSLiveFact(parent, facts)
		if e != nil {
			return e
		}
	}
	meta := releaseMap(current["metadata"])
	if meta["deletionTimestamp"] != nil || prior.Receipt == nil || prior.Receipt.Resource != final.ID || meta["uid"] != final.ExpectedUID || meta["uid"] != prior.Receipt.UID || meta["resourceVersion"] != prior.Receipt.ResourceVersion || releaseMap(meta["annotations"])[ReleaseActionAnnotation] != prior.ID || !releaseBaselineMatches(prior.Receipt.Object, current) {
		return ErrSuperseded
	}
	if e = c.verifyCoreDNSPopulation(ctx, api, parent.Plan); e != nil {
		return e
	}
	action := prior
	if completed == nil {
		object := releaseMutationObject(final)
		metadata := releaseMap(object["metadata"])
		metadata["uid"], metadata["resourceVersion"] = meta["uid"], meta["resourceVersion"]
		body, _ := json.Marshal(object)
		path, _ := resourceAPI(final.ID).Path(final.ID.Namespace, final.ID.Name)
		admitted, err := api.DryRun(ctx, kubeprotocol.Request{Method: "PATCH", Path: path, Body: body, ContentType: final.Mutation.ContentType})
		clear(body)
		if err != nil {
			return err
		}
		if !releaseResourceExpectedMatches(final, admitted) {
			return errReleaseAdmissionMismatch
		}
		action, e = c.executeFrozenResource(ctx, api, parent, final, releaseCoreDNSFinalizeStep)
		if e != nil {
			return e
		}
	}
	if action.Intent.Resource != final.ID || action.Intent.StepKey != releaseCoreDNSFinalizeStep {
		return ErrSuperseded
	}
	receipt, e := c.observeResource(ctx, api, final, parent.Plan.ObserveSeconds)
	if e != nil {
		return e
	}
	if _, e = persistReleaseActionHealth(ctx, c.store, action.ID, parent.OwnerEpoch, action.Revision, receipt); e != nil {
		return e
	}
	return c.verifyCoreDNSPopulation(ctx, api, parent.Plan)
}
