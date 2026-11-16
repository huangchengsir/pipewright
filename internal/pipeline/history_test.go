package pipeline

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/huangchengsir/pipewright/internal/configstate"
	"github.com/huangchengsir/pipewright/internal/vault"
)

func TestHistoryTracksChangedGraphAndCreatesUncommittedDraft(t *testing.T) {
	db := testDB(t)
	project := seedProject(t, db)
	service := New(db, WithHistoryCipher(vault.New(db, &[32]byte{1})))
	history := service.(HistoryReader)
	ctx := WithHistoryAuthor(context.Background(), "fixture-editor", HistorySourceCanvas)
	legacy, err := service.Save(ctx, project, validSpec())
	if err != nil || legacy.HistoryVersionID == "" {
		t.Fatalf("first version: %v %#v", err, legacy)
	}
	oldName := legacy.Spec.Stages[1].Name
	spec := legacy.Spec
	spec.Stages[1].Name = "Tests"
	spec.Stages[1].Jobs[0].Type = "script"
	spec.Stages[1].Jobs[0].Config = map[string]any{"image": "golang:1", "commands": "go test ./..."}
	importCtx := WithHistoryAuthor(ctx, "fixture-importer", HistorySourceImport)
	test, err := service.Save(importCtx, project, spec)
	if err != nil || test.HistoryVersionID == legacy.HistoryVersionID {
		t.Fatalf("second version: %v %#v", err, test)
	}
	spec.Stages[1].Jobs[0].Config = map[string]any{"image": "golang:1", "commands": "go test -race ./..."}
	race, err := service.Save(ctx, project, spec)
	if err != nil || race.HistoryVersionID == test.HistoryVersionID {
		t.Fatalf("third version: %v %#v", err, race)
	}
	same, err := service.Save(ctx, project, race.Spec)
	if err != nil || same.HistoryVersionID != race.HistoryVersionID {
		t.Fatalf("unchanged graph recorded a version: %v %#v", err, same)
	}
	page, err := history.ListHistory(ctx, project, 0, 2)
	if err != nil || len(page.Versions) != 2 || page.NextBefore != 2 || !page.Versions[0].Current ||
		page.Versions[1].ID != test.HistoryVersionID || page.Versions[1].Actor != "fixture-importer" ||
		page.Versions[1].Source != HistorySourceImport {
		t.Fatalf("first history page: %v %#v", err, page)
	}
	page, err = history.ListHistory(ctx, project, page.NextBefore, 2)
	if err != nil || len(page.Versions) != 1 || page.Versions[0].ID != legacy.HistoryVersionID || page.NextBefore != 0 {
		t.Fatalf("second history page: %v %#v", err, page)
	}
	detail, err := history.GetHistory(ctx, project, test.HistoryVersionID)
	if err != nil || detail.PreviousID != legacy.HistoryVersionID || !detail.DraftAvailable {
		t.Fatalf("detail: %v %#v", err, detail)
	}
	var renamed bool
	for _, change := range detail.Changes {
		for _, field := range change.Details {
			renamed = renamed || field.Field == "name" && field.Before == oldName && field.After == "Tests"
		}
	}
	if !renamed {
		t.Fatalf("rename absent from diff: %#v", detail.Changes)
	}
	draft, revision, err := history.HistoryDraft(ctx, project, test.HistoryVersionID)
	if err != nil || revision < 1 || draft.Spec.Stages[1].Jobs[0].Config["commands"] != "go test ./..." {
		t.Fatalf("expected original safe command in draft: %v %#v", err, draft)
	}
	stale := revision - 1
	if _, err = service.Save(configstate.WithMutation(ctx, &stale, true, nil), project, draft.Spec); !errors.Is(err, configstate.ErrConflict) {
		t.Fatalf("stale draft save: %v", err)
	}
	current, err := service.Get(ctx, project)
	if err != nil || current.HistoryVersionID != race.HistoryVersionID {
		t.Fatalf("draft/conflict changed live graph: %v %#v", err, current)
	}
	restored, err := service.Save(configstate.WithMutation(ctx, &revision, true, nil), project, draft.Spec)
	if err != nil || restored.HistoryVersionID == test.HistoryVersionID || restored.HistoryVersionID == race.HistoryVersionID {
		t.Fatalf("restoration should append a new version: %v %#v", err, restored)
	}
}

