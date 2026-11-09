package kubernetes

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func releaseTestDiscovery(t *testing.T) *ReleaseDiscovery {
	t.Helper()
	d := NewReleaseDiscovery()
	for _, raw := range []string{`{"kind":"APIResourceList","groupVersion":"v1","resources":[{"name":"configmaps","kind":"ConfigMap","namespaced":true,"verbs":["get","patch"]},{"name":"secrets","kind":"Secret","namespaced":true},{"name":"pods","kind":"Pod","namespaced":true},{"name":"persistentvolumeclaims","kind":"PersistentVolumeClaim","namespaced":true}]}`, `{"kind":"APIResourceList","groupVersion":"apps/v1","resources":[{"name":"deployments","kind":"Deployment","namespaced":true},{"name":"statefulsets","kind":"StatefulSet","namespaced":true},{"name":"daemonsets","kind":"DaemonSet","namespaced":true}]}`} {
		if e := d.Add([]byte(raw)); e != nil {
			t.Fatal(e)
		}
	}
	return d
}
func TestReleaseManifestASTAndSecrets(t *testing.T) {
	files, e := InlineReleaseFile("apiVersion: v1\nkind: ConfigMap\nmetadata: {name: config}\ndata:\n  Corefile: |\n    custom ${HOST}: {{ PORT }}\n  literal: ${LITERAL}\n---\napiVersion: v1\nkind: Secret\nmetadata: {name: secret}\nstringData: {password: '${PASSWORD}'}\n")
	if e != nil {
		t.Fatal(e)
	}
	f, e := FreezeReleaseManifest(files, map[string]ReleaseVariableValue{"HOST": {Type: "string", Value: "example.test"}, "PORT": {Type: "number", Value: json.Number("53")}, "LITERAL": {Type: "string", Value: "${KEEP}"}, "PASSWORD": {Type: "string", Value: "private-value", Secret: true}}, releaseTestDiscovery(t), "app")
	if e != nil {
		t.Fatal(e)
	}
	if len(f.Resources) != 2 || !f.Sensitive {
		t.Fatal("freeze lost resources or privacy")
	}
	config := f.Resources[0].Object
	if config["kind"] != "ConfigMap" {
		t.Fatal(config)
	}
	data := releaseMap(config["data"])
	if data["Corefile"] != "custom example.test: 53\n" || data["literal"] != "${KEEP}" {
		t.Fatal(data)
	}
	secret := f.Resources[1].Object
	if secret["stringData"] != nil || releaseMap(secret["data"])["password"] != "cHJpdmF0ZS12YWx1ZQ==" {
		t.Fatal("secret normalization failed")
	}
}
func TestReleaseManifestRejectsStructureInjection(t *testing.T) {
	for _, content := range []string{"apiVersion: v1\nkind: ConfigMap\nmetadata: {name: a, name: b}", "apiVersion: v1\nkind: ConfigMap\nmetadata: {name: a}\ndata: &d {a: b}\nother: *d", "apiVersion: v1\nkind: ConfigMap\nmetadata: {name: a}\ndata: {'${KEY}': value}", "apiVersion: v1\nkind: ConfigMap\nmetadata: {name: a}\ndata: {a: '${MISSING}'}", "apiVersion: v1\nkind: ConfigMap\nmetadata: {name: a}\n---\napiVersion: v1\nkind: ConfigMap\nmetadata: {name: a}"} {
		files, _ := InlineReleaseFile(content)
		if _, e := FreezeReleaseManifest(files, nil, releaseTestDiscovery(t), "app"); e == nil {
			t.Fatal("accepted ambiguous YAML", content)
		}
	}
	files, _ := InlineReleaseFile("apiVersion: v1\nkind: ConfigMap\nmetadata: {name: a}\ndata: {value: '${VALUE}'}")
	f, e := FreezeReleaseManifest(files, map[string]ReleaseVariableValue{"VALUE": {Type: "string", Value: "---\napiVersion: v1\nkind: Secret"}}, releaseTestDiscovery(t), "app")
	if e != nil || len(f.Resources) != 1 {
		t.Fatal("scalar changed document structure", e)
	}
}
func TestReleaseFileFreezeBoundsAndTraversal(t *testing.T) {
	root := t.TempDir()
	if e := os.Mkdir(filepath.Join(root, "manifests"), 0700); e != nil {
		t.Fatal(e)
	}
	for _, name := range []string{"b.yaml", "a.yml"} {
		if e := os.WriteFile(filepath.Join(root, "manifests", name), []byte("apiVersion: v1"), 0600); e != nil {
			t.Fatal(e)
		}
	}
	files, e := ReadReleaseFiles(root, "manifests")
	if e != nil || len(files) != 2 || !strings.HasSuffix(files[0].Path, "a.yml") {
		t.Fatal(files, e)
	}
	for _, p := range []string{"../outside", "C:\\secret", "manifests/../../secret"} {
		if _, e = ReadReleaseFiles(root, p); e == nil {
			t.Fatal("escaped root", p)
		}
	}
}
func releaseTestObject(s string) map[string]any {
	var obj map[string]any
	json.Unmarshal([]byte(s), &obj)
	return obj
}
func TestReleaseHealthRequiresCurrentOwnedPods(t *testing.T) {
	actual := releaseTestObject(`{"apiVersion":"apps/v1","kind":"Deployment","metadata":{"name":"app","namespace":"app","uid":"deployment-uid","generation":2},"spec":{"replicas":1,"template":{"spec":{"containers":[{"name":"app","image":"repo/image@sha256:abc"}]}}},"status":{"observedGeneration":2,"replicas":1,"updatedReplicas":1,"availableReplicas":1}}`)
	r := ReleaseResource{ID: ReleaseResourceID{Kind: "Deployment", Namespace: "app", Name: "app"}, Object: map[string]any{"kind": "Deployment"}, Health: "workload"}
	rs := releaseTestObject(`{"kind":"ReplicaSet","metadata":{"namespace":"app","uid":"rs-uid","ownerReferences":[{"uid":"deployment-uid","controller":true}]},"spec":{"template":{"spec":{"containers":[{"name":"app","image":"repo/image@sha256:abc"}]}}}}`)
	pod := releaseTestObject(`{"kind":"Pod","metadata":{"name":"p","namespace":"app","uid":"pod-uid","ownerReferences":[{"uid":"rs-uid","controller":true}]},"spec":{"containers":[{"name":"app","image":"repo/image@sha256:abc"}]},"status":{"phase":"Running","conditions":[{"type":"Ready","status":"True"},{"type":"ContainersReady","status":"True"},{"type":"Initialized","status":"True"}],"containerStatuses":[{"name":"app","image":"repo/image@sha256:abc","ready":true,"started":true,"state":{"running":{"startedAt":"now"}}}]}}`)
	if h := EvaluateReleaseHealth(r, actual, []map[string]any{pod}); h.State == "ready" {
		t.Fatal("selector without owner chain accepted")
	}
	if h := EvaluateReleaseHealth(r, actual, []map[string]any{rs, pod}); h.State != "ready" {
		t.Fatal(h)
	}
	releaseMap(releaseItems(releaseMap(pod["status"])["containerStatuses"])[0])["ready"] = false
	if h := EvaluateReleaseHealth(r, actual, []map[string]any{rs, pod}); h.State == "ready" {
		t.Fatal("Running unready container accepted")
	}
}
func TestReleaseHealthObservationWindowResets(t *testing.T) {
	var w ReleaseHealthWindow
	now := time.Now()
	if w.Observe(ReleaseHealth{State: "ready"}, now, 30) {
		t.Fatal("window bypass")
	}
	w.Observe(ReleaseHealth{State: "pending"}, now.Add(20*time.Second), 30)
	if w.Observe(ReleaseHealth{State: "ready"}, now.Add(31*time.Second), 30) {
		t.Fatal("old readiness survived flap")
	}
	if !w.Observe(ReleaseHealth{State: "ready"}, now.Add(62*time.Second), 30) {
		t.Fatal("stable ready not accepted")
	}
}
func TestReleaseDiscoveryUsesRealPluralAndScope(t *testing.T) {
	d := NewReleaseDiscovery()
	if e := d.Add([]byte(`{"kind":"APIResourceList","groupVersion":"example.io/v1","resources":[{"name":"people","kind":"Person","namespaced":false,"verbs":["get"]}]}`)); e != nil {
		t.Fatal(e)
	}
	r, e := d.Resolve("example.io/v1", "Person")
	if e != nil || r.Name != "people" {
		t.Fatal(r, e)
	}
	if _, e = r.Path("app", "x"); e == nil {
		t.Fatal("namespace silently ignored")
	}
	if p, e := r.Path("", "x"); e != nil || p != "/apis/example.io/v1/people/x" {
		t.Fatal(p, e)
	}
	if ReleaseAdapterFor(r.Group, r.Kind).Implemented {
		t.Fatal("discovery claimed adapter")
	}
}

