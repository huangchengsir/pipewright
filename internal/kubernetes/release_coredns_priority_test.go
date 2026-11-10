package kubernetes

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/huangchengsir/pipewright/internal/kubeprotocol"
	schedulingv1 "k8s.io/api/scheduling/v1"
	"testing"
	"time"

	"github.com/huangchengsir/pipewright/internal/storetest"
)

func TestCoreDNSCandidateFreezesExplicitNonPreemptingClass(t *testing.T) {
	ctx := context.Background()
	fixture := newCoreDNSFixture(t, storetest.Open(t))
	original := fixture.api.objects["/apis/apps/v1/namespaces/app/deployments/dns"]
	pod := releaseMap(releaseMap(releaseMap(original["spec"])["template"])["spec"])
	pod["priorityClassName"] = "system-cluster-critical"
	pod["priority"] = float64(2000000000)
	cfg, _ := json.Marshal(fixture.config)
	plan := fixture.plan
	c := NewReleaseController(fixture.f.s, nil)
	if _, err := c.prepareCoreDNSPlan(ctx, fixture.api, &plan, fixture.policy.Authorization, cfg); err != nil {
		t.Fatal(err)
	}
	first := plan.Resources[0]
	if first.ID.Group != "scheduling.k8s.io" || first.ID.Kind != "PriorityClass" || first.ID.Name != "pw-dns-nonpreempting" || first.ID.Namespace != "" || !first.Ephemeral || first.Phase != 0 || releaseInt(first.Object["value"]) != 0 || first.Object["globalDefault"] != false || first.Object["preemptionPolicy"] != "Never" {
		t.Fatal("candidate lacks first, explicit, non-default, non-preempting PriorityClass")
	}
	for _, r := range plan.Resources {
		if r.ID.Kind == "Deployment" && r.Ephemeral {
			spec := releaseMap(releaseMap(releaseMap(r.Object["spec"])["template"])["spec"])
			if spec["priorityClassName"] != first.ID.Name || spec["preemptionPolicy"] != "Never" || spec["priority"] != nil {
				t.Fatal("candidate inherited priority or is not bound to non-preempting class")
			}
		}
	}
	path, _ := resourceAPI(first.ID).Path("", first.ID.Name)
	fixture.api.objects[path] = releaseObjectCopy(first.Object)
	releaseMap(fixture.api.objects[path]["metadata"])["uid"] = "foreign-class"
	plan = fixture.plan
	if _, err := c.prepareCoreDNSPlan(ctx, fixture.api, &plan, fixture.policy.Authorization, cfg); !errors.Is(err, ErrInUse) {
		t.Fatal("pre-existing class was borrowed", err)
	}
}

// Match the real typed API encoding: globalDefault:false is omitted on wire.
type coreDNSPriorityAPI struct {
	*coreDNSExecutionAPI
	corrupt         string
	candidateWrites int
}

