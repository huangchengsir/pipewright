package ai

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/huangchengsir/pipewright/internal/mask"
)

func TestMonitoringDiagnosisUsesMetricsContext(t *testing.T) {
	for _, provider := range []string{ProviderClaude, ProviderOpenAI, ProviderOllama} {
		t.Run(provider, func(t *testing.T) {
			srv, capture := diagStubLLM(t, provider, stubDiagnosisJSON)
			svc, _, _ := newService(t, srv.Client())
			configureEnabled(t, svc, provider, srv.URL)
			m := mask.NewMasker()
			m.RegisterSecret(fakeRunSecret)
			got, err := svc.Diagnose(ctx(), DiagnoseInput{Monitoring: true, FailureLog: `{"kind":"metrics","cpuLoadRatioPercent":91}` + "\n" + `{"kind":"processes","name":"` + fakeRunSecret + `"}`, Masker: m})
			if err != nil || got.Status != "ready" {
				t.Fatalf("diagnosis: %+v %v", got, err)
			}
			var body any
			if err := json.Unmarshal([]byte(capture.get()), &body); err != nil {
				t.Fatal(err)
			}
			request, _ := json.Marshal(body)
			if strings.Contains(string(request), fakeRunSecret) {
				t.Fatal("monitoring input leaked secret")
			}
			for _, expected := range []string{"cpuLoadRatioPercent", "进程", "中文", "fixScript"} {
				if !strings.Contains(string(request), expected) {
					t.Fatalf("monitoring context missing %s", expected)
				}
			}
		})
	}
}
