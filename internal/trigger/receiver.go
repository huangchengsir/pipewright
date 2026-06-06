package trigger

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/huangchengsir/pipewright/internal/store"
	"github.com/huangchengsir/pipewright/internal/vault"
)

// replayWindow 是 webhook 时间戳新鲜度窗口:|now - ts| 超过此值视为重放,拒绝(401)。
const replayWindow = 5 * time.Minute

// nowFunc 供测试注入当前时间(默认 time.Now)。
var nowFunc = time.Now

// Gitee webhook 头(冻结契约)。
const (
	HeaderGiteeEvent     = "X-Gitee-Event"
	HeaderGiteeToken     = "X-Gitee-Token"
	HeaderGiteeTimestamp = "X-Gitee-Timestamp"
	HeaderGiteeDelivery  = "X-Gitee-Delivery"
)

// Gitee 事件名(X-Gitee-Event 取值)。
const (
	eventPush         = "Push Hook"
	eventTag          = "Tag Push Hook"
	eventMergeRequest = "Merge Request Hook"
	// eventRelease 是 Gitee「发行版」事件(Story 8-11 / FR-8-11)。
	eventRelease = "Release Hook"
)

// 投递处理结果(accepted / 各 ignored 原因枚举,冻结契约)。
const (
	OutcomeAccepted           = "accepted"
	IgnoredEventNotSubscribed = "event_not_subscribed"
	IgnoredNoBranchMatch      = "no_branch_match"
	IgnoredDuplicate          = "duplicate"
	IgnoredUnmatchedIgnored   = "unmatched_ignored"
	// IgnoredPathNoMatch:配了路径过滤,但本次 push 改动文件不匹配任一 glob(monorepo · P0)。
	IgnoredPathNoMatch = "path_no_match"
	// IgnoredUnmatchedRecorded:未匹配但策略=record——本期记录一次「已记录未部署」,不创建可执行运行。
	IgnoredUnmatchedRecorded = "unmatched_recorded"
)

// 接收层错误。错误体永不含密钥明文/密文/master key。
var (
	// ErrTokenNotFound 表示路径 token 未定位到任何项目触发配置。
	ErrTokenNotFound = errors.New("trigger: webhook token not found")
	// ErrUnauthorized 表示签名/密钥校验失败(→ 401,不创建)。
	ErrUnauthorized = errors.New("trigger: webhook signature invalid")
)

// RunCreator 抽象「创建一次运行」的能力,由 run.Service 实现(在 httpapi/main 装配时适配)。
// 解析出的环境/目标服务器作为运行内部元数据传入(供 Epic 4),不进入冻结 run-detail DTO。
type RunCreator interface {
	CreateWebhookRun(ctx context.Context, projectID string, in RunRequest) (runID string, err error)
}

// RunRequest 是创建一次运行的入参(去耦 run 包类型,避免在领域层互相引用)。
type RunRequest struct {
	Branch                  string
	Commit                  string
	Actor                   string
	ResolvedEnvironment     string
	ResolvedTargetServerIDs []string
}

// Result 是一次 webhook 投递的处理结果(供 HTTP 层构造响应体)。
type Result struct {
	Accepted bool
	RunID    string
	Ignored  string // accepted 时为空;否则为 ignored 原因枚举
}

// Delivery 是从 HTTP 请求解析出的 webhook 投递(头 + 原始 body)。
// RawBody 是验签所需的原始字节(签名模式按 Gitee 文档对 timestamp+"\n"+secret 计算,
// 不依赖 body;但 body 仍用于解析事件)。
type Delivery struct {
	Token      string
	Event      string
	TokenHdr   string // X-Gitee-Token 头(密码模式=明文密钥;签名模式=base64 HMAC)
	Timestamp  string
	DeliveryID string
	RawBody    []byte
}

