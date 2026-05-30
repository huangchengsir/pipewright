package httpapi

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-git/go-billy/v5"
	"github.com/go-git/go-billy/v5/memfs"
	gogit "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	githttp "github.com/go-git/go-git/v5/plumbing/transport/http"
	"github.com/go-git/go-git/v5/storage/memory"
	"github.com/huangchengsir/pipewright/internal/project"
	"github.com/huangchengsir/pipewright/internal/vault"
)

// ---- 冻结 source 契约(Story 3.6;FR-4 预埋;camelCase) ----------------------
//
// 形状定死:tree {ref,path,entries:[{name,path,type,size?}]};
// blob {ref,path,size,binary,truncated,content}。7-4 只消费、不改形状。

// sourceCloneTimeout 是单次浅克隆硬超时(防黑洞 IP / 慢 DNS 挂死)。
const sourceCloneTimeout = 20 * time.Second

// maxBlobBytes 是单文件读取上限(超出截断;防 OOM,NFR-4)。
const maxBlobBytes = 512 * 1024 // 512KB

type sourceEntryDTO struct {
	Name string `json:"name"`
	Path string `json:"path"`
	Type string `json:"type"` // dir | file
	Size *int64 `json:"size,omitempty"`
}

// sourceTreeDTO 是 tree 端点响应(冻结)。degraded=true 时 entries 空 + degradedReason 人读。
type sourceTreeDTO struct {
	Ref            string           `json:"ref"`
	Path           string           `json:"path"`
	Entries        []sourceEntryDTO `json:"entries"`
	Degraded       bool             `json:"degraded,omitempty"`
	DegradedReason string           `json:"degradedReason,omitempty"`
}

// sourceBlobDTO 是 blob 端点响应(冻结)。binary=true 时 content 空、不返原始字节流。
// degraded=true(code-review P6)= 克隆失败的降级响应,与「真实空文件」区分(否则前端误判空文件
// 显空白编辑器);前端据 degraded 标志而非 content==""&&size>0 推断。
type sourceBlobDTO struct {
	Ref            string `json:"ref"`
	Path           string `json:"path"`
	Size           int64  `json:"size"`
	Binary         bool   `json:"binary"`
	Truncated      bool   `json:"truncated"`
	Content        string `json:"content"`
	Degraded       bool   `json:"degraded,omitempty"`
	DegradedReason string `json:"degradedReason,omitempty"`
}

// SourceReader 抽象「按 ref 读仓库树/blob」能力(go-git 浅克隆只读;注入便于测试)。
type SourceReader interface {
	// Tree 列 ref 下 dir 的直接子项(name 升序;dir 在前)。克隆失败返回 degraded=true(不报错)。
	Tree(ctx context.Context, repoURL, token, ref, dir string) (sourceTreeDTO, error)
	// Blob 读 ref 下 file 的内容(binary 检测 + 512KB 截断)。克隆失败/文件不存在返回对应错误。
	Blob(ctx context.Context, repoURL, token, ref, file string) (sourceBlobDTO, error)
}

// 源码读取领域错误(供 handler 映射状态码)。
var (
	errSourcePathNotFound = errors.New("source: path not found")
	errSourceCloneFailed  = errors.New("source: clone failed")
)

// goGitSourceReader 是基于 go-git 的只读 SourceReader 实现。
type goGitSourceReader struct {
	// allowInsecure 仅供测试:为 true 时跳过 SSRF scheme/host 校验(放行 file:// 夹具)。
	// 生产构造(NewSourceReader)绝不设置此字段。
	allowInsecure bool
}

// NewSourceReader 构造生产 SourceReader(严格 SSRF 收口;无 init 副作用)。
func NewSourceReader() SourceReader { return goGitSourceReader{} }

// newInsecureSourceReader 仅供测试:放行 file:// 等本地夹具(跳过 SSRF 校验)。
func newInsecureSourceReader() SourceReader { return goGitSourceReader{allowInsecure: true} }

