// analyze.go 是「仓库浅克隆 + manifest 技术栈检测」(Story 2.5 / FR-8)。
//
// RepoAnalyzer 用 go-git 的 CloneContext(内存 storer + memfs 工作区,Depth:1 +
// SingleBranch)拉取仓库的最新一次提交,读若干 manifest 文件(package.json / go.mod /
// pom.xml / build.gradle / requirements.txt / pyproject.toml / Dockerfile)推断语言、
// 版本、构建工具、产物类型,产出中性的 RepoAnalysis 供 ai.Service.Generate 构 prompt。
//
// 安全:token 仅作为 BasicAuth.Password 经参数化 API 传入,绝不进 URL / 日志 / 错误 /
// 分析结果。复用 project 包同款 SSRF 收口(生产仅 http/https;拒元数据/链路本地/回环;
// 私网放行);allowInsecure 仅供测试注入(放行 file:// 夹具),生产构造永远为 false。
//
// 健壮性:克隆失败(网络不可达 / GFW / 鉴权)绝不报致命错——返回 Cloned=false 的降级
// 分析(语言等字段留空),由上层仅凭 repoUrl + NL 让 LLM 推断。Depth:1 + 内存 storer
// 限制体量,clone 完即随 GC 释放内存(无落盘、无驻留)。无 init() 副作用。
package ai

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/url"
	"strings"
	"time"

	"github.com/go-git/go-billy/v5"
	"github.com/go-git/go-billy/v5/memfs"
	gogit "github.com/go-git/go-git/v5"
	githttp "github.com/go-git/go-git/v5/plumbing/transport/http"
	"github.com/go-git/go-git/v5/storage/memory"
)

// cloneTimeout 是单次浅克隆的硬超时(防黑洞 IP / 慢 DNS 把 goroutine 挂死)。
const cloneTimeout = 20 * time.Second

// maxManifestBytes 是单个 manifest 文件读取上限(防超大文件吃内存)。
const maxManifestBytes = 1 << 20 // 1MB

// 语言枚举(分析输出 + 下游 prompt 用;与 settings.Toolchain.Language 取值对齐)。
const (
	langNode   = "node"
	langGo     = "go"
	langJava   = "java"
	langPython = "python"
)

// RepoAnalysis 是仓库分析结果(中性结构;喂给 prompt,也回前端展示)。
// Cloned=false 表示克隆失败的降级态:其余字段留空,仅凭 repoUrl + NL 推断。
type RepoAnalysis struct {
	// Cloned 表示是否成功浅克隆并读到 manifest;false = 降级(网络/鉴权失败)。
	Cloned bool
	// Language 是检测到的主语言(node/go/java/python;未识别为空)。
	Language string
	// LanguageVersion 是从 manifest 提取的语言/工具链版本(如 "22";未识别为空)。
	LanguageVersion string
	// BuildTool 是构建工具(npm/go/maven/gradle/pip 等;未识别为空)。
	BuildTool string
	// HasDockerfile 表示仓库根含 Dockerfile。
	HasDockerfile bool
	// ArtifactHint 是产物类型提示(image/jar/dist;未识别为空)。
	ArtifactHint string
	// Signals 是检测命中的证据(如 "package.json"、"engines.node=22"),供前端展示判断依据。
	Signals []string
	// DegradeReason 是降级原因(人读;克隆失败时填,绝无 token/URL 密钥)。Cloned=true 时为空。
	DegradeReason string
}

// RepoAnalyzer 抽象「浅克隆 + manifest 检测」能力(注入便于测试 / 替换)。
type RepoAnalyzer interface {
	// Analyze 用 token 浅克隆 repoURL 读 manifest;克隆失败返回 Cloned=false 的降级分析
	// (不返回错误,健壮降级)。token 进程内取用、用完即弃,绝不进结果/日志/错误。
	Analyze(ctx context.Context, repoURL, token string) RepoAnalysis
}

// goGitAnalyzer 是基于 go-git CloneContext 的 RepoAnalyzer 实现。
type goGitAnalyzer struct {
	// allowInsecure 仅供测试:为 true 时跳过 SSRF scheme/host 校验(放行 file:// 夹具)。
	// 生产路径(NewRepoAnalyzer)绝不设置此字段。
	allowInsecure bool
}

