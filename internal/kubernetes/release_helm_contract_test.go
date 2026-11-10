package kubernetes

import (
	"bytes"
	"compress/gzip"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
)

func helmSecretFixture(t *testing.T, release map[string]any) map[string]any {
	t.Helper()
	raw, e := json.Marshal(release)
	if e != nil {
		t.Fatal(e)
	}
	var compressed bytes.Buffer
	z := gzip.NewWriter(&compressed)
	if _, e = z.Write(raw); e != nil {
		t.Fatal(e)
	}
	if e = z.Close(); e != nil {
		t.Fatal(e)
	}
	encoded := base64.StdEncoding.EncodeToString([]byte(base64.StdEncoding.EncodeToString(compressed.Bytes())))
	return releaseTestObject(`{"apiVersion":"v1","kind":"Secret","metadata":{"name":"sh.helm.release.v1.app.v1","namespace":"app","labels":{"name":"app","owner":"helm","version":"1","status":"deployed","createdAt":"100"}},"type":"helm.sh/release.v1","data":{"release":"` + encoded + `"}}`)
}
func TestHelmStorageCannotChangeFrozenValuesManifestOrRelease(t *testing.T) {
	release := releaseTestObject(`{"name":"app","namespace":"app","version":1,"manifest":"frozen-template","chart":{"metadata":{"name":"chart","version":"1.0.0"}},"config":{"password":"private-fixture"},"info":{"status":"pending-install","notes":"fixed-private-notes"},"apply_method":"ssa"}`)
	id := ReleaseResourceID{Version: "v1", Kind: "Secret", Resource: "secrets", Namespace: "app", Name: "sh.helm.release.v1.app.v1"}
	anchor := id
	anchor.Name = "sh.helm.release.v1.app"
	plan := ReleasePlan{Helm: &ReleaseHelmPlan{Name: "app", Namespace: "app", Revision: 1, StorageAnchor: anchor, Release: release}}
	actual := releaseObjectCopy(release)
	releaseMap(actual["info"])["status"] = "deployed"
	releaseMap(actual["info"])["last_deployed"] = "2026-11-08T00:00:00Z"
	if !releaseHelmStorageAllowed(plan, id, helmSecretFixture(t, actual)) {
		t.Fatal("Helm known dynamic metadata blocked")
	}
	for _, field := range []string{"config", "manifest", "name", "namespace", "chart"} {
		changed := releaseObjectCopy(actual)
		switch field {
		case "config":
			releaseMap(changed[field])["password"] = "foreign"
		case "chart":
			releaseMap(releaseMap(changed[field])["metadata"])["version"] = "2.0.0"
		default:
			changed[field] = "foreign"
		}
		if releaseHelmStorageAllowed(plan, id, helmSecretFixture(t, changed)) {
			t.Fatal("frozen storage content changed", field)
		}
	}
	secret := helmSecretFixture(t, actual)
	releaseMap(releaseMap(secret["metadata"])["labels"])["foreign"] = "leak"
	if releaseHelmStorageAllowed(plan, id, secret) {
		t.Fatal("unapproved storage label accepted")
	}
	foreign := id
	foreign.Name = "sh.helm.release.v1.other.v1"
	if releaseHelmStorage(plan, foreign) {
		t.Fatal("storage widened to another release")
	}
}
func TestReleaseValuesTypedAndLiteralRuntimeVariables(t *testing.T) {
	rendered, e := RenderReleaseValues([]byte("replicas: '${COUNT}'\nvalue: '${SECRET}'\ncommand: 'echo $${HOSTNAME}'\n"), map[string]ReleaseVariableValue{"COUNT": {Type: "number", Value: 3}, "SECRET": {Type: "string", Value: "${UNBOUND}", Secret: true}})
	if e != nil || !strings.Contains(string(rendered), "replicas: 3") || !strings.Contains(string(rendered), "${UNBOUND}") || !strings.Contains(string(rendered), "echo ${HOSTNAME}") {
		t.Fatal("values AST lost types/literals", string(rendered), e)
	}
	for _, bad := range []string{"password: ${MISSING}", "a: 1\na: 2", "a: &alias {secret: value}\nb: *alias", "---\na: 1\n---\nb: 2"} {
		if _, e = RenderReleaseValues([]byte(bad), nil); e == nil {
			t.Fatal("unsafe values accepted", bad)
		}
	}
}
