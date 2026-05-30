package oauth

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/huangchengsir/pipewright/internal/vault"
)

// 领域错误(续 provider.go)。错误体永不含 client_secret / access_token 明文。
var (
	// ErrClientIDRequired 表示保存配置缺 client_id。
	ErrClientIDRequired = errors.New("oauth: client id required")
	// ErrClientSecretRequired 表示该 provider 既无既有 secret 又无新 secret。
	ErrClientSecretRequired = errors.New("oauth: client secret required")
	// ErrVaultUnconfigured 表示保险库未配置 master key,无法加密/读取 client_secret 或存 token。
	ErrVaultUnconfigured = errors.New("oauth: vault unconfigured")
	// ErrAppNotConfigured 表示该 provider 的 OAuth 应用未配置/未启用,无法发起授权。
	ErrAppNotConfigured = errors.New("oauth: provider app not configured or disabled")
	// ErrExchangeFailed 表示 code→token 兑换或取用户信息失败(人读,绝无 secret/token)。
	ErrExchangeFailed = errors.New("oauth: exchange failed")
)

// AppConfig 是对外可见的 OAuth 应用配置视图:client_secret 仅掩码 + configured 标志,绝无明文。
type AppConfig struct {
	Provider     string
	ClientID     string
	BaseURL      string
	Enabled      bool
	MaskedSecret string
	Configured   bool
	UpdatedAt    *time.Time
}

// SaveAppInput 是 upsert OAuth 应用配置的入参。
// ClientSecret 为**只写**指针:nil/空串 = 保留既有 secret(不清空);非空 = 轮换为新 secret。
type SaveAppInput struct {
	Provider     string
	ClientID     string
	BaseURL      string
	ClientSecret *string
	Enabled      bool
}

// TokenResult 是 Exchange 的产出:access_token + 解析出的登录名。
// access_token 仅供调用方立即经 vault.Create 存成凭据,绝不回显/日志。
type TokenResult struct {
	AccessToken string
	Login       string
}

// Service 定义 OAuth 领域对外接口。
type Service interface {
	// ListApps 返回所有已配置 provider 的应用视图(client_secret 仅掩码)。
	ListApps(ctx context.Context) ([]AppConfig, error)
	// SaveApp 校验并 upsert 单个 provider 的 OAuth 应用配置;
	// clientSecret 省略/空保留旧密文、非空轮换(vault.SealSecret 加密)。返回更新后视图(掩码)。
	SaveApp(ctx context.Context, in SaveAppInput) (*AppConfig, error)
	// AuthorizeURL 据已配置的 client_id + provider 端点 + scope + state + redirectURI 构造授权 URL。
	// 该 provider 未配置/未启用 → ErrAppNotConfigured。
	AuthorizeURL(ctx context.Context, provider, state, redirectURI string) (string, error)
	// Exchange 用 code 经 provider tokenURL 换 access_token(server-to-server,注入 http.Client),
	// 再用 token 拉 userURL 取登录名。client_secret 绝不出现在错误/日志/响应。
	Exchange(ctx context.Context, provider, code, redirectURI string) (*TokenResult, error)
	// IssueState 生成绑定 sessionID 的随机 state(CSRF;放进 authorize URL)。
	IssueState(sessionID string) (string, error)
	// ConsumeState 校验并一次性消费 callback 带回的 state(存在+未过期+绑定会话匹配)。
	ConsumeState(state, sessionID string) bool
}

// service 是 store + vault + 注入 http.Client + 内存 state 存储支撑的 Service 实现。
type service struct {
	db     *sql.DB
	vault  vault.Vault
	client *http.Client
	states *stateStore
}

// New 构造 Service。
//   - db:经参数化 SQL 触库。
//   - v:凭据保险库,复用其 secretbox(master key)加解密 client_secret;并经 v.Create 存 token。
//   - client:Exchange 兑换/取用户用的 HTTP 客户端(应带超时);为 nil 时回退默认带超时客户端。
//     单测可注入指向 httptest mock provider 的 client。
func New(db *sql.DB, v vault.Vault, client *http.Client) Service {
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	return &service{db: db, vault: v, client: client, states: newStateStore()}
}

func (s *service) IssueState(sessionID string) (string, error) { return s.states.issue(sessionID) }

func (s *service) ConsumeState(state, sessionID string) bool {
	return s.states.consume(state, sessionID)
}

