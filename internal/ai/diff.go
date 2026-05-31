// diff.go 是「成功/失败差异对比」(Story 7.3 / FR-25)的领域计算核心。
//
// RunDiffer 给两个 (repoURL, commit)——baseline(上一次成功运行)与 current(本次失败
// 运行)——经 go-git 克隆仓库到内存,解析两个 commit 的 tree,算文件级 diff(路径 + 状态
// added|modified|deleted|renamed + 增删行数),截断(文件数上限防 OOM),并产出一句人读
// summary(含简单「最可疑」启发式)。
//
// 复用 2-5/3-6 的克隆能力 + SSRF 收口 + test-only allowInsecure(放行 file:// 夹具)。本文件
// 不依赖 httpapi / run 包(避免 import 成环);httpapi 层取 run/baseline/凭据后调用此处。
//
// 安全:token 仅作为 BasicAuth.Password 经参数化 API 传入,绝不进 URL / 日志 / 错误 / 结果。
// 脱敏尽力:本层只输出文件路径 + 行数计数,不回传任何文件内容/diff 正文,故 diff 正文中的明文
// secret 不会出网。
//
// 健壮性:克隆失败 / commit 不可达绝不报致命错——返回 Available=false 的降级结果(由上层映射
// 为 available:false degraded,绝不 500)。克隆到内存 storer,用完即随 GC 释放(无落盘、无驻留)。
// 无 init() 副作用。
package ai

import (
	"context"
	"sort"
	"strings"
	"time"

	"github.com/go-git/go-billy/v5/memfs"
	gogit "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	fdiff "github.com/go-git/go-git/v5/plumbing/format/diff"
	"github.com/go-git/go-git/v5/plumbing/object"

	"github.com/go-git/go-git/v5/storage/memory"
	"github.com/huangchengsir/pipewright/internal/gitauth"
)

// diffCloneTimeout 是单次克隆的硬超时(防黑洞 IP / 慢 DNS 把 goroutine 挂死)。
const diffCloneTimeout = 30 * time.Second

// maxDiffFiles 是 diff 文件数上限:超出则截断(Truncated=true),防大改动 OOM(NFR-4)。
const maxDiffFiles = 200

// maxFileDiffLines 是单文件增删行数统计上限(code-review P1;防巨型单文件计数撑爆)。
const maxFileDiffLines = 50000

// 文件级 diff 状态枚举(冻结契约;与前端/DTO 对齐)。
const (
	diffStatusAdded    = "added"
	diffStatusModified = "modified"
	diffStatusDeleted  = "deleted"
	diffStatusRenamed  = "renamed"
)

// FileDiff 是单个文件的差异(路径 + 状态 + 增删行数)。中性结构,httpapi 层映射为冻结 DTO。
type FileDiff struct {
	// Path 是文件路径(重命名时为新路径)。
	Path string
	// Status ∈ added|modified|deleted|renamed。
	Status string
	// Additions 是新增行数(deleted 时为 0)。
	Additions int
	// Deletions 是删除行数(added 时为 0)。
	Deletions int
}

// RunDiff 是一次「baseline→current」差异计算结果(中性;httpapi 层映射为冻结 DTO)。
// Available=false 表示降级态(克隆失败 / commit 不可达):Files 空,Reason 人读。
type RunDiff struct {
	// Available 表示差异是否成功算出;false = 降级(克隆失败 / commit 不可达 / 空 commit)。
	Available bool
	// Reason 是降级原因(人读;Available=false 时填,绝无 token/URL 密钥)。Available=true 时空。
	Reason string
	// Files 是文件级差异(按 path 升序;最多 maxDiffFiles 条)。
	Files []FileDiff
	// Truncated 表示文件数超 maxDiffFiles 被截断。
	Truncated bool
	// Summary 是一句人读总结(含「最可疑」启发式)。
	Summary string
}

// RunDiffer 抽象「给两 commit 算文件级 diff」能力(注入便于测试 / 替换)。
type RunDiffer interface {
	// Diff 克隆 repoURL,解析 baselineCommit→currentCommit 两 tree,算文件级 diff。
	// 克隆失败 / commit 不可达返回 Available=false 的降级结果(不返回错误,健壮降级)。
	// token 进程内取用、用完即弃,绝不进结果/日志/错误。
	Diff(ctx context.Context, repoURL, token, baselineCommit, currentCommit string) RunDiff
}

// goGitDiffer 是基于 go-git 的 RunDiffer 实现。
type goGitDiffer struct {
	// allowInsecure 仅供测试:为 true 时跳过 SSRF scheme/host 校验(放行 file:// 夹具)。
	// 生产路径(NewRunDiffer)绝不设置此字段。
	allowInsecure bool
}

// NewRunDiffer 构造生产 RunDiffer(严格 SSRF 收口;无 init 副作用)。
func NewRunDiffer() RunDiffer { return goGitDiffer{} }

// newInsecureRunDiffer 仅供测试:放行 file:// 等本地夹具(跳过 SSRF 校验)。
func newInsecureRunDiffer() RunDiffer { return goGitDiffer{allowInsecure: true} }

