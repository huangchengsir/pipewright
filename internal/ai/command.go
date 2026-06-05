// command.go 是「AI 运维命令助手」领域层(运维终端 P1 · 护城河 · AI moat)。
//
// Service.CommandSuggest 把用户中文描述 → 单条可执行 shell 命令 + 中文解释 + 风险等级;
// Service.ExplainCommand 解释一条既有命令(+ 风险等级)。两者都为「AI 运维终端」右栏的
// AI 助手供能(对标阿里云 Cloud Shell 运维助手)。
//
// **危险命令护城河(确定性兜底)**:LLM 给出的 risk 仅作参考,出库前一律经 classifyCommandRisk
// 做**确定性正则**复核——破坏性/不可逆操作(rm -rf /、mkfs、dd of=/dev/*、:(){ fork bomb、
// chmod 777、find -delete、shutdown/reboot 等)**无条件升级为 danger**,即便 AI 判 safe。
// 这保证「危险拦截」不依赖 AI 在线/诚实,是平台的确定性安全下限(与 risk.go 同一姿态)。
//
// 安全铁律(AC-SEC):
//   - 用户输入 NL / 上下文出网进 prompt 前一律过 mask.Masker 脱敏;模型回的命令/解释/理由
//     二次脱敏(模型可能复述用户贴进来的凭据)。
//   - apiKey 仅进程内解密取用、用完即弃,绝不进 prompt / 日志 / 错误 / 结果。
//
// 优雅降级:与 Generate 一致,**生成本就需要 AI**——未配置/未启用 → ErrAINotConfigured,
// 由上层映射为「去设置配 AI」的人读提示(终端 + 复制粘贴 P0 不受影响,照常可用)。
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

// 命令风险等级(命令级,与 risk.go 的脚本 finding 等级解耦;对齐前端命令卡左色条配色:
// safe=cyan「只读·安全」/ write=amber「写操作·谨慎」/ danger=red「高危·破坏性」)。
const (
	// CmdRiskSafe 表示只读 / 查询类命令(ls、ps、cat、df 等),可直接执行。
	CmdRiskSafe = "safe"
	// CmdRiskWrite 表示会修改文件 / 服务 / 状态的命令(写文件、重启服务、安装包),需谨慎。
	CmdRiskWrite = "write"
	// CmdRiskDanger 表示破坏性 / 不可逆命令(删除、格式化、覆盖设备、关机重启),必须二次确认。
	CmdRiskDanger = "danger"
)

// commandSuggestMaxTokens / explainMaxTokens 是两类 chat 的 max_tokens(单条命令 + 短解释,够用即可)。
const (
	commandSuggestMaxTokens = 700
	explainMaxTokens        = 600
)

// maxNLChars 是用户自然语言描述 / 命令进 prompt 的字符上限(防超长吃 token)。
const maxNLChars = 2000

// completeMaxTokens 是补全 chat 的 max_tokens(单行命令补全,极短;控成本/延迟)。
const completeMaxTokens = 64

// CommandContext 是终端会话上下文(进 prompt 作背景 + 辅助风险判定;字段均可空)。
type CommandContext struct {
	OS        string // 目标 OS(如 linux / alpine;可空)
	Shell     string // 当前 shell(如 /bin/sh;可空)
	Container string // 容器名 / ID(可空)
	Cwd       string // 当前工作目录(可空)
}

// CommandSuggestInput 是命令建议入参。Masker 出网前脱敏;为 nil 时兜底构空 Masker(不 panic)。
type CommandSuggestInput struct {
	NL      string
	Context CommandContext
	Masker  *mask.Masker
}

// CommandSuggestion 是命令建议结果(契约形状;camelCase;绝无明文密钥)。
// Risk 经确定性复核后的最终等级(safe|write|danger)。
type CommandSuggestion struct {
	Command     string    `json:"command"`
	Explanation string    `json:"explanation"`
	Risk        string    `json:"risk"`
	Reason      string    `json:"reason"`
	GeneratedAt time.Time `json:"generatedAt"`
}

// CompleteCommandInput 是命令补全入参(P2 智能补全)。Partial 为用户已输入的命令前缀。
type CompleteCommandInput struct {
	Partial string
	Context CommandContext
	Masker  *mask.Masker
}

