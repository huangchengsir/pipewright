package kubernetes

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/huangchengsir/pipewright/internal/store"
	"github.com/huangchengsir/pipewright/internal/storetest"
	"strings"
	"testing"
)

// This tests specialized read-only rollback planning against sealed historical
// receipts. It does not stand in for executing the Helm binary or a cluster.
func TestHelmRollbackPlansExactPreviousSealedRevisionAndRejectsReplacement(t *testing.T) {
	storetest.ForEachDialect(t, func(t *testing.T, st *store.Store) {
		f, base := releaseStoreFixture(t, st)
		ctx := context.Background()
		cmID := ReleaseResourceID{Version: "v1", Resource: "configmaps", Kind: "ConfigMap", Namespace: "app", Name: "config"}
		anchor := ReleaseResourceID{Version: "v1", Resource: "secrets", Kind: "Secret", Namespace: "app", Name: "sh.helm.release.v1.app"}
		hookID := ReleaseResourceID{Group: "batch", Version: "v1", Resource: "jobs", Kind: "Job", Namespace: "app", Name: "fixture-hook"}
		scopes := []ReleaseAccess{{ID: cmID, Actions: []string{"get", "create", "patch", "rollback"}}, {ID: anchor, Actions: []string{"get", "create", "update", "patch", "helm-storage", "rollback"}}}
		scopes = append(scopes, ReleaseAccess{ID: hookID, Actions: []string{"get", "create", "patch", "delete", "watch", "helm-hook", "related-pods", "rollback"}, Containers: []string{"hook"}})
		if _, e := f.s.SaveReleaseGrant(ctx, f.conn.ID, "", 0, ReleaseGrantInput{ProjectID: "project-1", Environment: "production", Executor: Executor{Kind: "local"}, Enabled: true, Resources: scopes}); e != nil {
			t.Fatal(e)
		}
		request := base.Authorization.Request
		request.Resources = scopes
		auth, e := f.s.AuthorizeRelease(ctx, request)
		if e != nil {
			t.Fatal(e)
		}
		makeRelease := func(revision int, value string) map[string]any {
			return map[string]any{"name": "app", "namespace": "app", "version": revision, "chart": map[string]any{"metadata": map[string]any{"name": "chart", "version": strconvI(revision) + ".0.0"}}, "config": map[string]any{"password": value}, "manifest": "apiVersion: v1\nkind: ConfigMap\nmetadata: {name: config, namespace: app}\ndata: {password: " + value + "}\n", "info": map[string]any{"status": "deployed", "notes": "private-notes"}, "apply_method": "ssa"}
		}
		previous, current := makeRelease(1, "old-private-value"), makeRelease(2, "current-private-value")
		makeSecret := func(release map[string]any, uid, rv string) map[string]any {
			secret := helmSecretFixture(t, release)
			meta := releaseMap(secret["metadata"])
			meta["name"] = anchor.Name + ".v" + strconvI(int(releaseInt(release["version"])))
			meta["uid"] = uid
			meta["resourceVersion"] = rv
			labels := releaseMap(meta["labels"])
			labels["version"] = strconvI(int(releaseInt(release["version"])))
			labels["status"] = releaseMap(release["info"])["status"]
			return secret
		}
		previousSecret := makeSecret(previous, "old-secret-uid", "17")
		oldID := anchor
		oldID.Name += ".v1"
		latestID := anchor
		latestID.Name += ".v2"
		desired := releaseTestObject(`{"apiVersion":"v1","kind":"ConfigMap","metadata":{"name":"config","namespace":"app","annotations":{"meta.helm.sh/release-name":"app","meta.helm.sh/release-namespace":"app"},"labels":{"app.kubernetes.io/managed-by":"Helm"}},"data":{"password":"current-private-value"}}`)
		resource := releaseResourceFromObject(cmID, desired, "declaration")
		plan := ReleasePlan{Version: 2, Mode: "helm", Authorization: auth, Resources: []ReleaseResource{resource}, Helm: &ReleaseHelmPlan{Name: "app", Namespace: "app", Action: "upgrade", Revision: 2, StorageAnchor: anchor, Storage: []ReleaseResource{releaseResourceFromObject(oldID, previousSecret, "declaration")}, Release: current}, Config: json.RawMessage(`{"namespace":"app","mode":"helm"}`), TimeoutSeconds: 300}
		hook := testOwnershipHook()
		plan.Resources = append(plan.Resources, releaseResourceFromObject(hookID, hook, "workload"))
		plan.Deletes = []ReleaseResourceID{hookID}
		plan.Helm.Hooks = true
		source, e := f.s.CreateRelease(ctx, "helm-source", "node", "source-owner", plan)
		if e != nil {
			t.Fatal(e)
		}
		live := releaseObjectCopy(desired)
		releaseMap(live["metadata"])["uid"] = "config-uid"
		releaseMap(live["metadata"])["resourceVersion"] = "18"
		releaseMap(releaseMap(live["metadata"])["annotations"])[ReleaseActionAnnotation] = "kact-config"
		latestSecret := makeSecret(current, "latest-secret-uid", "18")
		releaseMap(latestSecret["metadata"])["annotations"] = map[string]any{ReleaseActionAnnotation: "kact-storage"}
		// Persist only sealed facts, as a completed controller would have left them.
		insertFact := func(id string, rid ReleaseResourceID, object map[string]any) {
			t.Helper()
			body, _ := json.Marshal(releaseCanonicalObject(object))
			verb := "patch"
			health := ReleaseHealth{State: "declared"}
			if rid == hookID {
				verb = "create"
				health = ReleaseHealth{State: "ready", Reason: "job_completed"}
			}
			intent := ReleaseActionIntent{Version: 2, ParentID: source.PlanID, StepKey: id, Resource: rid, Verb: verb, Body: body, ExpectedUID: releaseString(releaseMap(object["metadata"])["uid"]), ExpectedRV: "17", AuthorizationFingerprint: auth.Fingerprint}
			raw, _ := json.Marshal(intent)
			cipher, e := f.s.seal(releaseActionPurpose, id, 1, raw)
			if e != nil {
				t.Fatal(e)
			}
			receipt := ReleaseActionReceipt{Resource: rid, UID: releaseString(releaseMap(object["metadata"])["uid"]), ResourceVersion: "18", Object: object, Health: health}
			raw, _ = json.Marshal(receipt)
			rc, e := f.s.seal(releaseActionReceiptPurpose, id, 1, raw)
			if e != nil {
				t.Fatal(e)
			}
			if _, e = st.DB.Exec(`INSERT INTO k8s_release_actions(id,release_id,step_key,resource_key,owner_epoch,revision,state,intent_cipher,intent_id,receipt_cipher,created_at,updated_at) VALUES(?,?,?,'key',?,1,'verified',?,?,?,1,1)`, id, source.ID, id, source.OwnerEpoch, cipher, releaseByteDigest(cipher), rc); e != nil {
				t.Fatal(e)
			}
		}
		insertFact("kact-config", cmID, live)
		insertFact("kact-storage", latestID, latestSecret)
		hookLive := releaseObjectCopy(hook)
		releaseMap(hookLive["metadata"])["uid"] = "hook-uid"
		releaseMap(hookLive["metadata"])["resourceVersion"] = "18"
		releaseMap(releaseMap(hookLive["metadata"])["annotations"])[ReleaseActionAnnotation] = "kact-hook"
		insertFact("kact-hook", hookID, hookLive)
		insertDeletedHook := func(uid, state string, deleted bool) {
			t.Helper()
			intent := ReleaseActionIntent{Version: 2, ParentID: source.PlanID, StepKey: "kact-hook-delete", Resource: hookID, Verb: "delete", ExpectedUID: uid, ExpectedRV: "18", AuthorizationFingerprint: auth.Fingerprint}
			raw, _ := json.Marshal(intent)
			cipher, err := f.s.seal(releaseActionPurpose, "kact-hook-delete", 1, raw)
			if err != nil {
				t.Fatal(err)
			}
			receipt := ReleaseActionReceipt{Resource: hookID, UID: uid, ResourceVersion: "18", Deleted: deleted, Health: ReleaseHealth{State: "declared", Reason: "resource_deleted"}}
			raw, _ = json.Marshal(receipt)
			rc, err := f.s.seal(releaseActionReceiptPurpose, "kact-hook-delete", 1, raw)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = st.DB.Exec(`DELETE FROM k8s_release_actions WHERE id='kact-hook-delete'`); err != nil {
				t.Fatal(err)
			}
			if _, err = st.DB.Exec(`INSERT INTO k8s_release_actions(id,release_id,step_key,resource_key,owner_epoch,revision,state,intent_cipher,intent_id,receipt_cipher,created_at,updated_at) VALUES(?,?,?,'key',?,1,?,?,?,?,2,2)`, "kact-hook-delete", source.ID, "kact-hook-delete", source.OwnerEpoch, state, cipher, releaseByteDigest(cipher), rc); err != nil {
				t.Fatal(err)
			}
		}
		insertDeletedHook("hook-uid", "verified", true)
		if _, e = st.DB.Exec(`UPDATE k8s_releases SET state='ready',owner_fenced=1 WHERE id=?`, source.ID); e != nil {
			t.Fatal(e)
		}
		source, e = f.s.GetRelease(ctx, source.ID)
		if e != nil {
			t.Fatal(e)
		}
		api := &helmOwnershipAPI{helmResourcesAPI{releaseControllerAPI: releaseControllerAPI{objects: map[string]map[string]any{"/api/v1/namespaces/app/configmaps/config": live, "/api/v1/namespaces/app/secrets/" + oldID.Name: previousSecret, "/api/v1/namespaces/app/secrets/" + latestID.Name: latestSecret}}}}
		c := NewReleaseController(f.s, nil)
		c.preview = func(context.Context, ReleaseAuthorization, string) (releaseAPI, error) { return api, nil }
		restored, e := c.PrepareRollback(ctx, "helm-rollback", "node", "rollback-owner", source.ID, source.Revision, source.PlanID)
		if e != nil {
			t.Fatal(e)
		}
		helm := restored.Plan.Helm
		if helm == nil || helm.Action != "rollback" || helm.Revision != 3 || helm.TargetRevision != 1 || !restored.Plan.ForceApproval || restored.ApprovedID != "" {
			t.Fatal("rollback reused source approval or wrong revision", helm)
		}
		if releaseMap(helm.Release["config"])["password"] != "old-private-value" || releaseMap(releaseMap(helm.Release["chart"])["metadata"])["version"] != "1.0.0" || releaseInt(releaseMap(helm.Release["info"])["rollback_revision"]) != 1 {
			t.Fatal("rollback re-rendered current chart instead of frozen history")
		}
		public, _ := json.Marshal(PreviewRelease(restored))
		if strings.Contains(string(public), "old-private-value") || strings.Contains(string(public), "private-notes") {
			t.Fatal("rollback preview exposed private Helm content")
		}
		for _, negative := range []string{"missing-delete", "unknown-delete", "foreign-delete-uid", "unconfirmed-delete", "missing-create", "recreated-hook"} {
			t.Run(negative, func(t *testing.T) {
				insertDeletedHook("hook-uid", "verified", true)
				hookPath, _ := resourceAPI(hookID).Path(hookID.Namespace, hookID.Name)
				switch negative {
				case "missing-delete":
					_, _ = st.DB.Exec(`DELETE FROM k8s_release_actions WHERE id='kact-hook-delete'`)
				case "unknown-delete":
					insertDeletedHook("hook-uid", "outcome_unknown", false)
				case "foreign-delete-uid":
					insertDeletedHook("foreign-uid", "verified", true)
				case "unconfirmed-delete":
					insertDeletedHook("hook-uid", "verified", false)
				case "missing-create":
					_, _ = st.DB.Exec(`DELETE FROM k8s_release_actions WHERE id='kact-hook'`)
				case "recreated-hook":
					replacement := releaseObjectCopy(hookLive)
					releaseMap(replacement["metadata"])["uid"] = "foreign-recreated-uid"
					api.objects[hookPath] = replacement
				}
				if _, err := c.PrepareRollback(ctx, "negative-"+negative, "node", "rollback-owner", source.ID, source.Revision, source.PlanID); err == nil {
					t.Fatal("incomplete or foreign deleted-hook history accepted")
				}
				delete(api.objects, hookPath)
				if negative == "missing-create" {
					insertFact("kact-hook", hookID, hookLive)
				}
			})
		}
		insertDeletedHook("hook-uid", "verified", true)
		releaseMap(latestSecret["metadata"])["uid"] = "replacement-secret"
		if _, e = c.PrepareRollback(ctx, "other-rollback", "node", "rollback-owner", source.ID, source.Revision, source.PlanID); !errors.Is(e, ErrSuperseded) {
			t.Fatal("same-name storage replacement accepted", e)
		}
	})
}