// NewInsecureRunDifferForTest 仅供**跨包测试**(httpapi 层 file:// 夹具):放行 file://
// 等本地地址(跳过 SSRF 校验)。生产路径绝不用此构造(应用 NewRunDiffer)。
func NewInsecureRunDifferForTest() RunDiffer { return goGitDiffer{allowInsecure: true} }

// Diff 克隆仓库并算 baseline→current 的文件级 diff;失败优雅降级为 Available=false。
func (d goGitDiffer) Diff(ctx context.Context, repoURL, token, baselineCommit, currentCommit string) RunDiff {
	repoURL = strings.TrimSpace(repoURL)
	baselineCommit = strings.TrimSpace(baselineCommit)
	currentCommit = strings.TrimSpace(currentCommit)

	if repoURL == "" {
		return RunDiff{Available: false, Reason: "仓库地址为空,无法计算差异", Files: []FileDiff{}}
	}
	if baselineCommit == "" || currentCommit == "" {
		return RunDiff{Available: false, Reason: "缺少可对比的提交,无法计算差异", Files: []FileDiff{}}
	}

	if !d.allowInsecure && !validRepoURL(repoURL) {
		// SSRF 拒绝:走降级(绝不泄漏 URL 细节),不报致命错。
		return RunDiff{Available: false, Reason: "仓库地址不可达或不被允许", Files: []FileDiff{}}
	}

	cctx, cancel := context.WithTimeout(ctx, diffCloneTimeout)
	defer cancel()

	// 克隆到内存(不设 Depth:浅克隆 HEAD 取不到任意历史 commit;此处需两个具体 commit 的 tree,
	// 故取全量历史。内存 storer 限驻留,用完即随 GC 释放)。
	storer := memory.NewStorage()
	auth := gitauth.BasicAuth(repoURL, token)
	repo, err := gogit.CloneContext(cctx, storer, memfs.New(), &gogit.CloneOptions{
		URL:  repoURL,
		Auth: auth,
		Tags: gogit.NoTags,
	})
	if err != nil {
		// 克隆失败(网络/鉴权/不存在):降级,不泄漏底层错误(可能含 URL 密钥)。
		return RunDiff{Available: false, Reason: "仓库克隆失败,暂时无法计算差异", Files: []FileDiff{}}
	}

	baseTree, berr := commitTree(repo, baselineCommit)
	curTree, cerr := commitTree(repo, currentCommit)
	if berr != nil || cerr != nil {
		// commit 不可达(history 不含该 sha / 解析失败):降级。
		return RunDiff{Available: false, Reason: "提交在仓库中不可达,无法计算差异", Files: []FileDiff{}}
	}

	changes, derr := baseTree.DiffContext(cctx, curTree)
	if derr != nil {
		return RunDiff{Available: false, Reason: "计算差异失败", Files: []FileDiff{}}
	}

	return buildRunDiff(cctx, changes)
}

// commitTree 解析 commit sha 并返回其 tree;sha 不可达/解析失败返回错误。
func commitTree(repo *gogit.Repository, sha string) (*object.Tree, error) {
	hash := plumbing.NewHash(sha)
	commit, err := repo.CommitObject(hash)
	if err != nil {
		return nil, err
	}
	return commit.Tree()
}

// buildRunDiff 把 go-git Changes 折算为文件级 RunDiff(状态 + 增删行数 + 截断 + summary)。
//
// OOM 护栏(code-review P1):**先据轻量路径排序 + 截断到 maxDiffFiles,再只对保留文件算 patch**
// (`PatchContext` 会把整份文件 patch 物化进内存)。否则巨 diff 先算完上万份完整 patch 占满内存、
// 再丢弃——截断在内存峰值之后生效=纸面合规。单文件行数另由 fileDiffFromChange 封顶。
func buildRunDiff(ctx context.Context, changes object.Changes) RunDiff {
	// 阶段一:仅取路径(不触 patch),用于确定性排序 + 截断。
	type lightChange struct {
		ch   *object.Change
		path string
	}
	light := make([]lightChange, 0, len(changes))
	for _, ch := range changes {
		p := ch.To.Name
		if p == "" {
			p = ch.From.Name // deleted
		}
		if p == "" {
			continue
		}
		light = append(light, lightChange{ch: ch, path: p})
	}
	sort.SliceStable(light, func(i, j int) bool { return light[i].path < light[j].path })

	truncated := false
	if len(light) > maxDiffFiles {
		light = light[:maxDiffFiles]
		truncated = true
	}

	// 阶段二:只对截断后保留的文件算 patch(限内存峰值)。
	files := make([]FileDiff, 0, len(light))
	for _, lc := range light {
		fd, ok := fileDiffFromChange(ctx, lc.ch)
		if !ok {
			continue
		}
		files = append(files, fd)
	}

	return RunDiff{
		Available: true,
		Files:     files,
		Truncated: truncated,
		Summary:   summarize(files, truncated),
	}
}

