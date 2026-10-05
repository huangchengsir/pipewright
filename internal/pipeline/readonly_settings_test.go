package pipeline

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/huangchengsir/pipewright/internal/storetest"
)

func TestLoadExistingSettingsNoDefaultsOrCredentialDependency(t *testing.T) {
	storetest.SkipIfMySQL(t)
	db := storetest.OpenDB(t)
	id := seedSettingsProject(t, db)
	if _, err := LoadExistingSettings(context.Background(), db, id); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("missing settings: %v", err)
	}
	const stamp = "2026-10-05T00:00:00Z"
	if _, err := db.Exec(`INSERT INTO pipeline_settings (project_id,build_json,environments_json,steps_json,created_at,updated_at) VALUES (?, '{}', '[{"name":"unused","envVars":[{"key":"SECRET_UNUSED","secret":true,"credentialId":"deleted"}]}]', '[]', ?, ?)`, id, stamp, stamp); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`ALTER TABLE credentials RENAME TO broken_credentials`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`PRAGMA query_only=ON`); err != nil {
		t.Fatal(err)
	}
	st, err := LoadExistingSettings(context.Background(), db, id)
	if err != nil || st == nil || len(st.Environments) != 1 || st.Environments[0].EnvVars[0].CredentialID != "deleted" || st.UpdatedAt.Format("2006-01-02T15:04:05Z07:00") != stamp {
		t.Fatalf("read-only settings = %+v, %v", st, err)
	}
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM pipeline_settings`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("defaults created: %d, %v", count, err)
	}
}

func TestExecutionValidationUsesActualArtifactPath(t *testing.T) {
	for _, tc := range []struct {
		name  string
		build BuildConfig
		code  string
	}{
		{"image_toolchain_unused", BuildConfig{Model: BuildModelToolchain, ArtifactType: ArtifactImage}, ""},
		{"file_toolchain_needed", BuildConfig{ArtifactType: ArtifactJAR}, "toolchain_incomplete"},
		{"invalid_artifact", BuildConfig{ArtifactType: "unsupported"}, "build_invalid"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			found := ""
			for _, issue := range ValidateExecutionBuild(tc.build) {
				if issue.Severity == SeverityError {
					found = issue.Code
				}
			}
			if found != tc.code {
				t.Fatalf("error %q want %q", found, tc.code)
			}
		})
	}
	if issues := ValidateExecutionScript(PipelineStep{Image: "alpine", Commands: []string{"echo ok"}, Env: []BuildVar{{Secret: true}}}); len(issues) != 0 {
		t.Fatal(issues)
	}
	if issues := ValidateExecutionScript(PipelineStep{}); len(issues) != 1 || issues[0].Code != "script_incomplete" {
		t.Fatal(issues)
	}
}
