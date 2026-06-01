package pipeline

import (
	"context"
	"strings"
	"testing"
)

// TestSettingsStepRoundTrip 验证脚本步骤往返:补 id、type 缺省 script、命令保序、明文 env 保留。
func TestSettingsStepRoundTrip(t *testing.T) {
	svc, _, _, _, projID := newSettingsSvc(t)
	ctx := context.Background()

	in := SettingsInput{
		Steps: []PipelineStep{
			{
				Name:     "运行测试",
				Image:    "golang:1.23",
				Commands: []string{"go vet ./...", "  ", "go test ./..."}, // 空行应被剔除
				Env:      []BuildVar{{Key: "CGO_ENABLED", Secret: false, Value: "0"}},
				WorkDir:  "src/app",
			},
			{
				Name:     "lint",
				Type:     StepTypeScript,
				Image:    "node:20",
				Commands: []string{"npm ci", "npm run lint"},
			},
		},
	}
	st, err := svc.Save(ctx, projID, in)
	if err != nil {
		t.Fatalf("Save steps: %v", err)
	}
	if len(st.Steps) != 2 {
		t.Fatalf("步骤数 = %d", len(st.Steps))
	}
	s0 := st.Steps[0]
	if s0.ID == "" {
		t.Fatalf("步骤 id 应补全")
	}
	if s0.Type != StepTypeScript {
		t.Fatalf("type 缺省应为 script, got %q", s0.Type)
	}
	if s0.Image != "golang:1.23" || s0.WorkDir != "src/app" {
		t.Fatalf("image/workDir 往返失败: %+v", s0)
	}
	if len(s0.Commands) != 2 || s0.Commands[0] != "go vet ./..." || s0.Commands[1] != "go test ./..." {
		t.Fatalf("命令应保序且剔空行: %+v", s0.Commands)
	}
	if len(s0.Env) != 1 || s0.Env[0].Value != "0" {
		t.Fatalf("明文 env 应保留: %+v", s0.Env)
	}
	if st.Steps[1].Name != "lint" {
		t.Fatalf("步骤顺序应保留: %+v", st.Steps)
	}
}

// TestSettingsStepOptsRoundTrip 验证任务级 timeout/retry/资源规格往返(存读一致;负值钳为 0)。
func TestSettingsStepOptsRoundTrip(t *testing.T) {
	svc, _, _, _, projID := newSettingsSvc(t)
	ctx := context.Background()

	in := SettingsInput{
		Steps: []PipelineStep{
			{
				Name:           "build",
				Image:          "node:20",
				Commands:       []string{"npm run build"},
				TimeoutSeconds: 600,
				Retries:        2,
				Resource:       Resource{CPU: "1.5", Memory: "1g"},
			},
			{
				Name:           "neg",
				Image:          "node:20",
				Commands:       []string{"echo hi"},
				TimeoutSeconds: -5, // 负值应钳为 0
				Retries:        -1,
			},
		},
	}
	st, err := svc.Save(ctx, projID, in)
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	s0 := st.Steps[0]
	if s0.TimeoutSeconds != 600 || s0.Retries != 2 {
		t.Fatalf("timeout/retry round-trip failed: %+v", s0)
	}
	if s0.Resource.CPU != "1.5" || s0.Resource.Memory != "1g" {
		t.Fatalf("resource round-trip failed: %+v", s0.Resource)
	}
	s1 := st.Steps[1]
	if s1.TimeoutSeconds != 0 || s1.Retries != 0 {
		t.Fatalf("negative timeout/retry should clamp to 0: %+v", s1)
	}
}

func TestSettingsStepEmptyNameRejected(t *testing.T) {
	svc, _, _, _, projID := newSettingsSvc(t)
	_, err := svc.Save(context.Background(), projID, SettingsInput{
		Steps: []PipelineStep{{Name: "  ", Image: "node:20", Commands: []string{"echo hi"}}},
	})
	if err == nil || !strings.Contains(err.Error(), "step name must not be empty") {
		t.Fatalf("err = %v, want ErrInvalidStep (name)", err)
	}
}

func TestSettingsStepMissingImageRejected(t *testing.T) {
	svc, _, _, _, projID := newSettingsSvc(t)
	_, err := svc.Save(context.Background(), projID, SettingsInput{
		Steps: []PipelineStep{{Name: "build", Commands: []string{"make"}}},
	})
	if err == nil || !strings.Contains(err.Error(), "requires an image") {
		t.Fatalf("err = %v, want ErrInvalidStep (image)", err)
	}
}

func TestSettingsStepMissingCommandsRejected(t *testing.T) {
	svc, _, _, _, projID := newSettingsSvc(t)
	_, err := svc.Save(context.Background(), projID, SettingsInput{
		Steps: []PipelineStep{{Name: "build", Image: "node:20", Commands: []string{"   ", ""}}},
	})
	if err == nil || !strings.Contains(err.Error(), "requires at least one command") {
		t.Fatalf("err = %v, want ErrInvalidStep (commands)", err)
	}
}

