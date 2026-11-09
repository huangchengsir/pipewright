package kubeagent

import (
	"github.com/huangchengsir/pipewright/internal/kubeprotocol"
	"slices"
	"testing"
)

func TestHelmCommandClientValidationMatchesPlanningAndExecution(t *testing.T) {
	for _, action := range []string{"install", "upgrade"} {
		for _, planning := range []bool{false, true} {
			args := helmCommandArgs(kubeprotocol.HelmTask{Action: action, Name: "app", Namespace: "app", TimeoutSeconds: 30}, planning, "/private/chart.tgz", "/private/values.yaml", "/private/config")
			count := 0
			for _, arg := range args {
				if arg == "--disable-openapi-validation" {
					count++
				}
			}
			if count != 1 {
				t.Errorf("%s planning=%v: incompatible client schema validation enabled", action, planning)
			}
			for _, flag := range []string{"--skip-crds", "--no-hooks", "--wait=hookOnly", "--timeout", "30s", "--kubeconfig=/private/config", "--values", "/private/values.yaml"} {
				if !slices.Contains(args, flag) {
					t.Errorf("lost controlled Helm flag %s", flag)
				}
			}
			if slices.Contains(args, "--dry-run=server") != planning {
				t.Error("planning dry-run changed")
			}
			if action == "upgrade" && (!slices.Contains(args, "--server-side=true") || !slices.Contains(args, "--history-max=0")) {
				t.Error("upgrade server mutation/history policy changed")
			}
			for _, arg := range args {
				if arg == "--force" || arg == "--force-conflicts" || arg == "--insecure-skip-tls-verify" {
					t.Error("unsafe CLI policy")
				}
			}
		}
	}
	for _, action := range []string{"template", "rollback", "status", "history"} {
		args := helmCommandArgs(kubeprotocol.HelmTask{Action: action, Name: "app", Namespace: "app", TimeoutSeconds: 30, Revision: 2}, false, "/private/chart.tgz", "", "/private/config")
		if slices.Contains(args, "--disable-openapi-validation") || slices.Contains(args, "--dry-run=server") {
			t.Errorf("unsupported flag added to %s", action)
		}
		if action == "rollback" && (!slices.Contains(args, "2") || !slices.Contains(args, "--server-side=true") || !slices.Contains(args, "--wait=hookOnly")) {
			t.Error("rollback semantics changed")
		}
	}
}
