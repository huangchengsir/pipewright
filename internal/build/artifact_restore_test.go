package build

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/huangchengsir/pipewright/internal/artifactstore"
	"github.com/huangchengsir/pipewright/internal/run"
)

// TestRestorePriorArtifactsJar 验证跨阶段产物传递:上游归档的 jar 真字节按 workspacePath 恢复到
// 下游阶段的新工作区原相对路径(backend/target/x.jar),使被拆到下游的 build_image 能 COPY 到。
func TestRestorePriorArtifactsJar(t *testing.T) {
	st, err := artifactstore.New(t.TempDir())
	if err != nil {
		t.Fatalf("artifactstore: %v", err)
	}
	jarBytes := []byte("PK-fake-jar-bytes-0.1.0")
	key, _, err := st.Put(bytes.NewReader(jarBytes))
	if err != nil {
		t.Fatalf("Put: %v", err)
	}

	b := &Builder{
		artStore: st,
		artifactLister: func(_ context.Context, _ string) ([]run.Artifact, error) {
			return []run.Artifact{{
				Name:      "aireboot-app.jar",
				Type:      run.ArtifactJar,
				Reference: key,
				Metadata: map[string]any{
					"stored": true, "format": "file",
					"workspacePath": "backend/target/app.jar",
				},
			}}, nil
		},
	}

	ws := t.TempDir()
	b.restorePriorArtifacts(context.Background(), &run.Run{ID: "r1"}, ws, &fakeReporter{})

	got, rerr := os.ReadFile(filepath.Join(ws, "backend", "target", "app.jar"))
	if rerr != nil {
		t.Fatalf("产物未恢复到 backend/target/app.jar: %v", rerr)
	}
	if !bytes.Equal(got, jarBytes) {
		t.Fatalf("恢复的字节不一致: %q", got)
	}
}

// TestRestorePriorArtifactsNoLister 验证未注入 lister / 制品库时为安全 no-op(向后兼容,首阶段)。
func TestRestorePriorArtifactsNoLister(t *testing.T) {
	b := &Builder{} // 无 artStore / artifactLister
	ws := t.TempDir()
	// 不应 panic、不应写任何文件。
	b.restorePriorArtifacts(context.Background(), &run.Run{ID: "r1"}, ws, &fakeReporter{})
	entries, _ := os.ReadDir(ws)
	if len(entries) != 0 {
		t.Fatalf("无 lister 时不应改动工作区,实际有 %d 项", len(entries))
	}
}

// TestRestorePriorArtifactsRejectsEscape 验证越界 workspacePath(../ 逃逸)被拒绝,不写出工作区。
func TestRestorePriorArtifactsRejectsEscape(t *testing.T) {
	st, _ := artifactstore.New(t.TempDir())
	key, _, _ := st.Put(bytes.NewReader([]byte("x")))
	b := &Builder{
		artStore: st,
		artifactLister: func(_ context.Context, _ string) ([]run.Artifact, error) {
			return []run.Artifact{{
				Name: "evil", Reference: key,
				Metadata: map[string]any{"stored": true, "format": "file", "workspacePath": "../escape.txt"},
			}}, nil
		},
	}
	ws := t.TempDir()
	b.restorePriorArtifacts(context.Background(), &run.Run{ID: "r1"}, ws, &fakeReporter{})
	if _, err := os.Stat(filepath.Join(filepath.Dir(ws), "escape.txt")); err == nil {
		t.Fatal("越界路径不应写出工作区")
	}
}
