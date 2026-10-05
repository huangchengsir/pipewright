package pipeline

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/huangchengsir/pipewright/internal/storetest"
)

func TestSavedEvidenceAndReadOnlyLoad(t *testing.T) {
	storetest.SkipIfMySQL(t) // Failure injection below uses SQLite triggers.
	svc, db, id := newSvc(t)
	ctx := context.Background()
	if _, err := LoadExisting(ctx, db, id); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("missing config: %v", err)
	}
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM pipeline_configs`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("pure read created rows: %d, %v", count, err)
	}
	cfg, err := svc.Get(ctx, id)
	if err != nil || cfg.SavedAt != nil {
		t.Fatalf("default: %#v, %v", cfg, err)
	}
	if _, err := svc.Save(ctx, id, Spec{}); err == nil {
		t.Fatal("invalid save passed")
	}
	read, err := LoadExisting(ctx, db, id)
	if err != nil || read.SavedAt != nil || !read.UpdatedAt.Equal(cfg.UpdatedAt) {
		t.Fatalf("invalid save/read changed evidence: %#v, %v", read, err)
	}
	if _, err := db.Exec(`CREATE TRIGGER reject_config BEFORE UPDATE ON pipeline_configs BEGIN SELECT RAISE(ABORT, 'test failure'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Save(ctx, id, cfg.Spec); err == nil {
		t.Fatal("failed persistence passed")
	}
	read, err = LoadExisting(ctx, db, id)
	if err != nil || read.SavedAt != nil {
		t.Fatalf("failed save evidence: %#v, %v", read, err)
	}
	if _, err := db.Exec(`DROP TRIGGER reject_config`); err != nil {
		t.Fatal(err)
	}
	saved, err := svc.Save(ctx, id, cfg.Spec)
	if err != nil || saved.SavedAt == nil || !saved.SavedAt.Equal(saved.UpdatedAt) {
		t.Fatalf("saved_at must share UPDATE timestamp: %#v, %v", saved, err)
	}
	read, err = LoadExisting(ctx, db, id)
	if err != nil || read.SavedAt == nil || !read.SavedAt.Equal(*saved.SavedAt) {
		t.Fatalf("read saved evidence: %#v, %v", read, err)
	}
	if _, err := db.Exec(`UPDATE pipeline_configs SET spec_json = 'invalid' WHERE project_id = ?`, id); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadExisting(ctx, db, id); err == nil {
		t.Fatal("corruption was treated as absence")
	}
}
