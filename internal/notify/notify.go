// Package notify 是多渠道通知的领域层(FR-19 · Story 5.1)。
//
// 平台持有零或多条通知渠道(webhook / email / wecom / dingtalk / feishu);后续构建/部署
// 事件(5-2 路由)经这些渠道通知,模板(5-3)渲染出 Payload,覆盖(5-4)按 channelID 消费。
// 本期实现 **webhook(注入 *http.Client POST JSON)+ email(net/smtp 发信)**;
// 其余 type 保存合法但 Send 返回人读的 not_implemented 提示。
//
// AC-SEC:敏感字段(如 email 的 SMTP 密码)明文经 vault secretbox(master key)加密后仅以
// **密文 BLOB** 入库;DB dump、REST 响应、日志、错误体绝无明文(响应仅 hasPassword:bool)。
// webhook URL 经 SSRF 收口:拒云元数据(169.254 链路本地)/未指定地址,允许私网自托管。
//
// **冻结契约(供 5-2~5-4 消费,定死):**
//   - Notifier.Send(ctx, *Channel, Payload) error —— 单条渠道投递抽象。
//   - Payload{Title, Body, Fields} —— 5-3 模板渲染后的载荷形状。
//   - Service:CRUD + Test(ctx,id) + SendVia(ctx, channelID, payload) —— 5-2 按 channelID 调 SendVia。
//
// master key 缺失时 vault 进入「未配置」态:涉及敏感字段加解密的操作返回明确错误,
// 平台仍正常启动(不 panic)。未配置 / 未启用渠道 → SendVia 优雅跳过(不发、不报错)。
package notify

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/huangjiawei/devopstool/internal/vault"
)

// 渠道类型枚举(DB 存小写字串;JSON 同名)。本期实现 webhook + email,其余占位。
const (
	// TypeWebhook 自定义 webhook:POST JSON 到用户配置的 URL。
	TypeWebhook = "webhook"
	// TypeEmail 邮件:经 SMTP 发信(密码 write-only,vault 加密)。
	TypeEmail = "email"
	// TypeWecom 企业微信(本期占位:保存合法,Send 返回 not_implemented)。
	TypeWecom = "wecom"
	// TypeDingtalk 钉钉(本期占位)。
	TypeDingtalk = "dingtalk"
	// TypeFeishu 飞书(本期占位)。
	TypeFeishu = "feishu"
)

// 领域错误。错误体永不含敏感字段明文(SMTP 密码 / 密文 / master key)。
var (
	// ErrNotFound 表示渠道不存在。
	ErrNotFound = errors.New("notify: channel not found")
	// ErrInvalidType 表示类型枚举非法。
	ErrInvalidType = errors.New("notify: invalid channel type")
	// ErrEmptyName 表示名称为空。
	ErrEmptyName = errors.New("notify: channel name must not be empty")
	// ErrInvalidConfig 表示 per-type 配置缺失/非法(如 webhook 缺 url、email 缺 smtpHost)。
	ErrInvalidConfig = errors.New("notify: invalid channel config")
	// ErrSSRFBlocked 表示 webhook URL 命中 SSRF 收口(云元数据/链路本地)。
	ErrSSRFBlocked = errors.New("notify: webhook url not allowed")
	// ErrVaultUnconfigured 表示保险库未配置 master key,无法加密/读取敏感字段。
	ErrVaultUnconfigured = errors.New("notify: vault unconfigured")
)

// validTypes 是冻结的 type 枚举集合(保存合法性校验)。
func validType(t string) bool {
	switch t {
	case TypeWebhook, TypeEmail, TypeWecom, TypeDingtalk, TypeFeishu:
		return true
	default:
		return false
	}
}

// Payload 是通知载荷(冻结契约;5-3 模板渲染后传入)。
//   - Title:标题/主题(email 用作 Subject;webhook 作 JSON title 字段)。
//   - Body:正文(纯文本)。
//   - Fields:结构化键值(如 project / status / branch),webhook 透出、email 附于正文。
type Payload struct {
	Title  string
	Body   string
	Fields map[string]string
}

// Channel 是对外可见的渠道视图:含非敏感 per-type 配置 + HasSecret 布尔,绝无敏感字段明文/密文。
type Channel struct {
	ID        string
	Name      string
	Type      string
	Enabled   bool
	Config    Config
	HasSecret bool
	CreatedAt time.Time
	UpdatedAt time.Time
}

// Config 是 per-type 非敏感配置的并集(按 type 取用对应字段;敏感字段绝不在此)。
//   - webhook:Url。
//   - email:SMTPHost, SMTPPort, From, To, Username(密码经 vault,不在此)。
type Config struct {
	// webhook
	URL string `json:"url,omitempty"`
	// email
	SMTPHost string `json:"smtpHost,omitempty"`
	SMTPPort int    `json:"smtpPort,omitempty"`
	From     string `json:"from,omitempty"`
	To       string `json:"to,omitempty"`
	Username string `json:"username,omitempty"`
}

