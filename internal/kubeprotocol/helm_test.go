package kubeprotocol

import "testing"

func TestHelmStorageRequiresExplicitReleaseScopedCapability(t *testing.T) {
	scope := Resource{Version: "v1", Resource: "secrets", Kind: "Secret", Namespace: "app", Name: "sh.helm.release.v1.app", Actions: []string{"get", "create"}}
	if HelmStorageResource(scope, "sh.helm.release.v1.app.v1") {
		t.Fatal("old Secret permission expanded")
	}
	scope.Actions = append(scope.Actions, "helm-storage")
	for _, name := range []string{"sh.helm.release.v1.app.v1", "sh.helm.release.v1.app.v10000"} {
		if !HelmStorageResource(scope, name) {
			t.Fatal(name)
		}
	}
	for _, name := range []string{"sh.helm.release.v1.other.v1", "sh.helm.release.v1.app.v01", "sh.helm.release.v1.app.v0", "sh.helm.release.v1.app.v10001", "sh.helm.release.v1.app.v1/extra"} {
		if HelmStorageResource(scope, name) {
			t.Fatal("storage anchor escaped", name)
		}
	}
}
