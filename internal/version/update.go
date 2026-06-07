package version

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"html"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// defaultRepo 是检查更新所查询的 GitHub 仓库(owner/name)。可经 PIPEWRIGHT_RELEASE_REPO 覆盖
// (便于 fork 指向自己的发布渠道)。
const defaultRepo = "huangchengsir/pipewright"

// checkTTL 是更新检查结果的缓存时长:GitHub 未鉴权 API 限速 60 次/小时/IP,缓存避免反复点击打爆。
const checkTTL = 15 * time.Minute

// UpdateInfo 是 /api/version/check 的返回:当前版本、最新版本及是否有更新。
// CheckError 非空表示这次检查本身失败(网络/限流);此时 UpdateAvailable 恒 false,
// 前端据此提示「检查失败」而非误报「已是最新」。
type UpdateInfo struct {
	Current         string `json:"current"`
	Latest          string `json:"latest"`
	UpdateAvailable bool   `json:"updateAvailable"`
	ReleaseURL      string `json:"releaseUrl"`
	PublishedAt     string `json:"publishedAt"`
	Notes           string `json:"notes"`
	CheckError      string `json:"checkError,omitempty"`
}

// ghRelease 是 GitHub releases/latest 响应里我们关心的字段。
type ghRelease struct {
	TagName     string `json:"tag_name"`
	HTMLURL     string `json:"html_url"`
	PublishedAt string `json:"published_at"`
	Body        string `json:"body"`
	Prerelease  bool   `json:"prerelease"`
}

// Checker 查询 GitHub 最新发布并与当前构建版本比对,带 TTL 缓存。零值不可用,请用 NewChecker。
type Checker struct {
	repo   string
	client *http.Client

	mu       sync.Mutex
	cached   *UpdateInfo
	cachedAt time.Time
	now      func() time.Time // 可注入,便于测试缓存过期
}

// NewChecker 返回默认 Checker(查 defaultRepo / 可经 env 覆盖,10s 超时)。
func NewChecker() *Checker {
	repo := defaultRepo
	if v := strings.TrimSpace(os.Getenv("PIPEWRIGHT_RELEASE_REPO")); v != "" {
		repo = v
	}
	return &Checker{
		repo:   repo,
		client: &http.Client{Timeout: 10 * time.Second},
		now:    time.Now,
	}
}

// apiBase 允许测试把请求打到本地 stub server;生产为空时用 GitHub 公网 API。
var apiBase = ""

// htmlBase 允许测试把重定向兜底打到本地 stub server;生产为空时用 github.com 网页站。
var htmlBase = ""

// resolveLatestViaRedirect 不走 API,改读 github.com/<repo>/releases/latest 的 302 重定向
// (会跳到 /releases/tag/<tag>),从 Location 解析出最新 tag。用于 API 被限流(常见 403)或
// 不可达时兜底 —— 实测部分网络(如 CN)下 api.github.com 403 但 github.com 网页站可达。
// 局限:只拿得到 tag 与 release 页 URL,拿不到 notes / 发布时间。
func (c *Checker) resolveLatestViaRedirect(ctx context.Context) (tag, htmlURL string, err error) {
	base := htmlBase
	if base == "" {
		base = "https://github.com"
	}
	u := fmt.Sprintf("%s/%s/releases/latest", base, c.repo)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return "", "", err
	}
	req.Header.Set("User-Agent", "pipewright/"+Version)
	// 不跟随重定向:停在 302 读 Location 即可(避免下载整页 HTML)。
	noRedir := &http.Client{
		Timeout:       c.client.Timeout,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	resp, err := noRedir.Do(req)
	if err != nil {
		return "", "", err
	}
	defer func() { _ = resp.Body.Close() }()

	const marker = "/releases/tag/"
	loc := resp.Header.Get("Location")
	i := strings.LastIndex(loc, marker)
	if i < 0 {
		return "", "", fmt.Errorf("无重定向 tag(状态 %d)", resp.StatusCode)
	}
	tag = strings.Trim(loc[i+len(marker):], "/")
	if tag == "" {
		return "", "", fmt.Errorf("重定向 tag 为空")
	}
	htmlURL = loc
	if strings.HasPrefix(loc, "/") {
		htmlURL = base + loc
	}
	return tag, htmlURL, nil
}

