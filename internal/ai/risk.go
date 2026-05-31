// risk.go 是「AI 脚本风险标注」(护城河 · AI moat)。
//
// Service.AnnotateRisks 对一条流水线的脚本步骤(script step)命令做风险标注:
//   - **确定性预扫(deterministic pre-pass)**:正则匹配显式高危模式(rm -rf /、
//     curl|sh、chmod 777、未固定 latest 镜像、命令里的明文密钥/token 等),**无需 AI 即可
//     给出价值**——这是优雅降级的下限(AI 未配/失败时仍有确定性结论)。
//   - **LLM 增强扫(可选)**:AI 已配且启用时,再让模型补充语义级风险(如「部署前缺健康
//     检查」「危险 sudo」等正则难枚举的项);AI 不可用 / 失败 / 不可解析时**静默跳过**,
//     仅返回确定性结论,绝不 500 / panic。
//
// 安全铁律(AC-SEC):
//   - 入参命令在出网进 prompt 前一律过 mask.Masker 脱敏;模型回的任意字段二次脱敏。
//   - **检测到明文密钥时,标注本身绝不回显该密钥**——只标「第 N 行疑似明文密钥」,把命中片段
//     替换为掩码占位,杜绝「为了报告泄漏而二次泄漏」。
//   - apiKey 仅进程内解密取用、用完即弃,绝不进 prompt / 日志 / 错误 / 结果。
//
// 优雅降级:AnnotateRisks 永远返回 (*RiskReport, nil) —— 它**不**把 AI 不可用作为错误;
// AIEnhanced 字段标识本次是否实际跑了 LLM 增强(供前端提示「配置 AI 可获更深分析」)。
package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/huangchengsir/pipewright/internal/mask"
)

// 风险等级枚举(对齐前端徽标配色:high=红、medium=琥珀、low=灰)。
const (
	// RiskHigh 表示高危(几乎肯定出问题 / 安全事故,如 rm -rf /、明文密钥)。
	RiskHigh = "high"
	// RiskMedium 表示中危(强烈建议修,如 curl|sh、chmod 777、latest 镜像)。
	RiskMedium = "medium"
	// RiskLow 表示低危 / 提示(如部署前缺健康检查、未加 set -e)。
	RiskLow = "low"
)

// riskAnnotateMaxTokens 是风险标注 chat 的 max_tokens(够装一组结构化 finding)。
const riskAnnotateMaxTokens = 1024

// maxScriptChars 是发往 LLM 的脚本字符上限(防超大脚本吃 token / 内存)。
const maxScriptChars = 8000

// ScriptStep 是风险标注的中性入参:一条脚本步骤(与 pipeline.PipelineStep 解耦,
// ai 包不 import pipeline)。Commands 为多行命令(顺序执行)。
type ScriptStep struct {
	Name     string   // 步骤名(prompt 上下文 / 标注定位;可空)
	Image    string   // 构建镜像(如 node:20;用于 latest / 未固定标签检测;可空)
	Commands []string // 多行命令(逐行扫描)
}

// RiskFinding 是一条风险标注。绝不含明文 secret(检测到明文密钥时命中片段已掩码)。
type RiskFinding struct {
	Level      string `json:"level"`      // high | medium | low
	StepName   string `json:"stepName"`   // 命中步骤名(可空)
	Line       int    `json:"line"`       // 命中命令在该步骤内的行号(1-based;0=步骤级/镜像级)
	Title      string `json:"title"`      // 简短风险标题(人读)
	Why        string `json:"why"`        // 为什么有风险(人读)
	Suggestion string `json:"suggestion"` // 修复建议(人读)
	Source     string `json:"source"`     // rule(确定性规则)| ai(LLM 增强)
}

// RiskReport 是风险标注结果(契约形状)。Findings 永不为 null(空切片表无风险)。
type RiskReport struct {
	Findings    []RiskFinding `json:"findings"`
	AIEnhanced  bool          `json:"aiEnhanced"` // 是否实际跑了 LLM 增强(false=仅确定性规则)
	AIReason    string        `json:"aiReason"`   // AIEnhanced=false 时的人读原因(未配 / 失败;绝无密钥)
	GeneratedAt time.Time     `json:"generatedAt"`
}

// AnnotateRisksInput 是风险标注入参。Masker 出网前脱敏(进 prompt 的脚本一律 Scrub);
// 为 nil 时本方法兜底构空 Masker(不 panic)。
type AnnotateRisksInput struct {
	Steps  []ScriptStep
	Masker *mask.Masker
}

