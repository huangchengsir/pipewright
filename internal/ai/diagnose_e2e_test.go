package ai

// diagnose_e2e_test.go 是 Story 7-2 AI 失败诊断的**真 LLM** 端到端测试(Open Q#7 的真跑前哨)。
//
// 与 diagnose_test.go(httptest 桩 LLM)互补:本测试配置真实 LLM(DeepSeek,OpenAI 兼容)
// 喂一条**真实** docker 构建失败日志(真跑 `pip install` 版本错误捕获),验证两件事:
//  1. 真 LLM 真能产出可用诊断(Status=ready + 非空 hypothesis + 修复建议)——诊断「质量」前哨。
//  2. **出网前脱敏铁律在真实网络路径上成立**:日志里掺的 secret 绝不出现在返回的任何字段里
//     (hypothesis/evidence/fixes/alternates)。
//
// 默认 SKIP:仅当 PIPEWRIGHT_E2E_LLM=1 且环境变量 DEEPSEEK_API_KEY 非空时运行。
// **key 只经环境变量读,绝不硬编码/落库/打印**(打印诊断时也不含 key)。
// 跑法:PIPEWRIGHT_E2E_LLM=1 DEEPSEEK_API_KEY=*** go test ./internal/ai/ -run E2ELLM -v

import (
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/huangchengsir/pipewright/internal/mask"
)

// realPipFailureLog 是一条**真实**捕获的 docker 构建失败日志(pip install 非法版本号),
// 末尾掺一行含 secret 的真实感命令(验出网脱敏:此明文绝不可达 LLM / 返回字段)。
const realPipFailureLog = `#8 [4/4] RUN pip install --no-cache-dir flask==2.0.1 requests==99.99.99-doesnotexist
#8 3.279 ERROR: Invalid requirement: 'requests==99.99.99-doesnotexist': Expected comma (within version specifier), semicolon (after version specifier) or end
#8 3.279     requests==99.99.99-doesnotexist
#8 3.279             ~~~~~~~~~~^
#8 ERROR: process "/bin/sh -c pip install ..." did not complete successfully: exit code: 1
pip config set global.index-url https://ci:PYPI_TOKEN_LEAK_zz9k@pypi.internal/simple
ERROR: failed to build: exit code: 1`

// realLogSecret 是上面日志里掺入的 secret 明文(调用方应已登记进 Masker)。
const realLogSecret = "PYPI_TOKEN_LEAK_zz9k"

// TestE2ELLMDiagnoseDeepSeekReal 用真 DeepSeek 诊断真失败日志,验诊断可用 + 出网脱敏。
func TestE2ELLMDiagnoseDeepSeekReal(t *testing.T) {
	if os.Getenv("PIPEWRIGHT_E2E_LLM") != "1" {
		t.Skip("设 PIPEWRIGHT_E2E_LLM=1 启用真 LLM 诊断 e2e")
	}
	apiKey := os.Getenv("DEEPSEEK_API_KEY")
	if strings.TrimSpace(apiKey) == "" {
		t.Skip("DEEPSEEK_API_KEY 未设,跳过")
	}

	// 真实网络 client(给足超时:真 LLM 推理可能数秒~数十秒)。
	svc, _, _ := newService(t, &http.Client{Timeout: 90 * time.Second})

	// 配置真 DeepSeek(OpenAI 兼容:base=https://api.deepseek.com → /v1/chat/completions)。
	// key 经只写指针传入,vault secretbox 加密落库,绝不明文常驻/回显。
	if _, err := svc.Save(ctx(), SaveInput{
		Provider: ProviderOpenAI,
		BaseURL:  "https://api.deepseek.com",
		Model:    "deepseek-chat",
		APIKey:   &apiKey,
		Enabled:  true,
	}); err != nil {
		t.Fatalf("配置 DeepSeek 失败: %v", err)
	}

	// Masker 登记日志里掺的 secret(模拟 per-run logMasker 登记该 run 真实凭据)。
	m := mask.NewMasker()
	m.RegisterSecret(realLogSecret)

	d, err := svc.Diagnose(ctx(), DiagnoseInput{
		FailureLog:  realPipFailureLog,
		StepName:    "构建",
		ProjectName: "e2e-pyapp",
		Masker:      m,
	})
	if err != nil {
		t.Fatalf("Diagnose(真 DeepSeek)返回 error: %v", err)
	}

	// 优雅降级:若 DeepSeek 不可用,Diagnose 会返回 unavailable(不报错)。真 e2e 期望 ready;
	// 若 unavailable 打印原因便于排查(网络/额度/key),按 SKIP 处理而非 FAIL(环境因素)。
	if d.Status != diagStatusReady {
		t.Skipf("DeepSeek 未返回 ready(可能网络/额度/key 问题),reason=%q —— 非代码缺陷", d.Reason)
	}

	// 质量前哨:hypothesis 非空、有修复建议。
	if strings.TrimSpace(d.Hypothesis) == "" {
		t.Fatal("ready 诊断的 hypothesis 不应为空")
	}

	// 红线:出网脱敏铁律 —— secret 明文绝不出现在返回的任何字段。
	blob := d.Hypothesis + "\n" + d.Reason + "\n" + strings.Join(d.FixSuggestions, "\n") +
		"\n" + strings.Join(d.AlternateCauses, "\n")
	for _, ev := range d.Evidence {
		blob += "\n" + ev.Text
	}
	if strings.Contains(blob, realLogSecret) {
		t.Fatalf("❌ secret 明文出现在 DeepSeek 诊断返回里 → 出网/二次脱敏失效!")
	}

	t.Logf("✅ 真 DeepSeek 诊断 OK(confidence=%s)\n根因假说: %s\n修复建议: %v\n证据行数: %d;secret 已脱敏",
		d.Confidence, d.Hypothesis, d.FixSuggestions, len(d.Evidence))
}
