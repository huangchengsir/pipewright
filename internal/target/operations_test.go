package target

import (
	"reflect"
	"strings"
	"testing"
)

func TestOpsActionsNarrowerThanLegacyManual(t *testing.T) {
	for _, action := range []string{"start", "stop", "restart", "pause", "unpause"} {
		if err := ValidateOpsAction("docker", "app", action); err != nil {
			t.Fatal(action, err)
		}
	}
	for _, action := range []string{"kill", "rm"} {
		if err := ValidateServiceParams("docker", "app", action); err != nil {
			t.Fatal("legacy manual regression", err)
		}
		if err := ValidateOpsAction("docker", "app", action); err == nil {
			t.Fatal("assistant authorized legacy destructive action", action)
		}
	}
	for _, target := range []string{"", "-app", "app;whoami", "a\nnext", strings.Repeat("a", 257), "a\u2028next"} {
		if ValidateServiceParams("docker", target, "restart") == nil {
			t.Fatal(target)
		}
	}
	if err := ValidateServiceParams("systemd", "nginx", "pause"); err == nil || err.Error() != "非法 action(systemd 仅支持 restart/stop/start)" {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(BuildServiceCmd("systemd", "restart", "nginx"), []string{"systemctl", "restart", "nginx"}) {
		t.Fatal("argv changed")
	}
	if BuildServiceCmd("unsupported", "start", "app") != nil {
		t.Fatal("unknown type")
	}
}
