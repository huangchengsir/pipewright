package ai

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/huangchengsir/pipewright/internal/mask"
	"github.com/huangchengsir/pipewright/internal/opschat"
	"github.com/huangchengsir/pipewright/internal/vault"
)

func TestTerminalPreviewIsLocalAndScrubsEachField(t *testing.T) {
	var count atomic.Int32
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { count.Add(1); t.Error("preview dispatched model") }))
	defer remote.Close()
	svc, _, _ := newService(t, remote.Client())
	key := "terminal-provider-secret"
	if _, err := svc.Save(ctx(), SaveInput{Provider: ProviderOpenAI, BaseURL: remote.URL, Model: "local-fake", APIKey: &key, Enabled: true}); err != nil {
		t.Fatal(err)
	}
	secret := "terminal-vault-secret-long"
	actual := svc.(*service)
	if _, err := actual.vault.Create(vault.CreateInput{Name: "terminal fixture", Type: vault.TypeGitToken, Secret: secret}); err != nil {
		t.Fatal(err)
	}
	values := []string{key, secret, "Bearer unknown-bearer", "password=unknown-password", "-----BEGIN PRIVATE KEY-----\nprivate-fixture", secret[:10], "safe output"}
	out, err := actual.PreviewTerminal(ctx(), values)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < len(values)-1; i++ {
		if out.Values[i] == values[i] {
			t.Errorf("field %d was not scrubbed", i)
		}
	}
	if out.Values[6] != "safe output" || len(out.Provider.ConfigHash) != 64 || count.Load() != 0 {
		t.Fatal("unexpected preview metadata or dispatch")
	}
	if _, err = actual.PreviewTerminal(ctx(), []string{strings.Repeat("中", 6000)}); !errors.Is(err, ErrTerminalSensitive) {
		t.Fatal("UTF8 byte limit not enforced")
	}
}

