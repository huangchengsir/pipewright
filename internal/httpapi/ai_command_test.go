package httpapi

import (
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/huangchengsir/pipewright/internal/ai"
)

// TestAICommandHappyPath 验证 /ai/command 回 200 + available + 命令/风险透传。
func TestAICommandHappyPath(t *testing.T) {
	stub := &diagStubAI{cmd: &ai.CommandSuggestion{
		Command:     "du -ah . | sort -rh | head -10",
		Explanation: "按大小列出当前目录占用最多的 10 项",
		Risk:        ai.CmdRiskSafe,
		Reason:      "只读查询",
		GeneratedAt: time.Now().UTC(),
	}}
	srv, client, csrf, _ := setupAIGenServer(t, stub, stubAnalyzer{res: ai.RepoAnalysis{Cloned: true}})

	resp := doJSON(t, client, http.MethodPost, srv.URL+"/api/ai/command", csrf,
		`{"nl":"磁盘占用 top 10","context":{"os":"alpine","shell":"/bin/sh","container":"app"}}`)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 200, body=%s", resp.StatusCode, raw)
	}
	dto := decode(t, resp)
	if dto["available"] != true {
		t.Fatalf("available 应为 true: %v", dto)
	}
	if dto["risk"] != "safe" {
		t.Fatalf("risk 透传错误: %v", dto)
	}
	if dto["command"] == "" {
		t.Fatalf("command 应非空: %v", dto)
	}
	if stub.gotNL != "磁盘占用 top 10" {
		t.Fatalf("NL 未透传到领域层: %q", stub.gotNL)
	}
}

// TestAICommandDegradesNoAI 验证 AI 未配(ErrAINotConfigured)→ 200 + available=false + reason。
func TestAICommandDegradesNoAI(t *testing.T) {
	stub := &diagStubAI{} // cmd=nil → ErrAINotConfigured
	srv, client, csrf, _ := setupAIGenServer(t, stub, stubAnalyzer{res: ai.RepoAnalysis{Cloned: true}})

	resp := doJSON(t, client, http.MethodPost, srv.URL+"/api/ai/command", csrf, `{"nl":"看磁盘"}`)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("AI 未配应 200(不阻断), got %d", resp.StatusCode)
	}
	dto := decode(t, resp)
	if dto["available"] != false {
		t.Fatalf("available 应为 false: %v", dto)
	}
	if dto["reason"] == "" {
		t.Fatalf("应给降级 reason: %v", dto)
	}
}

// TestAICommandEmptyNL 验证空 nl → 400。
func TestAICommandEmptyNL(t *testing.T) {
	stub := &diagStubAI{}
	srv, client, csrf, _ := setupAIGenServer(t, stub, stubAnalyzer{res: ai.RepoAnalysis{Cloned: true}})

	resp := doJSON(t, client, http.MethodPost, srv.URL+"/api/ai/command", csrf, `{"nl":"  "}`)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("空 nl 应 400, got %d", resp.StatusCode)
	}
}

// TestAIExplainHappyPath 验证 /ai/explain 回 200 + 解释 + 风险。
func TestAIExplainHappyPath(t *testing.T) {
	stub := &diagStubAI{explain: &ai.CommandExplanation{
		Explanation: "ps 列出所有进程,按内存降序取前 6 行。",
		Risk:        ai.CmdRiskSafe,
		GeneratedAt: time.Now().UTC(),
	}}
	srv, client, csrf, _ := setupAIGenServer(t, stub, stubAnalyzer{res: ai.RepoAnalysis{Cloned: true}})

	resp := doJSON(t, client, http.MethodPost, srv.URL+"/api/ai/explain", csrf,
		`{"command":"ps aux --sort=-%mem | head -6"}`)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	dto := decode(t, resp)
	if dto["available"] != true || dto["explanation"] == "" {
		t.Fatalf("解释响应错误: %v", dto)
	}
}

// TestAICompleteHappyPath 验证 /ai/complete 回 200 + available + 补全。
func TestAICompleteHappyPath(t *testing.T) {
	stub := &diagStubAI{completion: &ai.CompletionResult{Completion: "ls -la"}}
	srv, client, csrf, _ := setupAIGenServer(t, stub, stubAnalyzer{res: ai.RepoAnalysis{Cloned: true}})

	resp := doJSON(t, client, http.MethodPost, srv.URL+"/api/ai/complete", csrf, `{"partial":"ls","context":{"shell":"/bin/sh"}}`)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	dto := decode(t, resp)
	if dto["available"] != true || dto["completion"] != "ls -la" {
		t.Fatalf("补全响应错误: %v", dto)
	}
}

// TestAICompleteDegradesNoAI 验证未配 AI → 200 + available=false(前端走本地字典)。
func TestAICompleteDegradesNoAI(t *testing.T) {
	stub := &diagStubAI{} // completion=nil → ErrAINotConfigured
	srv, client, csrf, _ := setupAIGenServer(t, stub, stubAnalyzer{res: ai.RepoAnalysis{Cloned: true}})

	resp := doJSON(t, client, http.MethodPost, srv.URL+"/api/ai/complete", csrf, `{"partial":"ls"}`)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("AI 未配应 200, got %d", resp.StatusCode)
	}
	dto := decode(t, resp)
	if dto["available"] != false {
		t.Fatalf("available 应为 false: %v", dto)
	}
}

// TestAICompleteEmptyPartial 验证空 partial → 400。
func TestAICompleteEmptyPartial(t *testing.T) {
	stub := &diagStubAI{}
	srv, client, csrf, _ := setupAIGenServer(t, stub, stubAnalyzer{res: ai.RepoAnalysis{Cloned: true}})

	resp := doJSON(t, client, http.MethodPost, srv.URL+"/api/ai/complete", csrf, `{"partial":" "}`)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("空 partial 应 400, got %d", resp.StatusCode)
	}
}

// TestAIExplainEmptyCommand 验证空 command → 400。
func TestAIExplainEmptyCommand(t *testing.T) {
	stub := &diagStubAI{}
	srv, client, csrf, _ := setupAIGenServer(t, stub, stubAnalyzer{res: ai.RepoAnalysis{Cloned: true}})

	resp := doJSON(t, client, http.MethodPost, srv.URL+"/api/ai/explain", csrf, `{"command":""}`)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("空 command 应 400, got %d", resp.StatusCode)
	}
}