func (s *service) ListApps(ctx context.Context) ([]AppConfig, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT provider, client_id, client_secret_ciphertext, base_url, enabled, updated_at
		 FROM oauth_apps ORDER BY provider`)
	if err != nil {
		return nil, fmt.Errorf("oauth: list apps: %w", err)
	}
	defer func() { _ = rows.Close() }()

	apps := make([]AppConfig, 0)
	for rows.Next() {
		var (
			provider   string
			clientID   string
			sealed     []byte
			baseURL    string
			enabledInt int
			updatedStr string
		)
		if err := rows.Scan(&provider, &clientID, &sealed, &baseURL, &enabledInt, &updatedStr); err != nil {
			return nil, fmt.Errorf("oauth: scan app: %w", err)
		}
		apps = append(apps, s.toAppConfig(provider, clientID, baseURL, enabledInt != 0, sealed, updatedStr))
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("oauth: iterate apps: %w", err)
	}
	return apps, nil
}

func (s *service) SaveApp(ctx context.Context, in SaveAppInput) (*AppConfig, error) {
	provider, err := normalizeProvider(in.Provider)
	if err != nil {
		return nil, err
	}
	clientID := strings.TrimSpace(in.ClientID)
	if clientID == "" {
		return nil, ErrClientIDRequired
	}
	baseURL := strings.TrimSpace(in.BaseURL)
	// 自建必填 baseUrl,且须能推导端点(否则 authorize/exchange 必失败)。
	if provider == ProviderCustom {
		if _, derr := customProvider(baseURL); derr != nil {
			return nil, derr
		}
	}

	// 读既有行以实现 client_secret 保留语义。
	_, _, existingSealed, _, _, _, err := s.loadApp(ctx, provider)
	if err != nil {
		return nil, err
	}

	// client_secret 保留语义:nil/空 → 沿用既有密文;非空 → 轮换(SealSecret 加密)。
	sealed := existingSealed
	if in.ClientSecret != nil && *in.ClientSecret != "" {
		if s.vault == nil {
			return nil, ErrVaultUnconfigured
		}
		newSealed, serr := s.vault.SealSecret([]byte(*in.ClientSecret))
		if serr != nil {
			if errors.Is(serr, vault.ErrVaultUnconfigured) {
				return nil, ErrVaultUnconfigured
			}
			return nil, fmt.Errorf("oauth: seal client secret: %w", serr)
		}
		sealed = newSealed
	}
	if len(sealed) == 0 {
		return nil, ErrClientSecretRequired
	}

	enabled := 0
	if in.Enabled {
		enabled = 1
	}
	now := time.Now().UTC().Format(time.RFC3339)
	_, err = s.db.ExecContext(ctx,
		`INSERT INTO oauth_apps
		   (provider, client_id, client_secret_ciphertext, base_url, enabled, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(provider) DO UPDATE SET
		   client_id = excluded.client_id,
		   client_secret_ciphertext = excluded.client_secret_ciphertext,
		   base_url = excluded.base_url,
		   enabled = excluded.enabled,
		   updated_at = excluded.updated_at`,
		provider, clientID, sealed, baseURL, enabled, now, now,
	)
	if err != nil {
		return nil, fmt.Errorf("oauth: upsert app: %w", err)
	}

	cfg := s.toAppConfig(provider, clientID, baseURL, in.Enabled, sealed, now)
	return &cfg, nil
}

func (s *service) AuthorizeURL(ctx context.Context, provider, state, redirectURI string) (string, error) {
	prov, clientID, sealed, baseURL, enabled, _, err := s.loadApp(ctx, provider)
	if err != nil {
		return "", err
	}
	// 未配置(无 client_id/secret)或未启用 → 不可发起授权。
	if clientID == "" || len(sealed) == 0 || !enabled {
		return "", ErrAppNotConfigured
	}
	endpoints, err := resolveProvider(prov, baseURL)
	if err != nil {
		return "", err
	}

	u, err := url.Parse(endpoints.AuthorizeURL)
	if err != nil {
		return "", ErrAppNotConfigured
	}
	q := u.Query()
	q.Set("client_id", clientID)
	q.Set("redirect_uri", redirectURI)
	q.Set("response_type", "code")
	q.Set("scope", endpoints.scope())
	q.Set("state", state)
	u.RawQuery = q.Encode()
	return u.String(), nil
}

func (s *service) Exchange(ctx context.Context, provider, code, redirectURI string) (*TokenResult, error) {
	prov, clientID, sealed, baseURL, _, _, err := s.loadApp(ctx, provider)
	if err != nil {
		return nil, err
	}
	if clientID == "" || len(sealed) == 0 {
		return nil, ErrAppNotConfigured
	}
	if strings.TrimSpace(code) == "" {
		return nil, ErrExchangeFailed
	}
	endpoints, err := resolveProvider(prov, baseURL)
	if err != nil {
		return nil, err
	}

	// 解密 client_secret(进程内即用即弃,绝不日志/回显)。
	secret, err := s.openSecret(sealed)
	if err != nil {
		return nil, err
	}

	accessToken, err := s.requestToken(ctx, endpoints, clientID, secret, code, redirectURI)
	// 明文 secret 用完即弃。
	secret = ""
	if err != nil {
		return nil, err
	}

	login, err := s.requestUser(ctx, endpoints, accessToken)
	if err != nil {
		return nil, err
	}
	return &TokenResult{AccessToken: accessToken, Login: login}, nil
}

// requestToken POST code→token 到 provider tokenURL,取回 access_token(绝不日志 secret/token)。
func (s *service) requestToken(ctx context.Context, p Provider, clientID, clientSecret, code, redirectURI string) (string, error) {
	form := url.Values{}
	form.Set("client_id", clientID)
	form.Set("client_secret", clientSecret)
	form.Set("code", code)
	form.Set("grant_type", "authorization_code")
	form.Set("redirect_uri", redirectURI)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.TokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", ErrExchangeFailed
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	// GitHub 默认回表单编码,显式要 JSON;其余平台本就回 JSON,加这头无害。
	req.Header.Set("Accept", "application/json")

	resp, err := s.client.Do(req)
	if err != nil {
		return "", ErrExchangeFailed
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", ErrExchangeFailed
	}

	var parsed struct {
		AccessToken string `json:"access_token"`
		Error       string `json:"error"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return "", ErrExchangeFailed
	}
	if parsed.AccessToken == "" {
		// 兑换失败(如 code 失效):人读错误,绝不回 provider 原文(可能含敏感细节)。
		return "", ErrExchangeFailed
	}
	return parsed.AccessToken, nil
}