// Receiver 处理 Gitee webhook 投递:定位项目 → 解密密钥验签 → 解析/匹配 → 去重 → 创建运行。
// 它复用 trigger 包内部的密钥解密(经 vault.OpenSecret),不经任何对外暴露明文的接口。
type Receiver struct {
	db     *sql.DB
	vault  vault.Vault
	runner RunCreator
}

// NewReceiver 构造 webhook 接收器。
//   - db:经参数化 SQL 触库(读触发配置 + 去重表)。
//   - v:复用 vault secretbox 解密 webhook 签名密钥;未配置 → 验签不可用(明确拒绝,不 panic)。
//   - runner:命中后创建运行(run.Service 适配)。
//
// 不在此做任何重活(无 init() 副作用)。
func NewReceiver(db *sql.DB, v vault.Vault, runner RunCreator) *Receiver {
	return &Receiver{db: db, vault: v, runner: runner}
}

// Handle 处理一次投递,返回处理结果或错误。
//   - token 未定位到项目 → ErrTokenNotFound(HTTP 404,不泄漏存在性细节由上层决定)。
//   - 验签失败 / master key 未配置 → ErrUnauthorized(HTTP 401,不创建)。
//   - 事件未勾选 / 分支不匹配 / 重复投递 / 未匹配策略=ignore → Result{Accepted:false, Ignored:...}。
//   - 命中 → 创建运行,Result{Accepted:true, RunID:...}。
func (rc *Receiver) Handle(ctx context.Context, d Delivery) (*Result, error) {
	cfg, secret, err := rc.loadForVerify(ctx, d.Token)
	if err != nil {
		return nil, err
	}
	// 解密后的明文密钥用完即清零(防驻留)。
	defer zero(secret)

	mode, ok := verifySignature(d, secret)
	if !ok {
		return nil, ErrUnauthorized
	}
	// 防重放时间窗:签名模式必须带新鲜 timestamp(缺失/超窗 → 401);
	// 密码模式若带 timestamp 也校验(Gitee 密码模式通常不带,缺失则放行)。
	if !timestampFresh(d.Timestamp, mode) {
		return nil, ErrUnauthorized
	}

	// 解析事件 → 是否订阅。
	subscribed := eventSubscribed(d.Event, cfg.Events)
	parsed := parsePayload(d.RawBody)

	// 去重键:优先 X-Gitee-Delivery;缺失时回退 event+branch+commit 派生键。
	deliveryKey := strings.TrimSpace(d.DeliveryID)
	if deliveryKey == "" {
		deliveryKey = derivedKey(d.Event, parsed.Branch, parsed.Commit)
	}

	// 统一去重:所有路径(未订阅 / 未匹配 record|ignore / 命中)都先抢占同一 dedup claim,
	// 同一 delivery 幂等(UNIQUE 约束并发安全)。这避免「未匹配/record 投递改 delivery_id
	// 即无限灌库」——同 delivery 只入一行,重复投递一律 duplicate,不再无界增长。
	recID, dup, err := rc.claim(ctx, cfg.ProjectID, deliveryKey, d.Event, parsed.Branch, parsed.Commit)
	if err != nil {
		return nil, err
	}
	if dup {
		return &Result{Accepted: false, Ignored: IgnoredDuplicate}, nil
	}

	if !subscribed {
		rc.setOutcome(ctx, recID, IgnoredEventNotSubscribed)
		return &Result{Accepted: false, Ignored: IgnoredEventNotSubscribed}, nil
	}

	// 分支匹配(通配 glob)。
	mapping, matched := matchBranch(parsed.Branch, cfg.BranchMappings)
	if !matched {
		reason := IgnoredNoBranchMatch
		if cfg.UnmatchedPolicy == PolicyRecord {
			reason = IgnoredUnmatchedRecorded
		} else {
			reason = IgnoredUnmatchedIgnored
		}
		rc.setOutcome(ctx, recID, reason)
		return &Result{Accepted: false, Ignored: reason}, nil
	}

	// 路径过滤(monorepo · P0):配了 glob 且**拿得到**改动文件时,文件不匹配任一 glob → 忽略。
	// 拿不到改动文件列表(非 push 事件 / 畸形 payload / 无 commits)→ 放行不拦(诚实降级,见 matchPathFilters)。
	if len(cfg.PathFilters) > 0 && len(parsed.ChangedFiles) > 0 && !matchPathFilters(cfg.PathFilters, parsed.ChangedFiles) {
		rc.setOutcome(ctx, recID, IgnoredPathNoMatch)
		return &Result{Accepted: false, Ignored: IgnoredPathNoMatch}, nil
	}

	runID, err := rc.runner.CreateWebhookRun(ctx, cfg.ProjectID, RunRequest{
		Branch:                  parsed.Branch,
		Commit:                  parsed.Commit,
		Actor:                   "gitee",
		ResolvedEnvironment:     mapping.Environment,
		ResolvedTargetServerIDs: mapping.TargetServerIDs,
	})
	if err != nil {
		// 创建失败:回滚去重占位,允许重试。回滚 DELETE 的错误不吞,合并进返回错误
		// (否则占位残留会把后续重试误判为 duplicate)。
		if _, derr := rc.db.ExecContext(ctx, `DELETE FROM webhook_deliveries WHERE id = ?`, recID); derr != nil {
			return nil, fmt.Errorf("trigger: create run failed (%v) and rollback claim failed: %w", err, derr)
		}
		return nil, err
	}

	// 回填运行 id 与结果。运行已真实创建(幂等占位已落库),回填仅是注解;
	// 回填失败不再 `_,_=` 静默丢弃 —— 做一次最佳努力重试,仍失败也不把已成功的创建翻成 500
	// (去重占位仍在,后续重投会被正确判为 duplicate,不会重复创建)。
	if !rc.backfillRun(ctx, recID, runID) {
		_ = rc.backfillRun(ctx, recID, runID) // 单次重试(瞬时错误兜底)
	}
	return &Result{Accepted: true, RunID: runID}, nil
}