func TestHistoryProtectsOldValuesAndKeepsSummaryWithoutCipher(t *testing.T) {
	db := testDB(t)
	project := seedProject(t, db)
	otherProject := seedProject(t, db)
	ctx := context.Background()
	service := New(db, WithHistoryCipher(vault.New(db, &[32]byte{2})))
	history := service.(HistoryReader)

	spec := validSpec()
	if _, err := service.Get(ctx, project); err != nil {
		t.Fatal(err)
	}
	seed, err := service.Get(ctx, project)
	if err != nil {
		t.Fatal(err)
	}
	spec.Stages[0] = seed.Spec.Stages[0]
	spec.Stages[1].Jobs[0].Type = "script"
	sensitive := "historical-private-fixture"
	spec.Stages[1].Jobs[0].Config = map[string]any{"image": "node:20", "commands": "echo " + sensitive}
	first, err := service.Save(ctx, project, spec)
	if err != nil {
		t.Fatal(err)
	}
	spec = first.Spec
	spec.Stages[1].Jobs[0].Config = map[string]any{"image": "node:20", "commands": "npm ci"}
	second, err := service.Save(ctx, project, spec)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := history.GetHistory(ctx, otherProject, first.HistoryVersionID); !errors.Is(err, ErrHistoryNotFound) {
		t.Fatalf("cross-project lookup: %v", err)
	}
	detail, err := history.GetHistory(ctx, project, second.HistoryVersionID)
	if err != nil || detail.ChangeCount != 1 || len(detail.Changes[0].Details) != 0 {
		t.Fatalf("masked diff: %v %#v", err, detail)
	}
	blob, err := json.Marshal(detail)
	if err != nil || strings.Contains(string(blob), sensitive) || strings.Contains(string(blob), "npm ci") {
		t.Fatalf("history should only say configuration changed: %v %s", err, blob)
	}
	if _, _, err := history.HistoryDraft(ctx, project, first.HistoryVersionID); !errors.Is(err, ErrHistoryDraftUnavailable) {
		t.Fatalf("unsafe historical command draft: %v", err)
	}
	withoutCipher := New(db).(HistoryReader)
	detail, err = withoutCipher.GetHistory(ctx, project, second.HistoryVersionID)
	if err != nil || detail.ChangeCount != 1 || detail.DraftAvailable {
		t.Fatalf("detail without key: %v %#v", err, detail)
	}
	if _, _, err := withoutCipher.HistoryDraft(ctx, project, second.HistoryVersionID); !errors.Is(err, ErrHistoryDraftUnavailable) {
		t.Fatalf("draft without key: %v", err)
	}
	noKey := New(db)
	spec.Stages[1].Jobs[0].Name = "No-key save"
	third, err := noKey.Save(ctx, project, spec)
	if err != nil || third.HistoryVersionID == second.HistoryVersionID {
		t.Fatalf("normal changed save must work without a key: %v %#v", err, third)
	}
	if _, err := db.Exec("DELETE FROM projects WHERE id=?", project); err != nil {
		t.Fatal(err)
	}
	var versions int
	if err := db.QueryRow("SELECT COUNT(*) FROM pipeline_config_versions WHERE project_id=?", project).Scan(&versions); err != nil || versions != 0 {
		t.Fatalf("project deletion history cleanup: %v count=%d", err, versions)
	}
}

func TestHistoryDependencyDiffComparesStructuredIDs(t *testing.T) {
	before := Spec{Stages: []Stage{{ID: "one", Name: "Stage", Needs: []string{"a, b"},
		Jobs: []Job{{ID: "test", Name: "Test", Needs: []string{"a, b"}}}}}}
	after := Spec{Stages: []Stage{{ID: "one", Name: "Stage", Needs: []string{"a", "b"},
		Jobs: []Job{{ID: "test", Name: "Test", Needs: []string{"a", "b"}}}}}}
	changes := diffHistory(before, after)
	if len(changes) != 2 {
		t.Fatalf("expected both dependency changes: %#v", changes)
	}
	for _, change := range changes {
		if len(change.Details) != 1 || change.Details[0] != (HistoryFieldChange{
			Field: "needs", Before: "[\"a, b\"]", After: "[\"a\",\"b\"]",
		}) {
			t.Fatalf("ambiguous dependency display: %#v", change)
		}
	}
}

func TestHistoryDraftSupportsSafeSettingsAndDoesNotResurrectProtectedValues(t *testing.T) {
	stage := func(matrix []string, branches []string, config map[string]any) Spec {
		return Spec{Stages: []Stage{{ID: "build", Gate: true, When: When{Branches: branches, Events: []string{"manual"}},
			Matrix: map[string][]string{"version": matrix},
			Jobs:   []Job{{ID: "script", Type: "script", Config: config}},
		}}}
	}
	current := stage([]string{"1", "2"}, []string{"main"}, map[string]any{
		"timeoutSeconds": float64(60), "retries": float64(1), "localDocker": true,
		"commands": "go test -race ./...", "privateRef": "same-ref",
	})
	safe := stage([]string{"1"}, []string{"main"}, map[string]any{
		"timeoutSeconds": float64(30), "retries": float64(0), "localDocker": false,
		"commands": "go test ./...", "privateRef": "same-ref",
	})
	if !safeHistoricalDraft(safe, current) {
		t.Fatal("typed settings, literal-free commands, and current matrix values should round-trip")
	}
	safe.Stages[0].When.Branches = []string{"older-private-branch"}
	if safeHistoricalDraft(safe, current) {
		t.Fatal("older branch expression must not be returned")
	}
	safe.Stages[0].When.Branches = []string{"main"}
	safe.Stages[0].Matrix["version"] = []string{"older-private-value"}
	if safeHistoricalDraft(safe, current) {
		t.Fatal("older environment matrix value must not be returned")
	}
	safe.Stages[0].Matrix["version"] = []string{"1"}
	cfg := safe.Stages[0].Jobs[0].Config
	cfg["privateRef"] = "old-private-bind"
	cfg["serverId"] = "masked-server-ref"
	cfg["env"] = map[string]any{"PRIVATE": "old-inline-value"}
	changes := diffHistory(current, safe)
	var fields []string
	for _, change := range changes {
		if change.ID == "script" {
			fields = change.Fields
		}
	}
	if strings.Join(fields, ",") != "commands,variables,references,protected,config" || safeHistoricalDraft(safe, current) {
		t.Fatalf("protected historical changes must be masked/unavailable: %#v", fields)
	}
}
