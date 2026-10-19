package buildcache

import (
	"archive/tar"
	"bufio"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// tar.go 是缓存归档的 tar.gz 读写,带严格的解包防穿越(Zip Slip 防护)。
//
// 写:遍历 workspace 下指定 relPaths(目录递归),把每个文件/目录/符号链接以「相对工作区根」
// 的路径名写进 tar.gz。读:把归档解到目标工作区,逐条校验解出路径仍落在工作区内(拒 ../、绝对路径)。

// writeTarGz 把 workspace 下 relPaths(已 clean、相对工作区根)打包成 tar.gz 写入 w。
// 目录递归;符号链接存为 symlink 头(不跟随,避免把链接目标整棵打进来)。relPaths 里不存在的
// 路径跳过(首次构建某缓存目录可能还没生成)。ctx 取消即中止。
func writeTarGz(ctx context.Context, w io.Writer, workspace string, relPaths []string) (int64, error) {
	gz := gzip.NewWriter(w)
	tw := tar.NewWriter(gz)
	var payload int64

	for _, rel := range relPaths {
		if err := ctx.Err(); err != nil {
			_ = tw.Close()
			_ = gz.Close()
			return 0, err
		}
		// Walk does not follow links, but a configured root can have link parents.
		if hasSymlinkComponent(workspace, rel) {
			continue
		}
		root := filepath.Join(workspace, rel)
		if _, err := os.Lstat(root); err != nil {
			continue // 不存在 → 跳过(best-effort)
		}
		walkErr := filepath.Walk(root, func(p string, fi os.FileInfo, err error) error {
			if err != nil {
				return nil // 单项不可读 → 跳过,不让整次打包失败
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			default:
			}
			name, rerr := filepath.Rel(workspace, p)
			if rerr != nil {
				return nil
			}
			name = filepath.ToSlash(name)

			var link string
			if fi.Mode()&os.ModeSymlink != 0 {
				if l, lerr := os.Readlink(p); lerr == nil {
					link = l
				}
			}
			hdr, herr := tar.FileInfoHeader(fi, link)
			if herr != nil {
				return nil
			}
			hdr.Name = name
			if fi.IsDir() {
				hdr.Name += "/"
			}
			if err := tw.WriteHeader(hdr); err != nil {
				return err
			}
			if fi.Mode().IsRegular() {
				f, oerr := os.Open(p)
				if oerr != nil {
					return oerr
				}
				n, cerr := io.Copy(tw, f)
				_ = f.Close()
				if cerr != nil {
					return cerr
				}
				payload += n
			}
			return nil
		})
		if walkErr != nil {
			_ = tw.Close()
			_ = gz.Close()
			return 0, walkErr
		}
	}

	if err := tw.Close(); err != nil {
		_ = gz.Close()
		return 0, err
	}
	return payload, gz.Close()
}

// Lstat each component rather than following a configured symlink into another tree.
func hasSymlinkComponent(workspace, rel string) bool {
	p := workspace
	for _, part := range strings.Split(filepath.Clean(rel), string(filepath.Separator)) {
		p = filepath.Join(p, part)
		fi, err := os.Lstat(p)
		if err != nil || fi.Mode()&os.ModeSymlink != 0 {
			return true
		}
	}
	return false
}

// extractTarGz 把 r 的 tar.gz 解到 dest 工作区。每条目录条目严校解出路径仍在 dest 内(防穿越)。
// 符号链接解为 symlink(其 target 不在解包时跟随;仅恢复链接本身,与打包对称)。ctx 取消即中止。
func extractTarGz(ctx context.Context, r io.Reader, dest string) (int64, error) {
	compressed := bufio.NewReader(r)
	gz, err := gzip.NewReader(compressed)
	if err != nil {
		return 0, err
	}
	defer func() { _ = gz.Close() }()
	gz.Multistream(false)
	tr := tar.NewReader(gz)

	destAbs, err := filepath.Abs(dest)
	if err != nil {
		return 0, err
	}
	var payload int64

	for {
		select {
		case <-ctx.Done():
			return 0, ctx.Err()
		default:
		}
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return 0, err
		}
		target, terr := safeJoin(destAbs, hdr.Name)
		if terr != nil {
			return 0, terr // 穿越 → 整次解包失败(Restore 调用方当未命中处理)
		}
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, fileMode(hdr.Mode, 0o755)); err != nil {
				return 0, err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return 0, err
			}
			if err := writeFileFromTar(tr, target, fileMode(hdr.Mode, 0o644)); err != nil {
				return 0, err
			}
			payload += hdr.Size
		case tar.TypeSymlink:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return 0, err
			}
			_ = os.Remove(target) // 覆盖已存在的同名链接
			// symlink target 不校验(容器内才解引用;此处仅恢复链接本身,与打包对称)。
			if err := os.Symlink(hdr.Linkname, target); err != nil {
				return 0, err
			}
		default:
			// 其它类型(块设备/管道等)缓存场景不该出现,跳过。
		}
	}
	// tar EOF can precede the gzip trailer. Validate it before claiming a hit.
	if err := drainTarPadding(ctx, gz); err != nil {
		return 0, err
	}
	if _, err := compressed.Peek(1); !errors.Is(err, io.EOF) {
		return 0, errors.New("buildcache: unexpected trailing gzip data")
	}
	return payload, ctx.Err()
}

// Generated archives have no trailing payload. Allow only bounded zero padding.
func drainTarPadding(ctx context.Context, r io.Reader) error {
	remaining := 64 * 1024
	var buf [4096]byte
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		n, err := r.Read(buf[:])
		remaining -= n
		if remaining < 0 {
			return errors.New("buildcache: excessive tar padding")
		}
		for _, b := range buf[:n] {
			if b != 0 {
				return errors.New("buildcache: unexpected tar padding")
			}
		}
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		if n == 0 {
			return io.ErrNoProgress
		}
	}
}

// writeFileFromTar 把 tar 当前条目内容写到 target(覆盖)。
func writeFileFromTar(tr io.Reader, target string, mode os.FileMode) error {
	f, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(f, tr); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}

// safeJoin 把归档内条目名拼到 destAbs 下,并校验结果仍在 destAbs 内(防 ../ / 绝对路径穿越)。
func safeJoin(destAbs, name string) (string, error) {
	clean := filepath.Clean("/" + filepath.FromSlash(name)) // 锚到根后 Clean,吞掉前导 ../
	joined := filepath.Join(destAbs, clean)
	rel, err := filepath.Rel(destAbs, joined)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("buildcache: archive entry escapes workspace: %q", name)
	}
	return joined, nil
}

// fileMode 把 tar 头里的 mode(int64)转 os.FileMode,非法/为 0 时用 fallback。
func fileMode(mode int64, fallback os.FileMode) os.FileMode {
	m := os.FileMode(mode).Perm()
	if m == 0 {
		return fallback
	}
	return m
}