func TestReleasePodAdmissionAdditionsRemainHealthyButRequiredFieldsCannotDrift(t *testing.T) {
	want := releaseTestObject(`{"containers":[{"name":"app","image":"repo:approved","env":[{"name":"CONFIG","value":"expected"}]}],"volumes":[{"name":"config","configMap":{"name":"config"}}]}`)
	actual := releaseTestObject(`{"containers":[{"name":"app","image":"repo:approved","env":[{"name":"CONFIG","value":"expected"},{"name":"INJECTED","value":"extra"}]},{"name":"mesh","image":"proxy:pinned"}],"volumes":[{"name":"config","configMap":{"name":"config"}},{"name":"kube-api-access","projected":{}}]}`)
	if !releasePodSpecMatches(want, actual) {
		t.Fatal("admission additions blocked current template")
	}
	if ReleaseObjectMatches(want, actual) {
		t.Fatal("strict approved-object drift check was weakened")
	}
	releaseMap(releaseItems(actual["containers"])[0])["image"] = "repo:foreign"
	if releasePodSpecMatches(want, actual) {
		t.Fatal("required approved image was ignored")
	}
	if ReleaseSafeReason("secretToken123") != "resource_check_failed" {
		t.Fatal("API reason leaked")
	}
}

func TestReleaseManifestExplicitRuntimeVariableEscape(t *testing.T) {
	files, _ := InlineReleaseFile("apiVersion: v1\nkind: Secret\nmetadata: {name: vars}\nstringData:\n  whole: '$${HOSTNAME}'\n  embedded: 'echo $${HOSTNAME} ${BOUND}'\n  value: '${SECRET}'\n")
	f, e := FreezeReleaseManifest(files, map[string]ReleaseVariableValue{"BOUND": {Type: "string", Value: "bound"}, "SECRET": {Type: "string", Value: "${LITERAL}", Secret: true}}, releaseTestDiscovery(t), "app")
	if e != nil {
		t.Fatal(e)
	}
	data := releaseMap(f.Resources[0].Object["data"])
	for key, want := range map[string]string{"whole": "${HOSTNAME}", "embedded": "echo ${HOSTNAME} bound", "value": "${LITERAL}"} {
		got, err := base64.StdEncoding.DecodeString(data[key].(string))
		if err != nil || string(got) != want {
			t.Fatal("literal escape changed", key, string(got), err)
		}
	}
}
func TestSecretBindingsCannotBecomePublicResourceOrContainerIdentity(t *testing.T) {
	for _, source := range []string{"apiVersion: v1\nkind: ConfigMap\nmetadata: {name: '${PASSWORD}'}\ndata: {value: safe}", "apiVersion: v1\nkind: Pod\nmetadata: {name: app}\nspec: {containers: [{name: '${PASSWORD}', image: 'repo:tag'}]}"} {
		files, e := InlineReleaseFile(source)
		if e != nil {
			t.Fatal(e)
		}
		if _, e = FreezeReleaseManifest(files, map[string]ReleaseVariableValue{"PASSWORD": {Type: "string", Value: "secret-name", Secret: true}}, releaseTestDiscovery(t), "app"); !errors.Is(e, ErrVariable) {
			t.Fatal("secret identifier exposed", e)
		}
	}
	files, _ := InlineReleaseFile("apiVersion: v1\nkind: Secret\nmetadata: {name: app}\nstringData: {name: '${PASSWORD}'}")
	if _, e := FreezeReleaseManifest(files, map[string]ReleaseVariableValue{"PASSWORD": {Type: "string", Value: "secret-value", Secret: true}}, releaseTestDiscovery(t), "app"); e != nil {
		t.Fatal("private Secret data mistaken for public identity", e)
	}
}

