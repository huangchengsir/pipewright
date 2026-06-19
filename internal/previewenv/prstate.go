package previewenv

// prstate.go 实现「读某 PR 当前状态(open/closed/merged)」的能力,供自动回收 sweeper 据此判定
// 是否该回收预览环境(R4 E4.1 自动回收)。
//
// 安全铁律(confirmed-closed-only):
//   - 仅在**确证** PR 已 closed 或 merged 时才返回该终态;任何不确定(API 报错 / 缺凭据 / 未知 host /
//     无法解析 / 平台返回意外结构)一律返回 PRStateUnknown,**绝不**让 sweeper 回收。
//   - token 仅作鉴权头/参数,绝不进 URL / 日志 / 错误。
//
// 平台覆盖(诚实):复用 prstatus.Detect 的平台识别(GitHub / Gitee 公有云及企业版自托管 base)。
//   - GitHub:GET /repos/{owner}/{repo}/pulls/{n} → state(open|closed)+ merged(bool);merged 优先映射 merged。
//   - Gitee:GET /repos/{owner}/{repo}/pulls/{n} → state(open|closed|merged)。
//   - 其余 host / 解析失败 → PRStateUnknown(永不回收)。

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/huangchengsir/pipewright/internal/prstatus"
)

// PRState 是一个 PR 的判定状态(自动回收只在 closed / merged 时触发)。
type PRState string

const (
	// PRStateOpen 表示 PR 仍开放(不回收)。
	PRStateOpen PRState = "open"
	// PRStateClosed 表示 PR 已关闭未合并(回收)。
	PRStateClosed PRState = "closed"
	// PRStateMerged 表示 PR 已合并(回收)。
	PRStateMerged PRState = "merged"
	// PRStateUnknown 表示无法确证(API 错误 / 缺凭据 / 未知平台 / 结构异常)——**绝不回收**。
	PRStateUnknown PRState = "unknown"
)

// IsClosedOrMerged 报告该状态是否「确证已终结」(closed 或 merged),即可安全回收。
func (s PRState) IsClosedOrMerged() bool { return s == PRStateClosed || s == PRStateMerged }

// PRStateChecker 抽象「读某项目某 PR 的当前状态」的能力(sweeper 依赖;便于测试注入)。
type PRStateChecker interface {
	// State 返回某项目某 PR 的状态。任何不确定一律返回 (PRStateUnknown, nil)(sweeper 据此跳过,
	// 不回收);仅在确证 closed/merged 时返回对应终态。返回 error 时 sweeper 亦不回收。
	State(ctx context.Context, projectID string, prNumber int) (PRState, error)
}

// ProjectRepoResolver 把项目 id 解析为「仓库地址 + 平台访问令牌」(由上层用 project.Service + vault
// 适配注入,避免 previewenv import project/vault 形成环)。token 为空表示无可用凭据。
// 解析失败(项目不存在等)返回 error → checker 据此返回 unknown。
type ProjectRepoResolver interface {
	Resolve(ctx context.Context, projectID string) (repoURL, token string, err error)
}

// httpChecker 是基于 HTTP 的 PR 状态读取器(复用 prstatus.Detect 识别平台 + 同款 base/UA 约定)。
type httpChecker struct {
	resolver      ProjectRepoResolver
	client        *http.Client
	githubAPIBase string
	giteeAPIBase  string
}

// NewPRStateChecker 构造 HTTP PR 状态读取器。client 为 nil 用带超时的默认 client;
// API base 缺省指向公有云,测试 / 企业版自托管可经 WithBaseURLs 覆盖。
func NewPRStateChecker(resolver ProjectRepoResolver, client *http.Client) *httpChecker {
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	return &httpChecker{
		resolver:      resolver,
		client:        client,
		githubAPIBase: "https://api.github.com",
		giteeAPIBase:  "https://gitee.com/api/v5",
	}
}

// WithBaseURLs 覆盖平台 API base(测试注入 stub server;或企业版自托管)。
func (c *httpChecker) WithBaseURLs(github, gitee string) *httpChecker {
	if github != "" {
		c.githubAPIBase = strings.TrimRight(github, "/")
	}
	if gitee != "" {
		c.giteeAPIBase = strings.TrimRight(gitee, "/")
	}
	return c
}

