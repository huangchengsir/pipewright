package ai

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/huangchengsir/pipewright/internal/mask"
	"github.com/huangchengsir/pipewright/internal/opschat"
	"github.com/huangchengsir/pipewright/internal/vault"
)

type rotatingProbeVault struct {
	vault.Vault
	rotate func()
}

func (v *rotatingProbeVault) OpenSecret(sealed []byte) ([]byte, error) {
	if v.rotate != nil {
		rotate := v.rotate
		v.rotate = nil
		rotate()
	}
	return v.Vault.OpenSecret(sealed)
}

func TestProbeUsesOneConfigurationAndKeySnapshot(t *testing.T) {
	var oldRequests, newRequests atomic.Int32
	oldServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		oldRequests.Add(1)
		if r.Header.Get("Authorization") != "Bearer old-key" {
			t.Error("probe mixed destinations and keys")
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer oldServer.Close()
	newServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		newRequests.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer newServer.Close()
	svc, db, _ := newService(t, oldServer.Client())
	key := "old-key"
	if _, err := svc.Save(ctx(), SaveInput{Provider: ProviderOpenAI, BaseURL: oldServer.URL, APIKey: &key}); err != nil {
		t.Fatal(err)
	}
	s := svc.(*service)
	newSealed, err := s.vault.SealSecret([]byte("new-key"))
	if err != nil {
		t.Fatal(err)
	}
	s.vault = &rotatingProbeVault{Vault: s.vault, rotate: func() {
		if _, err := db.Exec("UPDATE ai_config SET base_url=?, api_key_ciphertext=? WHERE id=1", newServer.URL, newSealed); err != nil {
			t.Error(err)
		}
	}}
	result, err := svc.Test(ctx(), TestInput{})
	if err != nil || !result.OK || oldRequests.Load() != 1 || newRequests.Load() != 0 {
		t.Fatal(result, err, oldRequests.Load(), newRequests.Load())
	}
}

func TestOpsStreamTimeoutDoesNotInheritProbeTimeout(t *testing.T) {
	client := &http.Client{Timeout: 5 * time.Millisecond, Transport: opsTransport(func(r *http.Request) (*http.Response, error) {
		select {
		case <-time.After(50 * time.Millisecond):
		case <-r.Context().Done():
			return nil, r.Context().Err()
		}
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(
			"data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"finished\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n"))}, nil
	})}
	svc, _, _ := newService(t, client)
	key := "key"
	if _, err := svc.Save(ctx(), SaveInput{Provider: ProviderOpenAI, BaseURL: "http://127.0.0.1:1", Model: "m", APIKey: &key, Enabled: true}); err != nil {
		t.Fatal(err)
	}
	m := NewOpsModel(svc, mask.NewMasker())
	text, err := m.(opschat.StreamModel).AnalyzeStream(context.Background(), opschat.AnalysisRequest{
		Provider: m.Info(), Question: "status", Items: []opschat.AnalysisItem{{Status: opschat.Succeeded, Output: "data"}}, MaxResponseBytes: 16 << 10,
	}, func(opschat.StreamChunk) error { return nil })
	if err != nil || text != "finished" {
		t.Fatal(text, err)
	}
	probe, err := svc.Test(ctx(), TestInput{})
	if err != nil || probe.OK || client.Timeout != 5*time.Millisecond {
		t.Fatal("probe deadline was changed", probe, err, client.Timeout)
	}
}
