// diagnose.go 是「AI 失败分析」(Story 7.2 / FR-22)。
//
// Service.Diagnose 据一次失败运行的失败日志,先用注入的 mask.Masker **脱敏**(AC-SEC-04
// 出网前脱敏铁律:发往 LLM 的任何字符 / 取出的证据均不含明文 secret),再构 prompt(脱敏
// 日志 + 少量通用错误模式提示 + 期望 JSON schema),调 LLM chat(复用 7-1/2-5 per-provider
// chat),容错剥 fence 解析为结构化诊断,并施加**质量门控**:空 hypothesis / 解析失败 /
// 超时 / 未配 → status=unavailable + 人读 reason(绝无密钥)。
//
// 优雅降级铁律(NFR-10):Diagnose 对所有「AI 不可用 / 不可解析 / 低质」路径返回一个
// status=unavailable 的 *Diagnosis(nil error),供上层 HTTP 200 + 不阻断 run 终态;
// 它**不**把 LLM/网络错误透传为 Go error(那会被 handler 误映射 500)。
//
// evidence 取自**脱敏后**日志:即便模型回了原始行,也以脱敏日志的对应行为准,二次保证无明文。
package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/huangchengsir/pipewright/internal/mask"
)

// diagnoseMaxTokens 是诊断 chat 的 max_tokens(够装一份结构化诊断)。
const diagnoseMaxTokens = 1024

// maxFailureLogChars 是发往 LLM 的失败日志字符上限(防超大日志吃 token / 内存);
// 超出取**末尾**(失败原因通常在日志末)。脱敏后再截断,确保截断不漏过明文。
const maxFailureLogChars = 8000

// 诊断 status / confidence 枚举(对齐冻结 diagnosis 子 DTO)。
const (
	diagStatusReady       = "ready"
	diagStatusUnavailable = "unavailable"

	confHigh   = "high"
	confMedium = "medium"
	confLow    = "low"
)

// DiagnoseInput 是失败诊断入参。Masker 必带(出网前脱敏);为 nil 时本方法兜底构空 Masker
// (但调用方理应已登记 run 相关 secret;空 Masker 仅保证不 panic,不替代登记)。
type DiagnoseInput struct {
	FailureLog  string       // 失败日志原文(脱敏前)
	StepName    string       // 失败步骤名(prompt 上下文;可空)
	ProjectName string       // 项目名(prompt 上下文;可空)
	Masker      *mask.Masker // 出网前脱敏器(已登记 run 相关 secret)
}

// DiagnosisEvidence 是一条日志证据(取自**脱敏后**日志;绝无明文 secret)。
type DiagnosisEvidence struct {
	Line      int    `json:"line"`      // 失败日志行号(1-based)
	Text      string `json:"text"`      // 该行脱敏后文本
	Highlight bool   `json:"highlight"` // 是否命中行(高亮)
}

// Diagnosis 是 AI 失败诊断的领域模型(对齐冻结 diagnosis 子 DTO)。
type Diagnosis struct {
	Status          string   `json:"status"` // ready | unavailable | pending
	Reason          string   `json:"reason"` // status≠ready 时人读原因(绝无密钥)
	Hypothesis      string   `json:"hypothesis"`
	Confidence      string   `json:"confidence"` // high | medium | low
	AlternateCauses []string `json:"alternateCauses"`
	FixSuggestions  []string `json:"fixSuggestions"`
	// FixScript 是可直接复制粘贴的「修复脚本/补丁片段」(护城河 · AI moat),针对失败步骤给出
	// 具体可执行的改法(如 shell 命令、配置 diff)。脱敏后落库;空串 = 模型未给出。
	FixScript   string              `json:"fixScript"`
	Evidence    []DiagnosisEvidence `json:"evidence"`
	GeneratedAt time.Time           `json:"generatedAt"`
}

// unavailable 构造一个 status=unavailable 的诊断(带人读 reason;绝无密钥)。
func unavailable(reason string) *Diagnosis {
	return &Diagnosis{
		Status:          diagStatusUnavailable,
		Reason:          reason,
		AlternateCauses: []string{},
		FixSuggestions:  []string{},
		Evidence:        []DiagnosisEvidence{},
		GeneratedAt:     time.Now().UTC(),
	}
}

// llmDiagnosis 是 LLM 期望回的 JSON 形状(剥 fence 后 Unmarshal)。
type llmDiagnosis struct {
	Hypothesis      string   `json:"hypothesis"`
	Confidence      string   `json:"confidence"`
	AlternateCauses []string `json:"alternateCauses"`
	FixSuggestions  []string `json:"fixSuggestions"`
	// FixScript 是模型给的可执行修复脚本/补丁片段(护城河);脱敏后采纳,绝不回显原始 secret。
	FixScript string `json:"fixScript"`
	// EvidenceLines 是模型认为命中的行号(1-based);证据正文以脱敏日志为准,不取模型回的文本。
	EvidenceLines []int `json:"evidenceLines"`
}

