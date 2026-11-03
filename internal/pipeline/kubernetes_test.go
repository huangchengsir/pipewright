package pipeline

import (
	"encoding/json"
	"testing"
)

func kubeSpec() Spec {
	return Spec{Stages: []Stage{{ID: "build", Kind: KindBuild, Jobs: []Job{{ID: "push", Type: "push_image"}}}, {ID: "deploy", Kind: KindDeploy, Needs: []string{"build"}, Jobs: []Job{{ID: "kube", Type: KubernetesJobType, Config: map[string]any{"connectionId": "connection-1", "credentialId": "kcred_test", "context": "production", "namespace": "app", "deployment": "backend", "container": "app", "artifactSource": `{"stageId":"build","jobId":"push","declarationIndex":0}`, "executor": map[string]any{"kind": "local"}}}}}}}
}
func TestStrictKubernetesConfig(t *testing.T) {
	if e := ValidateKubernetesStages(kubeSpec()); e != nil {
		t.Fatal(e)
	}
	for _, change := range []func(*Spec){func(s *Spec) { s.Stages[1].Jobs[0].Config["script"] = "kubectl apply" }, func(s *Spec) {
		s.Stages[1].Jobs[0].Config["executor"] = map[string]any{"kind": "local", "serverId": "server-1"}
	}, func(s *Spec) { s.Stages[1].Jobs[0].Config["credentialId"] = "ordinary-secret" }, func(s *Spec) {
		s.Stages[1].Jobs[0].Config["artifactSource"] = `{"stageId":"build","jobId":"push","declarationIndex":0,"latest":true}`
	}, func(s *Spec) { s.Stages[0].Jobs[0].Type = "script" }, func(s *Spec) { s.Stages[1].AllowFailure = true }} {
		s := kubeSpec()
		change(&s)
		if e := ValidateKubernetesStages(s); e == nil {
			b, _ := json.Marshal(s)
			t.Fatal("unsafe config accepted", string(b))
		}
	}
}
func TestProtectedKubernetesCredentialCannotBeGenericReference(t *testing.T) {
	s := kubeSpec()
	s.Stages[0].Jobs[0].Config = map[string]any{"credentialId": "kcred_test"}
	if e := ValidateKubernetesStages(s); e != ErrProtectedCredential {
		t.Fatal(e)
	}
}

func TestKubernetesStringConfigAndDuplicateKeys(t *testing.T) {
	s := kubeSpec()
	c := s.Stages[1].Jobs[0].Config
	c["executor"] = `{"kind":"ssh","serverId":"server-1"}`
	c["timeoutSeconds"] = "300"
	if e := ValidateKubernetesStages(s); e != nil {
		t.Fatal(e)
	}
	for _, bad := range []string{`{"kind":"local","kind":"ssh","serverId":"server-1"}`, `{"kind":"local","Kind":"ssh"}`, `{"kind":"ssh","serverId":"server-1","script":"run"}`} {
		c["executor"] = bad
		if e := ValidateKubernetesStages(s); e == nil {
			t.Fatal("ambiguous executor", bad)
		}
	}
	c["executor"] = `{"kind":"local"}`
	for _, bad := range []string{"3e2", "0300", "300.0", " 300", "-300"} {
		c["timeoutSeconds"] = bad
		if e := ValidateKubernetesStages(s); e == nil {
			t.Fatal("ambiguous timeout", bad)
		}
	}
}