// fileDiffFromChange 把单个 go-git Change 折算为 FileDiff:据 From/To 判状态(含重命名),
// 据 patch chunks 统计增删行数。无法判定(空 change / patch 失败)返回 ok=false 跳过。
func fileDiffFromChange(ctx context.Context, ch *object.Change) (FileDiff, bool) {
	action, err := ch.Action()
	if err != nil {
		return FileDiff{}, false
	}

	fromName := ch.From.Name
	toName := ch.To.Name

	var fd FileDiff
	switch {
	case fromName == "":
		fd.Status = diffStatusAdded
		fd.Path = toName
	case toName == "":
		fd.Status = diffStatusDeleted
		fd.Path = fromName
	case fromName != toName:
		fd.Status = diffStatusRenamed
		fd.Path = toName
	default:
		fd.Status = diffStatusModified
		fd.Path = toName
	}
	_ = action

	// 统计增删行数:经 patch 的 file-patch chunks(Add/Delete 类型按行计数)。
	// 二进制文件 chunks 为空 → 0/0(仍记一条 modified/added/deleted,行数计 0)。
	// 单文件行数封顶(code-review P1):统计到 maxFileDiffLines 即停,防被改成几百万行的单文件
	// 的计数撑爆(patch 物化由上游 200 文件截断 + 30s 超时兜底,此处再封单文件计数上限)。
	if patch, perr := ch.PatchContext(ctx); perr == nil && patch != nil {
	count:
		for _, fp := range patch.FilePatches() {
			if fp.IsBinary() {
				continue
			}
			for _, chunk := range fp.Chunks() {
				n := countLines(chunk.Content())
				switch chunk.Type() {
				case fdiff.Add:
					fd.Additions += n
				case fdiff.Delete:
					fd.Deletions += n
				}
				if fd.Additions+fd.Deletions >= maxFileDiffLines {
					break count
				}
			}
		}
	}
	return fd, true
}

// countLines 统计一个 chunk 内容的行数(末尾无换行也算一行;空串算 0)。
func countLines(content string) int {
	if content == "" {
		return 0
	}
	n := strings.Count(content, "\n")
	if !strings.HasSuffix(content, "\n") {
		n++
	}
	return n
}

// summarize 产出一句人读总结:文件数 + 总增删 + 简单「最可疑」启发式。
// 启发式:优先点名依赖清单(package.json/go.mod/...)变更,否则点名改动最大(增删合计最多)的文件。
func summarize(files []FileDiff, truncated bool) string {
	if len(files) == 0 {
		return "两次提交之间无文件差异"
	}
	totalAdd, totalDel := 0, 0
	for _, f := range files {
		totalAdd += f.Additions
		totalDel += f.Deletions
	}

	var b strings.Builder
	b.WriteString(plural(len(files), "文件"))
	b.WriteString("改动")
	b.WriteString("(+")
	b.WriteString(itoa(totalAdd))
	b.WriteString("/-")
	b.WriteString(itoa(totalDel))
	b.WriteString(")")
	if truncated {
		b.WriteString(",已截断展示")
	}

	if suspect := mostSuspect(files); suspect != "" {
		b.WriteString(";最可疑:")
		b.WriteString(suspect)
	}
	return b.String()
}

// dependencyManifests 是依赖/构建清单文件名(变更优先视为「最可疑」,常引入构建/运行差异)。
var dependencyManifests = map[string]string{
	"package.json":      "依赖变更",
	"package-lock.json": "依赖锁变更",
	"yarn.lock":         "依赖锁变更",
	"pnpm-lock.yaml":    "依赖锁变更",
	"go.mod":            "依赖变更",
	"go.sum":            "依赖锁变更",
	"pom.xml":           "依赖变更",
	"build.gradle":      "构建配置变更",
	"requirements.txt":  "依赖变更",
	"pyproject.toml":    "依赖变更",
	"Dockerfile":        "镜像构建变更",
}

// mostSuspect 据启发式挑「最可疑」文件:优先依赖/构建清单(按 files 顺序首个命中),
// 否则改动最大(增删合计最多)的文件。返回人读串(如 "package.json 依赖变更");无则空。
func mostSuspect(files []FileDiff) string {
	for _, f := range files {
		base := f.Path
		if i := strings.LastIndex(base, "/"); i >= 0 {
			base = base[i+1:]
		}
		if hint, ok := dependencyManifests[base]; ok {
			return f.Path + " " + hint
		}
	}
	// 退化:挑改动量最大的文件。
	best := -1
	bestIdx := -1
	for i, f := range files {
		churn := f.Additions + f.Deletions
		if churn > best {
			best = churn
			bestIdx = i
		}
	}
	if bestIdx >= 0 && best > 0 {
		return files[bestIdx].Path
	}
	return ""
}

// plural 拼「N 文件」式中文计数(中文无复数变化,直接拼数字 + 量词)。
func plural(n int, unit string) string {
	return itoa(n) + " " + unit
}

// itoa 是无依赖的非负整数转字符串(避免 import strconv 仅为一处)。
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
