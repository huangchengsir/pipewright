package pipeline

import "testing"

func TestReview106ConfigRejectsImpossibleProbeWait(t *testing.T) {
	for _, kind := range []string{"storage", "network-deny"} {
		for _, wait := range []int{0, 1} {
			t.Run(kind+string(rune('0'+wait)), func(t *testing.T) {
				cfg := releaseConfigFixture()
				checks := []any{map[string]any{"kind": "storage", "name": "check", "target": "claim", "waitSeconds": wait}}
				if kind == "network-deny" {
					checks = []any{map[string]any{"kind": "network-allow", "name": "allow", "target": "10.0.0.1:80"}, map[string]any{"kind": "network-deny", "name": "deny", "target": "10.0.0.1:80", "positiveControl": "allow", "waitSeconds": wait}}
				}
				cfg["checks"] = checks
				_, e := ParseKubernetesReleaseConfig(cfg)
				if (e == nil) != (wait == 0) {
					t.Fatal("unsupported wait was not rejected before runtime", e)
				}
			})
		}
	}
}
