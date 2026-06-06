package cron

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/huangchengsir/pipewright/internal/storetest"
)

func testDB(t *testing.T) *sql.DB {
	return storetest.OpenDB(t)
}

func seedProject(t *testing.T, db *sql.DB) string {
	t.Helper()
	now := time.Now().UTC().Format(time.RFC3339)
	cred := uuid.NewString()
	if _, err := db.Exec(
		`INSERT INTO credentials (id, name, type, scope, ciphertext, masked_value, created_at, updated_at)
		 VALUES (?, 'c', 'git_token', '', X'00', 'm', ?, ?)`, cred, now, now); err != nil {
		t.Fatalf("seed cred: %v", err)
	}
	pid := uuid.NewString()
	if _, err := db.Exec(
		`INSERT INTO projects (id, name, repo_url, default_branch, credential_id, created_at, updated_at)
		 VALUES (?, 'p', 'https://gitee.com/a/b.git', 'main', ?, ?, ?)`, pid, cred, now, now); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	return pid
}

func TestServiceSaveGetRoundTrip(t *testing.T) {
	db := testDB(t)
	svc := NewService(db)
	pid := seedProject(t, db)
	ctx := context.Background()

	// 默认无配置 → 禁用空。
	got, err := svc.Get(ctx, pid)
	if err != nil || got.Enabled || got.Expression != "" {
		t.Fatalf("默认应为禁用空配置;got=%+v err=%v", got, err)
	}

	saved, err := svc.Save(ctx, pid, SaveInput{Expression: "30 2 * * *", Branch: "main", Enabled: true})
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	if saved.Expression != "30 2 * * *" || saved.Branch != "main" || !saved.Enabled {
		t.Errorf("回读不符:%+v", saved)
	}

	// upsert 覆盖。
	if _, err := svc.Save(ctx, pid, SaveInput{Expression: "0 9 * * 1", Branch: "release", Enabled: false}); err != nil {
		t.Fatalf("Save2: %v", err)
	}
	g2, _ := svc.Get(ctx, pid)
	if g2.Expression != "0 9 * * 1" || g2.Enabled {
		t.Errorf("upsert 未覆盖:%+v", g2)
	}
}

func TestServiceSaveValidation(t *testing.T) {
	db := testDB(t)
	svc := NewService(db)
	pid := seedProject(t, db)
	ctx := context.Background()

	if _, err := svc.Save(ctx, pid, SaveInput{Expression: "bogus", Enabled: true}); !errors.Is(err, ErrInvalidExpression) {
		t.Errorf("非法表达式应 ErrInvalidExpression,得 %v", err)
	}
	if _, err := svc.Save(ctx, pid, SaveInput{Expression: "", Enabled: true}); !errors.Is(err, ErrEnabledNeedsExpression) {
		t.Errorf("启用空表达式应 ErrEnabledNeedsExpression,得 %v", err)
	}
	// 禁用 + 空表达式 → 允许(清空)。
	if _, err := svc.Save(ctx, pid, SaveInput{Enabled: false}); err != nil {
		t.Errorf("禁用空配置应允许:%v", err)
	}
}

func TestServiceSaveProjectNotFound(t *testing.T) {
	svc := NewService(testDB(t))
	if _, err := svc.Save(context.Background(), "nope", SaveInput{Expression: "* * * * *", Enabled: true}); !errors.Is(err, ErrProjectNotFound) {
		t.Errorf("项目不存在应 ErrProjectNotFound,得 %v", err)
	}
}

func TestListEnabled(t *testing.T) {
	db := testDB(t)
	svc := NewService(db)
	ctx := context.Background()
	p1 := seedProject(t, db)
	p2 := seedProject(t, db)

	_, _ = svc.Save(ctx, p1, SaveInput{Expression: "*/5 * * * *", Branch: "main", Enabled: true})
	_, _ = svc.Save(ctx, p2, SaveInput{Expression: "0 0 * * *", Branch: "", Enabled: false}) // 禁用

	entries, err := svc.ListEnabled(ctx)
	if err != nil {
		t.Fatalf("ListEnabled: %v", err)
	}
	if len(entries) != 1 || entries[0].ProjectID != p1 {
		t.Errorf("仅启用的 p1 应被列出,得 %+v", entries)
	}
}
