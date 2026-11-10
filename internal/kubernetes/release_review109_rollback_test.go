package kubernetes

import (
	"context"
	"encoding/json"
	"github.com/huangchengsir/pipewright/internal/kubeprotocol"
	"github.com/huangchengsir/pipewright/internal/store"
	"github.com/huangchengsir/pipewright/internal/storetest"
	"strings"
	"testing"
	"time"
)

type rollback109PodAPI struct{ *probeControllerAPI }

func rollback109AdmitPod(object map[string]any, name string) map[string]any {
	object = review107AdmitPod(object, name)
	spec := releaseMap(object["spec"])
	for _, key := range []string{"not-ready", "unreachable"} {
		spec["tolerations"] = append(releaseItems(spec["tolerations"]), map[string]any{"key": "node.kubernetes.io/" + key, "operator": "Exists", "effect": "NoExecute", "tolerationSeconds": 300})
	}
	return object
}
func (a *rollback109PodAPI) DryRun(ctx context.Context, in kubeprotocol.Request) (map[string]any, error) {
	var obj map[string]any
	if json.Unmarshal(in.Body, &obj) != nil {
		return nil, ErrInvalid
	}
	if in.Method == "PATCH" {
		return applyFixtureMerge(a.objects[in.Path], obj), nil
	}
	return rollback109AdmitPod(obj, "kube-api-access-dry01"), nil
}
func (a *rollback109PodAPI) ExecuteAction(ctx context.Context, id, owner string, revision int64, in ReleaseActionIntent, path string) (ReleaseAction, error) {
	a.writes++
	action, e := a.f.s.PrepareReleaseAction(ctx, id, owner, revision, in)
	if e != nil {
		return action, e
	}
	var obj map[string]any
	if json.Unmarshal(action.Intent.Body, &obj) != nil {
		return action, ErrInvalid
	}
	if in.Verb == "create" {
		obj = rollback109AdmitPod(obj, "kube-api-access-live1")
	} else {
		obj = applyFixtureMerge(a.objects[path], obj)
	}
	meta := releaseMap(obj["metadata"])
	meta["uid"] = "direct-pod-uid"
	meta["resourceVersion"] = "21"
	releaseMap(obj["spec"])["nodeName"] = "node-one"
	image := releaseMap(releaseItems(releaseMap(obj["spec"])["containers"])[0])["image"]
	obj["status"] = map[string]any{"phase": "Running", "conditions": []any{map[string]any{"type": "Ready", "status": "True"}, map[string]any{"type": "ContainersReady", "status": "True"}, map[string]any{"type": "Initialized", "status": "True"}}, "containerStatuses": []any{map[string]any{"name": "app", "image": image, "ready": true, "started": true, "state": map[string]any{"running": map[string]any{"startedAt": "2026-11-09T00:00:00Z"}}}}}
	named, _ := resourceAPI(in.Resource).Path(in.Resource.Namespace, in.Resource.Name)
	a.objects[named] = obj
	return a.f.s.RecordReleaseAction(ctx, action.ID, owner, action.Revision, "verified", ReleaseActionReceipt{Resource: in.Resource, UID: "direct-pod-uid", ResourceVersion: "21", Object: obj, Health: ReleaseHealth{State: "pending"}})
}
func TestReview109PodAdmittedDefaultsForwardUpdateRollbackPreparation(t *testing.T) {
	storetest.ForEachDialect(t, func(t *testing.T, st *store.Store) { testReview109Rollback(t, st) })
}
func testReview109Rollback(t *testing.T, st *store.Store) {
	ctx := context.Background()
	f, seed := releaseStoreFixture(t, st)
	id := ReleaseResourceID{Version: "v1", Resource: "pods", Kind: "Pod", Namespace: "app", Name: "direct"}
	scope := ReleaseAccess{ID: id, Actions: []string{"get", "create", "patch", "delete", "rollback"}, Containers: []string{"app"}}
	if _, e := f.s.SaveReleaseGrant(ctx, f.conn.ID, "", 0, ReleaseGrantInput{ProjectID: "project-1", Environment: "production", Executor: Executor{Kind: "local"}, Resources: []ReleaseAccess{scope}, RequireApproval: true, Enabled: true}); e != nil {
		t.Fatal(e)
	}
	request := seed.Authorization.Request
	request.Resources = []ReleaseAccess{scope}
	auth, e := f.s.AuthorizeRelease(ctx, request)
	if e != nil {
		t.Fatal(e)
	}
	obj := releaseTestObject(`{"apiVersion":"v1","kind":"Pod","metadata":{"name":"direct","namespace":"app","annotations":{"user":"approved"}},"spec":{"serviceAccountName":"default","restartPolicy":"Always","containers":[{"name":"app","image":"registry.invalid/app@sha256:` + strings.Repeat("a", 64) + `"}],"tolerations":[{"key":"custom","operator":"Exists","effect":"NoSchedule"}]}}`)
	resource := releaseResourceFromObject(id, obj, "workload")
	api := &rollback109PodAPI{&probeControllerAPI{releaseControllerAPI: &releaseControllerAPI{f: f, objects: map[string]map[string]any{}}}}
	c := NewReleaseController(f.s, nil)
	c.poll = time.Millisecond
	c.preview = func(context.Context, ReleaseAuthorization, string) (releaseAPI, error) { return api, nil }
	c.writer = func(context.Context, string, string) (releaseAPI, error) { return api, nil }
	var source ReleaseRecord
	for _, phase := range []string{"first", "second"} {
		plan := ReleasePlan{Version: 2, Mode: "manifest", MutationVersion: 1, Authorization: auth, Resources: []ReleaseResource{resource}, TimeoutSeconds: 300}
		if phase == "second" {
			path, _ := resourceAPI(id).Path(id.Namespace, id.Name)
			plan.Baseline = []ReleaseResource{releaseResourceFromObject(id, releaseObjectCopy(api.objects[path]), "workload")}
		}
		api.cleanup = false
		if e = c.dryRunReleaseResources(ctx, "owner-"+phase, &plan); e != nil {
			t.Fatal(phase, "dry-run", e)
		}
		record, e := f.s.CreateRelease(ctx, "pod-run-"+phase, "node", "owner-"+phase, plan)
		if e != nil {
			t.Fatal(e)
		}
		record, e = f.s.ApproveRelease(ctx, record.ID, record.OwnerEpoch, record.Revision, record.PlanID)
		if e != nil {
			t.Fatal(e)
		}
		api.record = record
		api.cleanup = true
		registerControllerTestWriter(t, f, record)
		source, e = c.Run(ctx, record.ID, record.OwnerEpoch, nil)
		if e != nil || !source.Successful() {
			t.Fatal(phase, "run", source.State, e)
		}
	}

	// Re-derivation is read-only and requires the original sealed same-scope
	// action/UID/declared fields; foreign evidence never becomes a declaration.
	baseline := source.Plan.Baseline[0]
	declared, err := f.s.rollbackDeclaredBaseline(ctx, source.Plan.Authorization, baseline)
	if err != nil {
		t.Fatal(err)
	}
	if len(releaseItems(releaseMap(declared.Object["spec"])["tolerations"])) != 1 {
		t.Fatal("sealed prior declaration not recovered")
	}
	for _, scenario := range []string{"uid", "missing-action", "scope", "environment", "connection", "executor", "cluster", "resource", "user-toleration", "user-annotation"} {
		changed := baseline
		changed.Object = releaseObjectCopy(baseline.Object)
		auth := source.Plan.Authorization
		switch scenario {
		case "uid":
			releaseMap(changed.Object["metadata"])["uid"] = "foreign"
		case "missing-action":
			releaseMap(releaseMap(changed.Object["metadata"])["annotations"])[ReleaseActionAnnotation] = "unknown-action"
		case "scope":
			auth.Request.ProjectID = "other-project"
		case "environment":
			auth.Request.Environment = "other"
		case "connection":
			auth.Request.ConnectionID = "other"
		case "executor":
			auth.Request.Executor = Executor{Kind: "ssh", ServerID: "other"}
		case "cluster":
			auth.ClusterKey = "other"
		case "resource":
			changed.ID.Name = "other"
		case "user-toleration":
			releaseMap(releaseItems(releaseMap(changed.Object["spec"])["tolerations"])[0])["effect"] = "NoExecute"
		case "user-annotation":
			releaseMap(releaseMap(changed.Object["metadata"])["annotations"])["user"] = "drift"
		}
		kept, err := f.s.rollbackDeclaredBaseline(ctx, auth, changed)
		if err != nil {
			t.Fatal(scenario, err)
		}
		if len(releaseItems(releaseMap(kept.Object["spec"])["tolerations"])) != 3 {
			t.Fatal("unproved baseline borrowed prior declaration", scenario)
		}
	}

	marker := releaseString(releaseMap(releaseMap(baseline.Object["metadata"])["annotations"])[ReleaseActionAnnotation])
	for _, state := range []string{"submitted", "outcome_unknown", "rejected"} {
		if _, err := st.DB.Exec(`UPDATE k8s_release_actions SET state=? WHERE id=?`, state, marker); err != nil {
			t.Fatal(err)
		}
		kept, err := f.s.rollbackDeclaredBaseline(ctx, source.Plan.Authorization, baseline)
		if _, restoreErr := st.DB.Exec(`UPDATE k8s_release_actions SET state='verified' WHERE id=?`, marker); restoreErr != nil {
			t.Fatal(restoreErr)
		}
		if err != nil || len(releaseItems(releaseMap(kept.Object["spec"])["tolerations"])) != 3 {
			t.Fatal("nonverified receipt borrowed as provenance", state, err)
		}
	}
	var sealed []byte
	if err := st.DB.QueryRow(`SELECT receipt_cipher FROM k8s_release_actions WHERE id=?`, marker).Scan(&sealed); err != nil {
		t.Fatal(err)
	}
	if _, err := st.DB.Exec(`UPDATE k8s_release_actions SET receipt_cipher=NULL WHERE id=?`, marker); err != nil {
		t.Fatal(err)
	}
	kept, err := f.s.rollbackDeclaredBaseline(ctx, source.Plan.Authorization, baseline)
	if _, restoreErr := st.DB.Exec(`UPDATE k8s_release_actions SET receipt_cipher=? WHERE id=?`, sealed, marker); restoreErr != nil {
		t.Fatal(restoreErr)
	}
	clear(sealed)
	if err != nil || len(releaseItems(releaseMap(kept.Object["spec"])["tolerations"])) != 3 {
		t.Fatal("missing receipt borrowed as provenance", err)
	}
	path, _ := resourceAPI(id).Path(id.Namespace, id.Name)
	healthy := releaseObjectCopy(api.objects[path])
	for _, scenario := range []string{"declared-array", "native-array", "uid"} {
		api.objects[path] = releaseObjectCopy(healthy)
		live := api.objects[path]
		switch scenario {
		case "declared-array":
			releaseMap(releaseItems(releaseMap(live["spec"])["tolerations"])[0])["effect"] = "NoExecute"
		case "native-array":
			releaseMap(releaseItems(releaseMap(live["spec"])["tolerations"])[1])["tolerationSeconds"] = 301
		case "uid":
			releaseMap(live["metadata"])["uid"] = "recreated"
		}
		before := api.writes
		api.cleanup = false
		if _, err := c.PrepareRollback(ctx, "reject-"+scenario, "node", "reject-owner", source.ID, source.Revision, source.PlanID); err == nil || api.writes != before {
			t.Fatal("drifted incarnation accepted or written", scenario, err)
		}
	}
	api.objects[path] = healthy
	api.cleanup = false
	rollback, e := c.PrepareRollback(ctx, "pod-rollback-108", "rollback-node", "rollback-owner", source.ID, source.Revision, source.PlanID)
	if e != nil {
		t.Fatalf("rollback of unchanged same-UID admitted Pod must prepare (closed error): %v", e)
	}
	if len(releaseItems(releaseMap(rollback.Plan.Resources[0].Object["spec"])["tolerations"])) != 1 || len(releaseItems(releaseMap(rollback.Plan.Baseline[0].Object["spec"])["tolerations"])) != 3 {
		t.Fatal("declaration and full actual baseline conflated")
	}
	before := api.writes
	if e = f.s.AcquireReleaseGates(ctx, rollback.ID, rollback.OwnerEpoch); e == nil {
		t.Fatal("rollback did not require independent approval")
	}
	rollback, e = f.s.ApproveRelease(ctx, rollback.ID, rollback.OwnerEpoch, rollback.Revision, rollback.PlanID)
	if e != nil {
		t.Fatal(e)
	}
	api.record = rollback
	api.cleanup = true
	registerControllerTestWriter(t, f, rollback)
	result, e := c.Run(ctx, rollback.ID, rollback.OwnerEpoch, nil)
	if e != nil || result.State != "rolled_back" || !result.Successful() || api.writes != before+1 {
		t.Fatal("approved rollback did not finish exactly once", result.State, e)
	}
	live := api.objects[path]
	if releaseMap(live["metadata"])["uid"] != "direct-pod-uid" || len(releaseItems(releaseMap(live["spec"])["tolerations"])) != 3 || len(releaseItems(releaseMap(live["spec"])["volumes"])) != 1 {
		t.Fatal("actual admitted fields/UID lost")
	}
	facts, e := f.s.ListReleaseActions(ctx, result.ID)
	if e != nil || len(facts) != 1 || facts[0].State != "verified" || facts[0].Receipt.Health.State != "ready" {
		t.Fatal("true final receipt missing", e)
	}
	if len(releaseItems(releaseMap(result.Plan.Resources[0].Mutation.Managed["spec"])["tolerations"])) != 1 {
		t.Fatal("admission defaults acquired deletion authority")
	}
	old, e := f.s.GetRelease(ctx, source.ID)
	if e != nil || old.PlanID != source.PlanID {
		t.Fatal("historical plan changed", e)
	}
}