func (a *coreDNSPriorityAPI) DryRun(ctx context.Context, in kubeprotocol.Request) (map[string]any, error) {
	object, err := a.releaseControllerAPI.DryRun(ctx, in)
	if err != nil || object["kind"] != "PriorityClass" {
		return object, err
	}
	body, _ := json.Marshal(object)
	var typed schedulingv1.PriorityClass
	if json.Unmarshal(body, &typed) != nil {
		return nil, ErrInvalid
	}
	body, _ = json.Marshal(typed)
	var actual map[string]any
	if json.Unmarshal(body, &actual) != nil {
		return nil, ErrInvalid
	}
	return actual, nil
}
func (a *coreDNSPriorityAPI) ExecuteAction(ctx context.Context, id, owner string, revision int64, in ReleaseActionIntent, path string) (ReleaseAction, error) {
	if in.Resource.Kind == "Deployment" && in.Verb != "delete" {
		a.candidateWrites++
	}
	if in.Resource.Kind == "PriorityClass" && in.Verb == "create" && a.corrupt == "unknown" {
		action, err := a.f.s.PrepareReleaseAction(ctx, id, owner, revision, in)
		if err != nil {
			return action, err
		}
		action, err = a.f.s.RecordReleaseAction(ctx, action.ID, owner, action.Revision, "outcome_unknown", ReleaseActionReceipt{Resource: in.Resource})
		if err != nil {
			return action, err
		}
		return action, ErrOutcomeUnknown
	}
	action, err := a.coreDNSExecutionAPI.ExecuteAction(ctx, id, owner, revision, in, path)
	if err != nil || in.Resource.Kind != "PriorityClass" || in.Verb != "create" {
		return action, err
	}
	named, _ := resourceAPI(in.Resource).Path("", in.Resource.Name)
	object := a.objects[named]
	// The receipt and the live API independently omit this API-default field.
	delete(object, "globalDefault")
	switch a.corrupt {
	case "uid":
		releaseMap(object["metadata"])["uid"] = "foreign-replacement"
	case "marker":
		releaseMap(releaseMap(object["metadata"])["annotations"])[ReleaseActionAnnotation] = "foreign-action"
	case "default":
		object["globalDefault"] = true
	case "policy":
		object["preemptionPolicy"] = "PreemptLowerPriority"
	case "value":
		object["value"] = float64(1)
	}
	return action, nil
}
func preparePriorityCoreDNSFixture(t *testing.T, dropClass bool, corrupt string) (*ReleaseController, *coreDNSPriorityAPI, *coreDNSFixture, ReleaseRecord, error) {
	t.Helper()
	ctx := context.Background()
	fixture := newCoreDNSFixture(t, storetest.Open(t))
	c := NewReleaseController(fixture.f.s, nil)
	c.poll = time.Millisecond
	api := &coreDNSPriorityAPI{coreDNSExecutionAPI: &coreDNSExecutionAPI{coreDNSPlanAPI: fixture.api}, corrupt: corrupt}
	cfg, _ := json.Marshal(fixture.config)
	scout := fixture.plan
	extra, err := c.prepareCoreDNSPlan(ctx, api, &scout, fixture.policy.Authorization, cfg)
	if err != nil {
		t.Fatal(err)
	}
	scopes := append([]ReleaseAccess(nil), extra...)
	for _, resource := range scout.Resources {
		actions := []string{"get", "create", "patch", "delete", "coredns", "probe", "related-pods", "related-replicasets", "related-endpointslices"}
		if resource.ID.Kind == "PriorityClass" {
			if dropClass {
				continue
			}
			actions = []string{"get", "create", "delete", "coredns"}
		}
		scopes = append(scopes, ReleaseAccess{ID: resource.ID, Actions: actions, Containers: releaseContainerNames(resource)})
	}
	if _, err = fixture.f.s.SaveReleaseGrant(ctx, fixture.f.conn.ID, "", 0, ReleaseGrantInput{ProjectID: "project-1", Environment: "production", Executor: Executor{Kind: "local"}, Resources: mergeReleaseScopes(scopes), RequireApproval: true, Enabled: true}); err != nil {
		t.Fatal(err)
	}
	auth, err := fixture.f.s.ReleasePolicyCatalog(ctx, fixture.policy.Authorization.Request)
	if err != nil {
		t.Fatal(err)
	}
	c.preview = func(context.Context, ReleaseAuthorization, string) (releaseAPI, error) { return api, nil }
	c.writer = func(context.Context, string, string) (releaseAPI, error) { return api, nil }
	files, _ := InlineReleaseFile(stringMustJSON(fixture.desired))
	record, err := c.PrepareManifest(ctx, "priority-coredns-run", "node", "owner", auth, files, nil, cfg, 600, 0)
	return c, api, fixture, record, err
}
func TestCoreDNSPriorityClassNeedsExplicitGrantBeforeAnyWrite(t *testing.T) {
	_, api, fixture, _, err := preparePriorityCoreDNSFixture(t, true, "")
	if err == nil || api.writes != 0 {
		t.Fatal("missing cluster exact grant did not stop before writes")
	}
	records, e := fixture.f.s.ListReleases(context.Background(), "priority-coredns-run")
	if e != nil || len(records) != 0 {
		t.Fatal("ungranted class created a runnable release")
	}
}
func TestCoreDNSPriorityClassVerifiedCreateRequiredBeforeCandidateOrLiveWrites(t *testing.T) {
	for _, corrupt := range []string{"", "uid", "marker", "default", "policy", "value", "unknown"} {
		t.Run(map[string]string{"": "verified", "uid": "foreign-uid", "marker": "foreign-marker", "default": "became-global-default", "policy": "became-preempting", "value": "priority-drift", "unknown": "outcome-unknown"}[corrupt], func(t *testing.T) {
			c, api, fixture, record, err := preparePriorityCoreDNSFixture(t, false, corrupt)
			if err != nil {
				t.Fatal("preparation failed", err)
			}
			if record.Plan.CoreDNS.PriorityClass == nil {
				t.Fatal("class identity absent from frozen plan")
			}
			for _, scope := range record.Plan.Authorization.Request.Resources {
				if scope.ID.Kind == "PriorityClass" {
					if len(scope.Actions) != 4 || !hasReleaseAction(scope.Actions, "get") || !hasReleaseAction(scope.Actions, "create") || !hasReleaseAction(scope.Actions, "delete") || !hasReleaseAction(scope.Actions, "coredns") {
						t.Fatal("class grant implicitly expanded")
					}
				}
			}
			record, err = fixture.f.s.ApproveRelease(context.Background(), record.ID, record.OwnerEpoch, record.Revision, record.PlanID)
			if err != nil {
				t.Fatal(err)
			}
			api.record = record
			registerControllerTestWriter(t, fixture.f, record)
			result, err := c.Run(context.Background(), record.ID, record.OwnerEpoch, nil)
			if corrupt == "" {
				if err != nil || !result.Successful() || api.liveWrites != 2 || api.candidateWrites != 1 {
					t.Fatal("verified non-preempting candidate failed", result.State, err)
				}
				path, _ := resourceAPI(*record.Plan.CoreDNS.PriorityClass).Path("", record.Plan.CoreDNS.PriorityClass.Name)
				if api.objects[path] != nil {
					t.Fatal("temporary class not cleaned")
				}
				facts, e := fixture.f.s.ListReleaseActions(context.Background(), record.ID)
				if e != nil {
					t.Fatal(e)
				}
				created, deleted := false, false
				for _, fact := range facts {
					if fact.Intent.Resource.Kind == "PriorityClass" && fact.State == "verified" {
						created = created || fact.Intent.Verb == "create"
						deleted = deleted || fact.Intent.Verb == "delete" && fact.Receipt != nil && fact.Receipt.Deleted && fact.Intent.ExpectedUID != "" && fact.Intent.ExpectedRV != ""
					}
				}
				if !created || !deleted {
					t.Fatal("class create/delete CAS facts missing")
				}
			} else if err == nil || api.liveWrites != 0 || api.candidateWrites != 0 {
				t.Fatal("unverified class allowed DNS candidate/live mutation", result.State, err)
			}
			if corrupt == "unknown" && result.State != "outcome_unknown" {
				t.Fatal("ambiguous class create did not retain outcome_unknown")
			}
		})
	}
}
func TestPriorityClassFalseOmissionIsNarrowAndRejectsPolicyDrift(t *testing.T) {
	id := ReleaseResourceID{Group: "scheduling.k8s.io", Version: "v1", Resource: "priorityclasses", Kind: "PriorityClass", Name: "nonpreempting"}
	desired := releaseTestObject(`{"apiVersion":"scheduling.k8s.io/v1","kind":"PriorityClass","metadata":{"name":"nonpreempting"},"value":0,"globalDefault":false,"preemptionPolicy":"Never"}`)
	body, _ := json.Marshal(desired)
	var typed schedulingv1.PriorityClass
	_ = json.Unmarshal(body, &typed)
	body, _ = json.Marshal(typed)
	actual := map[string]any{}
	_ = json.Unmarshal(body, &actual)
	if _, present := actual["globalDefault"]; present || !releaseApprovedFieldsMatch(id, desired, actual) || !releaseResourceExpectedMatches(releaseResourceFromObject(id, desired, "declaration"), actual) {
		t.Fatal("official false omission rejected")
	}
	for _, key := range []string{"globalDefault", "preemptionPolicy", "value"} {
		changed := releaseObjectCopy(actual)
		changed[key] = map[string]any{"globalDefault": true, "preemptionPolicy": "PreemptLowerPriority", "value": float64(1)}[key]
		if releaseApprovedFieldsMatch(id, desired, changed) || releaseResourceExpectedMatches(releaseResourceFromObject(id, desired, "declaration"), changed) {
			t.Fatal("class changed policy accepted")
		}
	}
	wrong := id
	wrong.Kind = "ConfigMap"
	if releaseApprovedFieldsMatch(wrong, desired, actual) {
		t.Fatal("false omission normalized for unrelated resource")
	}
}