func TestReleasePodRequiresActualContainerImageAfterMutableSpecChange(t *testing.T) {
	pod := releaseTestObject(`{"apiVersion":"v1","kind":"Pod","metadata":{"name":"app","namespace":"app","uid":"pod"},"spec":{"containers":[{"name":"app","image":"repo/app:v2"}]},"status":{"phase":"Running","conditions":[{"type":"Ready","status":"True"},{"type":"ContainersReady","status":"True"},{"type":"Initialized","status":"True"}],"containerStatuses":[{"name":"app","image":"repo/app:v1","ready":true,"started":true,"state":{"running":{"startedAt":"now"}}}]}}`)
	if h := releasePodHealth(pod, false); h.State == "ready" || h.Reason != "container_image_not_observed" {
		t.Fatal("old image status counted ready", h)
	}
	releaseMap(releaseItems(releaseMap(pod["status"])["containerStatuses"])[0])["image"] = "repo/app:v2"
	if h := releasePodHealth(pod, false); h.State != "ready" {
		t.Fatal(h)
	}
	if !releaseContainerImageMatches("busybox", "docker.io/library/busybox:latest") {
		t.Fatal("canonical runtime name rejected")
	}
}
func TestReleaseManifestRejectsNonStringNamespace(t *testing.T) {
	for _, namespace := range []string{"42", "true", "[app]"} {
		files, _ := InlineReleaseFile("apiVersion: v1\nkind: ConfigMap\nmetadata: {name: config, namespace: " + namespace + "}\ndata: {x: y}")
		if _, e := FreezeReleaseManifest(files, nil, releaseTestDiscovery(t), "app"); !errors.Is(e, ErrManifest) {
			t.Fatal("invalid namespace silently defaulted", namespace, e)
		}
	}
}