func TestTerminalCompleteConsentAndSensitiveBoundaries(t *testing.T) {
	var count atomic.Int32
	var response atomic.Value
	response.Store("ls -la")
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count.Add(1)
		raw, _ := io.ReadAll(r.Body)
		if strings.Contains(string(raw), "fixture-vault-secret") || strings.Contains(string(raw), "fixture-provider-key") {
			t.Error("secret reached prompt")
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]string{"content": response.Load().(string)}}}})
	}))
	defer remote.Close()
	svc, _, _ := newService(t, remote.Client())
	actual := svc.(*service)
	key := "fixture-provider-key"
	_, err := svc.Save(ctx(), SaveInput{Provider: ProviderOpenAI, BaseURL: remote.URL, Model: "local-fake", APIKey: &key, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	_, err = actual.vault.Create(vault.CreateInput{Name: "fixture", Type: vault.TypeGitToken, Secret: "fixture-vault-secret"})
	if err != nil {
		t.Fatal(err)
	}
	preview, err := actual.PreviewTerminal(ctx(), []string{"ls"})
	if err != nil {
		t.Fatal(err)
	}
	for _, prefix := range []string{"fixture-vault-secret", "ls " + "fixture-vault-secret"[8:], "ls " + "fixture-vault-secret"[3:12], key, "password=unknown", "ls\nwhoami", "ls\x1b[31m", strings.Repeat("中", 400)} {
		_, err = svc.CompleteCommand(ctx(), CompleteCommandInput{Partial: prefix, ExpectedProvider: &preview.Provider})
		if !errors.Is(err, ErrTerminalSensitive) {
			t.Fatalf("expected sensitive rejection: %v", err)
		}
	}
	if count.Load() != 0 {
		t.Fatal("rejected input dispatched")
	}
	bad := preview.Provider
	bad.ConfigHash = "changed"
	if _, err = svc.CompleteCommand(ctx(), CompleteCommandInput{Partial: "ls", ExpectedProvider: &bad}); !errors.Is(err, opschat.ErrConsent) {
		t.Fatal(err)
	}
	if count.Load() != 0 {
		t.Fatal("stale consent dispatched")
	}
	out, err := svc.CompleteCommand(ctx(), CompleteCommandInput{Partial: "ls", ExpectedProvider: &preview.Provider})
	if err != nil || out.Completion != "ls -la" || count.Load() != 1 {
		t.Fatal(out, err, count.Load())
	}
	for _, unsafe := range []string{"ls\nwhoami", "ls\x1b[31m", "ls fixture-vault-secret", "ls " + "fixture-vault-secret"[8:], "ls " + "fixture-vault-secret"[3:12], "other"} {
		response.Store(unsafe)
		out, err = svc.CompleteCommand(ctx(), CompleteCommandInput{Partial: "ls", ExpectedProvider: &preview.Provider})
		if err != nil || out.Completion != "" {
			t.Fatal("unsafe completion retained", err)
		}
	}
	// Do not restore an original sensitive prefix after an invalid model response.
	m := mask.NewMasker()
	m.RegisterSecret("unregistered-fixture")
	_, err = svc.CompleteCommand(ctx(), CompleteCommandInput{Partial: "ls unregistered-fixture", Masker: m})
	if !errors.Is(err, ErrTerminalSensitive) {
		t.Fatal(err)
	}
}

func TestTerminalPreviewFailsClosedWithoutVault(t *testing.T) {
	svc, db, _ := newService(t, http.DefaultClient)
	actual := svc.(*service)
	actual.vault = vault.New(db, nil)
	if _, err := actual.PreviewTerminal(ctx(), []string{"safe"}); err == nil {
		t.Fatal("unavailable vault accepted")
	}
	if _, err := svc.CompleteCommand(ctx(), CompleteCommandInput{Partial: "ls", ExpectedProvider: &opschat.ProviderInfo{ConfigHash: "x"}}); err == nil {
		t.Fatal("unavailable vault accepted completion")
	}
}

func TestTerminalPreviewCroppedRegisteredSecretsAndBoundedFailure(t *testing.T) {
	var count atomic.Int32
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { count.Add(1); t.Error("redaction invoked provider") }))
	defer remote.Close()
	svc, _, _ := newService(t, remote.Client())
	actual := svc.(*service)
	key := "local-provider-key-XY1234567890"
	_, err := svc.Save(ctx(), SaveInput{Provider: ProviderOpenAI, BaseURL: remote.URL, Model: "m", APIKey: &key, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	secret := "ABCDEFGHIJ0123456789abcdefghijZYXWVUTSRQ"
	_, err = actual.vault.Create(vault.CreateInput{Name: "cropped fixture", Type: vault.TypeGitToken, Secret: secret})
	if err != nil {
		t.Fatal(err)
	}
	for _, fragment := range []string{secret[:8], secret[8:24], secret[len(secret)-8:], key[6:18]} {
		out, err := actual.PreviewTerminal(ctx(), []string{"prefix " + fragment + " suffix"})
		if err != nil || len(out.Values) != 1 || out.Values[0] != mask.Placeholder {
			t.Fatal("cropped registered secret not fully masked", err)
		}
	}
	preview, err := actual.PreviewTerminal(ctx(), []string{"ls"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = svc.CompleteCommand(ctx(), CompleteCommandInput{Partial: "ls", Context: CommandContext{Cwd: secret[8:24]}, ExpectedProvider: &preview.Provider})
	if !errors.Is(err, ErrTerminalSensitive) {
		t.Fatal("sensitive context was not rejected")
	}
	oversized := mask.NewMasker()
	oversized.RegisterSecret(strings.Repeat("Z", (4<<20)+1))
	if _, err = svc.CompleteCommand(ctx(), CompleteCommandInput{Partial: "ls", Masker: oversized}); err == nil {
		t.Fatal("scan budget failure dispatched")
	}
	if count.Load() != 0 {
		t.Fatal("redaction failures dispatched provider")
	}
}
