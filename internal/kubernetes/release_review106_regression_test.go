package kubernetes

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/huangchengsir/pipewright/internal/kubeprotocol"
	"github.com/huangchengsir/pipewright/internal/store"
	"github.com/huangchengsir/pipewright/internal/storetest"
	"strings"
	"testing"
	"time"
)

func TestReview106Candidate1AtomicCommandAndArgsOrder(t *testing.T) {
	for _, key := range []string{"command", "args"} {
		t.Run(key, func(t *testing.T) {
			expected := map[string]any{key: []any{"first", "second"}}
			actual := map[string]any{key: []any{"second", "first"}}
			if ReleasePodTemplateMatches(expected, actual) {
				t.Fatal("ordered atomic process vector was matched as an unordered multiset")
			}
		})
	}
}

func review106ConfigMapRun(t *testing.T, st *store.Store, remove bool) (*fixture, *ReleaseController, *probeControllerAPI, ReleaseRecord) {
	t.Helper()
	ctx := context.Background()
	f, p := releaseStoreFixture(t, st)
	id := ReleaseResourceID{Version: "v1", Resource: "configmaps", Kind: "ConfigMap", Namespace: "app", Name: "review106"}
	scope := ReleaseAccess{ID: id, Actions: []string{"get", "create", "delete"}}
	if _, e := f.s.SaveReleaseGrant(ctx, f.conn.ID, "", 0, ReleaseGrantInput{ProjectID: "project-1", Environment: "production", Executor: Executor{Kind: "local"}, Resources: []ReleaseAccess{scope}, RequireApproval: true, Enabled: true}); e != nil {
		t.Fatal(e)
	}
	request := p.Authorization.Request
	request.Resources = []ReleaseAccess{scope}
	auth, e := f.s.AuthorizeRelease(ctx, request)
	if e != nil {
		t.Fatal(e)
	}
	object := releaseTestObject(`{"apiVersion":"v1","kind":"ConfigMap","metadata":{"name":"review106","namespace":"app"},"data":{"declared":"fixture"}}`)
	resource := releaseResourceFromObject(id, object, "declaration")
	plan := ReleasePlan{Version: 2, Mode: "manifest", Authorization: auth, Resources: []ReleaseResource{resource}, TimeoutSeconds: 300, ObserveSeconds: 0}
	api := &probeControllerAPI{releaseControllerAPI: &releaseControllerAPI{f: f, objects: map[string]map[string]any{}}, cleanup: true}
	if remove {
		live := releaseObjectCopy(object)
		releaseMap(live["metadata"])["uid"] = "original-uid"
		releaseMap(live["metadata"])["resourceVersion"] = "17"
		plan.Resources[0].Delete = true
		plan.Deletes = []ReleaseResourceID{id}
		plan.Baseline = []ReleaseResource{releaseResourceFromObject(id, live, "declaration")}
		api.objects["/api/v1/namespaces/app/configmaps/review106"] = live
	}
	record, e := f.s.CreateRelease(ctx, "review106-cm-run", "node", "owner", plan)
	if e != nil {
		t.Fatal(e)
	}
	record, e = f.s.ApproveRelease(ctx, record.ID, record.OwnerEpoch, record.Revision, record.PlanID)
	if e != nil {
		t.Fatal(e)
	}
	api.record = record
	registerControllerTestWriter(t, f, record)
	c := NewReleaseController(f.s, nil)
	c.poll = time.Millisecond
	c.writer = func(context.Context, string, string) (releaseAPI, error) { return api, nil }
	c.preview = func(context.Context, ReleaseAuthorization, string) (releaseAPI, error) { return api, nil }
	result, e := c.Run(ctx, record.ID, record.OwnerEpoch, nil)
	if e != nil || !result.Successful() {
		t.Fatal("seed real controller release did not complete", result.State, result.Cleanup, e)
	}
	return f, c, api, result
}
func TestReview106Candidate2VerifyReuseRejectsReplacementUID(t *testing.T) {
	storetest.ForEachDialect(t, func(t *testing.T, st *store.Store) {
		_, c, api, record := review106ConfigMapRun(t, st, false)
		path := "/api/v1/namespaces/app/configmaps/review106"
		replacement := releaseObjectCopy(api.objects[path])
		releaseMap(replacement["metadata"])["uid"] = "different-new-uid"
		delete(releaseMap(releaseMap(replacement["metadata"])["annotations"]), ReleaseActionAnnotation)
		api.objects[path] = replacement
		if _, e := c.VerifyReuse(context.Background(), record.ID, record.PlanID); e == nil {
			t.Fatal("saved successful receipt was reused for a new UID with the same declared data")
		}
	})
}
func TestReview106Candidate3VerifyReuseAcceptsVerifiedNonEphemeralDeletion(t *testing.T) {
	storetest.ForEachDialect(t, func(t *testing.T, st *store.Store) {
		f, c, api, record := review106ConfigMapRun(t, st, true)
		facts, e := f.s.ListReleaseActions(context.Background(), record.ID)
		if e != nil {
			t.Fatal(e)
		}
		if len(facts) != 1 || facts[0].State != "verified" || facts[0].Receipt == nil || !facts[0].Receipt.Deleted || record.Plan.Resources[0].Ephemeral {
			t.Fatal("seed lacks sealed non-ephemeral verified deletion evidence")
		}
		if _, e = api.Read(context.Background(), "/api/v1/namespaces/app/configmaps/review106", ""); !errors.Is(e, ErrNotFound) {
			t.Fatal("seed object not absent", e)
		}
		writes := api.writes
		if _, e = c.VerifyReuse(context.Background(), record.ID, record.PlanID); e != nil {
			t.Fatal("saved verified same-UID deletion could not be inherited read-only", e)
		}
		if api.writes != writes {
			t.Fatal("reuse dispatched a mutation")
		}
	})
}
func TestReview106Candidate4CoreDNSKeyPatchReconcileRecognizesFrozenAction(t *testing.T) {
	storetest.ForEachDialect(t, func(t *testing.T, st *store.Store) {
		c, api, f, record := coreDNS105Prepare(t, st, "rv")
		result, e := c.Run(context.Background(), record.ID, record.OwnerEpoch, nil)
		if e == nil || result.State != "partial_failed" || result.Cleanup != CleanupVerified {
			t.Fatal("seed did not stop before final write", result.State, result.Cleanup, e)
		}
		facts, e := f.f.s.ListReleaseActions(context.Background(), record.ID)
		if e != nil {
			t.Fatal(e)
		}
		found := false
		for _, fact := range facts {
			if fact.Intent.Resource == record.Plan.Resources[record.Plan.CoreDNS.LiveIndex].ID && fact.Intent.StepKey != releaseCoreDNSFinalizeStep && fact.Intent.Verb == "patch" && fact.State == "verified" {
				found = true
				if _, ok := releasePlannedActionResource(record.Plan, fact); !ok {
					t.Error("actual sealed CoreDNS key-only mutation was not recognized by its frozen patch")
				}
			}
		}
		if !found {
			t.Fatal("seed live key-only receipt absent")
		}
		writes := api.writes
		if _, e = c.ReconcileRelease(context.Background(), result.ID, result.Revision); e != nil {
			t.Error("read-only reconcile could not consume owned key-only action", e)
		}
		if api.writes != writes {
			t.Fatal("read-only reconcile wrote resources")
		}
	})
}
func TestReview106Candidate5RecoverDepartedSettlesRunningAndReleasesGates(t *testing.T) {
	storetest.ForEachDialect(t, func(t *testing.T, st *store.Store) {
		ctx := context.Background()
		f, plan := releaseStoreFixture(t, st)
		record, e := f.s.CreateRelease(ctx, "review106-departed", "node", "departed-owner", plan)
		if e != nil {
			t.Fatal(e)
		}
		record, e = f.s.ApproveRelease(ctx, record.ID, record.OwnerEpoch, record.Revision, record.PlanID)
		if e != nil {
			t.Fatal(e)
		}
		if e = f.s.AcquireReleaseGates(ctx, record.ID, record.OwnerEpoch); e != nil {
			t.Fatal(e)
		}
		c := NewReleaseController(f.s, nil)
		e = c.RecoverDeparted(ctx, []string{record.OwnerEpoch})
		after, readErr := f.s.GetRelease(ctx, record.ID)
		if readErr != nil {
			t.Fatal(readErr)
		}
		var gates int
		if err := st.DB.QueryRow("SELECT COUNT(*) FROM k8s_resource_gates WHERE owner_id=?", record.ID).Scan(&gates); err != nil {
			t.Fatal(err)
		}
		t.Logf("closed facts: recoveryErrOwner=%t state=%s fenced=%t gateCount=%d", errors.Is(e, ErrOwner), after.State, after.OwnerFenced, gates)
		if e != nil || after.State != "failed" || !after.OwnerFenced || gates != 0 {
			t.Fatal("departed clean writer remained running or held resource gates")
		}
	})
}
func TestReview106Candidate7FieldRestrictedGrantAllowsReadOnlyPolicyCatalog(t *testing.T) {
	storetest.ForEachDialect(t, func(t *testing.T, st *store.Store) {
		ctx := context.Background()
		f, plan := releaseStoreFixture(t, st)
		grants, e := f.s.ListReleaseGrants(ctx, f.conn.ID)
		if e != nil || len(grants) != 1 {
			t.Fatal("seed grant", e)
		}
		scope := releaseRequestedAccess(plan.Resources[0].ID, "patch")
		scope.Containers = []string{"app"}
		scope.Fields = []string{"/spec/template/spec/containers/0/image"}
		old := grants[0]
		if _, e = f.s.SaveReleaseGrant(ctx, f.conn.ID, old.ID, old.Revision, ReleaseGrantInput{ProjectID: "project-1", Environment: "production", Executor: Executor{Kind: "local"}, Resources: []ReleaseAccess{scope}, RequireApproval: true, Enabled: true}); e != nil {
			t.Fatal(e)
		}
		request := plan.Authorization.Request
		request.Resources = []ReleaseAccess{scope}
		if _, e = f.s.AuthorizeRelease(ctx, request); e != nil {
			t.Fatal("explicit exact field mutation authorization must work before catalog check", e)
		}
		if _, e = f.s.ReleasePolicyCatalog(ctx, request); e != nil {
			t.Fatal("get-only catalog rejected an explicitly field-restricted image grant", e)
		}
	})
}

