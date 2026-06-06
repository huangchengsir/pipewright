package version

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"
)

// DeploymentMode 区分运行形态,决定自更新策略。
type DeploymentMode string

const (
	ModeBinary DeploymentMode = "binary" // 裸机 / install.sh 二进制 —— 可自替换 + 自重启
	ModeDocker DeploymentMode = "docker" // 容器 —— 不能替换自身镜像,给升级命令
)

// Mode 探测当前部署形态。容器内 Docker 会创建 /.dockerenv;另可经 PIPEWRIGHT_RUNTIME=docker 显式声明。
func Mode() DeploymentMode {
	if strings.EqualFold(os.Getenv("PIPEWRIGHT_RUNTIME"), "docker") {
		return ModeDocker
	}
	if _, err := os.Stat("/.dockerenv"); err == nil {
		return ModeDocker
	}
	return ModeBinary
}

// DockerUpgradeCommand 返回 Docker 部署的升级命令(compose 优先,附 docker run 兜底)。
func DockerUpgradeCommand() string {
	return "docker compose pull && docker compose up -d"
}

// CanSelfReplace 报告当前平台是否支持替换运行中的可执行文件。
// Windows 无法替换正在运行的 exe;仅 linux/darwin 支持。
func CanSelfReplace() bool {
	return runtime.GOOS == "linux" || runtime.GOOS == "darwin"
}

// ApplyBinaryUpdate 下载目标 tag 对应本平台的 release 二进制,核验校验和,原子替换当前可执行文件。
// 不重启;重启由调用方在响应已回后触发 Reexec。
func (c *Checker) ApplyBinaryUpdate(ctx context.Context, tag string) error {
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("定位当前可执行文件失败: %w", err)
	}
	exe, _ = filepath.EvalSymlinks(exe)

	// 资产名须与 .goreleaser.yaml 命名模板一致:pipewright_<版本无v>_<os>_<arch>.tar.gz
	verNoV := strings.TrimPrefix(tag, "v")
	asset := fmt.Sprintf("pipewright_%s_%s_%s.tar.gz", verNoV, runtime.GOOS, runtime.GOARCH)
	base := "https://github.com"
	if dlBase != "" {
		base = dlBase
	}
	assetURL := fmt.Sprintf("%s/%s/releases/download/%s/%s", base, c.repo, tag, asset)
	sumURL := fmt.Sprintf("%s/%s/releases/download/%s/checksums.txt", base, c.repo, tag)

	// 下载校验和清单,取本资产的期望 sha256。
	want, err := c.fetchChecksum(ctx, sumURL, asset)
	if err != nil {
		return err
	}

	// 下载 tar.gz 到临时文件(与目标同目录,确保 rename 原子且不跨文件系统)。
	dir := filepath.Dir(exe)
	tmpGz, err := os.CreateTemp(dir, ".pw-update-*.tar.gz")
	if err != nil {
		return fmt.Errorf("创建临时文件失败(目录不可写?): %w", err)
	}
	tmpGzPath := tmpGz.Name()
	defer os.Remove(tmpGzPath)

	sum := sha256.New()
	if err := c.download(ctx, assetURL, io.MultiWriter(tmpGz, sum)); err != nil {
		tmpGz.Close()
		return err
	}
	tmpGz.Close()
	if got := hex.EncodeToString(sum.Sum(nil)); got != want {
		return fmt.Errorf("校验和不匹配(期望 %s,实际 %s)", want, got)
	}

	// 解包出 pipewright 二进制到临时文件。
	newBin, err := extractBinary(tmpGzPath, dir)
	if err != nil {
		return err
	}
	defer os.Remove(newBin)

	if err := os.Chmod(newBin, 0o755); err != nil {
		return fmt.Errorf("设置可执行权限失败: %w", err)
	}
	// 原子替换:运行中的进程仍持有旧 inode,新二进制就位供下次 exec。
	if err := os.Rename(newBin, exe); err != nil {
		return fmt.Errorf("替换可执行文件失败(%s 无写权限?可改用有权限的用户运行,或手动升级): %w", exe, err)
	}
	return nil
}

// fetchChecksum 从 checksums.txt 取指定资产的 sha256。
func (c *Checker) fetchChecksum(ctx context.Context, url, asset string) (string, error) {
	var buf strings.Builder
	if err := c.download(ctx, url, &buf); err != nil {
		return "", fmt.Errorf("下载校验和失败: %w", err)
	}
	for _, line := range strings.Split(buf.String(), "\n") {
		f := strings.Fields(line)
		if len(f) == 2 && f[1] == asset {
			return f[0], nil
		}
	}
	return "", fmt.Errorf("校验和清单中无 %s", asset)
}

// download 把 url 内容写入 w,带超时与状态码校验。
func (c *Checker) download(ctx context.Context, url string, w io.Writer) error {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "pipewright/"+Version)
	resp, err := c.client.Do(req)
	if err != nil {
		return fmt.Errorf("下载失败: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("下载返回 %d: %s", resp.StatusCode, url)
	}
	if _, err := io.Copy(w, resp.Body); err != nil {
		return fmt.Errorf("写入失败: %w", err)
	}
	return nil
}

// extractBinary 从 tar.gz 解出名为 pipewright 的可执行文件到 dir 下的临时文件,返回其路径。
func extractBinary(gzPath, dir string) (string, error) {
	f, err := os.Open(gzPath)
	if err != nil {
		return "", err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return "", fmt.Errorf("解压失败: %w", err)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return "", fmt.Errorf("归档中未找到 pipewright 二进制")
		}
		if err != nil {
			return "", fmt.Errorf("解包失败: %w", err)
		}
		if hdr.Typeflag != tar.TypeReg || filepath.Base(hdr.Name) != "pipewright" {
			continue
		}
		out, err := os.CreateTemp(dir, ".pw-update-bin-*")
		if err != nil {
			return "", err
		}
		// 限制解包大小,防恶意超大归档(release 二进制约 40-50MB,给 200MB 上限)。
		if _, err := io.Copy(out, io.LimitReader(tr, 200<<20)); err != nil {
			out.Close()
			os.Remove(out.Name())
			return "", fmt.Errorf("写出二进制失败: %w", err)
		}
		out.Close()
		return out.Name(), nil
	}
}

// dlBase 允许测试把下载指向本地 stub server;生产为空时用 github.com。
var dlBase = ""

// Reexec 用新二进制替换当前进程映像(同 PID,重新绑定端口)。
// 监听 socket 在 exec 时关闭,新进程重新 bind;调用前须确保响应已发出。
func Reexec() error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	return syscall.Exec(exe, os.Args, os.Environ())
}