// NewRepoAnalyzer 构造生产 RepoAnalyzer(严格 SSRF 收口;无 init 副作用)。
func NewRepoAnalyzer() RepoAnalyzer {
	return goGitAnalyzer{}
}

// newInsecureRepoAnalyzer 仅供测试:放行 file:// 等本地夹具(跳过 SSRF 校验)。
func newInsecureRepoAnalyzer() RepoAnalyzer {
	return goGitAnalyzer{allowInsecure: true}
}

// Analyze 浅克隆仓库并检测技术栈;失败优雅降级为 Cloned=false。
func (a goGitAnalyzer) Analyze(ctx context.Context, repoURL, token string) RepoAnalysis {
	repoURL = strings.TrimSpace(repoURL)
	if repoURL == "" {
		return RepoAnalysis{Cloned: false, Signals: []string{}, DegradeReason: "仓库地址为空,无法克隆分析"}
	}

	if !a.allowInsecure {
		if !validRepoURL(repoURL) {
			// SSRF 拒绝:同样走降级(绝不泄漏 URL 细节),不报致命错。
			return RepoAnalysis{Cloned: false, Signals: []string{}, DegradeReason: "仓库地址不可达或不被允许"}
		}
	}

	fs := memfs.New()
	storer := memory.NewStorage()

	auth := &githttp.BasicAuth{Username: "git", Password: token}

	cctx, cancel := context.WithTimeout(ctx, cloneTimeout)
	defer cancel()

	_, err := gogit.CloneContext(cctx, storer, fs, &gogit.CloneOptions{
		URL:          repoURL,
		Auth:         auth,
		Depth:        1,
		SingleBranch: true,
		Tags:         gogit.NoTags,
	})
	if err != nil {
		// 克隆失败(网络/鉴权/不存在):降级,不泄漏底层错误(可能含 URL 密钥)。
		return RepoAnalysis{
			Cloned:        false,
			Signals:       []string{},
			DegradeReason: "仓库克隆失败,改为仅凭地址与补充说明推断",
		}
	}

	analysis := detectStack(fs)
	analysis.Cloned = true
	return analysis
}

// detectStack 读 memfs 工作区根的 manifest 文件,推断语言/版本/构建工具/产物。
// 顺序:Dockerfile(产物提示)→ node → go → java → python;首个命中确定主语言,
// 但 Dockerfile 与产物类型可叠加。Signals 累积所有证据。
func detectStack(fs billy.Filesystem) RepoAnalysis {
	res := RepoAnalysis{Signals: []string{}}

	hasDockerfile := fileExists(fs, "Dockerfile")
	if hasDockerfile {
		res.HasDockerfile = true
		res.Signals = append(res.Signals, "Dockerfile")
	}

	switch {
	case fileExists(fs, "package.json"):
		res.Language = langNode
		res.BuildTool = "npm"
		res.ArtifactHint = artifactFor(langNode, hasDockerfile)
		res.Signals = append(res.Signals, "package.json")
		if v := detectNodeVersion(fs); v != "" {
			res.LanguageVersion = v
		}
	case fileExists(fs, "go.mod"):
		res.Language = langGo
		res.BuildTool = "go"
		res.ArtifactHint = artifactFor(langGo, hasDockerfile)
		res.Signals = append(res.Signals, "go.mod")
		if v := detectGoVersion(fs); v != "" {
			res.LanguageVersion = v
			res.Signals = append(res.Signals, "go "+v)
		}
	case fileExists(fs, "pom.xml"):
		res.Language = langJava
		res.BuildTool = "maven"
		res.ArtifactHint = artifactFor(langJava, hasDockerfile)
		res.Signals = append(res.Signals, "pom.xml")
	case fileExists(fs, "build.gradle") || fileExists(fs, "build.gradle.kts"):
		res.Language = langJava
		res.BuildTool = "gradle"
		res.ArtifactHint = artifactFor(langJava, hasDockerfile)
		res.Signals = append(res.Signals, "build.gradle")
	case fileExists(fs, "requirements.txt"):
		res.Language = langPython
		res.BuildTool = "pip"
		res.ArtifactHint = artifactFor(langPython, hasDockerfile)
		res.Signals = append(res.Signals, "requirements.txt")
	case fileExists(fs, "pyproject.toml"):
		res.Language = langPython
		res.BuildTool = "pip"
		res.ArtifactHint = artifactFor(langPython, hasDockerfile)
		res.Signals = append(res.Signals, "pyproject.toml")
	}

	return res
}