func (s *service) Diagnose(ctx context.Context, in DiagnoseInput) (*Diagnosis, error) {
	masker := in.Masker
	if masker == nil {
		masker = mask.NewMasker() // 兜底:不 panic;但调用方理应已登记 secret
	}

	// 无失败日志:无可诊断,降级(不算错误)。
	if strings.TrimSpace(in.FailureLog) == "" {
		return unavailable("无失败日志,暂无可诊断内容"), nil
	}

	// 读配置 + 既有密文;未配置 / 未启用 → 降级 unavailable(绝不返 error)。
	cfg, sealed, err := s.load(ctx)
	if err != nil {
		// 读配置本身失败(如 DB 故障):仍降级,不阻断 run 终态 / 不 500。
		return unavailable("AI 配置读取失败,诊断暂不可用"), nil
	}
	if cfg == nil || !cfg.Configured || !cfg.Enabled {
		return unavailable("AI 未配置或未启用,无法生成诊断"), nil
	}

	provider := cfg.Provider
	baseURL := cfg.BaseURL
	if baseURL == "" {
		baseURL = defaultBaseURL(provider)
	}
	if baseURL == "" {
		return unavailable("AI 未配置或未启用,无法生成诊断"), nil
	}

	// apiKey:非 ollama 解密既有密钥进程内取用即弃;失败 → 降级(绝不泄漏)。
	apiKey := ""
	if provider != ProviderOllama {
		if len(sealed) == 0 {
			return unavailable("AI 未配置或未启用,无法生成诊断"), nil
		}
		plain, oerr := s.openSecret(sealed)
		if oerr != nil {
			return unavailable("AI 密钥不可用(保险库未配置),诊断暂不可用"), nil
		}
		apiKey = plain
	}

	// ---- 出网前脱敏铁律:日志在进 prompt / 取证据前一律 Scrub ----
	maskedLines := scrubLines(masker, in.FailureLog)
	maskedStep := masker.Scrub(in.StepName)
	maskedProject := masker.Scrub(in.ProjectName)

	prompt := buildDiagnosePrompt(maskedLines, maskedStep, maskedProject)

	text, cerr := s.chat(ctx, provider, baseURL, cfg.Model, apiKey, prompt)
	apiKey = "" // 明文用完即弃
	_ = apiKey
	if cerr != nil {
		// LLM 调用失败(超时 / 网络 / 非 2xx):降级 unavailable + 人读 reason(cerr 已确保不含密钥)。
		return unavailable("AI 诊断失败:" + humanizeDiagnoseErr(cerr)), nil
	}

	// 解析 + 质量门控。
	parsed, perr := parseDiagnosis(text)
	if perr != nil {
		return unavailable("AI 返回内容无法解析为有效诊断"), nil
	}
	if strings.TrimSpace(parsed.Hypothesis) == "" {
		return unavailable("AI 未给出有效根因假说"), nil
	}

	// 证据:以**脱敏后**日志为准取模型命中的行(模型回的文本不采信,二次保证无明文)。
	evidence := buildEvidence(maskedLines, parsed.EvidenceLines)

	// 二次脱敏铁律:模型回的任意字段都可能回显日志中的 secret,出库前一律 Scrub。
	d := &Diagnosis{
		Status:          diagStatusReady,
		Hypothesis:      masker.Scrub(parsed.Hypothesis),
		Confidence:      normalizeConfidence(parsed.Confidence),
		AlternateCauses: sanitizeMaskList(masker, parsed.AlternateCauses),
		FixSuggestions:  sanitizeMaskList(masker, parsed.FixSuggestions),
		FixScript:       masker.Scrub(strings.TrimSpace(parsed.FixScript)),
		Evidence:        evidence,
		GeneratedAt:     time.Now().UTC(),
	}
	return d, nil
}