// CompletionResult 是补全结果。Completion 为补全后的完整命令(以 Partial 原样开头);
// 无合理补全时等于 Partial(前端据「!= partial」判断是否展示建议)。
type CompletionResult struct {
	Completion string `json:"completion"`
}

// ExplainCommandInput 是命令解释入参。
type ExplainCommandInput struct {
	Command string
	Context CommandContext
	Masker  *mask.Masker
}

// CommandExplanation 是命令解释结果(含确定性风险等级,供前端徽标着色)。
type CommandExplanation struct {
	Explanation string    `json:"explanation"`
	Risk        string    `json:"risk"`
	GeneratedAt time.Time `json:"generatedAt"`
}

// ---- 确定性命令风险规则(护城河兜底:无 AI / AI 误判仍能拦危险) ----

// dangerCommandRule / writeCommandRule:单条命令的确定性正则规则。命中即给出等级 + 人读理由。
type cmdRiskRule struct {
	re     *regexp.Regexp
	reason string
}

// rm 的递归 + 强制标记探测(组合 -rf/-fr 或分离 -r -f,或 --recursive/--force)。
// 递归强制删除是不可逆的破坏性操作,无论目标路径都标 danger;详见 dangerousRmReason。
var (
	rmInvokeRe    = regexp.MustCompile(`(^|[|;&]|\s)rm\s`)
	rmRecursiveRe = regexp.MustCompile(`\s-[a-zA-Z]*r[a-zA-Z]*\b|--recursive\b`)
	rmForceRe     = regexp.MustCompile(`\s-[a-zA-Z]*f[a-zA-Z]*\b|--force\b`)
)

// dangerousRmReason 报告该命令是否为「递归 + 强制」rm(danger);否则返回空串。
func dangerousRmReason(c string) string {
	if !rmInvokeRe.MatchString(" " + c) {
		return ""
	}
	if rmRecursiveRe.MatchString(c) && rmForceRe.MatchString(c) {
		return "递归强制删除(rm -rf),误删不可逆。"
	}
	return ""
}

// dangerCommandRules:破坏性 / 不可逆模式 —— 命中无条件升级为 danger。
// (rm -rf 由 dangerousRmReason 在规则前单独判,覆盖任意目标路径。)
var dangerCommandRules = []cmdRiskRule{
	{regexp.MustCompile(`\bmkfs(\.\w+)?\b`), "格式化文件系统,目标数据全毁。"},
	{regexp.MustCompile(`\bdd\b[^|]*\bof=/dev/`), "dd 写裸设备,覆盖磁盘不可逆。"},
	{regexp.MustCompile(`>\s*/dev/(sd|nvme|hd|vd)\w*`), "重定向写入裸磁盘设备,破坏分区数据。"},
	{regexp.MustCompile(`:\s*\(\s*\)\s*\{[^}]*:\s*\|\s*:[^}]*\}\s*;\s*:`), "fork 炸弹,耗尽进程资源拖垮主机。"},
	{regexp.MustCompile(`\bchmod\s+(-[a-zA-Z]*\s+)*-?R?\s*777\b`), "递归 / 全权限授权,放大被提权篡改面。"},
	{regexp.MustCompile(`\bfind\b[^|]*\s-delete\b`), "find -delete 批量删除,范围错即批量误删。"},
	{regexp.MustCompile(`\b(shutdown|reboot|halt|poweroff|init\s+0|init\s+6)\b`), "关机 / 重启主机,中断所有服务。"},
	{regexp.MustCompile(`\bkill\s+-9\s+-1\b|\bkillall\s+-9\b`), "向所有进程发 SIGKILL,等同强制重启。"},
	{regexp.MustCompile(`\b(userdel|groupdel)\b`), "删除用户 / 组,可能丢失文件归属与访问。"},
	{regexp.MustCompile(`\bcrontab\s+-r\b`), "清空当前用户全部定时任务,不可恢复。"},
	{regexp.MustCompile(`(?i)\bdrop\s+(database|table)\b`), "删除数据库 / 表,数据不可逆丢失。"},
	{regexp.MustCompile(`\btruncate\s+-s\s*0\b`), "清空文件内容至 0 字节,原数据丢失。"},
	{regexp.MustCompile(`\biptables\s+-F\b`), "清空防火墙规则,可能瞬间暴露内网端口。"},
	{regexp.MustCompile(`\bgit\s+(reset\s+--hard|clean\s+-[a-z]*f)`), "强制丢弃工作区改动,未提交内容不可恢复。"},
}