// userAgent 是请求 UA(GitHub API 要求带 UA)。
const checkerUserAgent = "Pipewright"

// State 读某项目某 PR 的状态。安全铁律:任何不确定 → PRStateUnknown(不报错,sweeper 跳过)。
func (c *httpChecker) State(ctx context.Context, projectID string, prNumber int) (PRState, error) {
	if c.resolver == nil || prNumber < 1 {
		return PRStateUnknown, nil
	}
	repoURL, token, err := c.resolver.Resolve(ctx, projectID)
	if err != nil {
		// 解析失败(项目缺失等):无从确证 → unknown(不回收)。
		return PRStateUnknown, nil
	}
	if strings.TrimSpace(token) == "" {
		return PRStateUnknown, nil // 无凭据:无从确证 → 不回收。
	}
	target, ok := prstatus.Detect(repoURL)
	if !ok {
		return PRStateUnknown, nil // 未知 / 不支持平台 → 不回收。
	}

	path := fmt.Sprintf("/repos/%s/%s/pulls/%s",
		url.PathEscape(target.Owner), url.PathEscape(target.Repo), url.PathEscape(strconv.Itoa(prNumber)))

	switch target.Platform {
	case prstatus.GitHub:
		return c.fetchGitHub(ctx, c.githubAPIBase+path, token)
	case prstatus.Gitee:
		return c.fetchGitee(ctx, c.giteeAPIBase+path, token)
	default:
		return PRStateUnknown, nil
	}
}

// fetchGitHub 读 GitHub PR:state(open|closed)+ merged(bool)。merged=true → merged;
// 非 2xx / 解析失败 → unknown(不回收)。
func (c *httpChecker) fetchGitHub(ctx context.Context, endpoint, token string) (PRState, error) {
	body, ok := c.doGet(ctx, endpoint, token, true)
	if !ok {
		return PRStateUnknown, nil
	}
	var pr struct {
		State  string `json:"state"`
		Merged bool   `json:"merged"`
	}
	if err := json.Unmarshal(body, &pr); err != nil {
		return PRStateUnknown, nil
	}
	state := strings.ToLower(strings.TrimSpace(pr.State))
	switch {
	case pr.Merged:
		return PRStateMerged, nil
	case state == "closed":
		return PRStateClosed, nil
	case state == "open":
		return PRStateOpen, nil
	default:
		return PRStateUnknown, nil // 意外取值:保守不回收。
	}
}

// fetchGitee 读 Gitee PR:state(open|merged|closed)。非 2xx / 解析失败 → unknown(不回收)。
func (c *httpChecker) fetchGitee(ctx context.Context, endpoint, token string) (PRState, error) {
	// Gitee v5 既支持 access_token query 参数,也支持 Authorization 头;此处用头,绝不进 URL。
	body, ok := c.doGet(ctx, endpoint, token, false)
	if !ok {
		return PRStateUnknown, nil
	}
	var pr struct {
		State string `json:"state"`
	}
	if err := json.Unmarshal(body, &pr); err != nil {
		return PRStateUnknown, nil
	}
	switch strings.ToLower(strings.TrimSpace(pr.State)) {
	case "merged":
		return PRStateMerged, nil
	case "closed":
		return PRStateClosed, nil
	case "open":
		return PRStateOpen, nil
	default:
		return PRStateUnknown, nil
	}
}

// doGet 发一个鉴权 GET,2xx 返回响应体 + true;否则 (nil, false)。token 仅进头,绝不进 URL/日志。
func (c *httpChecker) doGet(ctx context.Context, endpoint, token string, github bool) ([]byte, bool) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, false
	}
	req.Header.Set("Authorization", "token "+token)
	req.Header.Set("User-Agent", checkerUserAgent)
	if github {
		req.Header.Set("Accept", "application/vnd.github+json")
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return nil, false // 网络错误:不确定 → 不回收。
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, false // 非 2xx(401/403/404/5xx):不确定 → 不回收。
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, false
	}
	return body, true
}