func TestSettingsStepUnsupportedTypeRejected(t *testing.T) {
	svc, _, _, _, projID := newSettingsSvc(t)
	_, err := svc.Save(context.Background(), projID, SettingsInput{
		Steps: []PipelineStep{{Name: "deploy", Type: "deploy", Image: "node:20", Commands: []string{"x"}}},
	})
	if err == nil || !strings.Contains(err.Error(), "unsupported step type") {
		t.Fatalf("err = %v, want ErrInvalidStep (type)", err)
	}
}

// TestSettingsStepSecretEnvRoundTripMasked 验证步骤 secret env:只留 credentialId 引用 + 回掩码,绝无明文。
func TestSettingsStepSecretEnvRoundTripMasked(t *testing.T) {
	svc, v, _, _, projID := newSettingsSvc(t)
	const plaintext = "STEP_SECRET_LEAKMARKER_zzz999"
	credID := seedRealCred(t, v, plaintext)

	st, err := svc.Save(context.Background(), projID, SettingsInput{
		Steps: []PipelineStep{{
			Name:     "migrate",
			Image:    "postgres:16",
			Commands: []string{"psql -f migrate.sql"},
			Env:      []BuildVar{{Key: "PGPASSWORD", Secret: true, CredentialID: credID, Value: plaintext}}, // value 应被丢弃
		}},
	})
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	got := st.Steps[0].Env[0]
	if !got.Secret || got.CredentialID != credID {
		t.Fatalf("secret 引用未保留: %+v", got)
	}
	if got.Value != "" {
		t.Fatalf("secret 项绝不应回显明文 value: %q", got.Value)
	}
	if got.MaskedValue == "" || strings.Contains(got.MaskedValue, "LEAKMARKER") {
		t.Fatalf("应回掩码且不含明文: %q", got.MaskedValue)
	}
}

func TestSettingsStepSecretEnvCredentialNotFound(t *testing.T) {
	svc, _, _, _, projID := newSettingsSvc(t)
	_, err := svc.Save(context.Background(), projID, SettingsInput{
		Steps: []PipelineStep{{
			Name: "x", Image: "node:20", Commands: []string{"echo"},
			Env: []BuildVar{{Key: "T", Secret: true, CredentialID: "00000000-0000-0000-0000-000000000000"}},
		}},
	})
	if err == nil || !strings.Contains(err.Error(), "referenced credential not found") {
		t.Fatalf("err = %v, want ErrCredentialNotFound", err)
	}
}

// TestSettingsStepSecAfterDBNoPlaintext 断言:存含 secret 引用的步骤后,steps_json 与整库不含明文。
func TestSettingsStepSecAfterDBNoPlaintext(t *testing.T) {
	svc, v, db, dbPath, projID := newSettingsSvc(t)
	const stepSecret = "STEP_ENVLEAK_marker_value_4242"
	cred := seedRealCred(t, v, stepSecret)

	_, err := svc.Save(context.Background(), projID, SettingsInput{
		Steps: []PipelineStep{{
			Name: "migrate", Image: "postgres:16", Commands: []string{"psql"},
			Env: []BuildVar{{Key: "PGPASSWORD", Secret: true, CredentialID: cred, Value: stepSecret}},
		}},
	})
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	_, _ = db.Exec(`PRAGMA wal_checkpoint(TRUNCATE)`)

	var stepsJSON string
	if err := db.QueryRow(`SELECT steps_json FROM pipeline_settings WHERE project_id = ?`, projID).Scan(&stepsJSON); err != nil {
		t.Fatalf("read steps row: %v", err)
	}
	if strings.Contains(stepsJSON, stepSecret) || strings.Contains(stepsJSON, "LEAK") {
		t.Fatalf("steps_json 含明文/泄漏标记!: %s", stepsJSON)
	}
	for _, suffix := range []string{"", "-wal", "-shm"} {
		raw := readSettingsFileMaybe(t, dbPath+suffix)
		if strings.Contains(string(raw), stepSecret) {
			t.Fatalf("整库文件 %s 含明文 step secret!", dbPath+suffix)
		}
	}
}

// TestSettingsNoStepsBackwardCompat 验证无自定义步骤时 Get 返回空切片(非 nil),行为不变。
func TestSettingsNoStepsBackwardCompat(t *testing.T) {
	svc, _, _, _, projID := newSettingsSvc(t)
	st, err := svc.Get(context.Background(), projID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if st.Steps == nil || len(st.Steps) != 0 {
		t.Fatalf("默认步骤应为空切片, got %+v", st.Steps)
	}
}