// writeCommandRules:会修改文件 / 服务 / 状态的模式 —— 命中升级为至少 write(未触发 danger 时)。
var writeCommandRules = []cmdRiskRule{
	{regexp.MustCompile(`\brm\b`), "删除文件。"},
	{regexp.MustCompile(`\b(mv|cp)\b`), "移动 / 复制文件,可能覆盖目标。"},
	{regexp.MustCompile(`\b(chmod|chown|chgrp)\b`), "修改文件权限 / 归属。"},
	{regexp.MustCompile(`\bsed\s+-i\b`), "就地改写文件内容。"},
	{regexp.MustCompile(`\b(mkdir|touch|ln|tee)\b`), "创建 / 写入文件。"},
	{regexp.MustCompile(`>>?\s*[^\s&|]+`), "重定向写入 / 追加文件。"},
	{regexp.MustCompile(`\b(systemctl|service)\s+(restart|start|stop|reload|disable|enable)\b`), "改变服务运行状态。"},
	{regexp.MustCompile(`\bnginx\s+-s\s+(reload|stop|quit)\b`), "重载 / 停止 nginx,短暂影响请求。"},
	{regexp.MustCompile(`\bdocker\s+(run|rm|stop|restart|kill|rmi|exec)\b`), "改变容器 / 镜像状态。"},
	{regexp.MustCompile(`\bkubectl\s+(apply|delete|rollout|scale|set|patch|edit)\b`), "变更 Kubernetes 资源。"},
	{regexp.MustCompile(`\bhelm\s+(install|upgrade|uninstall|rollback)\b`), "变更 Helm 发布。"},
	{regexp.MustCompile(`(?i)\b(apt|apt-get|yum|dnf|apk|pip|pip3|npm|yarn|pnpm)\s+(install|remove|add|del|uninstall|update|upgrade)\b`), "安装 / 卸载软件包,改变系统状态。"},
	{regexp.MustCompile(`\bkill\b|\bkillall\b|\bpkill\b`), "终止进程。"},
	{regexp.MustCompile(`\bgit\s+(push|merge|rebase|checkout|pull|commit)\b`), "改变仓库状态。"},
	{regexp.MustCompile(`\b(export|unset)\b`), "改变环境变量。"},
}

// classifyCommandRisk 对单条命令做确定性风险分级,返回(等级, 人读理由)。
// 优先级:danger > write > safe。命中 danger 规则即返回 danger;否则命中 write 即 write;
// 都不命中为 safe。理由用于在命令卡 / 拦截提示里给人读说明(绝不回显命令里的明文 secret)。
func classifyCommandRisk(cmd string) (string, string) {
	c := strings.TrimSpace(cmd)
	if c == "" {
		return CmdRiskSafe, ""
	}
	if reason := dangerousRmReason(c); reason != "" {
		return CmdRiskDanger, reason
	}
	for _, r := range dangerCommandRules {
		if r.re.MatchString(c) {
			return CmdRiskDanger, r.reason
		}
	}
	for _, r := range writeCommandRules {
		if r.re.MatchString(c) {
			return CmdRiskWrite, r.reason
		}
	}
	return CmdRiskSafe, ""
}

// reconcileRisk 把 AI 给的 risk 与确定性复核合并:取两者中更危险的等级。
// 确定性升级时,把确定性理由前置到 reason(让用户看到「为什么被拦」),AI 理由保留在后。
func reconcileRisk(aiRisk, aiReason, cmd string) (string, string) {
	detRisk, detReason := classifyCommandRisk(cmd)
	ai := normalizeCmdRisk(aiRisk)

	final := ai
	if riskRank(detRisk) > riskRank(ai) {
		final = detRisk
	}

	reason := strings.TrimSpace(aiReason)
	// 确定性比 AI 更危险:把确定性理由前置(优雅且可解释的拦截)。
	if riskRank(detRisk) > riskRank(ai) && detReason != "" {
		if reason == "" {
			reason = detReason
		} else {
			reason = detReason + " " + reason
		}
	}
	return final, reason
}

// riskRank 把等级映射为可比较的序(safe<write<danger),用于取更危险者。
func riskRank(r string) int {
	switch r {
	case CmdRiskDanger:
		return 2
	case CmdRiskWrite:
		return 1
	default:
		return 0
	}
}

