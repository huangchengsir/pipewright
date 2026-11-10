package pipeline

import "testing"

func releaseConfigFixture() map[string]any {
	return map[string]any{"version": 2, "mode": "manifest", "connectionId": "conn", "credentialId": "kcred_test", "context": "test", "namespace": "app", "executor": map[string]any{"kind": "local"}, "source": map[string]any{"kind": "inline", "content": "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: example\n"}}
}
func TestKubernetesReleaseV2StrictModes(t *testing.T) {
	c := releaseConfigFixture()
	got, e := ParseKubernetesReleaseConfig(c)
	if e != nil || got.TimeoutSeconds != 300 || *got.ObserveSeconds != 30 {
		t.Fatalf("config=%+v err=%v", got, e)
	}
	s := Spec{Stages: []Stage{{ID: "deploy", Kind: KindDeploy, Jobs: []Job{{ID: "k", Type: KubernetesJobType, Config: c}}}}}
	if e = ValidateKubernetesStages(s); e != nil {
		t.Fatal(e)
	}
	for _, mutate := range []func(map[string]any){func(c map[string]any) { c["Version"] = 2 }, func(c map[string]any) {
		c["variables"] = []any{map[string]any{"name": "PASSWORD", "type": "string", "source": "secret", "ref": "kcred_protected"}}
	}, func(c map[string]any) { c["source"] = `{"kind":"inline","Kind":"git","content":"x"}` }, func(c map[string]any) { c["source"] = map[string]any{"kind": "git", "path": "../secret"} }, func(c map[string]any) { c["mode"] = "image" }, func(c map[string]any) { c["source"] = map[string]any{"kind": "inline", "content": "x", "path": "repo"} }, func(c map[string]any) {
		c["variables"] = []any{map[string]any{"name": "PASSWORD", "type": "string", "source": "secret", "ref": "key", "value": "raw"}}
	}} {
		bad := releaseConfigFixture()
		mutate(bad)
		if _, e := ParseKubernetesReleaseConfig(bad); e == nil {
			t.Fatal("accepted ambiguous config", bad)
		}
	}
}
func TestKubernetesReleaseImageSources(t *testing.T) {
	c := releaseConfigFixture()
	delete(c, "source")
	c["mode"] = "image"
	c["workload"] = map[string]any{"kind": "StatefulSet", "name": "app"}
	c["images"] = []any{map[string]any{"container": "app", "source": map[string]any{"kind": "external", "reference": "registry.example/app:stable"}}}
	s := Spec{Stages: []Stage{{ID: "deploy", Kind: KindDeploy, Jobs: []Job{{ID: "k", Type: KubernetesJobType, Config: c}}}}}
	if e := ValidateKubernetesStages(s); e != nil {
		t.Fatal(e)
	}
	c["images"] = []any{map[string]any{"container": "app", "source": map[string]any{"kind": "artifact", "artifact": map[string]any{"stageId": "other", "jobId": "push", "declarationIndex": 0}}}}
	if e := ValidateKubernetesStages(s); e == nil {
		t.Fatal("selector expanded ancestry")
	}
}