// cloneWorktree 浅克隆 repoURL@ref 到内存 worktree(只读;Depth:1 + SingleBranch)。
// ref 为空时克隆默认分支。克隆失败返回 errSourceCloneFailed(不泄漏底层错误,可能含 URL 密钥)。
func (g goGitSourceReader) cloneWorktree(ctx context.Context, repoURL, token, ref string) (billy.Filesystem, error) {
	repoURL = strings.TrimSpace(repoURL)
	if repoURL == "" {
		return nil, errSourceCloneFailed
	}
	if !g.allowInsecure && !validSourceURL(repoURL) {
		return nil, errSourceCloneFailed
	}

	fs := memfs.New()
	storer := memory.NewStorage()
	auth := &githttp.BasicAuth{Username: "git", Password: token}

	cctx, cancel := context.WithTimeout(ctx, sourceCloneTimeout)
	defer cancel()

	opts := &gogit.CloneOptions{
		URL:          repoURL,
		Auth:         auth,
		Depth:        1,
		SingleBranch: true,
		Tags:         gogit.NoTags,
	}
	if ref = strings.TrimSpace(ref); ref != "" {
		// ref 作为分支名解析(默认分支由空 ref 走);非分支 ref(tag/sha)留待 go-git 解析失败降级。
		opts.ReferenceName = plumbing.NewBranchReferenceName(ref)
	}

	if _, err := gogit.CloneContext(cctx, storer, fs, opts); err != nil {
		return nil, errSourceCloneFailed
	}
	return fs, nil
}

// Tree 列 ref 下 dir 的直接子项。dir 经 cleanRelPath 规范化(穿越拒在 handler 层)。
// 克隆失败 → degraded(空 entries,不报错,绝不 500);dir 不存在 → errSourcePathNotFound。
func (g goGitSourceReader) Tree(ctx context.Context, repoURL, token, ref, dir string) (sourceTreeDTO, error) {
	out := sourceTreeDTO{Ref: ref, Path: dir, Entries: []sourceEntryDTO{}}

	fs, err := g.cloneWorktree(ctx, repoURL, token, ref)
	if err != nil {
		// 克隆失败优雅降级:tree 空 entries + degraded 说明(不 500;GFW/不可达友好)。
		out.Degraded = true
		out.DegradedReason = "仓库克隆失败,暂时无法读取源码树"
		return out, nil
	}

	readDir := dir
	if readDir == "" {
		readDir = "."
	}
	infos, rerr := fs.ReadDir(readDir)
	if rerr != nil {
		return out, errSourcePathNotFound
	}

	entries := make([]sourceEntryDTO, 0, len(infos))
	for _, fi := range infos {
		child := fi.Name()
		full := child
		if dir != "" {
			full = dir + "/" + child
		}
		e := sourceEntryDTO{Name: child, Path: full}
		if fi.IsDir() {
			e.Type = "dir"
		} else {
			e.Type = "file"
			sz := fi.Size()
			e.Size = &sz
		}
		entries = append(entries, e)
	}
	// 稳定排序:目录在前,组内按 name 升序(确定性输出便于前端/测试)。
	sort.SliceStable(entries, func(i, j int) bool {
		if (entries[i].Type == "dir") != (entries[j].Type == "dir") {
			return entries[i].Type == "dir"
		}
		return entries[i].Name < entries[j].Name
	})
	out.Entries = entries
	return out, nil
}