// ---- 确定性规则:命令行级正则 ----
//
// 每条规则匹配一行命令,命中即产出一条 finding(命中片段不回显明文 secret)。

// dangerousRule 是单行命令的确定性规则。
type dangerousRule struct {
	re         *regexp.Regexp
	level      string
	title      string
	why        string
	suggestion string
}

// 命令行级危险模式(顺序即报告顺序;一行可命中多条)。
var commandRules = []dangerousRule{
	{
		re:         regexp.MustCompile(`\bchmod\s+(-[a-zA-Z]*\s+)*-R\s+777\b|\bchmod\s+(-[a-zA-Z]*\s+)*777\s+-R\b`),
		level:      RiskHigh,
		title:      "chmod -R 777 递归全权限",
		why:        "递归授予整棵目录任意用户全权限,放大被提权/篡改面。",
		suggestion: "按需收紧到具体文件;避免递归 777。",
	},
	{
		re:         regexp.MustCompile(`\brm\s+-[a-zA-Z]*r[a-zA-Z]*f[a-zA-Z]*\s+(/|/\s|\$\{?\w*\}?/?\s|\.\s*$|\*)`),
		level:      RiskHigh,
		title:      "递归强制删除根/通配路径",
		why:        "rm -rf 指向根目录、未受控变量或通配,极易误删宿主/工作区数据,造成不可逆损失。",
		suggestion: "删除前固定到具体子目录;对变量加 `: \"${VAR:?}\"` 兜底,避免变量为空时删到根。",
	},
	{
		re:         regexp.MustCompile(`\bcurl\b[^|]*\|\s*(sudo\s+)?(sh|bash|zsh)\b`),
		level:      RiskMedium,
		title:      "管道执行远程脚本(curl | sh)",
		why:        "把网络下载内容直接交给 shell 执行,无法校验完整性,供应链投毒即 RCE。",
		suggestion: "先下载到本地、校验校验和/签名后再执行;或固定到可信制品仓库的特定版本。",
	},
	{
		re:         regexp.MustCompile(`\bwget\b[^|]*\|\s*(sudo\s+)?(sh|bash|zsh)\b`),
		level:      RiskMedium,
		title:      "管道执行远程脚本(wget | sh)",
		why:        "把网络下载内容直接交给 shell 执行,无法校验完整性,供应链投毒即 RCE。",
		suggestion: "先下载到本地、校验校验和/签名后再执行;或固定到可信制品仓库的特定版本。",
	},
	{
		re:         regexp.MustCompile(`\bchmod\s+(-[a-zA-Z]*\s+)*777\b`),
		level:      RiskMedium,
		title:      "chmod 777 全权限",
		why:        "授予任意用户读写执行权限,违背最小权限原则,易被提权或篡改。",
		suggestion: "按需收紧权限(如 755 / 750);仅对确需可执行的文件加 x 位。",
	},
}

// secretAssignRules 匹配「命令里明文赋值/传入密钥」的常见形态(命中即标 high;不回显明文值)。
// 覆盖:TOKEN=xxx / API_KEY=xxx / PASSWORD=xxx / --password xxx / -p xxx / _authToken=xxx /
// Authorization: Bearer xxx 等。
var secretAssignRules = []*regexp.Regexp{
	// KEY=value 形态(等号赋值);键名含 token/secret/password/passwd/api[_-]?key/access[_-]?key/auth。
	regexp.MustCompile(`(?i)\b([A-Z0-9_]*(?:TOKEN|SECRET|PASSWORD|PASSWD|API[_-]?KEY|ACCESS[_-]?KEY|AUTH[_-]?TOKEN|PRIVATE[_-]?KEY))\s*=\s*('[^']+'|"[^"]+"|[^\s'";|&]+)`),
	// --password value / --token value / -p value(命令行参数)。
	regexp.MustCompile(`(?i)(--(?:password|token|api[_-]?key|secret))[=\s]+('[^']+'|"[^"]+"|[^\s'";|&]+)`),
	// Authorization: Bearer xxx / x-api-key: xxx。
	regexp.MustCompile(`(?i)\b(authorization\s*:\s*bearer|x-api-key\s*:?)\s+('[^']+'|"[^"]+"|[^\s'";|&]+)`),
	// _authToken=xxx(npmrc 风格)。
	regexp.MustCompile(`(?i)(_authToken|_password)\s*=\s*('[^']+'|"[^"]+"|[^\s'";|&]+)`),
}

