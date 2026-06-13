// Package prstatus 把流水线运行结果回写为代码平台的「提交状态/检查」(Epic 8 · Story 8-9 / FR-8-9)。
//
// GitHub/Gitee 的 PR 检查本质是「给某个 commit SHA 打一条状态」(success/failure/pending),
// 平台在 PR 页把该 commit 的状态聚合展示。本包据项目仓库地址识别平台,经项目凭据(PAT/OAuth token)
// 调对应 commit-status API 回写本次运行结果,附跳回运行详情的 target_url。
//
// 安全:token 仅作鉴权头/参数,绝不进 URL/日志/错误;owner/repo/sha 经 url.PathEscape 进路径(防注入)。
// best-effort:失败仅由调用方记日志,绝不影响运行终态(NFR-10)。
package prstatus

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// userAgent 是回写请求的 User-Agent(GitHub API 要求带 UA;显式标识本服务)。
const userAgent = "Pipewright"

// 默认重试参数:提交状态回写是 best-effort,瞬时失败(网络抖动 / 5xx / 429 / 偶发 422)
// 重试几次显著提升落地率;非瞬时(4xx 鉴权/校验,如 401/404)不重试,快速失败。
const (
	defaultMaxAttempts = 3
	defaultBackoff     = 500 * time.Millisecond
)

// Platform 是受支持的代码平台。
type Platform string

const (
	GitHub Platform = "github"
	Gitee  Platform = "gitee"
)

// statusContext 是回写状态的「检查名」(平台 PR 页展示)。
const statusContext = "pipewright"

// Target 是解析出的回写目标(平台 + owner/repo)。
type Target struct {
	Platform Platform
	Owner    string
	Repo     string
}

// Detect 从仓库地址解析回写目标。不支持的 host / 无法解析 owner/repo → ok=false。
func Detect(repoURL string) (Target, bool) {
	u, err := url.Parse(strings.TrimSpace(repoURL))
	if err != nil {
		return Target{}, false
	}
	host := strings.ToLower(u.Hostname())
	var plat Platform
	switch {
	case host == "github.com" || strings.HasSuffix(host, ".github.com"):
		plat = GitHub
	case host == "gitee.com" || strings.HasSuffix(host, ".gitee.com"):
		plat = Gitee
	default:
		return Target{}, false
	}
	owner, repo, ok := ownerRepo(u.Path)
	if !ok {
		return Target{}, false
	}
	return Target{Platform: plat, Owner: owner, Repo: repo}, true
}

// ownerRepo 从 URL path 取 owner/repo(去 .git 后缀)。
func ownerRepo(p string) (string, string, bool) {
	p = strings.Trim(p, "/")
	parts := strings.Split(p, "/")
	if len(parts) < 2 || parts[0] == "" || parts[1] == "" {
		return "", "", false
	}
	repo := strings.TrimSuffix(parts[1], ".git")
	if repo == "" {
		return "", "", false
	}
	return parts[0], repo, true
}

// State 是回写的提交状态值(两平台共用枚举的交集)。
const (
	StateSuccess = "success"
	StateFailure = "failure"
	StatePending = "pending"
)

// Reporter 经 HTTP 回写提交状态。
type Reporter struct {
	client        *http.Client
	githubAPIBase string
	giteeAPIBase  string
	maxAttempts   int
	backoff       time.Duration
}

// NewReporter 构造 Reporter。client 为 nil 用默认;API base 缺省指向真实平台,测试可经 With* 覆盖。
func NewReporter(client *http.Client) *Reporter {
	if client == nil {
		client = http.DefaultClient
	}
	return &Reporter{
		client:        client,
		githubAPIBase: "https://api.github.com",
		giteeAPIBase:  "https://gitee.com/api/v5",
		maxAttempts:   defaultMaxAttempts,
		backoff:       defaultBackoff,
	}
}

// WithRetry 覆盖重试次数与退避间隔(attempts<1 归一为 1;测试常用 backoff=0 免等待)。
func (r *Reporter) WithRetry(attempts int, backoff time.Duration) *Reporter {
	if attempts < 1 {
		attempts = 1
	}
	r.maxAttempts = attempts
	r.backoff = backoff
	return r
}

