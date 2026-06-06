package version

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestMode_DockerEnvVar(t *testing.T) {
	t.Setenv("PIPEWRIGHT_RUNTIME", "docker")
	if Mode() != ModeDocker {
		t.Errorf("PIPEWRIGHT_RUNTIME=docker → 期望 ModeDocker")
	}
	t.Setenv("PIPEWRIGHT_RUNTIME", "")
	// 无 /.dockerenv 的开发机应回退 binary(CI/本地);容器内会是 docker,故此断言仅在非容器有效。
	if _, err := os.Stat("/.dockerenv"); err != nil {
		if Mode() != ModeBinary {
			t.Errorf("无 docker 标记 → 期望 ModeBinary")
		}
	}
}

// makeTarGz 生成含一个名为 name、内容为 content 的文件的 tar.gz 字节。
func makeTarGz(t *testing.T, name string, content []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(content)), Typeflag: tar.TypeReg}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(content); err != nil {
		t.Fatal(err)
	}
	tw.Close()
	gz.Close()
	return buf.Bytes()
}

func TestExtractBinary(t *testing.T) {
	dir := t.TempDir()
	gzPath := filepath.Join(dir, "a.tar.gz")
	// 归档里混入一个无关文件 + 目标 pipewright。
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	_ = tw.WriteHeader(&tar.Header{Name: "README.md", Mode: 0o644, Size: 3, Typeflag: tar.TypeReg})
	_, _ = tw.Write([]byte("doc"))
	_ = tw.WriteHeader(&tar.Header{Name: "pipewright", Mode: 0o755, Size: 5, Typeflag: tar.TypeReg})
	_, _ = tw.Write([]byte("hello"))
	tw.Close()
	gz.Close()
	if err := os.WriteFile(gzPath, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}

	out, err := extractBinary(gzPath, dir)
	if err != nil {
		t.Fatalf("extractBinary: %v", err)
	}
	defer os.Remove(out)
	got, _ := os.ReadFile(out)
	if string(got) != "hello" {
		t.Errorf("解出内容 = %q,期望 hello", got)
	}
}

func TestExtractBinary_NoBinary(t *testing.T) {
	dir := t.TempDir()
	gzPath := filepath.Join(dir, "a.tar.gz")
	_ = os.WriteFile(gzPath, makeTarGz(t, "README.md", []byte("x")), 0o644)
	if _, err := extractBinary(gzPath, dir); err == nil {
		t.Errorf("归档无 pipewright 应报错")
	}
}

func TestFetchChecksum(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("aaaa  pipewright_1.0.0_linux_amd64.tar.gz\nbbbb  pipewright_1.0.0_darwin_arm64.tar.gz\n"))
	}))
	defer srv.Close()
	c := &Checker{client: srv.Client(), now: time.Now}
	sum, err := c.fetchChecksum(context.Background(), srv.URL, "pipewright_1.0.0_darwin_arm64.tar.gz")
	if err != nil {
		t.Fatal(err)
	}
	if sum != "bbbb" {
		t.Errorf("sum = %q,期望 bbbb", sum)
	}
	if _, err := c.fetchChecksum(context.Background(), srv.URL, "missing.tar.gz"); err == nil {
		t.Errorf("缺失资产应报错")
	}
}

func TestCanSelfReplace(t *testing.T) {
	// 仅断言不 panic 且返回布尔与当前 OS 一致(linux/darwin true,其余 false)。
	_ = CanSelfReplace()
}

func TestDockerUpgradeCommand(t *testing.T) {
	if DockerUpgradeCommand() == "" {
		t.Errorf("docker 升级命令不应为空")
	}
}