// 镜像 / 部署 / 健康检查 / sudo / set -e 相关模式。
var (
	dockerPullRe = regexp.MustCompile(`(?i)\bdocker\s+(pull|run)\s+(\S+)`)
	healthHintRe = regexp.MustCompile(`(?i)\b(curl|wget|nc|health|healthz|ready|liveness|probe|rollout\s+status)\b`)
	deployHintRe = regexp.MustCompile(`(?i)\b(kubectl\s+(apply|rollout|set)|helm\s+(install|upgrade)|docker\s+(run|compose\s+up)|systemctl\s+(restart|start))\b`)
	sudoRule     = regexp.MustCompile(`(?i)(^|\s|&&|\|\|)\s*sudo\s+`)
	setERe       = regexp.MustCompile(`(?i)set\s+-[a-z]*e`)
)

func (s *service) AnnotateRisks(ctx context.Context, in AnnotateRisksInput) (*RiskReport, error) {
	masker := in.Masker
	if masker == nil {
		masker = mask.NewMasker()
	}

	report := &RiskReport{
		Findings:    []RiskFinding{},
		AIEnhanced:  false,
		GeneratedAt: time.Now().UTC(),
	}

	// ---- 1) 确定性预扫(无需 AI;优雅降级下限)----
	report.Findings = append(report.Findings, scanDeterministic(in.Steps)...)

	// ---- 2) LLM 增强扫(可选;AI 不可用则静默跳过)----
	cfg, sealed, err := s.load(ctx)
	if err != nil || cfg == nil || !cfg.Configured || !cfg.Enabled {
		report.AIReason = "AI 未配置或未启用,仅展示确定性规则结果"
		return report, nil
	}

	provider := cfg.Provider
	baseURL := cfg.BaseURL
	if baseURL == "" {
		baseURL = defaultBaseURL(provider)
	}
	if baseURL == "" {
		report.AIReason = "AI 未配置或未启用,仅展示确定性规则结果"
		return report, nil
	}

	apiKey := ""
	if provider != ProviderOllama {
		if len(sealed) == 0 {
			report.AIReason = "AI 未配置或未启用,仅展示确定性规则结果"
			return report, nil
		}
		plain, oerr := s.openSecret(sealed)
		if oerr != nil {
			report.AIReason = "AI 密钥不可用(保险库未配置),仅展示确定性规则结果"
			return report, nil
		}
		apiKey = plain
	}

	// 出网前脱敏:脚本进 prompt 前一律 Scrub。
	prompt := buildRiskPrompt(masker, in.Steps)

	text, cerr := s.chatWithTokens(ctx, provider, baseURL, cfg.Model, apiKey, prompt, riskAnnotateMaxTokens)
	apiKey = "" // 明文用完即弃
	_ = apiKey
	if cerr != nil {
		report.AIReason = "AI 增强分析失败:" + humanizeDiagnoseErr(cerr)
		return report, nil
	}

	aiFindings, perr := parseRiskFindings(text)
	if perr != nil {
		report.AIReason = "AI 返回内容无法解析,仅展示确定性规则结果"
		return report, nil
	}

	// 二次脱敏铁律 + 来源标注:模型回的任意字段可能回显脚本里的 secret,出库前一律 Scrub。
	for _, f := range aiFindings {
		report.Findings = append(report.Findings, RiskFinding{
			Level:      normalizeRiskLevel(f.Level),
			StepName:   masker.Scrub(strings.TrimSpace(f.StepName)),
			Line:       f.Line,
			Title:      masker.Scrub(strings.TrimSpace(f.Title)),
			Why:        masker.Scrub(strings.TrimSpace(f.Why)),
			Suggestion: masker.Scrub(strings.TrimSpace(f.Suggestion)),
			Source:     "ai",
		})
	}
	report.AIEnhanced = true
	return report, nil
}

