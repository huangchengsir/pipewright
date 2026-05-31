// Package ai 是「可配置 AI 提供商」的领域层(FR-24 / NFR-10 / Story 7.1)。
//
// 平台持有一份单例 AI 配置:provider(claude|openai|ollama)+ baseUrl + model +
// apiKey(加密)+ budget 声明 + enabled 开关。apiKey 明文经 vault secretbox(master
// key)加密后仅以**密文 BLOB** 入库;DB dump 绝无明文 key(AC-SEC)。对外(REST)
// 常态只暴露掩码;明文仅在 Test 探测时进程内解密取用、用完即弃,绝不回显/日志。
//
// master key 缺失时,涉及读已存密钥的操作降级为明确错误(ErrVaultUnconfigured),
// 平台不 panic。未配置 / 未 enabled → 下游优雅禁用,核心 CI/CD 路径完全不依赖此服务
// (NFR-10)。
//
// 本期为「最小可用」:Test 仅向 provider 发轻量探测(GET models/tags)校验连通 +
// 认证;budget 仅存声明(monthlyTokenLimit),不强制执行/计量(Epic7 用量统计后做)。
package ai

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/huangchengsir/pipewright/internal/vault"
)

// Provider 枚举(DB 存小写字串;JSON 同名)。空串 = 未配置。
const (
	// ProviderClaude 表示 Anthropic Claude(api.anthropic.com)。
	ProviderClaude = "claude"
	// ProviderOpenAI 表示 OpenAI(api.openai.com)。
	ProviderOpenAI = "openai"
	// ProviderOllama 表示本地 Ollama(localhost:11434;无需 key)。
	ProviderOllama = "ollama"
)

// 各 provider 默认 baseUrl(provider 非空但 baseUrl 为空时后端兜底)。
const (
	defaultBaseURLClaude = "https://api.anthropic.com"
	defaultBaseURLOpenAI = "https://api.openai.com"
	defaultBaseURLOllama = "http://localhost:11434"
)

// 领域错误。错误体永不含 apiKey 明文/密文/master key。
var (
	// ErrInvalidProvider 表示 provider 枚举非法(非 claude|openai|ollama 且非空)。
	ErrInvalidProvider = errors.New("ai: invalid provider")
	// ErrBaseURLRequired 表示 provider 非空但 baseUrl 为空(且无默认兜底)。
	ErrBaseURLRequired = errors.New("ai: base url required")
	// ErrAPIKeyRequired 表示非 ollama provider 既无既有 key 又无新 key。
	ErrAPIKeyRequired = errors.New("ai: api key required")
	// ErrVaultUnconfigured 表示保险库未配置 master key,无法加密/读取 apiKey。
	ErrVaultUnconfigured = errors.New("ai: vault unconfigured")
	// ErrAINotConfigured 表示 AI 提供商未配置 / 未启用,生成不可用(优雅降级,不致命)。
	ErrAINotConfigured = errors.New("ai: provider not configured or disabled")
	// ErrGenerateFailed 表示 LLM 调用 / 响应解析失败(人读错误,绝无密钥)。
	ErrGenerateFailed = errors.New("ai: generate failed")
)

// Budget 是预算声明(本期仅存,不强制执行)。MonthlyTokenLimit 为 nil 表示不限。
type Budget struct {
	MonthlyTokenLimit *int64 `json:"monthlyTokenLimit"`
}

// Config 是 AI 配置领域模型。APIKeyMasked 为掩码;apiKey 明文绝不在此模型常驻。
// Configured = provider 非空 +(provider=ollama 无需 key / 否则有 key)。
type Config struct {
	Configured   bool
	Enabled      bool
	Provider     string
	BaseURL      string
	Model        string
	APIKeyMasked string
	Budget       Budget
	UpdatedAt    *time.Time
}

// SaveInput 是保存配置的入参。
// APIKey 为**只写**指针:nil/空串 = 保留既有密钥(不清空);非空 = 轮换为新密钥。
type SaveInput struct {
	Provider string
	BaseURL  string
	Model    string
	APIKey   *string
	Budget   Budget
	Enabled  bool
}

// TestInput 是探测连接的入参(draft;省略字段回退已保存配置)。
// 指针字段为 nil 表示回退已存值;APIKey nil = 用已存密钥。
type TestInput struct {
	Provider *string
	BaseURL  *string
	Model    *string
	APIKey   *string
}

// TestResult 是探测结果(冻结契约形状)。Error 为人读错误(绝不含密钥);成功为空。
type TestResult struct {
	OK        bool
	LatencyMs int64
	Detail    string
	Error     string
}