// CreateInput 是创建渠道的入参(Secret 为敏感字段明文,不被持久化为明文)。
type CreateInput struct {
	Name    string
	Type    string
	Enabled bool
	Config  Config
	// Secret 为敏感字段明文(本期仅 email 的 SMTP 密码);空表示无敏感字段。
	Secret string
}

// UpdateInput 是更新渠道的入参;指针字段为 nil 表示不修改。
// Secret 非 nil:空串=清除已存密钥,非空=轮换为新密钥。
type UpdateInput struct {
	Name    *string
	Enabled *bool
	Config  *Config
	Secret  *string
}

// TestResult 是测试发送结果(冻结契约形状)。Error 为人读错误(绝无敏感字段);成功为空。
type TestResult struct {
	OK        bool
	LatencyMs int64
	Detail    string
	Error     string
}

// Notifier 抽象单条渠道的投递(冻结契约,供 5-2 事件路由消费)。
type Notifier interface {
	// Send 经 ch 投递 payload;成功返回 nil,失败返回人读错误(绝无敏感字段明文)。
	Send(ctx context.Context, ch *Channel, payload Payload) error
}

// Service 定义通知渠道领域对外接口(冻结契约)。
type Service interface {
	// List 返回所有渠道的视图(无敏感字段明文/密文;HasSecret 标识是否已配密钥)。
	List(ctx context.Context) ([]Channel, error)
	// Get 返回单条渠道视图。不存在 → ErrNotFound。
	Get(ctx context.Context, id string) (*Channel, error)
	// Create 校验并持久化新渠道;Secret 非空经 vault 加密入库(密文)。返回视图(无明文)。
	Create(ctx context.Context, in CreateInput) (*Channel, error)
	// Update 改名/启停/改配置/轮换密钥;返回更新后视图。不存在 → ErrNotFound。
	Update(ctx context.Context, id string, in UpdateInput) (*Channel, error)
	// Delete 删除渠道。不存在 → ErrNotFound。
	Delete(ctx context.Context, id string) error
	// Test 向指定渠道发一条测试通知,回显 ok/延迟/人读错误(绝无敏感字段)。
	Test(ctx context.Context, id string) (*TestResult, error)
	// SendVia 按 channelID 投递 payload(供 5-2 事件路由消费)。
	// 渠道不存在 → ErrNotFound;**未启用 → 优雅跳过(返回 nil,不发不报错)**;
	// 投递失败 → 人读错误。
	SendVia(ctx context.Context, channelID string, payload Payload) error
}

// service 是 store + vault + 注入 http.Client + 注入 emailSender 支撑的 Service 实现。
type service struct {
	db       *sql.DB
	vault    vault.Vault
	client   *http.Client
	sendMail emailSender // 注入 SMTP 发信(默认 net/smtp;单测注入 stub)
}

// New 构造 Service。
//   - db:经参数化 SQL 触库。
//   - v:凭据保险库,复用其 secretbox(master key)加解密敏感字段;v 为 nil 或未配置
//     master key 时,涉及敏感字段的读取/加密返回 ErrVaultUnconfigured。
//   - client:webhook 投递用 HTTP 客户端(应带超时,如 ~8s);为 nil 时回退默认带超时客户端。
//     单测可注入指向本地 httptest server 的 client + 短超时。
//
// 不在此做任何重活(无 init() 副作用,避免抬高空载内存)。
func New(db *sql.DB, v vault.Vault, client *http.Client) Service {
	if client == nil {
		client = &http.Client{Timeout: 8 * time.Second}
	}
	return &service{db: db, vault: v, client: client, sendMail: smtpSend}
}