// scanDeterministic 对每步每行跑确定性规则,产出 finding(source=rule)。
// 检测到明文密钥时**绝不回显明文**——仅标注位置,不复制命中片段。
func scanDeterministic(steps []ScriptStep) []RiskFinding {
	out := []RiskFinding{}
	for _, st := range steps {
		// 步骤/镜像级:latest 或无标签镜像。
		if f := scanImage(st); f != nil {
			out = append(out, *f)
		}

		hasSetE := false
		hasDeploy := false
		hasHealth := false
		for _, c := range st.Commands {
			if setERe.MatchString(c) {
				hasSetE = true
			}
			if deployHintRe.MatchString(c) {
				hasDeploy = true
			}
			if healthHintRe.MatchString(c) {
				hasHealth = true
			}
		}

		for i, raw := range st.Commands {
			line := i + 1
			cmd := raw

			// 危险命令规则。
			for _, rule := range commandRules {
				if rule.re.MatchString(cmd) {
					out = append(out, RiskFinding{
						Level:      rule.level,
						StepName:   st.Name,
						Line:       line,
						Title:      rule.title,
						Why:        rule.why,
						Suggestion: rule.suggestion,
						Source:     "rule",
					})
				}
			}

			// 明文密钥规则(只标位置,绝不回显命中片段)。
			if matchesSecretAssign(cmd) {
				out = append(out, RiskFinding{
					Level:      RiskHigh,
					StepName:   st.Name,
					Line:       line,
					Title:      "命令中疑似硬编码明文密钥",
					Why:        "在脚本里明文写入 token/密码/密钥会随日志、镜像层、配置一并泄漏;凭据应走平台凭据库注入。",
					Suggestion: "改用步骤级 secret 环境变量(凭据引用),执行时即取即注入,绝不在命令里写明文。",
					Source:     "rule",
				})
			}

			// 行内 latest 镜像(docker pull/run xxx:latest)。
			if f := scanInlineLatest(cmd, st.Name, line); f != nil {
				out = append(out, *f)
			}

			// sudo 提示(low)。
			if sudoRule.MatchString(cmd) {
				out = append(out, RiskFinding{
					Level:      RiskLow,
					StepName:   st.Name,
					Line:       line,
					Title:      "使用 sudo 提权",
					Why:        "构建/部署脚本中的 sudo 扩大了执行面;隔离构建容器内通常无需 sudo。",
					Suggestion: "确认是否真的需要 root;能用非特权用户就不用 sudo。",
					Source:     "rule",
				})
			}
		}

		// 步骤级:部署动作但全程无健康检查(low)。
		if hasDeploy && !hasHealth {
			out = append(out, RiskFinding{
				Level:      RiskLow,
				StepName:   st.Name,
				Line:       0,
				Title:      "部署前后缺少健康检查",
				Why:        "存在部署动作(kubectl/helm/docker/systemctl)但未见健康/就绪探测,故障可能静默发布。",
				Suggestion: "部署后加就绪检查(如 curl /healthz、kubectl rollout status),失败即中止。",
				Source:     "rule",
			})
		}

		// 步骤级:多行脚本未 set -e(low),错误不中止。
		if len(st.Commands) > 1 && !hasSetE {
			out = append(out, RiskFinding{
				Level:      RiskLow,
				StepName:   st.Name,
				Line:       0,
				Title:      "多行脚本未启用 set -e",
				Why:        "未加 set -e 时,中间命令失败仍会继续执行后续步骤,可能带病发布。",
				Suggestion: "在脚本开头加 `set -euo pipefail`,任一命令失败即中止。",
				Source:     "rule",
			})
		}
	}
	return out
}

// scanImage 检测步骤镜像是否未固定版本(:latest 或无标签)。
func scanImage(st ScriptStep) *RiskFinding {
	img := strings.TrimSpace(st.Image)
	if img == "" {
		return nil
	}
	tagged := strings.Contains(img, ":") && !strings.HasSuffix(img, ":")
	usesLatest := strings.HasSuffix(strings.ToLower(img), ":latest")
	if usesLatest || !tagged {
		return &RiskFinding{
			Level:      RiskMedium,
			StepName:   st.Name,
			Line:       0,
			Title:      "构建镜像未固定版本",
			Why:        "使用 latest 或无标签镜像会让构建不可复现,上游变更可能悄无声息地破坏流水线。",
			Suggestion: "固定到具体版本标签(如 node:20.11)或 digest(@sha256:...),保证可复现。",
			Source:     "rule",
		}
	}
	return nil
}

// scanInlineLatest 检测命令里 docker pull/run 的 latest 镜像引用。
func scanInlineLatest(cmd, stepName string, line int) *RiskFinding {
	m := dockerPullRe.FindStringSubmatch(cmd)
	if m == nil {
		return nil
	}
	ref := m[2]
	if strings.HasSuffix(strings.ToLower(ref), ":latest") || (!strings.Contains(ref, ":") && !strings.Contains(ref, "@")) {
		return &RiskFinding{
			Level:      RiskMedium,
			StepName:   stepName,
			Line:       line,
			Title:      "拉取/运行未固定版本镜像",
			Why:        "docker pull/run 使用 latest 或无标签镜像,构建/部署不可复现。",
			Suggestion: "固定到具体版本标签或 digest。",
			Source:     "rule",
		}
	}
	return nil
}