// Service 定义 AI 配置领域对外接口。
type Service interface {
	// Get 返回单例 AI 配置(apiKey 仅掩码)。首次无配置时返回惰性空默认
	// (provider 空 / enabled false / configured false)。
	Get(ctx context.Context) (*Config, error)
	// Save 校验并持久化配置;apiKey 省略/空保留旧密文、非空轮换(vault.SealSecret 加密)。
	// 返回更新后配置(掩码)。
	Save(ctx context.Context, in SaveInput) (*Config, error)
	// Test 向 provider 发轻量探测(注入的 http.Client),回显 ok/延迟/错误(人读,绝无密钥)。
	Test(ctx context.Context, in TestInput) (*TestResult, error)
	// Generate 据仓库分析 + 自然语言补充,构 prompt 调 LLM chat 生成结构化流水线提案(Story 2.5)。
	// 未配置 / 未启用 → ErrAINotConfigured;调用 / 解析失败 → 人读错误(绝无密钥)。
	Generate(ctx context.Context, in GenerateInput) (*Proposal, error)
	// Diagnose 据失败日志构 prompt 调 LLM chat 生成失败诊断(根因假说+置信度+证据;Story 7.2)。
	// **出网前必脱敏**:入参 Masker 登记的 secret 在日志进 prompt / 取证据前一律 [MASKED]。
	// 优雅降级铁律:AI 未配 / 超时 / 不可解析 / 空 hypothesis / 低质 → status=unavailable + reason,
	// **绝不返回 error 致上层 500**(本方法对降级路径返回 (*Diagnosis, nil),仅极少内部错回 error)。
	Diagnose(ctx context.Context, in DiagnoseInput) (*Diagnosis, error)
	// AnnotateRisks 对脚本步骤命令做风险标注(护城河):确定性正则预扫 + 可选 LLM 语义增强。
	// **出网前必脱敏**:入参 Masker 登记的 secret 在脚本进 prompt / 取证据前一律 [MASKED];
	// 检测到明文密钥的标注本身绝不回显该密钥。优雅降级铁律:AI 未配 / 失败 / 不可解析 → 仅返回
	// 确定性规则结果 + AIEnhanced=false + 人读 reason,**永远返回 (*RiskReport, nil),绝不 500**。
	AnnotateRisks(ctx context.Context, in AnnotateRisksInput) (*RiskReport, error)
}

// service 是 store + vault + 注入 http.Client 支撑的 Service 实现。
type service struct {
	db     *sql.DB
	vault  vault.Vault
	client *http.Client
}

// New 构造 Service。
//   - db:经参数化 SQL 触库。
//   - v:凭据保险库,复用其 secretbox(master key)加解密 apiKey;v 为 nil 或未配置
//     master key 时,涉及密钥的读取/加密返回 ErrVaultUnconfigured。
//   - client:Test 探测用的 HTTP 客户端(应带超时,如 ~8s);为 nil 时回退默认带超时客户端。
//     单测可注入指向本地 stub server 的 client + 短超时。
//
// 不在此做任何重活(无 init() 副作用,避免抬高空载内存)。
func New(db *sql.DB, v vault.Vault, client *http.Client) Service {
	if client == nil {
		client = &http.Client{Timeout: 8 * time.Second}
	}
	return &service{db: db, vault: v, client: client}
}

func (s *service) Get(ctx context.Context) (*Config, error) {
	cfg, _, err := s.load(ctx)
	if err != nil {
		return nil, err
	}
	return cfg, nil
}

