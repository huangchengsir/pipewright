package httpapi

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/huangchengsir/pipewright/internal/i18n"
	"github.com/huangchengsir/pipewright/internal/pipeline"
)

const invalidKubernetesConfigMessage = "Kubernetes 发布配置无效，请检查来源和资源设置"

func TestPipelineKubernetesConfigErrorIs400AndClosed(t *testing.T) {
	out := httptest.NewRecorder()
	writePipelineError(out, fmt.Errorf("private-config-error-canary: %w", pipeline.ErrKubernetesConfig))
	var body errBody
	if json.Unmarshal(out.Body.Bytes(), &body) != nil || out.Code != 400 || body.Error.Code != "kubernetes_invalid_input" || strings.Contains(out.Body.String(), "private-config-error-canary") {
		t.Fatal("invalid Kubernetes config misclassified or raw error exposed", out.Code)
	}
}
func TestPipelineHTTPInvalidKubernetesImageProducerReturnsLocalized400WithoutSave(t *testing.T) {
	srv, client, csrf, project := setupPipelineServer(t)
	endpoint := srv.URL + "/api/projects/" + project + "/pipeline"
	seed := `{"stages":[{"id":"source","name":"Source","kind":"source","jobs":[{"id":"git","name":"Git","type":"git_source","config":{}}]},{"id":"build","name":"Build","kind":"build","needs":["source"],"jobs":[{"id":"image","name":"Build image","type":"build_image","config":{"dockerfile":"Dockerfile"}}]}]}`
	seeded := doJSON(t, client, "PUT", endpoint, csrf, seed)
	seeded.Body.Close()
	if seeded.StatusCode != 200 {
		t.Fatal("saved baseline fixture failed")
	}
	before := doJSON(t, client, "GET", endpoint, csrf, "")
	original, _ := io.ReadAll(before.Body)
	before.Body.Close()
	body := `{"stages":[{"id":"source","name":"Source","kind":"source","jobs":[{"id":"git","name":"Git","type":"git_source","config":{}}]},{"id":"build","name":"Build","kind":"build","needs":["source"],"jobs":[{"id":"image","name":"Build image","type":"build_image","config":{"dockerfile":"Dockerfile"}}]},{"id":"deploy","name":"Deploy","kind":"deploy","needs":["build"],"jobs":[{"id":"kube","name":"Kubernetes","type":"deploy_kubernetes","config":{"version":2,"mode":"image","connectionId":"conn","credentialId":"kcred_fixture","context":"private-config-canary","namespace":"app","executor":{"kind":"local"},"workload":{"kind":"Deployment","name":"backend"},"images":[{"container":"app","source":{"kind":"artifact","artifact":{"stageId":"build","jobId":"image","declarationIndex":0}}}]}}]}]}`
	for _, locale := range i18n.Supported {
		request, _ := http.NewRequest("PUT", endpoint, strings.NewReader(body))
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("X-CSRF-Token", csrf)
		request.Header.Set("X-Pipewright-Locale", locale)
		response, err := client.Do(request)
		if err != nil {
			t.Fatal("HTTP fixture failed")
		}
		safe, _ := io.ReadAll(response.Body)
		response.Body.Close()
		var failure errBody
		if json.Unmarshal(safe, &failure) != nil || response.StatusCode != 400 || failure.Error.Code != "kubernetes_invalid_input" || failure.Error.Message != i18n.T(locale, invalidKubernetesConfigMessage) || strings.Contains(string(safe), "private-config-canary") {
			t.Fatalf("invalid selector HTTP mapping/localization failed locale=%s status=%d", locale, response.StatusCode)
		}
		if locale != "zh-CN" && failure.Error.Message == invalidKubernetesConfigMessage {
			t.Fatal("new message missing locale translation", locale)
		}
	}
	after := doJSON(t, client, "GET", endpoint, csrf, "")
	saved, _ := io.ReadAll(after.Body)
	after.Body.Close()
	if string(saved) != string(original) {
		t.Fatal("invalid image producer changed saved pipeline")
	}
}