// artifactFor 由语言 + 是否有 Dockerfile 推断产物类型提示。
// 有 Dockerfile → image;否则按语言默认(java→jar、node/python→dist、go→image)。
func artifactFor(language string, hasDockerfile bool) string {
	if hasDockerfile {
		return "image"
	}
	switch language {
	case langJava:
		return "jar"
	case langNode, langPython:
		return "dist"
	case langGo:
		return "image"
	default:
		return ""
	}
}

// detectNodeVersion 从 package.json(engines.node)或 .nvmrc 提取 Node 主版本号。
func detectNodeVersion(fs billy.Filesystem) string {
	if data := readManifest(fs, "package.json"); data != nil {
		var pkg struct {
			Engines struct {
				Node string `json:"node"`
			} `json:"engines"`
		}
		if json.Unmarshal(data, &pkg) == nil {
			if v := majorVersion(pkg.Engines.Node); v != "" {
				return v
			}
		}
	}
	if data := readManifest(fs, ".nvmrc"); data != nil {
		if v := majorVersion(strings.TrimSpace(string(data))); v != "" {
			return v
		}
	}
	return ""
}

// detectGoVersion 从 go.mod 的 `go 1.x` 指令提取版本(返回 "1.x")。
func detectGoVersion(fs billy.Filesystem) string {
	data := readManifest(fs, "go.mod")
	if data == nil {
		return ""
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "go ") {
			return strings.TrimSpace(strings.TrimPrefix(line, "go "))
		}
	}
	return ""
}

// majorVersion 从版本约束串(如 ">=22.0.0"、"^20"、"v18.1"、"22")提取主版本数字。
// 取首段连续数字;无数字返回空。
func majorVersion(spec string) string {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return ""
	}
	var b strings.Builder
	started := false
	for _, r := range spec {
		if r >= '0' && r <= '9' {
			b.WriteRune(r)
			started = true
			continue
		}
		if started {
			break
		}
	}
	return b.String()
}

// fileExists 报告工作区根下是否存在某文件(非目录)。
func fileExists(fs billy.Filesystem, name string) bool {
	info, err := fs.Stat(name)
	if err != nil {
		return false
	}
	return !info.IsDir()
}

// readManifest 读取工作区根下文件内容(限 maxManifestBytes);不存在/读失败返回 nil。
func readManifest(fs billy.Filesystem, name string) []byte {
	f, err := fs.Open(name)
	if err != nil {
		return nil
	}
	defer func() { _ = f.Close() }()
	data, err := io.ReadAll(io.LimitReader(f, maxManifestBytes))
	if err != nil {
		return nil
	}
	return data
}

// validRepoURL 对仓库地址做 SSRF 收口(生产路径),复用 project 包同款策略:
// 仅 http/https;拒云元数据/链路本地/回环;私网放行(自托管内网 Git 友好)。
// 返回 true=允许。解析失败/被拒返回 false(调用方走降级,绝不泄漏细节)。
func validRepoURL(repoURL string) bool {
	u, err := url.Parse(strings.TrimSpace(repoURL))
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
	if ip := net.ParseIP(host); ip != nil {
		return !blockedIP(ip)
	}
	addrs, err := net.LookupIP(host)
	if err != nil || len(addrs) == 0 {
		// 解析失败:留给 clone 路径(会降级);不在此误拒临时 DNS 抖动。
		return true
	}
	for _, ip := range addrs {
		if blockedIP(ip) {
			return false
		}
	}
	return true
}

// blockedIP 判定 IP 是否落在禁止区:回环、链路本地(含云元数据 169.254.169.254)、未指定。
func blockedIP(ip net.IP) bool {
	return ip.IsLoopback() ||
		ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() ||
		ip.IsUnspecified()
}