// backfillRun 回填去重记录的 run_id 与 outcome=accepted;成功返回 true。
func (rc *Receiver) backfillRun(ctx context.Context, recID, runID string) bool {
	_, err := rc.db.ExecContext(ctx,
		`UPDATE webhook_deliveries SET run_id = ?, outcome = ? WHERE id = ?`,
		runID, OutcomeAccepted, recID,
	)
	return err == nil
}

// derivedKey 在缺失 X-Gitee-Delivery 时构造去重派生键。
// 用长度前缀分隔各段并整体取 sha256,避免分支名含 ':' 导致跨段碰撞;
// 空 payload(畸形 JSON 派生空 branch/commit)经长度前缀后仍与非空段区分,
// 不与其它投递误碰。
func derivedKey(event, branch, commit string) string {
	h := sha256.New()
	for _, part := range []string{event, branch, commit} {
		// 长度前缀(十进制+冒号)消歧:同一拼接结果只能由唯一的分段还原。
		_, _ = fmt.Fprintf(h, "%d:%s", len(part), part)
	}
	return "derived:" + hex.EncodeToString(h.Sum(nil))
}

// loadForVerify 按 token 定位项目触发配置并解密签名密钥(明文仅在内存,调用方用后清零)。
// token 无匹配 → ErrTokenNotFound;master key 未配置/解密失败 → ErrUnauthorized(验签不可用)。
func (rc *Receiver) loadForVerify(ctx context.Context, token string) (*Config, []byte, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return nil, nil, ErrTokenNotFound
	}
	var (
		cfg             Config
		sealed          []byte
		eventsJSON      string
		mappingsJSON    string
		pathFiltersJSON string
	)
	err := rc.db.QueryRowContext(ctx,
		`SELECT project_id, webhook_token, webhook_secret_ciphertext, events_json,
		        branch_mappings_json, unmatched_policy, path_filters_json
		 FROM pipeline_triggers WHERE webhook_token = ?`, token,
	).Scan(&cfg.ProjectID, &cfg.WebhookToken, &sealed, &eventsJSON, &mappingsJSON, &cfg.UnmatchedPolicy, &pathFiltersJSON)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil, ErrTokenNotFound
		}
		return nil, nil, fmt.Errorf("trigger: load webhook config: %w", err)
	}

	if err := json.Unmarshal([]byte(eventsJSON), &cfg.Events); err != nil {
		return nil, nil, fmt.Errorf("trigger: parse events: %w", err)
	}
	cfg.BranchMappings = []BranchMapping{}
	if strings.TrimSpace(mappingsJSON) != "" {
		if err := json.Unmarshal([]byte(mappingsJSON), &cfg.BranchMappings); err != nil {
			return nil, nil, fmt.Errorf("trigger: parse branch mappings: %w", err)
		}
	}
	cfg.PathFilters = []string{}
	if strings.TrimSpace(pathFiltersJSON) != "" {
		if err := json.Unmarshal([]byte(pathFiltersJSON), &cfg.PathFilters); err != nil {
			return nil, nil, fmt.Errorf("trigger: parse path filters: %w", err)
		}
	}

	if rc.vault == nil {
		return nil, nil, ErrUnauthorized
	}
	plain, err := rc.vault.OpenSecret(sealed)
	if err != nil {
		// master key 未配置 / 解密失败:验签不可用,明确拒绝(不泄漏细节,不 panic)。
		return nil, nil, ErrUnauthorized
	}
	return &cfg, plain, nil
}

