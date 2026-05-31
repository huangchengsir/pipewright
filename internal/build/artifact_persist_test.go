package build

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/huangchengsir/pipewright/internal/artifactstore"
	"github.com/huangchengsir/pipewright/internal/pipeline"
	"github.com/huangchengsir/pipewright/internal/run"
)

func newStoreBuilder(t *testing.T) (*Builder, *artifactstore.Store) {
	t.Helper()
	st, err := artifactstore.New(t.TempDir())
	if err != nil {
		t.Fatalf("artifactstore.New: %v", err)
	}
	b := &Builder{driver: &recordingDriver{}, artStore: st}
	return b, st
}

func TestLocateJarStoresRealBytes(t *testing.T) {
	b, st := newStoreBuilder(t)
	ws := t.TempDir()
	jarBytes := []byte("PK\x03\x04 real jar bytes \x00\xff")
	if err := os.WriteFile(filepath.Join(ws, "app.jar"), jarBytes, 0o644); err != nil {
		t.Fatalf("write jar: %v", err)
	}

	art := b.locateFileArtifact(pipeline.ArtifactJAR, "shop", ws, func(string, string) {})

	// 产物应是制品库支撑:reference=句柄,metadata.stored=true,filename 记原名。
	if art.Metadata["stored"] != true {
		t.Fatalf("jar 产物应 stored=true,metadata=%v", art.Metadata)
	}
	if art.Metadata["filename"] != "app.jar" {
		t.Fatalf("filename 应为 app.jar,got %v", art.Metadata["filename"])
	}
	// 句柄取回的字节必须 == 原 jar 字节(真存了)。
	rc, err := st.Open(art.Reference)
	if err != nil {
		t.Fatalf("Open(reference): %v", err)
	}
	defer rc.Close()
	got, _ := io.ReadAll(rc)
	if !bytes.Equal(got, jarBytes) {
		t.Fatalf("制品库取回字节与构建 jar 不一致")
	}
}

func TestLocateDistStoresTarGz(t *testing.T) {
	b, st := newStoreBuilder(t)
	ws := t.TempDir()
	distDir := filepath.Join(ws, "dist")
	if err := os.MkdirAll(filepath.Join(distDir, "assets"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	_ = os.WriteFile(filepath.Join(distDir, "index.html"), []byte("<html>hi</html>"), 0o644)
	_ = os.WriteFile(filepath.Join(distDir, "assets", "app.js"), []byte("console.log(1)"), 0o644)

	art := b.locateFileArtifact(pipeline.ArtifactDist, "shop", ws, func(string, string) {})

	if art.Metadata["stored"] != true || art.Metadata["format"] != "tar.gz" {
		t.Fatalf("dist 产物应 stored=true format=tar.gz,metadata=%v", art.Metadata)
	}
	// 取回 tar.gz 解开,应含 index.html 与 assets/app.js 的真内容。
	rc, err := st.Open(art.Reference)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer rc.Close()
	gz, err := gzip.NewReader(rc)
	if err != nil {
		t.Fatalf("gzip: %v", err)
	}
	tr := tar.NewReader(gz)
	found := map[string]string{}
	for {
		h, e := tr.Next()
		if e == io.EOF {
			break
		}
		if e != nil {
			t.Fatalf("tar: %v", e)
		}
		if h.Typeflag == tar.TypeReg {
			b, _ := io.ReadAll(tr)
			found[h.Name] = string(b)
		}
	}
	if found["index.html"] != "<html>hi</html>" {
		t.Fatalf("tar.gz 内 index.html 内容不对: %q", found["index.html"])
	}
	if found["assets/app.js"] != "console.log(1)" {
		t.Fatalf("tar.gz 内 assets/app.js 内容不对: %q", found["assets/app.js"])
	}
}

// 无制品库注入 → 保持旧占位行为(reference=文件名,无 stored 标记),向后兼容。
func TestLocateWithoutStoreKeepsPlaceholder(t *testing.T) {
	b := &Builder{driver: &recordingDriver{}} // artStore=nil
	ws := t.TempDir()
	_ = os.WriteFile(filepath.Join(ws, "app.jar"), []byte("x"), 0o644)
	art := b.locateFileArtifact(pipeline.ArtifactJAR, "shop", ws, func(string, string) {})
	if art.Reference != "app.jar" {
		t.Fatalf("无制品库应保持 reference=文件名,got %q", art.Reference)
	}
	if _, ok := art.Metadata["stored"]; ok {
		t.Fatalf("无制品库不应有 stored 标记")
	}
	_ = run.ArtifactJar
}
