// Package buildcache 是「构建依赖缓存」(build cache · P0 引擎能力)。
//
// 背景:Pipewright 的构建在隔离容器里跑(dag_stage_exec 每个 script 阶段独立克隆临时工作区 →
// docker run --rm 挂载工作区跑命令)。临时工作区即用即销,故 node_modules/.m2/.gradle 这类
// 依赖目录每次构建都要重新拉取,很慢。build cache 把指定缓存目录**跨 run 持久化**:
// 构建前 Restore 到工作区、构建后 Save 回缓存库,按 key(分支 + lockfile hash)寻址。
//
// 设计:**键寻址**(key-addressed)磁盘库 —— 每个缓存 key 经 SHA-256 折成定长 hex,
// 对应缓存库里一个 tar.gz 归档(<root>/<2 位前缀>/<keyhash>.tgz)。Save 经临时文件 + 原子
// rename 落盘(key 已存在则覆盖,即最新一次构建的依赖快照胜出)。Restore 把归档解到工作区
// 对应路径(归档内路径已相对工作区根,解包时严校防穿越)。
//
// **可靠性铁律**:缓存问题绝不让构建失败(best-effort)——Restore 失败 = 冷构建(返回 nil,
// 调用方照常构建);Save 失败 = 只记日志(返回 nil)。所以本包的公有方法对「缓存未命中 /
// 解包损坏 / 写盘失败」一律不返回致命错误,只在真正的「输入非法 / 内部 bug」时返回 error。
//
// 无外部依赖、无 init 副作用;root 由 main 注入(默认 DB 同级 cache/,
// env PIPEWRIGHT_CACHE_DIR 可改;PIPEWRIGHT_NO_BUILD_CACHE=1 全关 → main 不注入)。
package buildcache

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// 领域错误。
var (
	// ErrEmptyRoot 表示构造时缓存根目录为空。
	ErrEmptyRoot = errors.New("buildcache: empty root")
)

// lockfileNames 是参与默认 key 推导的依赖锁文件名(命中工作区里这些文件就把其内容计入 hash)。
// 锁文件变 → key 变 → 自动冷构建(依赖确实变了,旧缓存不应复用);锁文件不变 → 命中缓存。
var lockfileNames = []string{
	"package-lock.json",
	"yarn.lock",
	"pnpm-lock.yaml",
	"npm-shrinkwrap.json",
	"pom.xml",
	"build.gradle",
	"build.gradle.kts",
	"gradle.lockfile",
	"go.sum",
	"Cargo.lock",
	"composer.lock",
	"Gemfile.lock",
	"poetry.lock",
	"requirements.txt",
	"Pipfile.lock",
}

// Store 是键寻址磁盘缓存库。零依赖;Save 经临时文件 + 原子 rename(并发/中断不留半截归档)。
type Store struct {
	root string
}

// New 构造缓存库,root 不存在则创建(0700:缓存内容可能含构建中间产物,限本用户)。
func New(root string) (*Store, error) {
	root = strings.TrimSpace(root)
	if root == "" {
		return nil, ErrEmptyRoot
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		return nil, fmt.Errorf("buildcache: mkdir root: %w", err)
	}
	return &Store{root: root}, nil
}

// Restore 把 key 对应缓存归档解到 workspace 下的对应路径(归档内路径已相对工作区根)。
// 返回 (true, nil) = 命中并恢复成功(暖构建);(false, nil) = 未命中或恢复失败(冷构建,
// 不致命,调用方照常构建)。仅在 key/workspace 非法时返回 error。
//
// paths 仅用于日志/契约对齐(实际恢复哪些路径由归档内容决定,即上次 Save 的那批);
// 传 nil 也能恢复整份归档。
func (s *Store) Restore(ctx context.Context, key string, _ []string, workspace string) (bool, error) {
	if s == nil {
		return false, nil
	}
	if strings.TrimSpace(key) == "" {
		return false, errors.New("buildcache: empty key")
	}
	if strings.TrimSpace(workspace) == "" {
		return false, errors.New("buildcache: empty workspace")
	}
	archive := s.pathFor(key)
	f, err := os.Open(archive)
	if err != nil {
		return false, nil // 未命中 → 冷构建(不致命)
	}
	defer func() { _ = f.Close() }()

	if err := extractTarGz(ctx, f, workspace); err != nil {
		// 归档损坏 / 解包中途失败 → 当作未命中(冷构建);残留的半截解包由后续构建覆盖,best-effort。
		return false, nil
	}
	return true, nil
}