// claim 抢占一条去重记录:成功插入 → (id,false);UNIQUE 冲突(重复投递)→ ("",true)。
func (rc *Receiver) claim(ctx context.Context, projectID, deliveryKey, event, branch, commit string) (string, bool, error) {
	id := uuid.NewString()
	_, err := rc.db.ExecContext(ctx,
		`INSERT INTO webhook_deliveries
		   (id, project_id, delivery_id, event, branch, commit_sha, run_id, outcome, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, '', '', ?)`,
		id, projectID, deliveryKey, event, branch, commit, time.Now().UTC().Format(time.RFC3339),
	)
	if err != nil {
		if isUniqueErr(err) {
			return "", true, nil
		}
		return "", false, fmt.Errorf("trigger: claim delivery: %w", err)
	}
	return id, false, nil
}

// setOutcome 在已抢占的去重记录上回填 ignored 原因(供 AC2 可查)。
// 失败不再静默 `_,_=` 丢弃 —— 做一次最佳努力重试;仍失败也不影响幂等正确性
// (占位行已存在,重复投递仍会被判 duplicate)。
func (rc *Receiver) setOutcome(ctx context.Context, recID, outcome string) {
	if rc.updateOutcome(ctx, recID, outcome) {
		return
	}
	_ = rc.updateOutcome(ctx, recID, outcome) // 单次重试(瞬时错误兜底)
}

// updateOutcome 执行一次 outcome 回填;成功返回 true。
func (rc *Receiver) updateOutcome(ctx context.Context, recID, outcome string) bool {
	_, err := rc.db.ExecContext(ctx,
		`UPDATE webhook_deliveries SET outcome = ? WHERE id = ?`, outcome, recID)
	return err == nil
}

// ---- 验签 ---------------------------------------------------------------

// 验签模式枚举(供调用方决定是否强制 timestamp 新鲜度)。
const (
	modeNone      = ""
	modePassword  = "password"
	modeSignature = "signature"
)