// Blob 读 ref 下 file 内容:binary 检测(含 NUL 字节 → binary=true 不返字节)+ 512KB 截断。
// 克隆失败 → errSourceCloneFailed(handler 映射 degraded blob);文件不存在/为目录 → errSourcePathNotFound。
func (g goGitSourceReader) Blob(ctx context.Context, repoURL, token, ref, file string) (sourceBlobDTO, error) {
	out := sourceBlobDTO{Ref: ref, Path: file, Content: ""}

	fs, err := g.cloneWorktree(ctx, repoURL, token, ref)
	if err != nil {
		return out, errSourceCloneFailed
	}

	info, serr := fs.Stat(file)
	if serr != nil || info.IsDir() {
		return out, errSourcePathNotFound
	}
	out.Size = info.Size()

	f, oerr := fs.Open(file)
	if oerr != nil {
		return out, errSourcePathNotFound
	}
	defer func() { _ = f.Close() }()

	// 读至多 maxBlobBytes+1:多读 1 字节以判断是否超限(截断)。
	data, rerr := io.ReadAll(io.LimitReader(f, maxBlobBytes+1))
	if rerr != nil {
		return out, errSourcePathNotFound
	}
	if int64(len(data)) > maxBlobBytes {
		data = data[:maxBlobBytes]
		out.Truncated = true
	}

	// binary 检测:含 NUL 字节即视为二进制(不返原始字节流,前端显「不可预览」)。
	if bytes.IndexByte(data, 0) >= 0 {
		out.Binary = true
		out.Content = ""
		return out, nil
	}
	out.Content = string(data)
	return out, nil
}

// validSourceURL 对仓库地址做 SSRF 收口(生产路径),复用 2-5 同款策略:
// 仅 http/https;拒云元数据/链路本地/回环;私网放行(自托管内网 Git 友好)。
func validSourceURL(repoURL string) bool {
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
		return !blockedSourceIP(ip)
	}
	addrs, err := net.LookupIP(host)
	if err != nil || len(addrs) == 0 {
		return true // 留给 clone 路径(会降级);不在此误拒临时 DNS 抖动。
	}
	for _, ip := range addrs {
		if blockedSourceIP(ip) {
			return false
		}
	}
	return true
}

// blockedSourceIP 判定 IP 是否落在禁止区:回环、链路本地(含云元数据)、未指定。
func blockedSourceIP(ip net.IP) bool {
	return ip.IsLoopback() ||
		ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() ||
		ip.IsUnspecified()
}

// cleanRelPath 把请求 path 规范化为相对仓库根的安全路径;穿越(..、绝对、越根)返回 ok=false。
// 空/"."/"/" 归一为空串(仓库根)。前导/尾随斜杠剥除;反斜杠拒(防 Windows 风格穿越)。
func cleanRelPath(p string) (string, bool) {
	p = strings.TrimSpace(p)
	if p == "" {
		return "", true
	}
	if strings.Contains(p, "\\") || strings.Contains(p, "\x00") {
		return "", false
	}
	// 绝对路径拒(越出仓库根)。
	if strings.HasPrefix(p, "/") {
		return "", false
	}
	cleaned := path.Clean(p)
	if cleaned == "." {
		return "", true
	}
	// path.Clean 后若仍以 .. 开头(或恰为 ..),即穿越根 → 拒。
	if cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return "", false
	}
	return cleaned, true
}

// sourceDeps 聚合 source 端点所需服务(复用已注入 projects + vault + 注入的 SourceReader)。
type sourceDeps struct {
	projects project.Service
	vault    vault.Vault
	reader   SourceReader
}