// scrubLines 把日志按行脱敏并截断(取末尾 maxFailureLogChars 字符,失败原因通常在末)。
func scrubLines(m *mask.Masker, raw string) []string {
	scrubbed := m.Scrub(raw)
	if len(scrubbed) > maxFailureLogChars {
		scrubbed = scrubbed[len(scrubbed)-maxFailureLogChars:]
	}
	// 统一换行后切行;去掉末尾纯空行。
	scrubbed = strings.ReplaceAll(scrubbed, "\r\n", "\n")
	lines := strings.Split(scrubbed, "\n")
	for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

// buildEvidence 据模型给的命中行号,从**脱敏后**日志行取证据(行号越界忽略);
// 无有效命中时回退取末尾若干行(仍脱敏),保证证据分栏非空。highlight 标命中行。
func buildEvidence(maskedLines []string, hitLines []int) []DiagnosisEvidence {
	hit := make(map[int]struct{}, len(hitLines))
	out := []DiagnosisEvidence{}
	for _, ln := range hitLines {
		idx := ln - 1 // 1-based → 0-based
		if idx < 0 || idx >= len(maskedLines) {
			continue
		}
		if _, dup := hit[ln]; dup {
			continue
		}
		hit[ln] = struct{}{}
		out = append(out, DiagnosisEvidence{Line: ln, Text: maskedLines[idx], Highlight: true})
	}
	if len(out) > 0 {
		return out
	}
	// 回退:取末尾最多 6 行作为非高亮证据(模型未指明命中行时仍给原始日志佐证)。
	start := 0
	if len(maskedLines) > 6 {
		start = len(maskedLines) - 6
	}
	for i := start; i < len(maskedLines); i++ {
		out = append(out, DiagnosisEvidence{Line: i + 1, Text: maskedLines[i], Highlight: false})
	}
	return out
}

// sanitizeMaskList 对模型回的字符串列表逐项再脱敏(二次保险:模型可能回显日志中的 secret)。
// nil → 空切片(契约:列表字段非 null)。
func sanitizeMaskList(m *mask.Masker, list []string) []string {
	out := make([]string, 0, len(list))
	for _, s := range list {
		out = append(out, m.Scrub(s))
	}
	return out
}

// normalizeConfidence 归一置信度(非法值兜底 low,促使前端明示「其它可能」)。
func normalizeConfidence(c string) string {
	switch strings.ToLower(strings.TrimSpace(c)) {
	case confHigh:
		return confHigh
	case confMedium:
		return confMedium
	default:
		return confLow
	}
}

// parseDiagnosis 容错剥 fence 解析 LLM 文本为 llmDiagnosis(复用 stripJSONFence)。
func parseDiagnosis(text string) (*llmDiagnosis, error) {
	jsonText := stripJSONFence(text)
	if strings.TrimSpace(jsonText) == "" {
		return nil, fmt.Errorf("%w: 模型未返回可解析内容", ErrGenerateFailed)
	}
	var d llmDiagnosis
	if err := json.Unmarshal([]byte(jsonText), &d); err != nil {
		return nil, fmt.Errorf("%w: 模型返回的不是有效 JSON 诊断", ErrGenerateFailed)
	}
	return &d, nil
}

// humanizeDiagnoseErr 把 chat 错误转人读消息(绝无明文密钥;ErrGenerateFailed 已人读)。
func humanizeDiagnoseErr(err error) string {
	msg := err.Error()
	if i := strings.Index(msg, ": "); i >= 0 {
		return strings.TrimSpace(msg[i+2:])
	}
	return "调用模型失败,请稍后重试"
}

// buildDiagnosePrompt 构造发给 LLM 的诊断 prompt:脱敏日志(带行号)+ 通用错误模式提示 +
// 期望 JSON schema + 措辞约束(「假说,非结论」+ 低置信明示其它可能)。
// 绝不含任何 apiKey / token / 明文 secret(日志已脱敏)。
func buildDiagnosePrompt(maskedLines []string, step, project string) string {
	var b strings.Builder
	b.WriteString("你是 CI/CD 失败分析助手。请根据下面一次流水线运行的**失败日志**,给出最可能的根因「假说」、修复建议与置信度。\n")
	b.WriteString("重要:你的结论是**假说,不是定论**;措辞须用「最可能的根因是 X(假说,非结论)」这类表述。\n")
	b.WriteString("当置信度不高(medium/low)时,必须在 alternateCauses 列出其它可能根因。\n\n")

	if strings.TrimSpace(project) != "" {
		b.WriteString("## 项目\n" + project + "\n\n")
	}
	if strings.TrimSpace(step) != "" {
		b.WriteString("## 失败步骤\n" + step + "\n\n")
	}

	b.WriteString("## 失败日志(已脱敏;[MASKED] 为被屏蔽的敏感信息,行首为行号)\n")
	for i, ln := range maskedLines {
		fmt.Fprintf(&b, "%d: %s\n", i+1, ln)
	}

	b.WriteString(`
## 常见失败模式参考(仅供参考,以实际日志为准)
- 依赖缺失 / 版本不存在(如 npm 404、go module not found、pip ResolutionImpossible)
- lockfile 未提交或与 manifest 不一致
- 认证 / 权限失败(401/403、registry token 无效)
- 编译 / 测试失败(语法错误、断言失败)
- 网络 / 超时(registry 不可达、DNS 失败)
- 资源不足(OOM、磁盘满)

## 输出要求
只输出一个 JSON 对象(不要任何解释文字、不要 markdown 代码块),严格符合以下结构:
{
  "hypothesis": "最可能的根因是 ...(假说,非结论)",
  "confidence": "high|medium|low",
  "alternateCauses": ["低置信时列出的其它可能根因"],
  "fixSuggestions": ["可执行的修复建议(人读要点,简短)"],
  "fixScript": "可直接复制粘贴的修复脚本/补丁片段(shell 命令或配置 diff),针对失败步骤给出具体改法;若无把握则留空字符串",
  "evidenceLines": [12, 13]
}
约束:
- confidence 仅能为 high/medium/low;不确定时用 low 或 medium 并填 alternateCauses。
- evidenceLines 为上面日志中支撑你假说的**行号**(整数数组),取最相关的少数几行。
- fixScript 是单个字符串(可含换行 \n),应是能直接执行/套用的命令或补丁;无具体可执行改法时给空字符串 ""。
- 不要在任何字段(含 fixScript)回显敏感信息或凭据。
`)
	return b.String()
}
