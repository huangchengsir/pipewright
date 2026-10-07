package opschat

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/huangchengsir/pipewright/internal/mask"
	"github.com/huangchengsir/pipewright/internal/target"
	"github.com/huangchengsir/pipewright/internal/vault"
)

func TestBoundedStdoutStderrSecretsBeforeByteLineAndUTF8Clipping(t *testing.T) {
	for _, cutoff := range []string{"full", "stdout_bytes", "stderr_bytes", "stdout_lines", "stderr_lines", "utf8_bytes", "process_lines"} {
		t.Run(cutoff, func(t *testing.T) {
			id := uuid.NewString()
			providerKey := "provider-secret-key"
			vaultKey := "vault-\u4e2d\u6587-password"
			if cutoff == "process_lines" {
				vaultKey = "vault-first\nvault-second"
			}
			m := &registeringModel{fakeModel: model(), key: providerKey}
			f := executor(id)
			s, v, db := fixture(t, f, m)
			m.masker, m.db = s.masker, db
			if _, err := v.Create(vault.CreateInput{Name: "review credential", Type: vault.TypeSSHPassword, Secret: vaultKey}); err != nil {
				t.Fatal(err)
			}
			c := chat(t, s, id)
			prefix := providerKey[:len(providerKey)-1]
			vaultPrefix := vaultKey[:len(vaultKey)-1]
			tool := "host_ports"
			if cutoff == "process_lines" {
				tool = "host_processes"
			}
			f.run = func(_ context.Context, _ target.ConnectionSnapshot, _ []string, l target.ExecutionLimits) (*target.LimitedResult, error) {
				res := &target.LimitedResult{Truncated: cutoff != "full" && cutoff != "process_lines"}
				switch cutoff {
				case "full":
					res.Stdout, res.Stderr = "stdout="+providerKey, "stderr="+vaultKey
				case "stdout_bytes":
					res.Stdout = strings.Repeat("x", l.Bytes-len(prefix)) + prefix
				case "stderr_bytes":
					res.Stdout = "ok\n"
					res.Stderr = strings.Repeat("x", l.Bytes-len(res.Stdout)-len(vaultPrefix)) + vaultPrefix
				case "stdout_lines":
					res.Stdout = strings.Repeat("row\n", l.Lines-1) + prefix
				case "stderr_lines":
					res.Stdout = "ok\n"
					res.Stderr = strings.Repeat("row\n", l.Lines-2) + vaultPrefix
				case "utf8_bytes":
					// Mimic the bounded SSH writer stopping two bytes into a rune and
					// dropping invalid UTF-8 when constructing its LimitedResult.
					raw := []byte(strings.Repeat("x", l.Bytes-len("vault-")-2) + vaultKey)
					res.Stdout = strings.ToValidUTF8(string(raw[:l.Bytes]), "")
				case "process_lines":
					res.Stdout = strings.Repeat("row\n", 30) + vaultKey + "\n" + strings.Repeat("row\n", 10)
				}
				return res, nil
			}
			r := submitTool(t, s, c, tool, "{}")
			snap := waitRun(t, s, c.ID, r.ID, Succeeded)
			waitJobs(t, s)
			call := snap.Calls[0]
			for _, secret := range []string{providerKey, vaultKey, prefix, vaultPrefix} {
				if strings.Contains(call.Output, secret) {
					t.Fatal("registered credential leaked", cutoff)
				}
			}
			if strings.Contains(call.Output, "vault-first") || strings.Contains(call.Output, "vault-") || !strings.Contains(call.Output, mask.Placeholder) || !utf8.ValidString(call.Output) || len(call.Output) > 64<<10 {
				t.Fatal("unsafe clipping or missing mask", cutoff, len(call.Output))
			}
			if cutoff != "full" && !call.Truncated {
				t.Fatal("truncation not persisted")
			}
			stored, err := s.Call(context.Background(), c.ID, call.ID)
			if err != nil || stored.Output != call.Output {
				t.Fatal("persisted result differs", err)
			}
			// Small batches can be explicitly shared. Their previews and model inputs
			// must contain only the already-sanitized persisted result.
			if len(call.Output) < 16<<10 {
				preview, err := s.AnalysisPreview(context.Background(), c.ID, []string{call.ID})
				if err != nil {
					t.Fatal(err)
				}
				ar, err := s.Analyze(context.Background(), c.ID, AnalysisInput{ClientRequestID: uuid.NewString(), Revision: c.Revision, CallIDs: preview.CallIDs, PreviewHash: preview.Hash, Provider: preview.Provider, Consent: true})
				if err != nil {
					t.Fatal(err)
				}
				waitRun(t, s, c.ID, ar.ID, Succeeded)
				waitJobs(t, s)
				m.mu.Lock()
				data, _ := json.Marshal(m.analyses)
				m.mu.Unlock()
				if strings.Contains(string(data), providerKey) || strings.Contains(string(data), "vault-") || strings.Contains(string(data), prefix) {
					t.Fatal("model input leaked credential")
				}
			}
		})
	}
}