// requestUser 用 access_token 拉 userURL 取登录名(绝不日志 token)。
func (s *service) requestUser(ctx context.Context, p Provider, accessToken string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.UserURL, nil)
	if err != nil {
		return "", ErrExchangeFailed
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Accept", "application/json")

	resp, err := s.client.Do(req)
	if err != nil {
		return "", ErrExchangeFailed
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", ErrExchangeFailed
	}

	var m map[string]any
	if err := json.Unmarshal(body, &m); err != nil {
		return "", ErrExchangeFailed
	}
	login := p.parseLogin(m)
	if login == "" {
		return "", ErrExchangeFailed
	}
	return login, nil
}

// loadApp 读单个 provider 的应用行;返回 (provider, clientID, sealedSecret, baseURL, enabled, updatedAt, err)。
// 无行 → 返回空默认(clientID="" / sealed=nil / enabled=false),无错误。
func (s *service) loadApp(ctx context.Context, provider string) (string, string, []byte, string, bool, string, error) {
	prov, err := normalizeProvider(provider)
	if err != nil {
		return "", "", nil, "", false, "", err
	}
	var (
		clientID   string
		sealed     []byte
		baseURL    string
		enabledInt int
		updatedStr string
	)
	err = s.db.QueryRowContext(ctx,
		`SELECT client_id, client_secret_ciphertext, base_url, enabled, updated_at
		 FROM oauth_apps WHERE provider = ?`, prov,
	).Scan(&clientID, &sealed, &baseURL, &enabledInt, &updatedStr)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return prov, "", nil, "", false, "", nil
		}
		return "", "", nil, "", false, "", fmt.Errorf("oauth: load app: %w", err)
	}
	return prov, clientID, sealed, baseURL, enabledInt != 0, updatedStr, nil
}

// openSecret 解密既有 client_secret 密文返回明文(供 Exchange 取用,用完即弃)。
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

// toAppConfig 组装对外视图:client_secret 仅掩码,绝无明文。
// 掩码不解密(无需明文):有密文即回固定掩码 ••••••,configured = 有 clientID + 有密文。
func (s *service) toAppConfig(provider, clientID, baseURL string, enabled bool, sealed []byte, updatedStr string) AppConfig {
	cfg := AppConfig{
		Provider:   provider,
		ClientID:   clientID,
		BaseURL:    baseURL,
		Enabled:    enabled,
		Configured: clientID != "" && len(sealed) > 0,
	}
	if len(sealed) > 0 {
		cfg.MaskedSecret = "••••••"
	}
	if t, perr := time.Parse(time.RFC3339, updatedStr); perr == nil {
		cfg.UpdatedAt = &t
	}
	return cfg
}