func (s *service) List(ctx context.Context) ([]Channel, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, name, type, enabled, config_json, secret_ciphertext, created_at, updated_at
		 FROM notification_channels ORDER BY created_at DESC, id`,
	)
	if err != nil {
		return nil, fmt.Errorf("notify: list channels: %w", err)
	}
	defer func() { _ = rows.Close() }()

	out := make([]Channel, 0)
	for rows.Next() {
		ch, _, err := scanChannel(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *ch)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("notify: iterate channels: %w", err)
	}
	return out, nil
}

func (s *service) Get(ctx context.Context, id string) (*Channel, error) {
	ch, _, err := s.load(ctx, id)
	if err != nil {
		return nil, err
	}
	return ch, nil
}

func (s *service) Create(ctx context.Context, in CreateInput) (*Channel, error) {
	if !validType(in.Type) {
		return nil, ErrInvalidType
	}
	name := strings.TrimSpace(in.Name)
	if name == "" {
		return nil, ErrEmptyName
	}
	cfg := normalizeConfig(in.Type, in.Config)
	if err := validateConfig(in.Type, cfg); err != nil {
		return nil, err
	}

	// 敏感字段:非空经 vault 加密入库(密文);空 → 无密钥(NULL)。
	var sealed []byte
	if in.Secret != "" {
		s2, err := s.seal(in.Secret)
		if err != nil {
			return nil, err
		}
		sealed = s2
	}

	cfgJSON, err := json.Marshal(cfg)
	if err != nil {
		return nil, fmt.Errorf("notify: marshal config: %w", err)
	}

	id := uuid.NewString()
	now := time.Now().UTC()
	nowStr := now.Format(time.RFC3339)
	enabled := boolToInt(in.Enabled)

	_, err = s.db.ExecContext(ctx,
		`INSERT INTO notification_channels (id, name, type, enabled, config_json, secret_ciphertext, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		id, name, in.Type, enabled, string(cfgJSON), sealed, nowStr, nowStr,
	)
	if err != nil {
		return nil, fmt.Errorf("notify: insert channel: %w", err)
	}

	return &Channel{
		ID:        id,
		Name:      name,
		Type:      in.Type,
		Enabled:   in.Enabled,
		Config:    cfg,
		HasSecret: len(sealed) > 0,
		CreatedAt: now,
		UpdatedAt: now,
	}, nil
}

func (s *service) Update(ctx context.Context, id string, in UpdateInput) (*Channel, error) {
	cur, curSealed, err := s.load(ctx, id)
	if err != nil {
		return nil, err
	}

	name := cur.Name
	if in.Name != nil {
		n := strings.TrimSpace(*in.Name)
		if n == "" {
			return nil, ErrEmptyName
		}
		name = n
	}
	enabled := cur.Enabled
	if in.Enabled != nil {
		enabled = *in.Enabled
	}
	cfg := cur.Config
	if in.Config != nil {
		cfg = normalizeConfig(cur.Type, *in.Config)
		if err := validateConfig(cur.Type, cfg); err != nil {
			return nil, err
		}
	}

	// 敏感字段保留语义:nil → 沿用既有密文;空串 → 清除;非空 → 轮换(加密)。
	sealed := curSealed
	if in.Secret != nil {
		if *in.Secret == "" {
			sealed = nil
		} else {
			s2, serr := s.seal(*in.Secret)
			if serr != nil {
				return nil, serr
			}
			sealed = s2
		}
	}

	cfgJSON, err := json.Marshal(cfg)
	if err != nil {
		return nil, fmt.Errorf("notify: marshal config: %w", err)
	}

	now := time.Now().UTC()
	nowStr := now.Format(time.RFC3339)
	_, err = s.db.ExecContext(ctx,
		`UPDATE notification_channels SET name = ?, enabled = ?, config_json = ?, secret_ciphertext = ?, updated_at = ?
		 WHERE id = ?`,
		name, boolToInt(enabled), string(cfgJSON), sealed, nowStr, id,
	)
	if err != nil {
		return nil, fmt.Errorf("notify: update channel: %w", err)
	}

	return &Channel{
		ID:        id,
		Name:      name,
		Type:      cur.Type,
		Enabled:   enabled,
		Config:    cfg,
		HasSecret: len(sealed) > 0,
		CreatedAt: cur.CreatedAt,
		UpdatedAt: now,
	}, nil
}