// verifySignature 校验 Gitee webhook 签名,密码模式或签名模式其一通过即可(恒定时间比较)。
//   - 密码模式:X-Gitee-Token == 明文密钥。
//   - 签名模式:base64(HMAC-SHA256(key=secret, msg=timestamp+"\n"+secret)) == X-Gitee-Token。
//
// 返回 (命中模式, 是否通过)。secret 为空(未配置/解密失败由上层拦截)→ 一律失败。
func verifySignature(d Delivery, secret []byte) (string, bool) {
	if len(secret) == 0 {
		return modeNone, false
	}
	got := []byte(strings.TrimSpace(d.TokenHdr))
	if len(got) == 0 {
		return modeNone, false
	}
	// 密码模式:明文密钥直接相等。
	if subtle.ConstantTimeCompare(got, secret) == 1 {
		return modePassword, true
	}
	// 签名模式:HMAC-SHA256(timestamp+"\n"+secret) base64。
	msg := strings.TrimSpace(d.Timestamp) + "\n" + string(secret)
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(msg))
	want := []byte(base64.StdEncoding.EncodeToString(mac.Sum(nil)))
	if subtle.ConstantTimeCompare(got, want) == 1 {
		return modeSignature, true
	}
	return modeNone, false
}

// timestampFresh 校验 webhook 时间戳新鲜度(防重放)。
//   - 签名模式:timestamp 必填且 |now-ts| ≤ replayWindow,否则 false。
//   - 密码模式:timestamp 缺失时放行(Gitee 密码模式通常不带);存在则同样校验窗口。
//
// Gitee timestamp 多为毫秒 epoch,亦容错秒 epoch:按数值量级自适应解析。
func timestampFresh(raw, mode string) bool {
	ts := strings.TrimSpace(raw)
	if ts == "" {
		// 签名模式必须带 timestamp(否则重放窗口失效);密码模式允许缺失。
		return mode == modePassword
	}
	t, ok := parseEpoch(ts)
	if !ok {
		return false
	}
	delta := nowFunc().Sub(t)
	if delta < 0 {
		delta = -delta
	}
	return delta <= replayWindow
}

// parseEpoch 解析 epoch 时间戳:13 位左右按毫秒,10 位左右按秒(自适应,容错 Gitee 两种格式)。
func parseEpoch(s string) (time.Time, bool) {
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil || n <= 0 {
		return time.Time{}, false
	}
	// 量级判定:>= 1e12 视为毫秒,否则视为秒。
	if n >= 1_000_000_000_000 {
		return time.UnixMilli(n), true
	}
	return time.Unix(n, 0), true
}

// ---- 事件 / payload 解析 ------------------------------------------------

// eventSubscribed 据 X-Gitee-Event 与 events 开关判定该事件类型是否被订阅。
func eventSubscribed(event string, ev Events) bool {
	switch strings.TrimSpace(event) {
	case eventPush:
		return ev.Push
	case eventTag:
		return ev.Tag
	case eventMergeRequest:
		return ev.PullRequest
	case eventRelease:
		return ev.Release
	default:
		return false
	}
}

// parsedPayload 是从 Gitee payload 解析出的关注字段。
//
// ChangedFiles 为本次 push 改动的文件路径并集(commits[].{added,modified,removed} 去重);
// 仅 push 事件有意义,其余事件 / 拿不到时为空(空 → 路径过滤放行,诚实降级)。
type parsedPayload struct {
	Branch       string
	Commit       string
	ChangedFiles []string
}

// pushCommit 是 push 事件 commits[] 元素里本包关心的改动文件三类(GitHub/Gitee 同形)。
type pushCommit struct {
	Added    []string `json:"added"`
	Modified []string `json:"modified"`
	Removed  []string `json:"removed"`
}

// giteePayload 是 Gitee push/tag/MR/release webhook 的部分字段(只取所需,容忍多余字段)。
type giteePayload struct {
	Ref         string       `json:"ref"`
	After       string       `json:"after"`
	CheckoutSHA string       `json:"checkout_sha"`
	Commits     []pushCommit `json:"commits"`
	HeadCommit  struct {
		ID string `json:"id"`
	} `json:"head_commit"`
	PullRequest struct {
		Head struct {
			Ref string `json:"ref"`
			SHA string `json:"sha"`
		} `json:"head"`
	} `json:"pull_request"`
	// Release 是 Gitee「Release Hook」/ GitHub「release」事件载荷:tag 名在 tag_name,
	// 目标 commit-ish(分支名或 SHA)在 target_commitish(Story 8-11 / FR-8-11)。
	Release struct {
		TagName         string `json:"tag_name"`
		TargetCommitish string `json:"target_commitish"`
	} `json:"release"`
}

