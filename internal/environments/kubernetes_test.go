package environments

import "testing"

func TestKubernetesEnvironmentRequiresWholeRunSuccess(t *testing.T) {
	cases := []struct {
		run     string
		ready   bool
		targets []TargetSummary
		want    string
	}{{"success", true, nil, DeployStatusSuccess}, {"failed", true, nil, DeployStatusFailed}, {"running", true, nil, DeployStatusFailed}, {"success", false, nil, DeployStatusFailed}, {"success", true, []TargetSummary{{Status: "failed"}}, DeployStatusFailed}, {"success", true, []TargetSummary{{Status: "success"}}, DeployStatusSuccess}}
	for _, tc := range cases {
		if got := kubernetesDeploymentStatus(tc.run, tc.ready, tc.targets); got != tc.want {
			t.Fatal(tc, got)
		}
	}
}