// tryRedirectFallback 在 API 受限/不可达时改用重定向解析最新 tag。成功则填好 out(清空
// CheckError、按需置 UpdateAvailable)并返回 true;失败返回 false,由调用方保留原 CheckError。
func (c *Checker) tryRedirectFallback(ctx context.Context, out *UpdateInfo, cur string) bool {
	tag, htmlURL, err := c.resolveLatestViaRedirect(ctx)
	if err != nil || tag == "" {
		return false
	}
	out.Latest = tag
	out.ReleaseURL = htmlURL
	out.CheckError = ""
	// 走 atom feed(非限流 API)补 release 说明 —— API 限流时仍能给用户看更新内容。
	// 拿不到不影响主流程(说明留空,前端不显示「查看更新说明」)。
	out.Notes = c.fetchReleaseNotes(ctx, tag)
	if isComparable(cur) && CompareVersions(tag, cur) > 0 {
		out.UpdateAvailable = true
	}
	return true
}

// fetchReleaseNotes 从 github.com 的 releases.atom(RSS,不走会限流的 api.github.com)取指定
// tag 的 release 正文,去 HTML 标签转纯文本(前端 <pre> 直接展示)。失败返回空串。
func (c *Checker) fetchReleaseNotes(ctx context.Context, tag string) string {
	base := htmlBase
	if base == "" {
		base = "https://github.com"
	}
	u := fmt.Sprintf("%s/%s/releases.atom", base, c.repo)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return ""
	}
	req.Header.Set("User-Agent", "pipewright/"+Version)
	resp, err := c.client.Do(req)
	if err != nil {
		return ""
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return ""
	}
	var feed struct {
		Entries []struct {
			ID      string `xml:"id"`
			Content string `xml:"content"`
		} `xml:"entry"`
	}
	if err := xml.NewDecoder(resp.Body).Decode(&feed); err != nil {
		return ""
	}
	for _, e := range feed.Entries {
		// id 形如 tag:github.com,2008:Repository/<repoID>/<tag>
		if strings.HasSuffix(e.ID, "/"+tag) {
			return htmlToText(e.Content)
		}
	}
	return ""
}

var (
	reBlockEnd = regexp.MustCompile(`(?i)</(h[1-6]|p|div|li|ul|ol|tr|pre)>`)
	reListItem = regexp.MustCompile(`(?i)<li[^>]*>`)
	reHr       = regexp.MustCompile(`(?i)<hr\s*/?>`)
	reAnyTag   = regexp.MustCompile(`<[^>]+>`)
)

// htmlToText 把 release notes 的 HTML 粗略转纯文本:块级标签转换行、li 加项目符号、去其余标签、
// 解实体、压多余空行。用于 <pre> 展示(非渲染 HTML/Markdown),够读即可。
func htmlToText(h string) string {
	h = reListItem.ReplaceAllString(h, "• ")
	h = reHr.ReplaceAllString(h, "\n———\n")
	h = reBlockEnd.ReplaceAllString(h, "\n")
	h = reAnyTag.ReplaceAllString(h, "")
	h = html.UnescapeString(h)
	lines := strings.Split(h, "\n")
	out := make([]string, 0, len(lines))
	for _, ln := range lines {
		if ln = strings.TrimSpace(ln); ln != "" {
			out = append(out, ln)
		}
	}
	return strings.Join(out, "\n")
}

// Check 返回更新信息。命中未过期缓存则直接返回;否则查 GitHub。
// 网络/解析失败不返回 error —— 而是在 UpdateInfo.CheckError 里带上原因(始终附当前版本),
// 让上层端点稳定返回 200、前端优雅降级。
func (c *Checker) Check(ctx context.Context) UpdateInfo {
	c.mu.Lock()
	if c.cached != nil && c.now().Sub(c.cachedAt) < checkTTL {
		cached := *c.cached
		c.mu.Unlock()
		return cached
	}
	c.mu.Unlock()

	info := c.fetch(ctx)

	// 仅缓存成功结果:失败不缓存,以便用户重试时立刻再查。
	if info.CheckError == "" {
		c.mu.Lock()
		cp := info
		c.cached = &cp
		c.cachedAt = c.now()
		c.mu.Unlock()
	}
	return info
}

