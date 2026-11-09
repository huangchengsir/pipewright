package kubernetes

import (
	"context"
	"github.com/huangchengsir/pipewright/internal/store"
	"github.com/huangchengsir/pipewright/internal/storetest"
	"testing"
)

func TestReview106ReuseReceiptAndDeletionNegativeNeighbors(t *testing.T) {
	for _, scenario := range []string{"same-owned", "marker", "recreated-delete", "unverified-delete"} {
		t.Run(scenario, func(t *testing.T) {
			storetest.ForEachDialect(t, func(t *testing.T, st *store.Store) {
				remove := scenario == "recreated-delete" || scenario == "unverified-delete"
				f, c, api, record := review106ConfigMapRun(t, st, remove)
				path := "/api/v1/namespaces/app/configmaps/review106"
				switch scenario {
				case "marker":
					releaseMap(releaseMap(api.objects[path]["metadata"])["annotations"])[ReleaseActionAnnotation] = "foreign-action"
				case "recreated-delete":
					api.objects[path] = releaseTestObject(`{"apiVersion":"v1","kind":"ConfigMap","metadata":{"name":"review106","namespace":"app","uid":"new-uid"},"data":{"declared":"fixture"}}`)
				case "unverified-delete":
					if _, e := st.DB.Exec("UPDATE k8s_release_actions SET state='submitted' WHERE release_id=?", record.ID); e != nil {
						t.Fatal(e)
					}
				}
				before := api.writes
				_, e := c.VerifyReuse(context.Background(), record.ID, record.PlanID)
				if (e == nil) != (scenario == "same-owned") {
					t.Fatal("reuse receipt/absence provenance boundary violated", scenario, e)
				}
				if api.writes != before {
					t.Fatal("reuse wrote to API")
				}
				_ = f
			})
		})
	}
}
func TestReview106FinalizedCoreDNSReuseUsesExactFinalReceipt(t *testing.T) {
	storetest.ForEachDialect(t, func(t *testing.T, st *store.Store) {
		c, api, _, record := coreDNS105Prepare(t, st, "")
		result, e := c.Run(context.Background(), record.ID, record.OwnerEpoch, nil)
		if e != nil || !result.Successful() {
			t.Fatal("CoreDNS seed", e)
		}
		before := api.writes
		if _, e = c.VerifyReuse(context.Background(), result.ID, result.PlanID); e != nil {
			t.Fatal("finalized exact Corefile could not reuse verified receipt", e)
		}
		if api.writes != before {
			t.Fatal("CoreDNS reuse performed a mutation")
		}
		path := "/api/v1/namespaces/app/configmaps/dns-config"
		releaseMap(api.objects[path]["metadata"])["uid"] = "new-external-uid"
		if _, e = c.VerifyReuse(context.Background(), result.ID, result.PlanID); e == nil {
			t.Fatal("final Corefile bytes hid a new UID")
		}
	})
}
func TestReview106ReconcileMatchesMutationRoleAndContentType(t *testing.T) {
	id := ReleaseResourceID{Version: "v1", Resource: "configmaps", Kind: "ConfigMap", Namespace: "app", Name: "config"}
	patch := releaseTestObject(`{"apiVersion":"v1","kind":"ConfigMap","metadata":{"name":"config","namespace":"app"},"data":{"changed":"new"}}`)
	object := releaseObjectCopy(patch)
	releaseMap(object["data"])["retained"] = "external"
	resource := releaseResourceFromObject(id, object, "declaration")
	resource.Mutation = &ReleaseMutation{ContentType: releaseMergeContentType, Patch: patch}
	plan := ReleasePlan{MutationVersion: 1, Resources: []ReleaseResource{resource}}
	action := ReleaseAction{Intent: ReleaseActionIntent{MutationVersion: 1, Resource: id, Verb: "patch", ContentType: releaseMergeContentType, Body: []byte(stringMustJSON(patch))}}
	if _, ok := releasePlannedActionResource(plan, action); !ok {
		t.Fatal("frozen key-only body not recognized")
	}
	for _, scenario := range []string{"content-type", "verb", "extra-key", "wrong-resource"} {
		changed := action
		switch scenario {
		case "content-type":
			changed.Intent.ContentType = "application/json"
		case "verb":
			changed.Intent.Verb = "create"
		case "extra-key":
			body := releaseObjectCopy(patch)
			releaseMap(body["data"])["foreign"] = "write"
			changed.Intent.Body = []byte(stringMustJSON(body))
		case "wrong-resource":
			changed.Intent.Resource.Name = "other"
		}
		if _, ok := releasePlannedActionResource(plan, changed); ok {
			t.Fatal("mutation acquired a different frozen role", scenario)
		}
	}
}
