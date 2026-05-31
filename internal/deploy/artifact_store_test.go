package deploy

import (
	"bytes"
	"context"
	"database/sql"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/huangchengsir/pipewright/internal/artifactstore"
	"github.com/huangchengsir/pipewright/internal/run"
)

// seedSuccessRunWithStoredArtifact 像 seedSuccessRunWithArtifact,但产物带制品库句柄 + metadata
// (Story 8-16:reference=storeKey,stored=true)。
func seedSuccessRunWithStoredArtifact(t *testing.T, db *sql.DB, rsvc run.Service, artType, storeKey string, meta map[string]any) (string, string) {
	t.Helper()
	runID, _ := seedSuccessRunWithArtifact(t, db, rsvc, artType, "placeholder")
	art, err := rsvc.AddArtifact(context.Background(), run.Artifact{
		RunID: runID, Type: artType, Name: "shop", Reference: storeKey, Metadata: meta,
	})
	if err != nil {
		t.Fatalf("AddArtifact stored: %v", err)
	}
	return runID, art.ID
}

// 制品库支撑的 jar 部署:目标机落的是**真 jar 字节**(经 Upload),不是占位 reference 串。
func TestDeployStoredJarUploadsRealBytes(t *testing.T) {
	db := testDB(t)
	rsvc := run.New(db)
	store, err := artifactstore.New(t.TempDir())
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	jarBytes := []byte("PK\x03\x04 REAL jar payload \x00\xff\xfe")
	key, _, err := store.Put(bytes.NewReader(jarBytes))
	if err != nil {
		t.Fatalf("Put: %v", err)
	}

	tgt := &stubTarget{}
	srv := seedServer(t, tgt, "web-1")
	runID, artID := seedSuccessRunWithStoredArtifact(t, db, rsvc, run.ArtifactJar, key,
		map[string]any{"stored": true, "format": "file", "filename": "app.jar"})

	svc := New(tgt, rsvc, WithArtifactStore(store))
	base := "/srv/app-" + uuid.NewString()[:6]
	res, err := svc.Deploy(context.Background(), DeployInput{
		RunID: runID, ArtifactID: artID, ServerIDs: []string{srv.ID},
		Config: map[string]string{"releaseBase": base},
	})
	if err != nil {
		t.Fatalf("Deploy: %v", err)
	}
	if res[0].Status != run.TargetSuccess {
		t.Fatalf("status = %s (msg %q), want success", res[0].Status, res[0].Message)
	}
	// 目标机发布目录里应被 Upload 了**真 jar 字节**(非 reference 串占位)。
	wantPath := base + "/releases/" + runID + "/app.jar"
	got, ok := tgt.uploads[wantPath]
	if !ok {
		t.Fatalf("未在 %s 上传产物;uploads=%v", wantPath, keysOf(tgt.uploads))
	}
	if !bytes.Equal(got, jarBytes) {
		t.Fatalf("上传的不是真 jar 字节")
	}
	// 且绝不应再走旧的「base64 写 reference 串」占位路径。
	for _, c := range tgt.calls {
		if len(c) >= 3 && strings.Contains(strings.Join(c, " "), "base64 -d") {
			t.Fatalf("制品库产物不应再用 base64 占位写入:%v", c)
		}
	}
}

// 制品库支撑的 dist 部署:上传 tar.gz 并远端解包(命令含 tar -xzf)。
func TestDeployStoredDistUploadsAndUntars(t *testing.T) {
	db := testDB(t)
	rsvc := run.New(db)
	store, _ := artifactstore.New(t.TempDir())
	key, _, _ := store.Put(bytes.NewReader([]byte("\x1f\x8b fake tar.gz bytes")))

	tgt := &stubTarget{}
	srv := seedServer(t, tgt, "web-1")
	runID, artID := seedSuccessRunWithStoredArtifact(t, db, rsvc, run.ArtifactDist, key,
		map[string]any{"stored": true, "format": "tar.gz"})

	svc := New(tgt, rsvc, WithArtifactStore(store))
	base := "/srv/web-" + uuid.NewString()[:6]
	res, err := svc.Deploy(context.Background(), DeployInput{
		RunID: runID, ArtifactID: artID, ServerIDs: []string{srv.ID},
		Config: map[string]string{"releaseBase": base},
	})
	if err != nil {
		t.Fatalf("Deploy: %v", err)
	}
	if res[0].Status != run.TargetSuccess {
		t.Fatalf("status = %s (msg %q), want success", res[0].Status, res[0].Message)
	}
	// 应上传了 tar.gz。
	tarPath := base + "/releases/" + runID + "/.pw-artifact.tar.gz"
	if _, ok := tgt.uploads[tarPath]; !ok {
		t.Fatalf("未上传 dist tar.gz 到 %s", tarPath)
	}
	// 远端应解包(命令序列含 tar -xzf)。
	var sawUntar bool
	for _, c := range tgt.calls {
		if len(c) >= 2 && c[0] == "tar" && c[1] == "-xzf" {
			sawUntar = true
		}
	}
	if !sawUntar {
		t.Fatalf("dist 部署应在远端 tar -xzf 解包;calls=%v", tgt.calls)
	}
}

func keysOf(m map[string][]byte) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