type review106ProgressiveAPI struct {
	*progressiveControllerAPI
	dryRunCalls  int
	dryRunErrors int
}

func (a *review106ProgressiveAPI) DryRun(ctx context.Context, in kubeprotocol.Request) (map[string]any, error) {
	a.dryRunCalls++
	object, e := a.progressiveControllerAPI.DryRun(ctx, in)
	if e != nil {
		a.dryRunErrors++
	}
	return object, e
}
func (a *review106ProgressiveAPI) ExecuteAction(ctx context.Context, id, owner string, revision int64, in ReleaseActionIntent, path string) (ReleaseAction, error) {
	if in.StepKey == "batch-1-resource-1" {
		return ReleaseAction{}, ErrDenied
	}
	return a.progressiveControllerAPI.ExecuteAction(ctx, id, owner, revision, in, path)
}
func TestReview106Candidate6ProgressiveRetryRebasesExistingCandidate(t *testing.T) {
	for _, change := range []string{"none", "uid", "user-annotation", "provenance", "unknown"} {
		t.Run(change, func(t *testing.T) {
			storetest.ForEachDialect(t, func(t *testing.T, st *store.Store) {
				ctx := context.Background()
				f, p := releaseStoreFixture(t, st)
				stableID, _ := releaseWorkloadID("Deployment", "app", "stable")
				candidateID, _ := releaseWorkloadID("Deployment", "app", "candidate")
				serviceID := ReleaseResourceID{Version: "v1", Resource: "services", Kind: "Service", Namespace: "app", Name: "route"}
				stableScope := releaseRequestedAccess(stableID, "patch")
				stableScope.Actions = append(stableScope.Actions, "scale", "related-hpas")
				stableScope.Containers = []string{"app"}
				candidateScope := releaseRequestedAccess(candidateID, "create")
				candidateScope.Actions = append(candidateScope.Actions, "patch", "scale", "related-hpas")
				candidateScope.Containers = []string{"app"}
				scopes := []ReleaseAccess{stableScope, candidateScope, {ID: serviceID, Actions: []string{"get", "related-endpointslices"}}}
				if _, e := f.s.SaveReleaseGrant(ctx, f.conn.ID, "", 0, ReleaseGrantInput{ProjectID: "project-1", Environment: "production", Executor: Executor{Kind: "local"}, Resources: scopes, RequireApproval: true, Enabled: true}); e != nil {
					t.Fatal(e)
				}
				request := p.Authorization.Request
				request.Resources = scopes
				auth, e := f.s.AuthorizeRelease(ctx, request)
				if e != nil {
					t.Fatal(e)
				}
				stable := releaseTestObject(`{"apiVersion":"apps/v1","kind":"Deployment","metadata":{"name":"stable","namespace":"app","uid":"stable-uid","resourceVersion":"17","generation":1,"annotations":{"approved.example/note":"original"}},"spec":{"replicas":2,"selector":{"matchLabels":{"app":"backend","track":"stable"}},"template":{"metadata":{"labels":{"app":"backend","track":"stable"}},"spec":{"containers":[{"name":"app","image":"registry.invalid/app@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}]}}},"status":{"observedGeneration":1,"replicas":2,"updatedReplicas":2,"availableReplicas":2}}`)
				service := releaseTestObject(`{"apiVersion":"v1","kind":"Service","metadata":{"name":"route","namespace":"app","uid":"service-uid","resourceVersion":"17"},"spec":{"selector":{"app":"backend"},"ports":[{"port":80,"targetPort":8080}],"clusterIP":"10.0.0.10"}}`)
				apiBase := &progressiveControllerAPI{releaseControllerAPI: releaseControllerAPI{f: f, objects: map[string]map[string]any{"/apis/apps/v1/namespaces/app/deployments/stable": stable, "/api/v1/namespaces/app/services/route": service}}}
				api := &review106ProgressiveAPI{progressiveControllerAPI: apiBase}
				c := NewReleaseController(f.s, nil)
				c.poll = time.Millisecond
				c.preview = func(context.Context, ReleaseAuthorization, string) (releaseAPI, error) { return api, nil }
				if _, e = st.DB.Exec(`INSERT INTO pipeline_runs(id,project_id,status,created_at) VALUES('progressive-run','project-1','running','now')`); e != nil {
					t.Fatal(e)
				}
				record, e := c.PrepareProgressive(ctx, "progressive-run", "node", "first-owner", auth, ReleaseProgressiveInput{Namespace: "app", Stable: "stable", Candidate: "candidate", Service: "route", TrackKey: "track", TrackValue: "next", Batches: []int{1, 2}, Images: []ReleaseImageUpdate{{Container: "app", Image: "registry.invalid/app@sha256:" + strings.Repeat("b", 64)}}}, []byte(`{"namespace":"app"}`), 300, 0)
				if e != nil {
					t.Fatal(e)
				}
				encoded, _ := json.Marshal(record.Plan.Batches[1][0])
				var frozen map[string]any
				json.Unmarshal(encoded, &frozen)
				mutation := releaseMap(frozen["mutation"])
				if mutation["strategy"] != "prior-create-cas-merge-v1" || mutation["originStepKey"] != "batch-0-resource-0" {
					t.Fatal("later candidate update lacks frozen same-plan create receipt recipe")
				}
				planID := record.PlanID
				record, e = f.s.ApproveRelease(ctx, record.ID, record.OwnerEpoch, record.Revision, record.PlanID)
				if e != nil {
					t.Fatal(e)
				}
				api.record = record
				registerControllerTestWriter(t, f, record)
				c.writer = func(context.Context, string, string) (releaseAPI, error) { return api, nil }
				var cleanupLogs []string
				result, e := c.Run(ctx, record.ID, record.OwnerEpoch, func(p, code string) {
					if p == "cleanup" {
						cleanupLogs = append(cleanupLogs, code)
					}
				})
				if strings.Join(cleanupLogs, ",") != "cleanup_started,cleanup_verified" {
					t.Fatal("verified cleanup phase missing", cleanupLogs)
				}
				if !errors.Is(e, ErrReleasePaused) || result.Cleanup != CleanupVerified || api.writes != 2 {
					t.Fatal("first batch did not release tool and preserve pause", result.State, result.Cleanup, api.writes, e)
				}
				record, e = f.s.SuspendRelease(ctx, result.ID, result.OwnerEpoch, result.Revision, ReleaseSuspension{Version: 2, RunID: result.RunID, NodeKey: result.NodeKey, PlanID: planID, Cursor: 1, Nodes: map[string]string{"node": "waiting_action"}})
				if e != nil {
					t.Fatal(e)
				}
				if change == "hpa" {
					api.hpa = true
				}
				if change == "service" {
					releaseMap(service["spec"])["externalTrafficPolicy"] = "Local"
				}
				if change == "ssa_owner" {
					candidatePath, _ := resourceAPI(candidateID).Path("app", "candidate")
					releaseMap(api.objects[candidatePath]["metadata"])["managedFields"] = []any{map[string]any{"operation": "Apply", "manager": "pipewright-release-v2"}}
				}
				continuation, e := f.s.RequestReleaseContinue(ctx, record.ID, record.Revision, planID)
				if e != nil {
					t.Fatal(e)
				}
				continuation, e = f.s.ClaimReleaseContinuation(ctx, continuation.ID, "second-owner")
				if e != nil {
					t.Fatal(e)
				}
				if _, e = st.DB.Exec(`UPDATE pipeline_runs SET status='running' WHERE id='progressive-run'`); e != nil {
					t.Fatal(e)
				}
				if e = f.s.ActivateReleaseContinuation(ctx, continuation.ID, "second-owner"); e != nil {
					t.Fatal(e)
				}
				record, e = f.s.GetRelease(ctx, record.ID)
				if e != nil {
					t.Fatal(e)
				}
				api.record = record
				registerControllerTestWriter(t, f, record)
				result, e = c.Run(ctx, record.ID, record.OwnerEpoch, nil)
				if e == nil || result.State != "partial_failed" || api.writes != 3 {
					t.Fatal("seed must stop after second candidate write before second stable write", result.State, api.writes, e)
				}
				candidatePath, _ := resourceAPI(candidateID).Path("app", "candidate")
				switch change {
				case "uid":
					releaseMap(api.objects[candidatePath]["metadata"])["uid"] = "foreign-uid"
				case "user-annotation":
					releaseMap(releaseMap(api.objects[candidatePath]["metadata"])["annotations"])["approved.example/note"] = "foreign"
				case "provenance":
					releaseMap(releaseMap(api.objects[candidatePath]["metadata"])["annotations"])[ReleaseActionAnnotation] = "foreign-action"
				case "unknown":
					if _, e = st.DB.Exec("UPDATE k8s_release_actions SET state='outcome_unknown' WHERE release_id=? AND step_key='batch-1-resource-0'", result.ID); e != nil {
						t.Fatal(e)
					}
				}
				beforeDryRuns, beforeDryErrors := api.dryRunCalls, api.dryRunErrors
				_, retryErr := c.PrepareRetry(ctx, "review106-new-retry", "node", "retry-owner", result.ID, auth)
				t.Logf("closed facts: sourceCursor=%d retryDryRunCalls=%d retryDryRunErrors=%d retryErrInvalid=%t", result.Cursor, api.dryRunCalls-beforeDryRuns, api.dryRunErrors-beforeDryErrors, errors.Is(retryErr, ErrInvalid))
				if change != "none" {
					if retryErr == nil || api.dryRunCalls != beforeDryRuns {
						t.Fatal("unproved candidate accepted", change, retryErr)
					}
					return
				}
				if api.dryRunCalls-beforeDryRuns != 2 || api.dryRunErrors != beforeDryErrors {
					t.Fatal("retry did not reach and finish both actual remaining dry-runs", retryErr)
				}
				if retryErr != nil {
					t.Fatal("retry of settled owned partial progressive release could not rebase its existing candidate", retryErr)
				}

			})
		})
	}
}