// Save 把 workspace 下 paths 指定的目录/文件打包成 tar.gz 存进缓存库(key 已存在则覆盖)。
// paths 为相对工作区根的路径(如 node_modules、.m2/repository);越界(.. / 绝对)路径被跳过。
// 不存在的路径跳过(首次构建可能还没生成某缓存目录,正常)。
//
// 返回 nil = 成功或 best-effort 跳过(无可缓存内容也算成功);仅在 key/workspace 非法时返回 error。
// 写盘失败由调用方决定如何记日志:本方法把 I/O 错误透出供日志,但调用方应忽略它(不阻断构建)。
func (s *Store) Save(ctx context.Context, key string, paths []string, workspace string) error {
	if s == nil {
		return nil
	}
	if strings.TrimSpace(key) == "" {
		return errors.New("buildcache: empty key")
	}
	if strings.TrimSpace(workspace) == "" {
		return errors.New("buildcache: empty workspace")
	}
	clean := cleanRelPaths(paths)
	if len(clean) == 0 {
		return nil // 无合法缓存路径 → 无操作(不致命)
	}

	dest := s.pathFor(key)
	if err := os.MkdirAll(filepath.Dir(dest), 0o700); err != nil {
		return fmt.Errorf("buildcache: mkdir shard: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(dest), ".save-*")
	if err != nil {
		return fmt.Errorf("buildcache: temp: %w", err)
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }() // 成功路径已 rename 走,Remove 无害

	if err := writeTarGz(ctx, tmp, workspace, clean); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("buildcache: write archive: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("buildcache: close temp: %w", err)
	}
	// 原子覆盖(rename 覆盖同名,Save 同 key 即最新依赖快照胜出)。
	if err := os.Rename(tmpName, dest); err != nil {
		return fmt.Errorf("buildcache: commit: %w", err)
	}
	return nil
}

// Has 报告 key 是否已有缓存归档。
func (s *Store) Has(key string) bool {
	if s == nil || strings.TrimSpace(key) == "" {
		return false
	}
	_, err := os.Stat(s.pathFor(key))
	return err == nil
}

// pathFor 把缓存 key 折成分片归档路径:<root>/<keyhash 前 2 位>/<keyhash>.tgz。
// key 经 SHA-256 → 64 位 hex,既杜绝路径穿越(key 任意字符都安全)又分片防单目录文件过多。
func (s *Store) pathFor(key string) string {
	sum := sha256.Sum256([]byte(key))
	h := hex.EncodeToString(sum[:])
	return filepath.Join(s.root, h[:2], h+".tgz")
}

// DeriveKey 推导确定性缓存 key:分支 + 工作区内命中的 lockfile 们的内容 hash(+ 可选 user 模板)。
//
//   - userKey 非空:把它作为 key 主体(用户自定义模板;调用方应已渲染好占位),仍并入 branch 与
//     lockfile hash 以保证「同模板不同分支/不同依赖」互不串缓存。
//   - userKey 空:用默认推导 = branch + 排序去重后的 lockfile (名+内容hash) 拼接再整体 hash。
//
// 同输入恒等同输出(确定性);任一 lockfile 内容变 → key 变 → 自动冷构建。
// 工作区不可读或无 lockfile 时降级为仅 branch(+userKey),仍是确定性的合法 key。
func DeriveKey(branch, userKey, workspace string) string {
	var sb strings.Builder
	sb.WriteString("v1\x00") // 版本前缀:未来缓存格式变更可改版本失效旧缓存
	sb.WriteString("branch=")
	sb.WriteString(strings.TrimSpace(branch))
	sb.WriteString("\x00")
	if u := strings.TrimSpace(userKey); u != "" {
		sb.WriteString("user=")
		sb.WriteString(u)
		sb.WriteString("\x00")
	}
	sb.WriteString("lock=")
	sb.WriteString(lockfilesDigest(workspace))

	sum := sha256.Sum256([]byte(sb.String()))
	return hex.EncodeToString(sum[:])
}

// lockfilesDigest 算工作区内命中的 lockfile 们的确定性摘要(名 + 内容 hash,按名排序去重)。
// 不递归扫整棵树(贵且不稳定):只看工作区根 + 一层常见子目录(frontend/backend/web/server/app/api)
// 下的 lockfile,覆盖单体仓库常见前后端分目录布局。无命中 → 返回固定串(仍确定性)。
func lockfilesDigest(workspace string) string {
	ws := strings.TrimSpace(workspace)
	if ws == "" {
		return "none"
	}
	dirs := []string{"."}
	dirs = append(dirs, "frontend", "backend", "web", "server", "app", "api")

	type entry struct {
		rel  string
		hash string
	}
	var entries []entry
	seen := map[string]bool{}
	for _, d := range dirs {
		for _, name := range lockfileNames {
			rel := filepath.Join(d, name)
			if seen[rel] {
				continue
			}
			full := filepath.Join(ws, rel)
			h, ok := fileContentHash(full)
			if !ok {
				continue
			}
			seen[rel] = true
			entries = append(entries, entry{rel: filepath.ToSlash(rel), hash: h})
		}
	}
	if len(entries) == 0 {
		return "none"
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].rel < entries[j].rel })
	var sb strings.Builder
	for _, e := range entries {
		sb.WriteString(e.rel)
		sb.WriteString("=")
		sb.WriteString(e.hash)
		sb.WriteString(";")
	}
	return sb.String()
}

// fileContentHash 算文件内容的 sha256 hex(目录/不可读 → false)。
func fileContentHash(path string) (string, bool) {
	fi, err := os.Stat(path)
	if err != nil || fi.IsDir() {
		return "", false
	}
	f, err := os.Open(path)
	if err != nil {
		return "", false
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", false
	}
	return hex.EncodeToString(h.Sum(nil)), true
}

// cleanRelPaths 归一缓存路径:trim、去空、剔越界(绝对路径 / 含 .. 逃出工作区)、去重保序。
func cleanRelPaths(paths []string) []string {
	out := make([]string, 0, len(paths))
	seen := map[string]bool{}
	for _, p := range paths {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		clean := filepath.Clean(p)
		if filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
			continue
		}
		if clean == "." || seen[clean] {
			continue
		}
		seen[clean] = true
		out = append(out, clean)
	}
	return out
}
