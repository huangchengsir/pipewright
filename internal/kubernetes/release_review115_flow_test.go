package kubernetes

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
)

type review115FlowAPI struct{ *review113NativeAPI }

func (a *review115FlowAPI) Read(ctx context.Context, path, query string) ([]byte, error) {
	if path == "/apis/apps/v1" {
		return []byte(`{"kind":"APIResourceList","groupVersion":"apps/v1","resources":[{"name":"deployments","kind":"Deployment","namespaced":true}]}`), nil
	}
	return a.review113NativeAPI.Read(ctx, path, query)
}
func TestReview115AtomicOmissionRetainUpdateRemoveAndApprovedRollback(t *testing.T) {
	ctx := context.Background()
	imageA := "registry.invalid/app@sha256:" + strings.Repeat("a", 64)
	imageB := "registry.invalid/app@sha256:" + strings.Repeat("b", 64)
	id := ReleaseResourceID{Group: "apps", Version: "v1", Resource: "deployments", Kind: "Deployment", Namespace: "app", Name: "atomic"}
	object := releaseTestObject(`{"apiVersion":"apps/v1","kind":"Deployment","metadata":{"name":"atomic","namespace":"app"},"spec":{"replicas":1,"selector":{"matchLabels":{"app":"atomic"}},"template":{"metadata":{"labels":{"app":"atomic"}},"spec":{"containers":[{"name":"app","image":"` + imageA + `","envFrom":[{"prefix":"","configMapRef":{"name":"source","optional":false}}]}]}}}}`)
	f, auth, live := review112OwnedSeed(t, id, object, func(o map[string]any) {
		o["status"] = map[string]any{"observedGeneration": 1, "replicas": 1, "updatedReplicas": 1, "availableReplicas": 1}
		native, e := review113NativeSerialize(o)
		if e != nil {
			t.Fatal(e)
		}
		for k := range o {
			delete(o, k)
		}
		for k, v := range native {
			o[k] = v
		}
	})
	path, _ := resourceAPI(id).Path(id.Namespace, id.Name)
	api := &review115FlowAPI{review113NativeAPI: &review113NativeAPI{review112OrderAPI: &review112OrderAPI{review110API: &review110API{probeControllerAPI: &probeControllerAPI{releaseControllerAPI: &releaseControllerAPI{f: f, objects: map[string]map[string]any{path: live}}}, workload: id, observedImageID: imageA}}}}
	c := NewReleaseController(f.s, nil)
	c.poll = time.Millisecond
	c.preview = func(context.Context, ReleaseAuthorization, string) (releaseAPI, error) { return api, nil }
	c.writer = func(context.Context, string, string) (releaseAPI, error) { return api, nil }
	sequence := 0
	execute := func(record ReleaseRecord, err error) ReleaseRecord {
		t.Helper()
		if err != nil {
			t.Fatal("prepare", err)
		}
		record, e := f.s.ApproveRelease(ctx, record.ID, record.OwnerEpoch, record.Revision, record.PlanID)
		if e != nil {
			t.Fatal(e)
		}
		api.record = record
		api.cleanup = true
		registerControllerTestWriter(t, f, record)
		done, e := c.Run(ctx, record.ID, record.OwnerEpoch, nil)
		if e != nil || !done.Successful() {
			t.Fatal("execution", done.State, e)
		}
		facts, e := f.s.ListReleaseActions(ctx, done.ID)
		if e != nil || len(facts) != 1 || facts[0].State != "verified" || facts[0].Receipt.Health.State != "ready" {
			t.Fatal("verified healthy receipt missing", e)
		}
		if releaseMap(api.objects[path]["metadata"])["uid"] != "order-owned-uid" {
			t.Fatal("workload incarnation changed")
		}
		return done
	}
	prepare := func(desired map[string]any) ReleaseRecord {
		t.Helper()
		sequence++
		raw, _ := json.Marshal(desired)
		files, e := InlineReleaseFile(string(raw))
		if e != nil {
			t.Fatal(e)
		}
		record, e := c.PrepareManifest(ctx, fmt.Sprint("atomic-run-", sequence), "node", fmt.Sprint("atomic-owner-", sequence), auth, files, nil, []byte(`{"namespace":"app"}`), 300, 0)
		return execute(record, e)
	}
	rollback := func(source ReleaseRecord) ReleaseRecord {
		t.Helper()
		sequence++
		record, e := c.PrepareRollback(ctx, fmt.Sprint("atomic-rollback-", sequence), "node", fmt.Sprint("atomic-rb-owner-", sequence), source.ID, source.Revision, source.PlanID)
		return execute(record, e)
	}
	assertEmptyPrefix := func(record ReleaseRecord) {
		t.Helper()
		items := releaseItems(review115Container(api.objects[path])["envFrom"])
		if len(items) != 1 {
			t.Fatal("array membership changed")
		}
		item := releaseMap(items[0])
		if _, present := item["prefix"]; present {
			t.Fatal("official response did not omit empty prefix")
		}
		if value, present := releaseMap(item["configMapRef"])["optional"]; !present || value != false {
			t.Fatal("pointer false disappeared")
		}
		declared := releaseItems(review115Container(record.Plan.Resources[0].Object)["envFrom"])
		if value, present := releaseMap(declared[0])["prefix"]; !present || value != "" {
			t.Fatal("approved empty prefix payload rewritten")
		}
	}
	retained := releaseObjectCopy(object)
	review115Container(retained)["image"] = imageB
	api.observedImageID = imageB
	first := prepare(retained)
	assertEmptyPrefix(first)
	updated := releaseObjectCopy(retained)
	item := releaseMap(releaseItems(review115Container(updated)["envFrom"])[0])
	item["prefix"] = "UPDATED_"
	releaseMap(item["configMapRef"])["name"] = "second"
	second := prepare(updated)
	if releaseMap(releaseItems(review115Container(api.objects[path])["envFrom"])[0])["prefix"] != "UPDATED_" {
		t.Fatal("atomic update omitted")
	}
	restored := rollback(second)
	assertEmptyPrefix(restored)
	removed := releaseObjectCopy(retained)
	delete(review115Container(removed), "envFrom")
	third := prepare(removed)
	if _, present := review115Container(api.objects[path])["envFrom"]; present {
		t.Fatal("owned atomic array not removed")
	}
	last := rollback(third)
	assertEmptyPrefix(last)
	if api.writes != 5 {
		t.Fatal("unexpected dispatch or replay count")
	}
}
