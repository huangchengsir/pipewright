// Package oauth 是「多 provider OAuth 凭据接入」的领域层。
//
// 运营者一次性配置各 git 平台(gitee|github|gitlab|custom)的 OAuth 应用(client_id +
// client_secret〔加密〕+ base_url〔自建用〕),用户在页面点「连接」→ 跳 git 平台授权 →
// 平台回跳带 code → 后端用 client_id/client_secret 换 access_token → 拉登录名 → 把 token
// 经 vault.Create 存成 git_token 凭据。之后克隆/构建直接复用该凭据(BasicAuth token)。
//
// 安全铁律:client_secret 经 vault secretbox(master key)加密后仅以密文入库;access_token /
// client_secret 绝不出现在错误体/日志/响应。state CSRF 防伪造授权码注入。
package oauth

import (
	"errors"
	"net/url"
	"strings"
)

// provider 枚举(DB 存小写字串;路由 path 同名)。
const (
	// ProviderGitee 表示码云 gitee.com。
	ProviderGitee = "gitee"
	// ProviderGitHub 表示 github.com。
	ProviderGitHub = "github"
	// ProviderGitLab 表示 gitlab.com。
	ProviderGitLab = "gitlab"
	// ProviderCustom 表示自建实例(据 base_url 推导 GitLab/Gitea 风格端点)。
	ProviderCustom = "custom"
)

// 领域错误。错误体永不含 client_secret / access_token 明文。
var (
	// ErrInvalidProvider 表示 provider 枚举非法(非 gitee|github|gitlab|custom)。
	ErrInvalidProvider = errors.New("oauth: invalid provider")
	// ErrBaseURLRequired 表示 custom provider 缺 base_url(无从推导端点)。
	ErrBaseURLRequired = errors.New("oauth: base url required for custom provider")
)

// userInfo 是从 provider user API 解析出的用户信息(仅取登录名)。
type userInfo struct {
	Login string
}

// Provider 描述一个 OAuth provider 的端点与解析策略。
// 内置预设为不可变模板;custom 经 resolve(baseURL) 据基址推导端点。
type Provider struct {
	// Name 是 provider 枚举值(gitee|github|gitlab|custom)。
	Name string
	// AuthorizeURL 是授权页地址(用户浏览器跳转目标)。
	AuthorizeURL string
	// TokenURL 是 code→token 兑换地址(后端 server-to-server POST)。
	TokenURL string
	// UserURL 是取当前用户信息的 API 地址(带 access_token)。
	UserURL string
	// DefaultScopes 是申请的最小权限(含仓库读)。
	DefaultScopes []string
	// parseLogin 从 user API 的 JSON map 解析登录名(各平台字段不同)。
	parseLogin func(m map[string]any) string
}

// scope 把 DefaultScopes 以空格连接(OAuth2 标准 scope 串)。
func (p Provider) scope() string { return strings.Join(p.DefaultScopes, " ") }

// presets 是内置 provider 预设注册表(map 注册,设计可扩展)。
// custom 不在此(需 base_url 动态推导),经 resolveProvider 单独处理。
var presets = map[string]Provider{
	ProviderGitee: {
		Name:          ProviderGitee,
		AuthorizeURL:  "https://gitee.com/oauth/authorize",
		TokenURL:      "https://gitee.com/oauth/token",
		UserURL:       "https://gitee.com/api/v5/user",
		DefaultScopes: []string{"user_info", "projects"},
		parseLogin:    parseLoginField("login"),
	},
	ProviderGitHub: {
		Name:          ProviderGitHub,
		AuthorizeURL:  "https://github.com/login/oauth/authorize",
		TokenURL:      "https://github.com/login/oauth/access_token",
		UserURL:       "https://api.github.com/user",
		DefaultScopes: []string{"repo", "read:user"},
		parseLogin:    parseLoginField("login"),
	},
	ProviderGitLab: {
		Name:          ProviderGitLab,
		AuthorizeURL:  "https://gitlab.com/oauth/authorize",
		TokenURL:      "https://gitlab.com/oauth/token",
		UserURL:       "https://gitlab.com/api/v4/user",
		DefaultScopes: []string{"read_repository", "read_user"},
		parseLogin:    parseLoginField("username"),
	},
}

// parseLoginField 返回一个从 JSON map 取字符串字段(回退 "id" 数字)的解析器。
func parseLoginField(field string) func(map[string]any) string {
	return func(m map[string]any) string {
		if v, ok := m[field].(string); ok && v != "" {
			return v
		}
		// 回退:部分实例可能只回 id(数字)。
		if v, ok := m["username"].(string); ok && v != "" {
			return v
		}
		if v, ok := m["login"].(string); ok && v != "" {
			return v
		}
		return ""
	}
}

// normalizeProvider 校验并归一 provider 枚举(空串非法)。
func normalizeProvider(p string) (string, error) {
	switch strings.TrimSpace(p) {
	case ProviderGitee:
		return ProviderGitee, nil
	case ProviderGitHub:
		return ProviderGitHub, nil
	case ProviderGitLab:
		return ProviderGitLab, nil
	case ProviderCustom:
		return ProviderCustom, nil
	default:
		return "", ErrInvalidProvider
	}
}

// resolveProvider 据 provider 名 + 配置的 baseURL 返回可用的 Provider 端点模板。
//   - gitee/github/gitlab:返回内置预设(忽略 baseURL,除非将来允许覆盖)。
//   - custom:据 baseURL 推导 GitLab/Gitea 风格端点(authorize/token/user)。
//     custom 缺 baseURL → ErrBaseURLRequired。
func resolveProvider(name, baseURL string) (Provider, error) {
	n, err := normalizeProvider(name)
	if err != nil {
		return Provider{}, err
	}
	if n == ProviderCustom {
		return customProvider(baseURL)
	}
	return presets[n], nil
}

// customProvider 据自建实例 baseURL 推导 GitLab/Gitea 风格 OAuth 端点。
// 约定(GitLab 与 Gitea 端点路径一致):
//
//	authorize: {base}/oauth/authorize
//	token    : {base}/oauth/token
//	user     : {base}/api/v4/user   (GitLab;Gitea 为 /api/v1/user,登录名字段均含 username/login)
//
// 用户解析器同时认 username/login 字段,兼容 GitLab(username)与 Gitea(login)。
func customProvider(baseURL string) (Provider, error) {
	base := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if base == "" {
		return Provider{}, ErrBaseURLRequired
	}
	if _, err := url.Parse(base); err != nil {
		return Provider{}, ErrBaseURLRequired
	}
	return Provider{
		Name:          ProviderCustom,
		AuthorizeURL:  base + "/oauth/authorize",
		TokenURL:      base + "/oauth/token",
		UserURL:       base + "/api/v4/user",
		DefaultScopes: []string{"read_repository", "read_user"},
		parseLogin:    parseLoginField("username"),
	}, nil
}
