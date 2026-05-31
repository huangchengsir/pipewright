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
	}
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
	req, err = http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(buf))
	if err != nil {
		return fmt.Errorf("prstatus: new request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "token "+token)
	if t.Platform == GitHub {
		req.Header.Set("Accept", "application/vnd.github+json")
	}

	resp, err := r.client.Do(req)
	if err != nil {
		return fmt.Errorf("prstatus: post status: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("prstatus: %s status post got HTTP %d", t.Platform, resp.StatusCode)
	}
	return nil
}

// StateForRunStatus 把运行终态映射为提交状态(success→success,其余终态→failure)。
func StateForRunStatus(runStatus string) string {
	if runStatus == "success" {
		return StateSuccess
	}
	return StateFailure
}
