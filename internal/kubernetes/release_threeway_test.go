package kubernetes

import (
	"encoding/json"
	"errors"
	"testing"
)

func TestOwnedPostThreeWayPreservesInjectedFieldsAndDeletesOnlyOwnedOmissions(t *testing.T) {
	old := releaseTestObject(`{"data":{"message":"old","remove":"owned"},"spec":{"containers":[{"name":"app","image":"old","env":[{"name":"MANAGED","value":"old"},{"name":"REMOVE","value":"owned"}]}]}}`)
	live := releaseTestObject(`{"data":{"message":"old","remove":"owned","outside":"keep"},"spec":{"containers":[{"name":"app","image":"old","ports":[{"containerPort":8080}],"env":[{"name":"MANAGED","value":"old"},{"name":"REMOVE","value":"owned"},{"name":"INJECTED","value":"keep"}]},{"name":"sidecar","image":"external"}],"volumes":[{"name":"injected","emptyDir":{}}]}}`)
	next := releaseTestObject(`{"data":{"message":"new","added":"new"},"spec":{"containers":[{"name":"app","image":"new","env":[{"name":"MANAGED","value":"new"}]}]}}`)
	for _, o := range []map[string]any{old, live, next} {
		o["apiVersion"] = "v1"
		o["kind"] = "Pod"
	}
	original := stringMustJSON(live)
	target, managed, patch, e := releaseThreeWay(old, live, next, false)
	if e != nil {
		t.Fatal(e)
	}
	if releaseMap(target["data"])["outside"] != "keep" || releaseMap(target["data"])["remove"] != nil || releaseMap(patch["data"])["remove"] != nil {
		t.Fatal("map ownership lost")
	}
	if _, ok := releaseMap(patch["data"])["remove"]; !ok {
		t.Fatal("owned omission was not an explicit deletion")
	}
	containers := releaseItems(releaseMap(target["spec"])["containers"])
	if len(containers) != 2 || releaseMap(containers[1])["name"] != "sidecar" || len(releaseItems(releaseMap(containers[0])["env"])) != 2 {
		t.Fatal("injected named array member was erased")
	}
	if releaseMap(managed["data"])["outside"] != nil || len(releaseItems(releaseMap(managed["spec"])["containers"])) != 1 {
		t.Fatal("external content became managed")
	}
	if stringMustJSON(live) != original {
		t.Fatal("compiler mutated live evidence")
	}
}
func TestOwnedPostThreeWayRejectsDriftAndExternalValueTakeover(t *testing.T) {
	for _, tc := range []struct{ old, live, next string }{
		{`{"data":{"owned":"old"}}`, `{"data":{"owned":"foreign"}}`, `{"data":{"owned":"new"}}`},
		{`{"data":{"owned":"old"}}`, `{"data":{"owned":"old","external":"foreign"}}`, `{"data":{"owned":"new","external":"overwrite"}}`},
		{`{"args":["old"]}`, `{"args":["old","external"]}`, `{"args":["new"]}`},
		{`{"containers":[{"name":"app","image":"old"}]}`, `{"containers":[{"name":"app","image":"foreign"},{"name":"sidecar","image":"keep"}]}`, `{"containers":[{"name":"app","image":"new"}]}`},
	} {
		if _, _, _, e := releaseThreeWay(releaseTestObject(tc.old), releaseTestObject(tc.live), releaseTestObject(tc.next), false); !errors.Is(e, ErrSuperseded) {
			t.Fatal("foreign field claim accepted", e)
		}
	}
}
func TestOwnedPostPartialImageIntentKeepsPreviouslyManagedEnvironment(t *testing.T) {
	old := releaseTestObject(`{"spec":{"replicas":3,"template":{"spec":{"containers":[{"name":"app","image":"old","env":[{"name":"MANAGED","value":"preserved"}]}]}}}}`)
	live := releaseObjectCopy(old)
	containers := releaseItems(releaseMap(releaseMap(releaseMap(live["spec"])["template"])["spec"])["containers"])
	containers = append(containers, releaseTestObject(`{"name":"sidecar","image":"external"}`))
	releaseMap(releaseMap(releaseMap(live["spec"])["template"])["spec"])["containers"] = containers
	next := releaseTestObject(`{"spec":{"template":{"spec":{"containers":[{"name":"app","image":"new"}]}}}}`)
	for _, o := range []map[string]any{old, live, next} {
		o["apiVersion"] = "apps/v1"
		o["kind"] = "Deployment"
	}
	target, managed, patch, e := releaseThreeWay(old, live, next, true)
	if e != nil {
		t.Fatal(e)
	}
	encoded, _ := json.Marshal(patch)
	if releaseMap(target["spec"])["replicas"] != float64(3) || releaseMap(patch["spec"])["replicas"] != nil || len(releaseItems(releaseMap(releaseMap(releaseMap(managed["spec"])["template"])["spec"])["containers"])) != 1 || len(encoded) == 0 {
		t.Fatal("partial intent erased unrelated managed fields or claimed HPA replicas")
	}
}
