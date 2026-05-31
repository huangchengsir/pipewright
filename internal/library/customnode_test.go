package library

import (
	"context"
	"errors"
	"testing"
)

// TestCustomNodeCRUD 覆盖自定义节点的建/列/取/改/删全链路 + config 快照原样回读。
func TestCustomNodeCRUD(t *testing.T) {
	db := testDB(t)
	svc := NewCustomNodeService(db)
	ctx := context.Background()

	created, err := svc.Create(ctx, CustomNodeInput{
		Name:        "前端构建",
		Description: "node 构建 + 产物收集",
		NodeType:    "templated",
		Summary:     "node:20 → dist",
		Config: map[string]any{
			"image":           "node:{{ver}}",
			"commandTemplate": "npm ci\nnpm run build",
			"params":          "ver=20",
			"artifactPath":    "dist/**",
		},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if created.ID == "" {
		t.Fatal("Create: empty id")
	}

	// config 快照应原样回读(自由 KV 不被裁剪)。
	if created.Config["image"] != "node:{{ver}}" || created.Config["commandTemplate"] != "npm ci\nnpm run build" {
		t.Fatalf("Create: config not preserved: %#v", created.Config)
	}

	// List 含刚建的节点。
	list, err := svc.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 1 || list[0].Name != "前端构建" {
		t.Fatalf("List: unexpected %#v", list)
	}

	// Get 回读。
	got, err := svc.Get(ctx, created.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.NodeType != "templated" || got.Summary != "node:20 → dist" {
		t.Fatalf("Get: unexpected %#v", got)
	}

	// Update 覆盖。
	updated, err := svc.Update(ctx, created.ID, CustomNodeInput{
		Name:     "前端构建 v2",
		NodeType: "templated",
		Config:   map[string]any{"image": "node:22"},
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if updated.Name != "前端构建 v2" || updated.Config["image"] != "node:22" {
		t.Fatalf("Update: unexpected %#v", updated)
	}

	// Delete + 二次取应 404。
	if err := svc.Delete(ctx, created.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := svc.Get(ctx, created.ID); !errors.Is(err, ErrCustomNodeNotFound) {
		t.Fatalf("Get after delete: want ErrCustomNodeNotFound, got %v", err)
	}
}

// TestCustomNodeValidation 覆盖名称/类型空校验 + 重名冲突。
func TestCustomNodeValidation(t *testing.T) {
	db := testDB(t)
	svc := NewCustomNodeService(db)
	ctx := context.Background()

	if _, err := svc.Create(ctx, CustomNodeInput{Name: "  ", NodeType: "script"}); !errors.Is(err, ErrInvalidCustomNode) {
		t.Fatalf("empty name: want ErrInvalidCustomNode, got %v", err)
	}
	if _, err := svc.Create(ctx, CustomNodeInput{Name: "x", NodeType: " "}); !errors.Is(err, ErrInvalidCustomNode) {
		t.Fatalf("empty type: want ErrInvalidCustomNode, got %v", err)
	}

	if _, err := svc.Create(ctx, CustomNodeInput{Name: "dup", NodeType: "script"}); err != nil {
		t.Fatalf("first create: %v", err)
	}
	if _, err := svc.Create(ctx, CustomNodeInput{Name: "dup", NodeType: "script"}); !errors.Is(err, ErrCustomNodeNameTaken) {
		t.Fatalf("dup name: want ErrCustomNodeNameTaken, got %v", err)
	}

	// nil config 应补 {} 而非崩。
	n, err := svc.Create(ctx, CustomNodeInput{Name: "nilcfg", NodeType: "script", Config: nil})
	if err != nil {
		t.Fatalf("nil config create: %v", err)
	}
	if n.Config == nil {
		t.Fatal("nil config: want non-nil empty map")
	}
}

// TestCustomNodeUpdateNotFound 校验更新不存在节点报 404。
func TestCustomNodeUpdateNotFound(t *testing.T) {
	db := testDB(t)
	svc := NewCustomNodeService(db)
	if _, err := svc.Update(context.Background(), "missing", CustomNodeInput{Name: "x", NodeType: "script"}); !errors.Is(err, ErrCustomNodeNotFound) {
		t.Fatalf("Update missing: want ErrCustomNodeNotFound, got %v", err)
	}
}
