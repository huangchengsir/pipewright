package opschat

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/huangchengsir/pipewright/internal/vault"
)

type selectionFixtureModel struct {
	*fakeModel
	alternative ProviderInfo
}

func (m *selectionFixtureModel) Providers() []ProviderInfo {
	m.mu.Lock()
	defer m.mu.Unlock()
	return []ProviderInfo{m.info, m.alternative}
}

func (m *selectionFixtureModel) Analyze(_ context.Context, request AnalysisRequest) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if request.Provider != m.info && request.Provider != m.alternative {
		return "", ErrConsent
	}
	m.analyses = append(m.analyses, request)
	return "results interpreted", nil
}

func TestSelectedModelPersistsPerReplyAndRejectsStaleSelection(t *testing.T) {
	ctx := context.Background()
	m := &selectionFixtureModel{
		fakeModel: model(),
		alternative: ProviderInfo{
			Provider: "fake", Model: "configured-other", ConfigHash: strings.Repeat("f", 64),
		},
	}
	server := uuid.NewString()
	s, _, _ := fixture(t, executor(server), m)
	initial := s.Capabilities()
	if len(initial.Models) != 2 || initial.Provider != initial.Models[0] || initial.Models[1] != m.alternative {
		t.Fatalf("unexpected available bindings: %+v", initial)
	}
	c := chat(t, s, server)
	command := TurnInput{ClientRequestID: uuid.NewString(), Revision: c.Revision, Text: "inspect state", Provider: initial.Models[1], AnalyzeResults: true}
	first, err := s.Submit(ctx, c.ID, command)
	if err != nil {
		t.Fatal(err)
	}
	waitSettledRun(t, s, c.ID, first.ID, Succeeded)
	m.mu.Lock()
	m.alternative.ConfigHash = strings.Repeat("a", 64)
	m.mu.Unlock()
	// Retrying the exact accepted request must still return the original run.
	if original, err := s.Submit(ctx, c.ID, command); err != nil || original.ID != first.ID {
		t.Fatalf("duplicate submission got %+v, %v", original, err)
	}
	c, err = s.Get(ctx, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	command.ClientRequestID, command.Revision = uuid.NewString(), c.Revision
	if _, err = s.Submit(ctx, c.ID, command); !errors.Is(err, ErrConsent) {
		t.Fatalf("stale selected model returned %v", err)
	}
	command.Provider, command.AnalyzeResults = ProviderInfo{}, false
	second, err := s.Submit(ctx, c.ID, command)
	if err != nil {
		t.Fatal(err)
	}
	waitSettledRun(t, s, c.ID, second.ID, Succeeded)
	entries, err := s.Entries(ctx, c.ID, "", 0)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{first.ID: initial.Models[1].Model, second.ID: initial.Provider.Model}
	for _, entry := range entries.Entries {
		if entry.Kind != "assistant" {
			continue
		}
		if entry.Model != want[entry.RunID] || entry.Model == "" {
			t.Fatalf("wrong persisted model for %s: %q", entry.RunID, entry.Model)
		}
		delete(want, entry.RunID)
	}
	if len(want) != 0 {
		t.Fatalf("missing model-bearing messages: %v", want)
	}
	tool := submitTool(t, s, c, "host_ports", "{}")
	waitSettledRun(t, s, c.ID, tool.ID, Succeeded)
	analysis, err := s.Analyze(ctx, c.ID, AnalysisInput{
		ClientRequestID: uuid.NewString(), Revision: c.Revision, RunID: tool.ID,
		AnalyzeResults: true, Provider: s.Capabilities().Models[1],
	})
	if err != nil {
		t.Fatal(err)
	}
	waitSettledRun(t, s, c.ID, analysis.ID, Succeeded)
	entries, err = s.Entries(ctx, c.ID, "", 0)
	if err != nil {
		t.Fatal(err)
	}
	var foundAnalysis bool
	for _, entry := range entries.Entries {
		if entry.Kind == "analysis" && entry.RunID == analysis.ID {
			foundAnalysis = entry.Model == initial.Models[1].Model
		}
	}
	if !foundAnalysis {
		t.Fatal("missing selected model label on analyzed results")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.analyses) != 1 || m.analyses[0].Provider != m.alternative {
		t.Fatal("analysis did not use selected binding")
	}
	if len(m.plans) != 2 || m.plans[0].Provider != initial.Models[1] || m.plans[1].Provider != initial.Provider {
		t.Fatalf("planner did not receive exact submitted provider")
	}
}

func TestModelCapabilitiesRegisterVaultCredentialsBeforeExposure(t *testing.T) {
	secret := "catalogue-persisted-credential"
	model := &selectionFixtureModel{
		fakeModel: model(),
		alternative: ProviderInfo{
			Provider: "fake", Model: "model-" + secret[:len(secret)-1], ConfigHash: strings.Repeat("f", 64),
		},
	}
	s, v, _ := fixture(t, executor(uuid.NewString()), model)
	if _, err := v.Create(vault.CreateInput{Name: "catalogue test", Type: vault.TypeGitToken, Secret: secret}); err != nil {
		t.Fatal(err)
	}
	if s.masker.ScrubTruncated(model.alternative.Model) != model.alternative.Model {
		t.Fatal("fixture does not have a cold secret masker")
	}
	capabilities := s.CapabilitiesContext(context.Background())
	if !capabilities.Available || !capabilities.ModelAvailable || len(capabilities.Models) != 1 || capabilities.Provider != model.Info() {
		t.Fatal("catalogue leaked a truncated stored credential or omitted the safe default")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	unavailable := s.CapabilitiesContext(ctx)
	if !unavailable.Available || unavailable.ModelAvailable || len(unavailable.Models) != 0 {
		t.Fatal("failed catalogue registration affected explicit tools or returned model choices")
	}
}