// normalizeCmdRisk 归一 AI 回的 risk 字串(非法 / 空 → 兜底 safe,再由确定性复核兜危险)。
func normalizeCmdRisk(r string) string {
	switch strings.ToLower(strings.TrimSpace(r)) {
	case CmdRiskDanger, "high", "critical":
		return CmdRiskDanger
	case CmdRiskWrite, "medium", "moderate":
		return CmdRiskWrite
	case CmdRiskSafe, "low", "read", "readonly", "read-only":
		return CmdRiskSafe
	default:
		return CmdRiskSafe
	}
}

func (s *service) CommandSuggest(ctx context.Context, in CommandSuggestInput) (*CommandSuggestion, error) {
	masker := in.Masker
	if masker == nil {
		masker = mask.NewMasker()
	}
	nl := strings.TrimSpace(in.NL)
	if nl == "" {
		return nil, fmt.Errorf("%w: 请描述你想做的操作", ErrGenerateFailed)
	}

	provider, baseURL, apiKey, err := s.resolveChatCreds(ctx)
	if err != nil {
		return nil, err
	}

	prompt := buildCommandPrompt(masker, nl, in.Context)
	text, cerr := s.chatWithTokens(ctx, provider, baseURL, s.modelFor(ctx), apiKey, prompt, commandSuggestMaxTokens)
	apiKey = "" // 明文用完即弃
	_ = apiKey
	if cerr != nil {
		return nil, cerr
	}

	parsed, perr := parseCommandSuggestion(text)
	if perr != nil {
		return nil, perr
	}

	// 二次脱敏 + 确定性风险复核(护城河)。
	command := masker.Scrub(strings.TrimSpace(parsed.Command))
	risk, reason := reconcileRisk(parsed.Risk, parsed.Reason, command)
	return &CommandSuggestion{
		Command:     command,
		Explanation: masker.Scrub(strings.TrimSpace(parsed.Explanation)),
		Risk:        risk,
		Reason:      masker.Scrub(strings.TrimSpace(reason)),
		GeneratedAt: time.Now().UTC(),
	}, nil
}

func (s *service) ExplainCommand(ctx context.Context, in ExplainCommandInput) (*CommandExplanation, error) {
	masker := in.Masker
	if masker == nil {
		masker = mask.NewMasker()
	}
	cmd := strings.TrimSpace(in.Command)
	if cmd == "" {
		return nil, fmt.Errorf("%w: 请提供要解释的命令", ErrGenerateFailed)
	}

	provider, baseURL, apiKey, err := s.resolveChatCreds(ctx)
	if err != nil {
		return nil, err
	}

	prompt := buildExplainPrompt(masker, cmd, in.Context)
	text, cerr := s.chatWithTokens(ctx, provider, baseURL, s.modelFor(ctx), apiKey, prompt, explainMaxTokens)
	apiKey = ""
	_ = apiKey
	if cerr != nil {
		return nil, cerr
	}

	// 解释取纯文本(剥可能的 fence);风险等级由确定性规则给(不依赖 AI)。
	explanation := strings.TrimSpace(stripCodeFence(text))
	if explanation == "" {
		return nil, fmt.Errorf("%w: 模型未返回可用解释", ErrGenerateFailed)
	}
	risk, _ := classifyCommandRisk(cmd)
	return &CommandExplanation{
		Explanation: masker.Scrub(explanation),
		Risk:        risk,
		GeneratedAt: time.Now().UTC(),
	}, nil
}

func (s *service) CompleteCommand(ctx context.Context, in CompleteCommandInput) (*CompletionResult, error) {
	masker := in.Masker
	if masker == nil {
		masker = mask.NewMasker()
	}
	partial := in.Partial // 保留原样(含尾空格语义);仅做长度判断时 Trim
	if strings.TrimSpace(partial) == "" {
		return nil, fmt.Errorf("%w: 无补全前缀", ErrGenerateFailed)
	}

	provider, baseURL, apiKey, err := s.resolveChatCreds(ctx)
	if err != nil {
		return nil, err
	}

	prompt := buildCompletePrompt(masker, partial, in.Context)
	text, cerr := s.chatWithTokens(ctx, provider, baseURL, s.modelFor(ctx), apiKey, prompt, completeMaxTokens)
	apiKey = ""
	_ = apiKey
	if cerr != nil {
		return nil, cerr
	}

	// 取首个非空行作为补全(模型偶尔多行/带噪声);脱敏。
	completion := masker.Scrub(firstLine(stripCodeFence(text)))
	// 安全约束:补全必须以用户前缀原样开头,否则视为无效(避免替换用户已输入内容)。
	if completion == "" || !strings.HasPrefix(completion, strings.TrimRight(partial, " ")) {
		completion = partial
	}
	return &CompletionResult{Completion: completion}, nil
}

