package run

import (
	"context"
	"testing"
)

// TestAddAndListArtifacts 验证 AddArtifact 落库 + ListArtifacts 回读形状/排序。
func TestAddAndListArtifacts(t *testing.T) {
	db := testDB(t)
	svc := New(db)
	projID := seedProject(t, db)

	r, err := svc.Create(context.Background(), projID, Trigger{Type: TriggerManual, Branch: "main"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// 无产物 → 空切片(非 nil)。
	got, err := svc.ListArtifacts(context.Background(), r.ID)
	if err != nil {
		t.Fatalf("ListArtifacts(empty): %v", err)
	}
	if got == nil || len(got) != 0 {
		t.Fatalf("空产物应返回非 nil 空切片, got %#v", got)
	}

	a1, err := svc.AddArtifact(context.Background(), Artifact{
		RunID:     r.ID,
		Type:      ArtifactImage,
		Name:      "shop-api",
		Reference: "registry.example.com/acme/shop-api:a1b2c3d",
		SizeBytes: 48210432,
		Metadata:  map[string]any{"digest": "sha256:abc", "stub": true},
	})
	if err != nil {
		t.Fatalf("AddArtifact image: %v", err)
	}
	if a1.ID == "" {
		t.Fatalf("AddArtifact 应分配 id")
	}
	if a1.CreatedAt.IsZero() {
		t.Fatalf("AddArtifact 应填充 createdAt")
	}

	if _, err := svc.AddArtifact(context.Background(), Artifact{
		RunID:     r.ID,
		Type:      ArtifactDist,
		Name:      "shop-web",
		Reference: "dist/shop-web",
	}); err != nil {
		t.Fatalf("AddArtifact dist: %v", err)
	}

	got, err = svc.ListArtifacts(context.Background(), r.ID)
	if err != nil {
		t.Fatalf("ListArtifacts: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("应有 2 条产物, got %d", len(got))
	}
	if got[0].Type != ArtifactImage || got[1].Type != ArtifactDist {
		t.Fatalf("产物顺序/类型不符: %s, %s", got[0].Type, got[1].Type)
	}
	if got[0].Reference != "registry.example.com/acme/shop-api:a1b2c3d" {
		t.Fatalf("reference 不符: %q", got[0].Reference)
	}
	if got[0].SizeBytes != 48210432 {
		t.Fatalf("sizeBytes 不符: %d", got[0].SizeBytes)
	}
	if v, ok := got[0].Metadata["digest"]; !ok || v != "sha256:abc" {
		t.Fatalf("metadata digest 丢失: %#v", got[0].Metadata)
	}
	// dist 无 metadata → 空 map(非 nil)。
	if got[1].Metadata == nil {
		t.Fatalf("无 metadata 应回读为空 map 而非 nil")
	}
}

// TestAddArtifactInvalidType 验证非冻结枚举 type 被拒。
func TestAddArtifactInvalidType(t *testing.T) {
	db := testDB(t)
	svc := New(db)
	projID := seedProject(t, db)
	r, _ := svc.Create(context.Background(), projID, Trigger{Type: TriggerManual})

	if _, err := svc.AddArtifact(context.Background(), Artifact{
		RunID: r.ID, Type: "tarball", Name: "x", Reference: "y",
	}); err != ErrInvalidArtifactType {
		t.Fatalf("非法 type 应返回 ErrInvalidArtifactType, got %v", err)
	}
}

// TestListArtifactsUnknownRun 验证不存在 run → 空切片(不报错;HTTP 层据 run 存在性决定 404)。
func TestListArtifactsUnknownRun(t *testing.T) {
	db := testDB(t)
	svc := New(db)
	got, err := svc.ListArtifacts(context.Background(), "no-such-run")
	if err != nil {
		t.Fatalf("ListArtifacts(unknown): %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("不存在 run 应返回空切片, got %d", len(got))
	}
}

// TestStubRunnerSuccessEmitsArtifacts 验证桩 runner 成功路径 emit 1~2 个桩产物(metadata.stub=true)。
func TestStubRunnerSuccessEmitsArtifacts(t *testing.T) {
	db := testDB(t)
	svc := New(db)
	pool := NewWorkerPool(svc, WithConcurrency(2))
	pool.Start()
	t.Cleanup(func() { pool.Stop(context.Background()) })

	projID := seedProject(t, db)
	r, err := svc.Create(context.Background(), projID,
		Trigger{Type: TriggerManual, Branch: "main", Commit: "a1b2c3d4e5", Actor: "admin"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	waitForStatus(t, svc, r.ID, StatusSuccess)

	arts, err := svc.ListArtifacts(context.Background(), r.ID)
	if err != nil {
		t.Fatalf("ListArtifacts: %v", err)
	}
	if len(arts) < 1 || len(arts) > 2 {
		t.Fatalf("成功 run 应 emit 1~2 个桩产物, got %d", len(arts))
	}
	sawImage := false
	for _, a := range arts {
		if !isValidArtifactType(a.Type) {
			t.Fatalf("桩产物 type 非冻结枚举: %q", a.Type)
		}
		if a.Reference == "" {
			t.Fatalf("桩产物 reference 不应为空")
		}
		if stub, ok := a.Metadata["stub"]; !ok || stub != true {
			t.Fatalf("桩产物应诚实标注 metadata.stub=true, got %#v", a.Metadata)
		}
		if a.Type == ArtifactImage {
			sawImage = true
			// reference 应含 commit 短串(类型寻址)。
			if a.Reference == "" {
				t.Fatalf("image 桩产物 reference 为空")
			}
		}
	}
	if !sawImage {
		t.Fatalf("桩产物应含一个 image 类型")
	}
}

// TestStubRunnerFailureNoArtifacts 验证失败 run 不 emit 产物(成功路径专属)。
func TestStubRunnerFailureNoArtifacts(t *testing.T) {
	db := testDB(t)
	svc := New(db)
	pool := NewWorkerPool(svc,
		WithConcurrency(1),
		WithRunner(&StubRunner{Steps: []string{"构建镜像"}, FailAt: 0}),
	)
	pool.Start()
	t.Cleanup(func() { pool.Stop(context.Background()) })

	projID := seedProject(t, db)
	r, _ := svc.Create(context.Background(), projID, Trigger{Type: TriggerManual})
	waitForStatus(t, svc, r.ID, StatusFailed)

	arts, err := svc.ListArtifacts(context.Background(), r.ID)
	if err != nil {
		t.Fatalf("ListArtifacts: %v", err)
	}
	if len(arts) != 0 {
		t.Fatalf("失败 run 不应有产物, got %d", len(arts))
	}
}