// matchesSecretAssign 报告该行是否疑似明文写入密钥(不回显命中内容)。
func matchesSecretAssign(cmd string) bool {
	for _, re := range secretAssignRules {
		if re.MatchString(cmd) {
			return true
		}
	}
	return false
}

// normalizeRiskLevel 归一风险等级(非法值兜底 low)。
func normalizeRiskLevel(l string) string {
	switch strings.ToLower(strings.TrimSpace(l)) {
	case RiskHigh:
		return RiskHigh
	case RiskMedium:
		return RiskMedium
	default:
		return RiskLow
	}
}

// llmRiskFinding 是 LLM 期望回的单条 finding 形状。
type llmRiskFinding struct {
	Level      string `json:"level"`
	StepName   string `json:"stepName"`
	Line       int    `json:"line"`
	Title      string `json:"title"`
	Why        string `json:"why"`
	Suggestion string `json:"suggestion"`
}

// llmRiskResponse 是 LLM 期望回的整体形状(剥 fence 后 Unmarshal)。
type llmRiskResponse struct {
	Findings []llmRiskFinding `json:"findings"`
}

// parseRiskFindings 容错剥 fence 解析 LLM 文本为 finding 列表(空标题项丢弃)。
func parseRiskFindings(text string) ([]llmRiskFinding, error) {
	jsonText := stripJSONFence(text)
	if strings.TrimSpace(jsonText) == "" {
		return nil, fmt.Errorf("%w: 模型未返回可解析内容", ErrGenerateFailed)
	}
	var r llmRiskResponse
	if err := json.Unmarshal([]byte(jsonText), &r); err != nil {
		return nil, fmt.Errorf("%w: 模型返回的不是有效 JSON", ErrGenerateFailed)
	}
	out := make([]llmRiskFinding, 0, len(r.Findings))
	for _, f := range r.Findings {
		if strings.TrimSpace(f.Title) == "" {
			continue
		}
		out = append(out, f)
	}
	return out, nil
}

// buildRiskPrompt 构造发给 LLM 的风险标注 prompt:脱敏脚本(带步骤名 + 行号)+ 期望 JSON schema。
// 脚本进 prompt 前一律 Scrub;绝不含 apiKey / token / 明文 secret。
func buildRiskPrompt(m *mask.Masker, steps []ScriptStep) string {
	var b strings.Builder
	b.WriteString("你是 CI/CD 脚本安全审计助手。请审计下面流水线的脚本步骤命令,找出**语义级**安全/可靠性风险。\n")
	b.WriteString("简单的正则可命中模式(rm -rf /、curl|sh、chmod 777、明文密钥、latest 镜像)已由规则引擎覆盖,请你**重点补充**:\n")
	b.WriteString("- 部署前/后缺健康检查、缺回滚\n- 危险的环境变量/凭据使用(虽非显式明文,但可能泄漏)\n- 不可复现/不幂等的步骤、竞态、误删风险\n- 过度授权、网络出网到不可信源\n\n")

	written := 0
	for si, st := range steps {
		stepName := m.Scrub(strings.TrimSpace(st.Name))
		if stepName == "" {
			stepName = fmt.Sprintf("步骤%d", si+1)
		}
		fmt.Fprintf(&b, "## %s(镜像:%s)\n", stepName, m.Scrub(strings.TrimSpace(st.Image)))
		for li, c := range st.Commands {
			scrubbed := m.Scrub(c)
			if written+len(scrubbed) > maxScriptChars {
				b.WriteString("…(脚本过长,已截断)\n")
				goto done
			}
			fmt.Fprintf(&b, "%d: %s\n", li+1, scrubbed)
			written += len(scrubbed)
		}
		b.WriteString("\n")
	}
done:

	b.WriteString(`
## 输出要求
只输出一个 JSON 对象(不要任何解释文字、不要 markdown 代码块),严格符合以下结构:
{
  "findings": [
    { "level": "high|medium|low", "stepName": "命中步骤名", "line": 3, "title": "简短风险标题", "why": "为什么有风险", "suggestion": "如何修复" }
  ]
}
约束:
- level 仅能为 high/medium/low。
- line 为该步骤内命令的行号(1-based);步骤级风险用 0。
- 不要重复规则引擎已能命中的显式模式;聚焦语义级风险。
- 不要在任何字段回显敏感信息或凭据;若发现疑似明文密钥,只描述位置,不要复制其值。
- 没有发现风险时 findings 为空数组。
`)
	return b.String()
}