// parsePayload 用 encoding/json 解析投递体,提取分支/标签(从 ref 取 refs/heads|tags/<x>
// 末段;release 事件取 release.tag_name)与 commit。
// 解析失败/字段缺失返回零值(匹配阶段据空分支不命中,不 panic)。
//
// 标签语义(tag push 与 release 一致):标签名落入 Branch 字段,沿用既有
// branch→环境映射(如模式 v* 命中 v1.2.3),保持下游处理统一简单——上游对
// Branch 的语义是「触发引用名」,push 为分支名、tag/release 为标签名。
func parsePayload(body []byte) parsedPayload {
	var p giteePayload
	_ = json.Unmarshal(body, &p)

	// 触发引用名:push/tag 取 ref 末段;release 事件 ref 缺省,取 release.tag_name。
	branch := refToBranch(p.Ref)
	if branch == "" {
		branch = strings.TrimSpace(p.Release.TagName)
	}
	if branch == "" {
		branch = strings.TrimSpace(p.PullRequest.Head.Ref)
	}

	commit := strings.TrimSpace(p.After)
	if commit == "" {
		commit = strings.TrimSpace(p.CheckoutSHA)
	}
	if commit == "" {
		commit = strings.TrimSpace(p.HeadCommit.ID)
	}
	if commit == "" {
		commit = strings.TrimSpace(p.PullRequest.Head.SHA)
	}
	// release 事件无 head_commit:回退到 target_commitish(分支名或 SHA)作为 commit 上下文。
	if commit == "" {
		commit = strings.TrimSpace(p.Release.TargetCommitish)
	}
	return parsedPayload{Branch: branch, Commit: commit, ChangedFiles: changedFiles(p.Commits)}
}