// resolveChatCreds 读单例配置并取出 chat 所需 provider/baseURL/apiKey(明文,调用方用完即弃)。
// 未配置 / 未启用 → ErrAINotConfigured;保险库不可用 → ErrVaultUnconfigured。复用 load + openSecret。
func (s *service) resolveChatCreds(ctx context.Context) (provider, baseURL, apiKey string, err error) {
	cfg, sealed, lerr := s.load(ctx)
	if lerr != nil {
		return "", "", "", lerr
	}
	if cfg == nil || !cfg.Configured || !cfg.Enabled {
		return "", "", "", ErrAINotConfigured
	}
	provider = cfg.Provider
	baseURL = cfg.BaseURL
	if baseURL == "" {
		baseURL = defaultBaseURL(provider)
	}
	if baseURL == "" {
		return "", "", "", ErrAINotConfigured
	}
	if provider != ProviderOllama {
		if len(sealed) == 0 {
			return "", "", "", ErrAINotConfigured
		}
		plain, oerr := s.openSecret(sealed)
		if oerr != nil {
			return "", "", "", oerr
		}
		apiKey = plain
	}
	return provider, baseURL, apiKey, nil
}

// modelFor 读取当前配置的 model(命令助手 chat 用)。读失败回空串(provider 默认模型由 provider 端决定)。
func (s *service) modelFor(ctx context.Context) string {
	if cfg, _, err := s.load(ctx); err == nil && cfg != nil {
		return cfg.Model
	}
	return ""
}

// llmCommandSuggestion 是 LLM 期望回的命令建议形状(剥 fence 后 Unmarshal)。
type llmCommandSuggestion struct {
	Command     string `json:"command"`
	Explanation string `json:"explanation"`
	Risk        string `json:"risk"`
	Reason      string `json:"reason"`
}

// parseCommandSuggestion 容错剥 fence 解析 LLM 文本为命令建议(空命令视为失败)。
func parseCommandSuggestion(text string) (*llmCommandSuggestion, error) {
	jsonText := stripJSONFence(text)
	if strings.TrimSpace(jsonText) == "" {
		return nil, fmt.Errorf("%w: 模型未返回可解析内容", ErrGenerateFailed)
	}
	var c llmCommandSuggestion
	if err := json.Unmarshal([]byte(jsonText), &c); err != nil {
		return nil, fmt.Errorf("%w: 模型返回的不是有效 JSON 命令", ErrGenerateFailed)
	}
	if strings.TrimSpace(c.Command) == "" {
		return nil, fmt.Errorf("%w: 模型未给出可执行命令", ErrGenerateFailed)
	}
	return &c, nil
}

// buildCommandPrompt 构造命令建议 prompt:脱敏的需求 + 上下文 + 期望 JSON schema。
func buildCommandPrompt(m *mask.Masker, nl string, c CommandContext) string {
	var b strings.Builder
	b.WriteString("你是经验丰富的 Linux 运维助手,正在一个容器的交互终端里协助工程师。\n")
	b.WriteString("根据用户的中文需求,产出**单条**可直接执行的 shell 命令,附简短中文解释与风险等级。\n\n")
	b.WriteString("## 终端上下文\n")
	fmt.Fprintf(&b, "- OS: %s\n", fallbackCtx(m.Scrub(strings.TrimSpace(c.OS)), "linux"))
	fmt.Fprintf(&b, "- Shell: %s\n", fallbackCtx(m.Scrub(strings.TrimSpace(c.Shell)), "/bin/sh"))
	fmt.Fprintf(&b, "- 容器: %s\n", fallbackCtx(m.Scrub(strings.TrimSpace(c.Container)), "(未知)"))
	fmt.Fprintf(&b, "- 当前目录: %s\n\n", fallbackCtx(m.Scrub(strings.TrimSpace(c.Cwd)), "(未知)"))
	b.WriteString("## 用户需求\n")
	b.WriteString(truncateRunes(m.Scrub(nl), maxNLChars))
	b.WriteString("\n\n")
	b.WriteString(`## 输出要求
只输出一个 JSON 对象(不要任何解释文字、不要 markdown 代码块),严格符合:
{"command":"单条可执行命令","explanation":"这条命令做什么(中文,1-2 句)","risk":"safe|write|danger","reason":"风险或注意点(中文,一句)"}
约束:
- 只产出一条命令(可含管道 / && );不要多行脚本、不要交互式编辑器(vi/nano)。
- risk 含义:safe=只读/查询;write=会修改文件/服务/状态;danger=破坏性/不可逆(删除、格式化、覆盖设备、关机重启、递归强删等)。
- 破坏性或不可逆操作必须标 danger,绝不淡化。
- 绝不在命令或任何字段写入明文密钥 / token / 密码;需要凭据时用占位符。
`)
	return b.String()
}

