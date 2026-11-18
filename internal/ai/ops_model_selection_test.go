package ai

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/huangchengsir/pipewright/internal/mask"
	"github.com/huangchengsir/pipewright/internal/opschat"
)

func TestOpsConfiguredModelsUseSameConnectionAndFreshBinding(t *testing.T) {
	var mu sync.Mutex
	var routed []string
	key := "model-catalog-private-credential"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+key || r.URL.Path != "/v1/chat/completions" {
			t.Error("selected model changed saved request destination or authorization")
		}
		var body struct{ Model string }
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		mu.Lock()
		routed = append(routed, body.Model)
		mu.Unlock()
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]string{"content": `{"text":"done","actions":[]}`}}}})
	}))
	defer server.Close()
	svc, db, _ := newService(t, server.Client())
	if _, err := svc.Save(ctx(), SaveInput{Provider: ProviderOpenAI, BaseURL: server.URL, Model: "default", APIKey: &key, Enabled: true}); err != nil {
		t.Fatal(err)
	}
	m := NewOpsModel(svc, mask.NewMasker(), " extra ", "invalid model", "default", "extra", key).(*opsModel)
	catalogue := m.Providers()
	if len(catalogue) != 2 || catalogue[0] != m.Info() || catalogue[1].Model != "extra" ||
		catalogue[1].Provider != catalogue[0].Provider || catalogue[1].ConfigHash == catalogue[0].ConfigHash {
		t.Fatalf("unexpected model catalogue: %+v", catalogue)
	}
	in := opsRequest()
	in.Provider = catalogue[1]
	if _, err := m.Plan(ctx(), in); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Analyze(ctx(), opschat.AnalysisRequest{
		Provider: catalogue[1], Items: []opschat.AnalysisItem{{CallID: "x", Output: "approved"}}, MaxResponseBytes: 4096,
	}); err != nil {
		t.Fatal(err)
	}
	in.Provider.Model = "not-on-allowlist"
	if _, err := m.Plan(ctx(), in); !errors.Is(err, opschat.ErrConsent) {
		t.Fatalf("unconfigured model returned %v", err)
	}
	if _, err := db.Exec("UPDATE ai_config SET model=? WHERE id=1", "changed-default"); err != nil {
		t.Fatal(err)
	}
	in.Provider = catalogue[1]
	if _, err := m.Plan(ctx(), in); !errors.Is(err, opschat.ErrConsent) {
		t.Fatalf("stale alternative returned %v", err)
	}
	in.Provider = opschat.ProviderInfo{}
	if _, err := m.Plan(ctx(), in); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if !reflect.DeepEqual(routed, []string{"extra", "extra", "changed-default"}) {
		t.Fatalf("provider received %v", routed)
	}
	if len(m.Providers()) != 3 || strings.Contains(m.Info().Model, key) {
		t.Fatal("catalogue did not follow changed default")
	}
}

func TestOpsModelCatalogueHidesShortCredentials(t *testing.T) {
	svc, _, _ := newService(t, &http.Client{})
	key := "xy"
	if _, err := svc.Save(ctx(), SaveInput{
		Provider: ProviderOpenAI, BaseURL: "http://127.0.0.1:12345", Model: "default", APIKey: &key, Enabled: true,
	}); err != nil {
		t.Fatal(err)
	}
	model := NewOpsModel(svc, mask.NewMasker(), key, "provider-"+key+"-preview", "clean-model").(*opsModel)
	catalogue := model.Providers()
	if len(catalogue) != 2 || catalogue[0].Model != "default" || catalogue[1].Model != "clean-model" {
		t.Fatal("catalogue exposed a model containing the configured credential or dropped a valid model")
	}
}