func (s *service) Save(ctx context.Context, in SaveInput) (*Config, error) {
	provider, err := normalizeProvider(in.Provider)
	if err != nil {
		return nil, err
	}

	// 读既有行(判 apiKey 保留语义 + budget 兜底);无行视为空默认。
	_, existingSealed, err := s.load(ctx)
	if err != nil {
		return nil, err
	}

	baseURL := strings.TrimSpace(in.BaseURL)
	if provider != "" && baseURL == "" {
		baseURL = defaultBaseURL(provider)
	}
	if provider != "" && baseURL == "" {
		return nil, ErrBaseURLRequired
	}

	// apiKey 保留语义:nil/空 → 沿用既有密文;非空 → 轮换(SealSecret 加密)。
	sealed := existingSealed
	if in.APIKey != nil && *in.APIKey != "" {
		if s.vault == nil {
			return nil, ErrVaultUnconfigured
		}
		newSealed, serr := s.vault.SealSecret([]byte(*in.APIKey))
		if serr != nil {
			if errors.Is(serr, vault.ErrVaultUnconfigured) {
				return nil, ErrVaultUnconfigured
			}
			return nil, fmt.Errorf("ai: seal api key: %w", serr)
		}
		sealed = newSealed
	}

	// 非 ollama provider 须有 key(既有或新轮换)。
	if provider != "" && provider != ProviderOllama && len(sealed) == 0 {
		return nil, ErrAPIKeyRequired
	}

	budgetJSON, err := json.Marshal(normalizeBudget(in.Budget))
	if err != nil {
		return nil, fmt.Errorf("ai: marshal budget: %w", err)
	}

	enabled := 0
	if in.Enabled {
		enabled = 1
	}

	nowStr := time.Now().UTC().Format(time.RFC3339)
	// 单例 upsert:首存 INSERT(id=1),已存 ON CONFLICT 更新各列(created_at 保留)。
	_, err = s.db.ExecContext(ctx,
		`INSERT INTO ai_config
		   (id, provider, base_url, model, api_key_ciphertext, budget_json, enabled, created_at, updated_at)
		 VALUES (1, ?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(id) DO UPDATE SET
		   provider = excluded.provider,
		   base_url = excluded.base_url,
		   model = excluded.model,
		   api_key_ciphertext = excluded.api_key_ciphertext,
		   budget_json = excluded.budget_json,
		   enabled = excluded.enabled,
		   updated_at = excluded.updated_at`,
		provider, baseURL, strings.TrimSpace(in.Model), sealed, string(budgetJSON), enabled, nowStr, nowStr,
	)
	if err != nil {
		return nil, fmt.Errorf("ai: upsert config: %w", err)
	}
	return s.Get(ctx)
}

func (s *service) Test(ctx context.Context, in TestInput) (*TestResult, error) {
	// 读既有行作为 draft 字段的回退值(apiKey 回退到已存密钥)。
	_, existingSealed, err := s.load(ctx)
	if err != nil {
		return nil, err
	}
	var existing Config
	if c, _, lerr := s.load(ctx); lerr == nil {
		existing = *c
	}

	provider := existing.Provider
	if in.Provider != nil {
		p, perr := normalizeProvider(*in.Provider)
		if perr != nil {
			return nil, perr
		}
		provider = p
	}
	if provider == "" {
		return nil, ErrInvalidProvider
	}

	baseURL := existing.BaseURL
	if in.BaseURL != nil {
		baseURL = strings.TrimSpace(*in.BaseURL)
	}
	if baseURL == "" {
		baseURL = defaultBaseURL(provider)
	}
	if baseURL == "" {
		return nil, ErrBaseURLRequired
	}

	// apiKey:draft 非空 → 用 draft;否则解密已存密钥(ollama 不需要)。
	apiKey := ""
	if in.APIKey != nil && *in.APIKey != "" {
		apiKey = *in.APIKey
	} else if provider != ProviderOllama && len(existingSealed) > 0 {
		plain, oerr := s.openSecret(existingSealed)
		if oerr != nil {
			return nil, oerr
		}
		apiKey = plain
	}
	if provider != ProviderOllama && apiKey == "" {
		return nil, ErrAPIKeyRequired
	}

	res := s.probe(ctx, provider, baseURL, apiKey)
	// 明文 key 用完即弃(尽力清理本地副本)。
	apiKey = ""
	return res, nil
}

// probe 向 provider 发轻量探测;2xx → ok+延迟;否则 ok=false + 人读错误(绝无密钥)。
func (s *service) probe(ctx context.Context, provider, baseURL, apiKey string) *TestResult {
	// SSRF 收口:baseUrl 用户可控,探测向其发请求——拒云元数据/链路本地/未指定;
	// 回环仅 ollama(本地默认 localhost:11434)放行,其余 provider 拒回环;私网放行(自托管,
	// 与仓库既定策略一致)。否则管理员可借 test 探测做内网/元数据端口探活(盲 SSRF)。
	if !validProbeURL(provider, baseURL) {
		return &TestResult{OK: false, Error: "baseUrl 不被允许:仅 http/https,且不可指向云元数据/链路本地地址"}
	}

	reqURL, header := probeRequest(provider, baseURL, apiKey)

	start := time.Now()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		// URL 构造失败(如 baseUrl 非法):人读错误,绝不含 key。
		return &TestResult{OK: false, Error: "请求构造失败:baseUrl 可能非法"}
	}
	for k, v := range header {
		req.Header.Set(k, v)
	}

	resp, err := s.client.Do(req)
	latency := time.Since(start).Milliseconds()
	if err != nil {
		return &TestResult{OK: false, LatencyMs: latency, Error: mapTransportError(err)}
	}
	defer func() { _ = resp.Body.Close() }()
	// 读弃少量响应体以复用连接;不解析(避免泄漏/重对象)。
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return &TestResult{
			OK:        true,
			LatencyMs: latency,
			Detail:    fmt.Sprintf("连接正常(HTTP %d)", resp.StatusCode),
		}
	}
	return &TestResult{
		OK:        false,
		LatencyMs: latency,
		Error:     mapStatusError(resp.StatusCode),
	}
}