// buildExplainPrompt 构造命令解释 prompt:脱敏命令 + 上下文 → 逐段中文解释(纯文本)。
func buildExplainPrompt(m *mask.Masker, cmd string, c CommandContext) string {
	var b strings.Builder
	b.WriteString("你是 Linux 运维助手。请用中文逐段解释下面这条命令的含义、每个参数 / 管道做什么、以及执行它的潜在影响。\n")
	b.WriteString("用简洁的纯文本(可分点),不要 markdown 代码块,不要复述无关内容。\n\n")
	if os := m.Scrub(strings.TrimSpace(c.OS)); os != "" {
		fmt.Fprintf(&b, "环境:%s\n", os)
	}
	b.WriteString("命令:\n")
	b.WriteString(truncateRunes(m.Scrub(cmd), maxNLChars))
	b.WriteString("\n")
	return b.String()
}

// buildCompletePrompt 构造命令补全 prompt:已输入前缀 + 上下文 → 一行完整命令(以前缀开头)。
func buildCompletePrompt(m *mask.Masker, partial string, c CommandContext) string {
	var b strings.Builder
	b.WriteString("你是 Linux shell 命令补全引擎。根据用户已输入的命令前缀,补全成一条最可能、可直接执行的完整命令。\n")
	b.WriteString("规则:\n")
	b.WriteString("- 输出必须以用户已输入的前缀**原样开头**,只在其后追加内容,不要改写前缀。\n")
	b.WriteString("- 只输出补全后的完整命令本身,**一行**,不要解释、不要 markdown、不要反引号。\n")
	b.WriteString("- 若前缀已是完整命令或没有合理补全,就原样输出该前缀。\n")
	fmt.Fprintf(&b, "环境:OS %s, shell %s, 容器 %s, 目录 %s\n",
		fallbackCtx(m.Scrub(strings.TrimSpace(c.OS)), "linux"),
		fallbackCtx(m.Scrub(strings.TrimSpace(c.Shell)), "/bin/sh"),
		fallbackCtx(m.Scrub(strings.TrimSpace(c.Container)), "-"),
		fallbackCtx(m.Scrub(strings.TrimSpace(c.Cwd)), "-"))
	b.WriteString("前缀:\n")
	b.WriteString(truncateRunes(m.Scrub(partial), maxNLChars))
	b.WriteString("\n")
	return b.String()
}

// firstLine 取文本首个非空行并 Trim(补全场景模型应只回一行,容错多行噪声)。
func firstLine(s string) string {
	for _, ln := range strings.Split(s, "\n") {
		if t := strings.TrimSpace(ln); t != "" {
			return t
		}
	}
	return ""
}

// fallbackCtx 上下文字段空时给占位(避免 prompt 出现空值)。
func fallbackCtx(v, dflt string) string {
	if v == "" {
		return dflt
	}
	return v
}

// truncateRunes 按 rune 截断(避免切坏多字节 UTF-8),超长加省略提示。
func truncateRunes(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max]) + "…(已截断)"
}

// stripCodeFence 容错剥 markdown 代码围栏(解释场景:模型偶尔把整段包进 ```),保留纯文本。
func stripCodeFence(text string) string {
	t := strings.TrimSpace(text)
	if strings.HasPrefix(t, "```") {
		if idx := strings.IndexByte(t, '\n'); idx >= 0 {
			t = t[idx+1:]
		} else {
			t = strings.TrimPrefix(t, "```")
		}
		if idx := strings.LastIndex(t, "```"); idx >= 0 {
			t = t[:idx]
		}
	}
	return strings.TrimSpace(t)
}