func (s *service) Delete(ctx context.Context, id string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM notification_channels WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("notify: delete channel: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *service) Test(ctx context.Context, id string) (*TestResult, error) {
	ch, sealed, err := s.load(ctx, id)
	if err != nil {
		return nil, err
	}
	payload := testPayload()
	start := time.Now()
	if derr := s.deliver(ctx, ch, sealed, payload); derr != nil {
		return &TestResult{
			OK:        false,
			LatencyMs: time.Since(start).Milliseconds(),
			Error:     derr.Error(), // deliver 已保证人读、绝无敏感字段
		}, nil
	}
	return &TestResult{
		OK:        true,
		LatencyMs: time.Since(start).Milliseconds(),
		Detail:    "测试通知已发送",
	}, nil
}

func (s *service) SendVia(ctx context.Context, channelID string, payload Payload) error {
	ch, sealed, err := s.load(ctx, channelID)
	if err != nil {
		return err
	}
	// 未启用渠道:优雅跳过(不发、不报错),供 5-2 路由批量调用时安全忽略停用渠道。
	if !ch.Enabled {
		return nil
	}
	return s.deliver(ctx, ch, sealed, payload)
}

// deliver 按 type 分派投递(webhook/email 实现;其余 not_implemented 人读)。
// 返回人读错误(绝无敏感字段明文)。需 sealed(敏感字段密文)时在各 notifier 内解密、用完即弃。
func (s *service) deliver(ctx context.Context, ch *Channel, sealed []byte, payload Payload) error {
	switch ch.Type {
	case TypeWebhook:
		return s.sendWebhook(ctx, ch, payload)
	case TypeEmail:
		return s.sendEmail(ctx, ch, sealed, payload)
	default:
		// wecom / dingtalk / feishu:本期占位,人读提示(绝不 panic)。
		return fmt.Errorf("该渠道类型(%s)暂未实现发送(not_implemented),敬请后续版本支持", ch.Type)
	}
}

// load 读取单条渠道行 + 既有密文(密文绝不外泄,仅供 deliver/test 内部解密用)。
func (s *service) load(ctx context.Context, id string) (*Channel, []byte, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, name, type, enabled, config_json, secret_ciphertext, created_at, updated_at
		 FROM notification_channels WHERE id = ?`, id,
	)
	ch, sealed, err := scanChannel(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil, ErrNotFound
		}
		return nil, nil, err
	}
	return ch, sealed, nil
}

// seal 用 vault 加密敏感字段明文,返回密文 BLOB。vault 未配置 → ErrVaultUnconfigured。
func (s *service) seal(plaintext string) ([]byte, error) {
	if s.vault == nil {
		return nil, ErrVaultUnconfigured
	}
	sealed, err := s.vault.SealSecret([]byte(plaintext))
	if err != nil {
		if errors.Is(err, vault.ErrVaultUnconfigured) {
			return nil, ErrVaultUnconfigured
		}
		return nil, fmt.Errorf("notify: seal secret: %w", err)
	}
	return sealed, nil
}

// openSecret 解密既有密文返回明文(供 email 发信取用,用完即弃)。
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

// scanChannel 把一行扫描为 Channel 视图 + 密文(密文仅供内部解密,不入视图)。
func scanChannel(sc scanner) (*Channel, []byte, error) {
	var (
		ch         Channel
		enabledInt int
		cfgJSON    string
		sealed     []byte
		createdStr string
		updatedStr string
	)
	if err := sc.Scan(&ch.ID, &ch.Name, &ch.Type, &enabledInt, &cfgJSON, &sealed, &createdStr, &updatedStr); err != nil {
		return nil, nil, err
	}
	ch.Enabled = enabledInt != 0
	ch.HasSecret = len(sealed) > 0
	if strings.TrimSpace(cfgJSON) != "" {
		if err := json.Unmarshal([]byte(cfgJSON), &ch.Config); err != nil {
			return nil, nil, fmt.Errorf("notify: parse config: %w", err)
		}
	}
	created, err := time.Parse(time.RFC3339, createdStr)
	if err != nil {
		return nil, nil, fmt.Errorf("notify: parse created_at: %w", err)
	}
	ch.CreatedAt = created
	updated, err := time.Parse(time.RFC3339, updatedStr)
	if err != nil {
		return nil, nil, fmt.Errorf("notify: parse updated_at: %w", err)
	}
	ch.UpdatedAt = updated
	return &ch, sealed, nil
}

// scanner 抽象 *sql.Row 与 *sql.Rows 的 Scan。
type scanner interface {
	Scan(dest ...any) error
}

// normalizeConfig 按 type 仅保留该 type 相关字段(剔除跨 type 噪声),并 trim。
func normalizeConfig(t string, c Config) Config {
	switch t {
	case TypeWebhook:
		return Config{URL: strings.TrimSpace(c.URL)}
	case TypeEmail:
		return Config{
			SMTPHost: strings.TrimSpace(c.SMTPHost),
			SMTPPort: c.SMTPPort,
			From:     strings.TrimSpace(c.From),
			To:       strings.TrimSpace(c.To),
			Username: strings.TrimSpace(c.Username),
		}
	default:
		return Config{}
	}
}

// validateConfig 校验 per-type 必填字段。webhook URL 的 SSRF 收口在 webhook.go 投递前再做一次。
func validateConfig(t string, c Config) error {
	switch t {
	case TypeWebhook:
		if c.URL == "" {
			return ErrInvalidConfig
		}
		if !validWebhookURL(c.URL) {
			return ErrSSRFBlocked
		}
		return nil
	case TypeEmail:
		if c.SMTPHost == "" || c.SMTPPort <= 0 || c.From == "" || c.To == "" {
			return ErrInvalidConfig
		}
		return nil
	default:
		// wecom/dingtalk/feishu:本期占位,不强制 config(保存合法)。
		return nil
	}
}

// testPayload 构造一条测试通知载荷。
func testPayload() Payload {
	return Payload{
		Title: "devopsTool 测试通知",
		Body:  "这是一条来自 devopsTool 的测试通知,用于验证渠道连通性。",
		Fields: map[string]string{
			"source": "devopsTool",
			"kind":   "test",
		},
	}
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