// probeRequest 按 provider 构造探测请求 URL + header(绝不日志 key)。
//   - claude:GET {baseUrl}/v1/models;header x-api-key + anthropic-version: 2023-06-01
//   - openai:GET {baseUrl}/v1/models;header Authorization: Bearer <key>
//   - ollama:GET {baseUrl}/api/tags;无 key
func probeRequest(provider, baseURL, apiKey string) (string, map[string]string) {
	base := strings.TrimRight(baseURL, "/")
	header := map[string]string{}
	switch provider {
	case ProviderClaude:
		header["x-api-key"] = apiKey
		header["anthropic-version"] = "2023-06-01"
		return base + "/v1/models", header
	case ProviderOpenAI:
		header["Authorization"] = "Bearer " + apiKey
		return base + "/v1/models", header
	case ProviderOllama:
		return base + "/api/tags", header
	default:
		return base, header
	}
}

// mapStatusError 把非 2xx 状态映射为人读错误(绝无密钥)。
func mapStatusError(status int) string {
	switch {
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		return fmt.Sprintf("认证失败(HTTP %d):请检查 API 密钥", status)
	default:
		return fmt.Sprintf("探测失败:HTTP %d", status)
	}
}

// mapTransportError 把传输层错误映射为人读错误(超时/连接失败;绝无密钥/URL 细节泄漏 key)。
func mapTransportError(err error) string {
	if errors.Is(err, context.DeadlineExceeded) {
		return "连接超时:provider 无响应"
	}
	msg := err.Error()
	if strings.Contains(msg, "context deadline exceeded") || strings.Contains(msg, "Client.Timeout") {
		return "连接超时:provider 无响应"
	}
	return "连接失败:无法连接到 provider"
}

// load 读取单例配置行 + 既有密文(密文绝不外泄,仅供保存/探测内部用)。
// 无行 → 返回惰性空默认 Config(configured/enabled false),nil 密文,无错误。
func (s *service) load(ctx context.Context) (*Config, []byte, error) {
	var (
		provider   string
		baseURL    string
		model      string
		sealed     []byte
		budgetJSON string
		enabledInt int
		createdStr string
		updatedStr string
	)
	err := s.db.QueryRowContext(ctx,
		`SELECT provider, base_url, model, api_key_ciphertext, budget_json, enabled, created_at, updated_at
		 FROM ai_config WHERE id = 1`,
	).Scan(&provider, &baseURL, &model, &sealed, &budgetJSON, &enabledInt, &createdStr, &updatedStr)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			// 惰性空默认:从未配置。
			return &Config{
				Configured:   false,
				Enabled:      false,
				Provider:     "",
				BaseURL:      "",
				Model:        "",
				APIKeyMasked: "",
				Budget:       Budget{MonthlyTokenLimit: nil},
				UpdatedAt:    nil,
			}, nil, nil
		}
		return nil, nil, fmt.Errorf("ai: load config: %w", err)
	}

	var budget Budget
	if strings.TrimSpace(budgetJSON) != "" {
		if jerr := json.Unmarshal([]byte(budgetJSON), &budget); jerr != nil {
			return nil, nil, fmt.Errorf("ai: parse budget: %w", jerr)
		}
	}

	cfg := &Config{
		Enabled:  enabledInt != 0,
		Provider: provider,
		BaseURL:  baseURL,
		Model:    model,
		Budget:   budget,
	}

	// 掩码:有密钥时解密即时算掩码、明文用完即弃;无密钥(ollama / 未设)为空。
	// master key 缺失/轮换致密钥不可解密时**优雅降级**(NFR-10):掩码退化为占位,
	// 其余字段(provider/baseUrl/model/budget/enabled)照常返回,**不让整个 GET 503**;
	// 需要明文的 Save/Test 仍在各自解密路径报 ErrVaultUnconfigured。
	if len(sealed) > 0 {
		masked, merr := s.maskSealed(sealed)
		if merr != nil {
			masked = "••••" // 占位:有密钥但当前不可读
		}
		cfg.APIKeyMasked = masked
	}

	cfg.Configured = isConfigured(provider, len(sealed) > 0)

	if updated, perr := time.Parse(time.RFC3339, updatedStr); perr == nil {
		cfg.UpdatedAt = &updated
	}

	return cfg, sealed, nil
}