// isTransient 判定 HTTP 状态码是否值得重试:429(限流)、5xx(服务端)、422(经验上 GitHub
// 偶发瞬时校验失败,见真机测试)。401/403/404 等非瞬时 → 不重试,快速失败。
func isTransient(code int) bool {
	return code == http.StatusTooManyRequests || code >= 500 || code == http.StatusUnprocessableEntity
}

// WithBaseURLs 覆盖平台 API base(测试注入 stub server)。
func (r *Reporter) WithBaseURLs(github, gitee string) *Reporter {
	if github != "" {
		r.githubAPIBase = strings.TrimRight(github, "/")
	}
	if gitee != "" {
		r.giteeAPIBase = strings.TrimRight(gitee, "/")
	}
	return r
}

// Report 给 target 的 commit sha 回写一条状态。state ∈ success|failure|pending。
// token 为平台访问令牌(绝不进 URL/日志)。非 2xx → 错误(含状态码,不含 token)。
func (r *Reporter) Report(ctx context.Context, t Target, sha, state, description, targetURL, token string) error {
	sha = strings.TrimSpace(sha)
	if sha == "" {
		return fmt.Errorf("prstatus: empty commit sha")
	}
	path := fmt.Sprintf("/repos/%s/%s/statuses/%s",
		url.PathEscape(t.Owner), url.PathEscape(t.Repo), url.PathEscape(sha))

	var (
		endpoint string
		body     map[string]any
		req      *http.Request
		err      error
	)
	switch t.Platform {
	case GitHub:
		endpoint = r.githubAPIBase + path
		body = map[string]any{"state": state, "context": statusContext, "description": description, "target_url": targetURL}
	case Gitee:
		endpoint = r.giteeAPIBase + path
		// Gitee v5 提交状态:access_token 作 body 参数 + Authorization 头双保险。
		body = map[string]any{"access_token": token, "state": state, "context": statusContext, "description": description, "target_url": targetURL}
	default:
		return fmt.Errorf("prstatus: unsupported platform %q", t.Platform)
	}

	buf, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("prstatus: marshal: %w", err)
	}

	// best-effort 重试:瞬时失败(网络/429/5xx/422)退避后重试,提升偶发抖动下的落地率;
	// 非瞬时失败(鉴权/校验)立即返回。每次都用新 reader 重建请求(body 可重读)。
	attempts := r.maxAttempts
	if attempts < 1 {
		attempts = 1
	}
	var lastErr error
	for attempt := 1; attempt <= attempts; attempt++ {
		req, err = http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(buf))
		if err != nil {
			return fmt.Errorf("prstatus: new request: %w", err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "token "+token)
		req.Header.Set("User-Agent", userAgent)
		if t.Platform == GitHub {
			req.Header.Set("Accept", "application/vnd.github+json")
		}

		resp, derr := r.client.Do(req)
		if derr != nil {
			lastErr = fmt.Errorf("prstatus: post status: %w", derr) // 网络错误:可重试
		} else {
			code := resp.StatusCode
			_ = resp.Body.Close()
			if code >= 200 && code < 300 {
				return nil
			}
			lastErr = fmt.Errorf("prstatus: %s status post got HTTP %d", t.Platform, code)
			if !isTransient(code) {
				return lastErr // 非瞬时:快速失败,不重试
			}
		}
		// 瞬时失败且仍有重试次数 → 退避后再试(响应 ctx 取消)。
		if attempt < attempts {
			if r.backoff > 0 {
				select {
				case <-time.After(r.backoff):
				case <-ctx.Done():
					return ctx.Err()
				}
			}
		}
	}
	return lastErr
}

// StateForRunStatus 把运行终态映射为提交状态(success→success,其余终态→failure)。
func StateForRunStatus(runStatus string) string {
	if runStatus == "success" {
		return StateSuccess
	}
	return StateFailure
}