// changedFiles 把 push 事件 commits[].{added,modified,removed} 汇成去重的文件路径并集
// (保留首次出现序;trim 后剔空)。无 commits / 全空 → nil(路径过滤据此放行,诚实降级)。
func changedFiles(commits []pushCommit) []string {
	seen := make(map[string]struct{})
	out := make([]string, 0, 8)
	add := func(paths []string) {
		for _, f := range paths {
			f = strings.TrimSpace(f)
			if f == "" {
				continue
			}
			if _, dup := seen[f]; dup {
				continue
			}
			seen[f] = struct{}{}
			out = append(out, f)
		}
	}
	for _, c := range commits {
		add(c.Added)
		add(c.Modified)
		add(c.Removed)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// refToBranch 从 refs/heads/main 或 refs/tags/v1 取末段分支/标签名;非 ref 形式原样返回。
func refToBranch(ref string) string {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return ""
	}
	if strings.HasPrefix(ref, "refs/heads/") {
		return strings.TrimPrefix(ref, "refs/heads/")
	}
	if strings.HasPrefix(ref, "refs/tags/") {
		return strings.TrimPrefix(ref, "refs/tags/")
	}
	return ref
}

// ---- 分支匹配 -----------------------------------------------------------

// matchBranch 在分支映射中找第一条通配命中 branch 的映射(path.Match glob,支持 `*`)。
func matchBranch(branch string, mappings []BranchMapping) (BranchMapping, bool) {
	branch = strings.TrimSpace(branch)
	if branch == "" {
		return BranchMapping{}, false
	}
	for _, m := range mappings {
		pattern := strings.TrimSpace(m.BranchPattern)
		if pattern == "" {
			continue
		}
		if ok, err := path.Match(pattern, branch); err == nil && ok {
			return m, true
		}
		// path.Match 的 `*` 不跨 `/`;分支模式如 release/* 需对含 `/` 的分支也命中,
		// 故对含 `/` 的模式额外做前缀通配兜底(release/* 命中 release/1.2)。
		if globMatch(pattern, branch) {
			return m, true
		}
	}
	return BranchMapping{}, false
}

// globMatch 是补充的 glob 匹配:`*` 匹配任意字符(含 `/`),其余字符精确。
// 用于 path.Match 因 `*` 不跨 `/` 而漏匹配的场景(如 release/* 对 release/1.2)。
func globMatch(pattern, s string) bool {
	if !strings.Contains(pattern, "*") {
		return pattern == s
	}
	parts := strings.Split(pattern, "*")
	pos := 0
	for i, part := range parts {
		if part == "" {
			continue
		}
		idx := strings.Index(s[pos:], part)
		if idx < 0 {
			return false
		}
		// 首段必须从开头匹配(非通配前缀)。
		if i == 0 && idx != 0 {
			return false
		}
		pos += idx + len(part)
	}
	// 末段非空时,模式不以 `*` 结尾,须匹配到字符串结尾。
	if last := parts[len(parts)-1]; last != "" {
		return strings.HasSuffix(s, last)
	}
	return true
}

// ---- 路径过滤(monorepo · P0)-------------------------------------------

// matchPathFilters 判断「本次改动文件」是否匹配任一路径过滤 glob(任一文件命中任一 glob 即放行)。
// 调用方已保证 filters 与 files 均非空(空文件列表走放行降级,不进此函数)。
func matchPathFilters(filters, files []string) bool {
	for _, pat := range filters {
		pat = strings.TrimSpace(pat)
		if pat == "" {
			continue
		}
		for _, f := range files {
			if pathGlobMatch(pat, strings.TrimSpace(f)) {
				return true
			}
		}
	}
	return false
}

// pathGlobMatch 是支持 `**` 的路径 glob 匹配(按路径段递归):
//   - `**` 匹配任意数量的路径段(含零段,跨 `/`),如 `backend/**` 命中 `backend/a/b.go`、`**/*.go` 命中 `a/b.go`。
//   - `*`  匹配同一路径段内任意字符(不跨 `/`),如 `*.md` 命中 `README.md` 但不命中 `docs/x.md`。
//   - 其余按段精确匹配(段内仍走 path.Match 支持 `*`/`?`)。
//
// 语义对齐常见 CI(GitLab rules:changes / GitHub paths)。pattern/name 均以 `/` 切段后比较。
func pathGlobMatch(pattern, name string) bool {
	if pattern == "" || name == "" {
		return false
	}
	return segMatch(strings.Split(pattern, "/"), strings.Split(name, "/"))
}

// segMatch 按段做带 `**` 的 glob 匹配:`**` 段可吞 0..n 个 name 段(递归回溯);
// 普通段用 path.Match 段内匹配(`*`/`?` 不跨 `/`)。
func segMatch(pat, name []string) bool {
	for len(pat) > 0 {
		if pat[0] == "**" {
			// 折叠连续的 `**`。
			for len(pat) > 1 && pat[1] == "**" {
				pat = pat[1:]
			}
			if len(pat) == 1 {
				return true // 末尾 `**` 吞掉所有剩余段(含零段)。
			}
			// `**` 吞 0..len(name) 段,逐一尝试让后续模式从某位置起匹配。
			for i := 0; i <= len(name); i++ {
				if segMatch(pat[1:], name[i:]) {
					return true
				}
			}
			return false
		}
		if len(name) == 0 {
			return false
		}
		if ok, err := path.Match(pat[0], name[0]); err != nil || !ok {
			return false
		}
		pat, name = pat[1:], name[1:]
	}
	return len(name) == 0
}

// ---- helpers ------------------------------------------------------------

// zero 把字节切片清零(明文密钥用完即弃)。
func zero(b []byte) {
	for i := range b {
		b[i] = 0
	}
}

// isUniqueErr 判断错误是否为唯一约束冲突(modernc sqlite 文本含 UNIQUE)。
func isUniqueErr(err error) bool { return store.IsUniqueErr(err) }
