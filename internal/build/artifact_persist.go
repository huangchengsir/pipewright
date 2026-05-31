package build

// artifact_persist.go 把构建产出的 jar 文件 / dist 目录的**真字节**拷进制品库(Story 8-16 / FR-8-16),
// 使产物在临时工作区销毁后仍可被部署取用。无制品库注入(b.artStore==nil)时保持旧行为(reference=文件名,
// 占位),向后兼容。
//
//   - jar : 文件原样字节 → store.Put → reference=storeKey;metadata 记 filename(部署落盘名)、format=file。
//   - dist: 目录打成 tar.gz 字节流 → store.Put → reference=storeKey;metadata 记 format=tar.gz(部署远端解包)。
//
// 制品库句柄(sha256)落入 run_artifacts.reference;部署侧据 metadata.stored 判定走「取真字节上传」而非占位。

import (
	"archive/tar"
	"compress/gzip"
	"io"
	"os"
	"path/filepath"

	"github.com/huangchengsir/pipewright/internal/run"
)

// storeJarBytes 把 jar 文件字节存入制品库,改写 art.Reference 为存储句柄并补 metadata。
// store 为 nil 或存储失败 → 不改 art(保持旧占位行为),返回是否已存。
func (b *Builder) storeJarBytes(art *run.Artifact, jarPath string, onLine func(stream, line string)) bool {
	if b.artStore == nil {
		return false
	}
	f, err := os.Open(jarPath)
	if err != nil {
		onLine(streamStderr, "制品库:打开 jar 失败(降级占位):"+err.Error())
		return false
	}
	defer func() { _ = f.Close() }()

	key, size, err := b.artStore.Put(f)
	if err != nil {
		onLine(streamStderr, "制品库:存 jar 失败(降级占位):"+err.Error())
		return false
	}
	filename := filepath.Base(jarPath)
	art.Reference = key
	art.SizeBytes = size
	if art.Metadata == nil {
		art.Metadata = map[string]any{}
	}
	art.Metadata["stored"] = true
	art.Metadata["format"] = "file"
	art.Metadata["filename"] = filename
	onLine(streamStdout, "制品库:已归档 jar "+filename+"(句柄 "+key[:12]+"…)")
	return true
}

// storeDistDir 把 dist 目录打成 tar.gz 字节流存入制品库,改写 art.Reference 并补 metadata。
// store 为 nil 或失败 → 不改 art(保持旧占位)。tar 用流式管道,大目录不占额外内存/磁盘临时。
func (b *Builder) storeDistDir(art *run.Artifact, dirPath string, onLine func(stream, line string)) bool {
	if b.artStore == nil {
		return false
	}
	pr, pw := io.Pipe()
	go func() {
		// 把 dirPath 下内容打成 tar.gz 写入管道(出错经 CloseWithError 传到读端)。
		pw.CloseWithError(tarGzDir(dirPath, pw))
	}()

	key, size, err := b.artStore.Put(pr)
	_ = pr.Close()
	if err != nil {
		onLine(streamStderr, "制品库:存 dist(tar.gz)失败(降级占位):"+err.Error())
		return false
	}
	art.Reference = key
	art.SizeBytes = size
	if art.Metadata == nil {
		art.Metadata = map[string]any{}
	}
	art.Metadata["stored"] = true
	art.Metadata["format"] = "tar.gz"
	onLine(streamStdout, "制品库:已归档 dist 目录为 tar.gz(句柄 "+key[:12]+"…)")
	return true
}

// tarGzDir 把 root 目录下的内容(不含 root 本身这层)打成 tar.gz 写入 w。
// 仅打普通文件与目录(跳过符号链接等特殊文件,避免逃逸/不可移植);路径用相对 root 的斜杠路径。
func tarGzDir(root string, w io.Writer) error {
	gz := gzip.NewWriter(w)
	tw := tar.NewWriter(gz)

	walkErr := filepath.Walk(root, func(p string, fi os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if p == root {
			return nil // 不打 root 自身,只打其内容
		}
		rel, rerr := filepath.Rel(root, p)
		if rerr != nil {
			return rerr
		}
		// 只处理普通文件与目录(符号链接/设备等跳过,防逃逸 + 跨平台可移植)。
		if !fi.Mode().IsRegular() && !fi.IsDir() {
			return nil
		}
		hdr, herr := tar.FileInfoHeader(fi, "")
		if herr != nil {
			return herr
		}
		hdr.Name = filepath.ToSlash(rel)
		if fi.IsDir() {
			hdr.Name += "/"
		}
		if werr := tw.WriteHeader(hdr); werr != nil {
			return werr
		}
		if fi.IsDir() {
			return nil
		}
		f, oerr := os.Open(p)
		if oerr != nil {
			return oerr
		}
		defer func() { _ = f.Close() }()
		_, cerr := io.Copy(tw, f)
		return cerr
	})

	// 先关 tar/gz 把缓冲刷净,再返回(walk 错误优先)。
	if cerr := tw.Close(); cerr != nil && walkErr == nil {
		walkErr = cerr
	}
	if cerr := gz.Close(); cerr != nil && walkErr == nil {
		walkErr = cerr
	}
	return walkErr
}