func (c *Checker) fetch(ctx context.Context) UpdateInfo {
	cur := Version
	out := UpdateInfo{Current: cur}

	base := apiBase
	if base == "" {
		base = "https://api.github.com"
	}
	url := fmt.Sprintf("%s/repos/%s/releases/latest", base, c.repo)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		out.CheckError = "构造请求失败"
		return out
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("User-Agent", "pipewright/"+cur) // GitHub 要求 UA,否则 403

	resp, err := c.client.Do(req)
	if err != nil {
		// API 不可达:尝试网页站重定向兜底(可能 API 域被挡而网页站可达)。
		if c.tryRedirectFallback(ctx, &out, cur) {
			return out
		}
		out.CheckError = "无法连接 GitHub(网络不可达?)"
		return out
	}
	defer func() { _ = resp.Body.Close() }()

	switch resp.StatusCode {
	case http.StatusOK:
		// 继续解析
	case http.StatusNotFound:
		// 仓库尚无任何 release:不是错误,只是「无可用更新」。
		out.CheckError = ""
		return out
	case http.StatusForbidden, http.StatusTooManyRequests:
		// API 限流(CN 网络常见):退回网页站重定向解析,绕开 API。
		if c.tryRedirectFallback(ctx, &out, cur) {
			return out
		}
		out.CheckError = "GitHub API 限流,请稍后再试"
		return out
	default:
		out.CheckError = fmt.Sprintf("GitHub 返回 %d", resp.StatusCode)
		return out
	}

	var rel ghRelease
	if err := json.NewDecoder(resp.Body).Decode(&rel); err != nil {
		out.CheckError = "解析 GitHub 响应失败"
		return out
	}

	out.Latest = rel.TagName
	out.ReleaseURL = rel.HTMLURL
	out.PublishedAt = rel.PublishedAt
	out.Notes = rel.Body
	// 仅当当前是正式语义版本(可比较)且 latest 更高时,才判定「有更新」。
	// 开发态(dev / 非 vX.Y.Z)无法可靠比较 → 不误报。
	if isComparable(cur) && rel.TagName != "" && CompareVersions(rel.TagName, cur) > 0 {
		out.UpdateAvailable = true
	}
	return out
}

// isComparable 判断版本串是否为可比较的语义版本(形如 v1.2.3 / 1.2.3,允许 -prerelease 后缀)。
// dev / none / 空 等开发态返回 false。
func isComparable(v string) bool {
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	if v == "" {
		return false
	}
	core := v
	if i := strings.IndexAny(v, "-+"); i >= 0 {
		core = v[:i]
	}
	parts := strings.Split(core, ".")
	if len(parts) == 0 {
		return false
	}
	for _, p := range parts {
		if _, err := strconv.Atoi(p); err != nil {
			return false
		}
	}
	return true
}

// CompareVersions 比较两个语义版本,返回 -1 / 0 / 1(a<b / a==b / a>b)。
// 规则:剥前导 v;按 . 拆 major/minor/patch 数值比较(缺位补 0);
// 主版本相等时,带 prerelease 的低于不带(1.0.0-rc.1 < 1.0.0);
// 两者都带 prerelease 时按点分段逐段比较(数值段按数值,否则按字典序)。
func CompareVersions(a, b string) int {
	ac, ap := splitVersion(a)
	bc, bp := splitVersion(b)

	for i := 0; i < 3; i++ {
		if ac[i] != bc[i] {
			if ac[i] < bc[i] {
				return -1
			}
			return 1
		}
	}
	// core 相等:比较 prerelease。无 prerelease 视为更高。
	switch {
	case ap == "" && bp == "":
		return 0
	case ap == "":
		return 1
	case bp == "":
		return -1
	default:
		return comparePrerelease(ap, bp)
	}
}

// splitVersion 把版本串解析为 [major,minor,patch] 与 prerelease 串(去掉 build 元数据 +...)。
func splitVersion(v string) ([3]int, string) {
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	if i := strings.IndexByte(v, '+'); i >= 0 {
		v = v[:i] // 丢弃 build 元数据
	}
	core := v
	pre := ""
	if i := strings.IndexByte(v, '-'); i >= 0 {
		core, pre = v[:i], v[i+1:]
	}
	var nums [3]int
	for i, p := range strings.Split(core, ".") {
		if i > 2 {
			break
		}
		nums[i], _ = strconv.Atoi(p)
	}
	return nums, pre
}

func comparePrerelease(a, b string) int {
	as := strings.Split(a, ".")
	bs := strings.Split(b, ".")
	n := len(as)
	if len(bs) < n {
		n = len(bs)
	}
	for i := 0; i < n; i++ {
		ai, aerr := strconv.Atoi(as[i])
		bi, berr := strconv.Atoi(bs[i])
		switch {
		case aerr == nil && berr == nil: // 都是数值段
			if ai != bi {
				if ai < bi {
					return -1
				}
				return 1
			}
		case aerr == nil: // 数值段低于字母段(SemVer 规则)
			return -1
		case berr == nil:
			return 1
		default:
			if as[i] != bs[i] {
				if as[i] < bs[i] {
					return -1
				}
				return 1
			}
		}
	}
	// 前缀相等:段更多的更高(1.0.0-rc.1 < 1.0.0-rc.1.1)。
	switch {
	case len(as) == len(bs):
		return 0
	case len(as) < len(bs):
		return -1
	default:
		return 1
	}
}