// resolveSourceContext 取项目(repoUrl + 默认分支 + 凭据 token)与规范化 ref/path。
// 项目不存在 → 404 project_not_found;path 穿越 → 400 invalid_path。已写错误响应时返回 ok=false。
func (d sourceDeps) resolveSourceContext(w http.ResponseWriter, r *http.Request) (repoURL, token, ref, cleanPath string, ok bool) {
	id := chi.URLParam(r, "id")
	proj, err := d.projects.Get(r.Context(), id)
	if err != nil {
		if errors.Is(err, project.ErrNotFound) {
			writeError(w, http.StatusNotFound, "project_not_found", "项目不存在")
			return "", "", "", "", false
		}
		writeError(w, http.StatusInternalServerError, "internal", "服务器内部错误")
		return "", "", "", "", false
	}

	ref = strings.TrimSpace(r.URL.Query().Get("ref"))
	if ref == "" {
		ref = proj.DefaultBranch // ref 默认项目默认分支
	}

	cleanPath, valid := cleanRelPath(r.URL.Query().Get("path"))
	if !valid {
		writeError(w, http.StatusBadRequest, "invalid_path", "路径非法或越出仓库根")
		return "", "", "", "", false
	}

	// 取仓库凭据(进程内取用即弃);取不到不致命 → 空 token 尝试(私有仓库会走克隆失败降级)。
	token = ""
	if d.vault != nil && strings.TrimSpace(proj.CredentialID) != "" {
		if t, terr := d.vault.Get(proj.CredentialID); terr == nil {
			token = t
		}
	}
	return proj.RepoURL, token, ref, cleanPath, true
}

// makeSourceTreeHandler 返回 GET /api/projects/{id}/source/tree?ref=&path= handler(认证;只读)。
// 项目不存在 404;穿越 400;克隆失败 → 200 degraded(空树,绝不 500);dir 不存在 404 path_not_found。
func makeSourceTreeHandler(d sourceDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if d.projects == nil || d.reader == nil {
			writeError(w, http.StatusServiceUnavailable, "internal", "源码读取所需服务未初始化")
			return
		}
		repoURL, token, ref, cleanPath, ok := d.resolveSourceContext(w, r)
		if !ok {
			return
		}

		tree, err := d.reader.Tree(r.Context(), repoURL, token, ref, cleanPath)
		if err != nil {
			if errors.Is(err, errSourcePathNotFound) {
				writeError(w, http.StatusNotFound, "path_not_found", "路径不存在")
				return
			}
			writeError(w, http.StatusInternalServerError, "internal", "服务器内部错误")
			return
		}
		// ref 回填规范化值(默认分支补全后)。
		tree.Ref = ref
		writeJSON(w, http.StatusOK, tree)
	}
}

// makeSourceBlobHandler 返回 GET /api/projects/{id}/source/blob?ref=&path= handler(认证;只读)。
// 项目不存在 404;穿越 400;克隆失败 → 200 degraded blob(binary=false,content 空,绝不 500);
// 文件不存在 404 path_not_found;binary → binary=true 不返字节;超 512KB → truncated=true。
func makeSourceBlobHandler(d sourceDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if d.projects == nil || d.reader == nil {
			writeError(w, http.StatusServiceUnavailable, "internal", "源码读取所需服务未初始化")
			return
		}
		repoURL, token, ref, cleanPath, ok := d.resolveSourceContext(w, r)
		if !ok {
			return
		}
		if cleanPath == "" {
			writeError(w, http.StatusBadRequest, "invalid_path", "blob 需指定文件路径")
			return
		}

		blob, err := d.reader.Blob(r.Context(), repoURL, token, ref, cleanPath)
		if err != nil {
			switch {
			case errors.Is(err, errSourcePathNotFound):
				writeError(w, http.StatusNotFound, "path_not_found", "文件不存在")
			case errors.Is(err, errSourceCloneFailed):
				// 克隆失败优雅降级:200 + degraded 标志(code-review P6:与「真实空文件」区分,
				// 前端据此显「源码暂不可读」而非空白编辑器;与 tree degraded 一致)。绝不 500。
				writeJSON(w, http.StatusOK, sourceBlobDTO{
					Ref: ref, Path: cleanPath, Content: "",
					Degraded: true, DegradedReason: "仓库克隆失败,暂时无法读取该文件",
				})
			default:
				writeError(w, http.StatusInternalServerError, "internal", "服务器内部错误")
			}
			return
		}
		blob.Ref = ref
		writeJSON(w, http.StatusOK, blob)
	}
}
