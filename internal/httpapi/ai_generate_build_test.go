package httpapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/huangchengsir/pipewright/internal/auth"
	"github.com/huangchengsir/pipewright/internal/pipeline"
	"github.com/huangchengsir/pipewright/internal/project"
	"github.com/huangchengsir/pipewright/internal/store"
	"github.com/huangchengsir/pipewright/internal/storetest"
	"github.com/huangchengsir/pipewright/internal/trigger"
	"github.com/huangchengsir/pipewright/internal/vault"
)

const aiApplyBuildSecret = "ai-apply-build-LEAKMARKER-secret"

// Exercise the real router and persistence without opening a listener.
type aiApplyBuildFixture struct {
	handler      http.Handler
	db           *sql.DB
	vault        vault.Vault
	settings     pipeline.SettingsService
	projectID    string
	credentialID string
	cookies      []*http.Cookie
	csrf         string
}

func newAIApplyBuildFixture(t *testing.T) *aiApplyBuildFixture {
	t.Helper()
	st := storetest.OpenForDialect(t, store.SQLite)
	v := vault.New(st.DB, testMasterKey())
	cred, err := v.Create(vault.CreateInput{Name: "build token", Type: vault.TypeGitToken, Secret: aiApplyBuildSecret})
	if err != nil {
		t.Fatalf("create credential: %v", err)
	}
	projects := project.New(st.DB, v, stubProber{branch: "main"})
	proj, err := projects.Create(context.Background(), project.CreateInput{
		Name: "ai build preservation", RepoURL: "https://example.com/app.git", CredentialID: cred.ID,
	})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	authSvc := auth.NewService(st.DB, nil)
	if err := authSvc.Bootstrap("admin", "testpass"); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	settings := pipeline.NewSettingsService(st.DB, v)
	f := &aiApplyBuildFixture{
		db: st.DB, vault: v, settings: settings, projectID: proj.ID, credentialID: cred.ID,
		handler: New(testWebFSAuth(), authSvc, WithVault(v), WithProjects(projects),
			WithPipelines(pipeline.New(st.DB)), WithPipelineSettings(settings), WithTriggers(trigger.New(st.DB, v))),
	}
	w := f.request(http.MethodPost, "/api/auth/login", `{"username":"admin","password":"testpass"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("login status = %d, body=%s", w.Code, w.Body.String())
	}
	resp := w.Result()
	defer resp.Body.Close()
	f.cookies = resp.Cookies()
	for _, c := range f.cookies {
		if c.Name == cookieCsrf {
			f.csrf = c.Value
		}
	}
	if f.csrf == "" {
		t.Fatal("login did not return CSRF cookie")
	}
	return f
}

func (f *aiApplyBuildFixture) request(method, path, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("X-CSRF-Token", f.csrf)
	for _, c := range f.cookies {
		r.AddCookie(c)
	}
	w := httptest.NewRecorder()
	f.handler.ServeHTTP(w, r)
	return w
}

func (f *aiApplyBuildFixture) seed(t *testing.T, cache pipeline.Cache) *pipeline.Settings {
	t.Helper()
	vars := []pipeline.BuildVar{
		{Key: "BUILD_MODE", Value: "production"},
		{Key: "NPM_TOKEN", Secret: true, CredentialID: f.credentialID},
	}
	_, err := f.settings.Save(context.Background(), f.projectID, pipeline.SettingsInput{
		Build: pipeline.BuildConfig{
			Model: pipeline.BuildModelDockerfile, DockerfilePath: "old/Dockerfile",
			Toolchain:    pipeline.Toolchain{Language: "go", Version: "1.26"},
			ArtifactType: pipeline.ArtifactImage, Vars: vars, Cache: cache,
		},
		Environments: []pipeline.Environment{{
			Name: "production", TargetServerIDs: []string{"server-1"}, EnvVars: vars,
			ImageRegistry: pipeline.ImageRegistry{Type: pipeline.RegistryHarbor, URL: "https://registry.example.com", CredentialID: f.credentialID},
		}},
		Steps: []pipeline.PipelineStep{{
			Name: "package", Type: pipeline.StepTypeScript, Image: "node:22", Commands: []string{"npm ci", "npm run build"},
			Env: vars, WorkDir: "app", TimeoutSeconds: 120, Retries: 2, Resource: pipeline.Resource{CPU: "1", Memory: "512m"},
		}},
	})
	if err != nil {
		t.Fatalf("seed settings: %v", err)
	}
	before, err := f.settings.Get(context.Background(), f.projectID)
	if err != nil {
		t.Fatalf("read seeded settings: %v", err)
	}
	return before
}

func (f *aiApplyBuildFixture) readBack(t *testing.T) *pipeline.Settings {
	t.Helper()
	// A fresh service and GET ensure assertions do not inspect Save's input or return value.
	after, err := pipeline.NewSettingsService(f.db, f.vault).Get(context.Background(), f.projectID)
	if err != nil {
		t.Fatalf("read persisted settings: %v", err)
	}
	w := f.request(http.MethodGet, "/api/projects/"+f.projectID+"/pipeline/settings", "")
	if w.Code != http.StatusOK {
		t.Fatalf("settings GET status = %d, body=%s", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), aiApplyBuildSecret) {
		t.Fatal("settings GET leaked secret")
	}
	var dto settingsDTO
	if err := json.Unmarshal(w.Body.Bytes(), &dto); err != nil {
		t.Fatalf("decode settings: %v", err)
	}
	if !reflect.DeepEqual(dto, toSettingsDTO(after)) {
		t.Fatalf("settings GET differs from persisted configuration: %+v", dto)
	}
	var buildJSON, envJSON, stepsJSON string
	if err := f.db.QueryRowContext(context.Background(),
		`SELECT build_json, environments_json, steps_json FROM pipeline_settings WHERE project_id = ?`, f.projectID,
	).Scan(&buildJSON, &envJSON, &stepsJSON); err != nil {
		t.Fatalf("read stored JSON: %v", err)
	}
	for _, raw := range []string{buildJSON, envJSON, stepsJSON} {
		if strings.Contains(raw, aiApplyBuildSecret) || strings.Contains(raw, "maskedValue") || strings.Contains(raw, "maskedCredential") {
			t.Fatal("stored settings contain secret plaintext or response-only masks")
		}
	}
	return after
}

func TestAIApplyBuildPreservesStoredSettings(t *testing.T) {
	for _, tc := range []struct {
		name  string
		cache pipeline.Cache
		build aiProposalBuildDTO
	}{
		{"toolchain_enabled_cache", pipeline.Cache{Enabled: true, Paths: []string{"node_modules", ".npm"}},
			aiProposalBuildDTO{Model: "toolchain", Toolchain: aiToolchainDTO{Language: "node", Version: "22"}, ArtifactType: "dist", DockerfilePath: "generated/Dockerfile"}},
		{"dockerfile_disabled_cache_with_paths", pipeline.Cache{Enabled: false, Paths: []string{".m2/repository"}},
			aiProposalBuildDTO{Model: "dockerfile", ArtifactType: "jar", DockerfilePath: "app/Dockerfile"}},
		{"empty_cache_paths", pipeline.Cache{Enabled: false, Paths: []string{}},
			aiProposalBuildDTO{Model: "dockerfile", ArtifactType: "image", DockerfilePath: "Dockerfile"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newAIApplyBuildFixture(t)
			before := f.seed(t, tc.cache)
			wantBuild := before.Build
			wantBuild.Model = tc.build.Model
			wantBuild.Toolchain = pipeline.Toolchain{Language: tc.build.Toolchain.Language, Version: tc.build.Toolchain.Version}
			wantBuild.ArtifactType = tc.build.ArtifactType
			wantBuild.DockerfilePath = tc.build.DockerfilePath
			body, err := json.Marshal(map[string]any{
				"proposal": map[string]any{"build": map[string]any{
					"model": tc.build.Model, "toolchain": tc.build.Toolchain, "artifactType": tc.build.ArtifactType, "dockerfilePath": tc.build.DockerfilePath,
					"vars":  []map[string]any{{"key": "INJECTED", "secret": true, "credentialId": "fabricated"}},
					"cache": map[string]any{"enabled": true, "paths": []string{"injected"}},
				}},
				"selections": map[string]any{"build": true},
			})
			if err != nil {
				t.Fatalf("marshal apply request: %v", err)
			}
			for attempt := 0; attempt < 2; attempt++ {
				w := f.request(http.MethodPost, "/api/projects/"+f.projectID+"/pipeline/ai-apply", string(body))
				if w.Code != http.StatusOK {
					t.Fatalf("apply status = %d, body=%s", w.Code, w.Body.String())
				}
				var applied aiApplyDTO
				if err := json.Unmarshal(w.Body.Bytes(), &applied); err != nil {
					t.Fatalf("decode applied response: %v", err)
				}
				if !applied.Applied.Settings || applied.Applied.Spec || applied.Applied.Triggers {
					t.Fatalf("unexpected applied sections: %+v", applied.Applied)
				}
				after := f.readBack(t)
				if !reflect.DeepEqual(after.Build, wantBuild) {
					t.Errorf("persisted build = %+v, want %+v", after.Build, wantBuild)
				}
				if !reflect.DeepEqual(after.Environments, before.Environments) || !reflect.DeepEqual(after.Steps, before.Steps) {
					t.Errorf("AI build apply changed environments or steps: %+v", after)
				}
			}
		})
	}
}

func TestAIApplyBuildDefaultStoredSettings(t *testing.T) {
	f := newAIApplyBuildFixture(t)
	w := f.request(http.MethodPost, "/api/projects/"+f.projectID+"/pipeline/ai-apply",
		`{"proposal":{"build":{"model":"toolchain","toolchain":{"language":"node","version":"22"},"artifactType":"dist"}},"selections":{"build":true}}`)
	if w.Code != http.StatusOK {
		t.Fatalf("apply status = %d, body=%s", w.Code, w.Body.String())
	}
	after := f.readBack(t)
	if after.Build.Model != "toolchain" || after.Build.Toolchain != (pipeline.Toolchain{Language: "node", Version: "22"}) || after.Build.ArtifactType != "dist" {
		t.Fatalf("proposal was not persisted: %+v", after.Build)
	}
	if len(after.Build.Vars) != 0 || after.Build.Cache.Enabled || len(after.Build.Cache.Paths) != 0 {
		t.Fatalf("AI fabricated variables or cache settings: %+v", after.Build)
	}
}

func TestAIApplyBuildRejectedOrUnselectedPreservesStoredSettings(t *testing.T) {
	for _, tc := range []struct {
		name   string
		body   string
		status int
		code   string
	}{
		{"unselected", `{"proposal":{"build":{"model":"invalid"}},"selections":{"build":false}}`, http.StatusOK, ""},
		{"invalid_model", `{"proposal":{"build":{"model":"invalid"}},"selections":{"build":true}}`, http.StatusUnprocessableEntity, "invalid_build"},
		{"invalid_artifact", `{"proposal":{"build":{"model":"toolchain","artifactType":"invalid"}},"selections":{"build":true}}`, http.StatusUnprocessableEntity, "invalid_build"},
		{"malformed_json", `{`, http.StatusBadRequest, "bad_request"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newAIApplyBuildFixture(t)
			before := f.seed(t, pipeline.Cache{Enabled: true, Paths: []string{"node_modules", ".npm"}})
			w := f.request(http.MethodPost, "/api/projects/"+f.projectID+"/pipeline/ai-apply", tc.body)
			if w.Code != tc.status {
				t.Fatalf("apply status = %d, want %d, body=%s", w.Code, tc.status, w.Body.String())
			}
			if tc.code != "" {
				var body struct {
					Error struct {
						Code string `json:"code"`
					} `json:"error"`
				}
				if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil || body.Error.Code != tc.code {
					t.Fatalf("unexpected apply error: %s", w.Body.String())
				}
			} else {
				var applied aiApplyDTO
				if err := json.Unmarshal(w.Body.Bytes(), &applied); err != nil || applied.Applied.Spec || applied.Applied.Settings || applied.Applied.Triggers {
					t.Fatalf("unselected sections were applied: %s", w.Body.String())
				}
			}
			if after := f.readBack(t); !reflect.DeepEqual(after, before) {
				t.Fatalf("rejected or unselected apply changed persisted settings: %+v", after)
			}
		})
	}
}

func TestAIApplyBuildAuthCSRFAndProjectNotFound(t *testing.T) {
	for _, tc := range []struct {
		name    string
		cookies bool
		csrf    bool
		missing bool
		status  int
		code    string
	}{
		{"unauthenticated", false, false, false, http.StatusUnauthorized, "unauthorized"},
		{"missing_csrf", true, false, false, http.StatusForbidden, "csrf_invalid"},
		{"project_not_found", true, true, true, http.StatusNotFound, "project_not_found"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newAIApplyBuildFixture(t)
			before := f.seed(t, pipeline.Cache{Enabled: true, Paths: []string{"node_modules"}})
			projectID := f.projectID
			if tc.missing {
				projectID = "nonexistent"
			}
			r := httptest.NewRequest(http.MethodPost, "/api/projects/"+projectID+"/pipeline/ai-apply",
				strings.NewReader(`{"proposal":{"build":{"model":"toolchain","artifactType":"dist"}},"selections":{"build":true}}`))
			r.Header.Set("Content-Type", "application/json")
			if tc.cookies {
				for _, c := range f.cookies {
					r.AddCookie(c)
				}
			}
			if tc.csrf {
				r.Header.Set("X-CSRF-Token", f.csrf)
			}
			w := httptest.NewRecorder()
			f.handler.ServeHTTP(w, r)
			if w.Code != tc.status {
				t.Fatalf("apply status = %d, want %d, body=%s", w.Code, tc.status, w.Body.String())
			}
			var body struct {
				Error struct {
					Code string `json:"code"`
				} `json:"error"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil || body.Error.Code != tc.code {
				t.Fatalf("unexpected apply error: %s", w.Body.String())
			}
			if after := f.readBack(t); !reflect.DeepEqual(after, before) {
				t.Fatalf("rejected apply changed persisted settings: %+v", after)
			}
		})
	}
}