// openSecret 解密既有密文返回明文(供 Test 探测取用,用完即弃)。
func (s *service) openSecret(sealed []byte) (string, error) {
	if s.vault == nil {
		return "", ErrVaultUnconfigured
	}
	plain, err := s.vault.OpenSecret(sealed)
	if err != nil {
		if errors.Is(err, vault.ErrVaultUnconfigured) {
			return "", ErrVaultUnconfigured
		}
		// 解密失败(错误 master key / 篡改):按未配置态处理,不泄漏细节。
		return "", ErrVaultUnconfigured
	}
	return string(plain), nil
}

// maskSealed 解密密文并算掩码;明文用完即弃。vault 未配置 → ErrVaultUnconfigured。
func (s *service) maskSealed(sealed []byte) (string, error) {
	if s.vault == nil {
		return "", ErrVaultUnconfigured
	}
	plain, err := s.vault.OpenSecret(sealed)
	if err != nil {
		if errors.Is(err, vault.ErrVaultUnconfigured) {
			return "", ErrVaultUnconfigured
		}
		return "", ErrVaultUnconfigured
	}
	masked := maskKey(string(plain))
	for i := range plain {
		plain[i] = 0 // 明文用完清零
	}
	return masked, nil
}

// maskKey 生成 apiKey 掩码:保留末 4 位,前置点掩码。如 "••••a91f"。
// 不足以还原密钥;短 key 全掩码。
func maskKey(key string) string {
	const dots = "••••"
	r := []rune(key)
	if len(r) <= 4 {
		return dots
	}
	return dots + string(r[len(r)-4:]) // 按 rune 取末 4,避免切坏多字节 UTF-8
}

// validProbeURL 对 test 探测 baseUrl 做 SSRF 收口:scheme 仅 http/https;**恒拒云元数据
// (169.254.169.254,链路本地)/链路本地/未指定地址**(最高价值 SSRF 目标)。
// 回环(本地 Ollama / 管理员本机)与私网(自托管内网 LLM)放行——与 analyze.go/prober「放行私网」
// 的既定自托管姿态一致(单管理员对 localhost/内网本就有访问,不构成提权)。
func validProbeURL(_ /*provider*/, baseURL string) bool {
	u, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil {
		return false
	}
	switch strings.ToLower(u.Scheme) {
	case "http", "https":
	default:
		return false
	}
	host := u.Hostname()
	if host == "" {
		return false
	}
	blocked := func(ip net.IP) bool {
		// 云元数据 169.254.169.254 / 链路本地 / 未指定:恒拒(回环、私网放行)。
		return ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified()
	}
	if ip := net.ParseIP(host); ip != nil {
		return !blocked(ip)
	}
	addrs, lerr := net.LookupIP(host)
	if lerr != nil || len(addrs) == 0 {
		return true // DNS 抖动不误拒;探测自身会自然失败
	}
	for _, ip := range addrs {
		if blocked(ip) {
			return false
		}
	}
	return true
}

// isConfigured 判定配置是否「已配置」:provider 非空 +(ollama 无需 key / 否则有 key)。
func isConfigured(provider string, hasKey bool) bool {
	if provider == "" {
		return false
	}
	if provider == ProviderOllama {
		return true
	}
	return hasKey
}

// normalizeProvider 校验并归一 provider;空串允许(表示清空配置)。
func normalizeProvider(p string) (string, error) {
	p = strings.TrimSpace(p)
	switch p {
	case "", ProviderClaude, ProviderOpenAI, ProviderOllama:
		return p, nil
	default:
		return "", ErrInvalidProvider
	}
}

// normalizeBudget 归一预算声明(负数视为 nil/不限)。
func normalizeBudget(b Budget) Budget {
	if b.MonthlyTokenLimit != nil && *b.MonthlyTokenLimit < 0 {
		return Budget{MonthlyTokenLimit: nil}
	}
	return b
}

// defaultBaseURL 返回 provider 的默认 baseUrl(后端兜底)。
func defaultBaseURL(provider string) string {
	switch provider {
	case ProviderClaude:
		return defaultBaseURLClaude
	case ProviderOpenAI:
		return defaultBaseURLOpenAI
	case ProviderOllama:
		return defaultBaseURLOllama
	default:
		return ""
	}
}
