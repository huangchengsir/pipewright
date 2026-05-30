package pipeline

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/huangchengsir/pipewright/internal/store"
	"github.com/huangchengsir/pipewright/internal/vault"
)

// testMasterKey 返回确定性测试用 master key。
func testMasterKey() *[32]byte {
	var k [32]byte
	for i := range k {
		k[i] = byte(i + 11)
	}
	return &k
}

// settingsTestDB 打开临时 SQLite(含全部迁移),返回 *sql.DB 与库文件路径(供整库 dump)。
func settingsTestDB(t *testing.T) (*sql.DB, string) {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "test.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st.DB, dbPath
}

// seedSettingsProject 插入一个最小凭据 + 项目,返回 project id。
func seedSettingsProject(t *testing.T, db *sql.DB) string {
	t.Helper()
	now := time.Now().UTC().Format(time.RFC3339)
	credID := uuid.NewString()
	_, err := db.Exec(
		`INSERT INTO credentials (id, name, type, scope, ciphertext, masked_value, created_at, updated_at)
		 VALUES (?, 'c', 'git_token', '', X'00', 'm', ?, ?)`,
		credID, now, now,
	)
	if err != nil {
		t.Fatalf("seed credential: %v", err)
	}
	projID := uuid.NewString()
	_, err = db.Exec(
		`INSERT INTO projects (id, name, repo_url, default_branch, credential_id, created_at, updated_at)
		 VALUES (?, 'p', 'https://example.com/p.git', 'main', ?, ?, ?)`,
		projID, credID, now, now,
	)
	if err != nil {
		t.Fatalf("seed project: %v", err)
	}
	return projID
}

// seedRealCred 经 vault 真实创建一个含明文 secret 的 git_token 凭据,返回 (id, 明文)。
func seedRealCred(t *testing.T, v vault.Vault, secret string) string {
	t.Helper()
	c, err := v.Create(vault.CreateInput{Name: "tok", Type: vault.TypeGitToken, Secret: secret})
	if err != nil {
		t.Fatalf("vault.Create: %v", err)
	}
	return c.ID
}

func newSettingsSvc(t *testing.T) (SettingsService, vault.Vault, *sql.DB, string, string) {
	t.Helper()
	db, dbPath := settingsTestDB(t)
	v := vault.New(db, testMasterKey())
	svc := NewSettingsService(db, v)
	projID := seedSettingsProject(t, db)
	return svc, v, db, dbPath, projID
}

func TestSettingsGetLazyDefault(t *testing.T) {
	svc, _, _, _, projID := newSettingsSvc(t)
	st, err := svc.Get(context.Background(), projID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if st.Build.Model != BuildModelDockerfile {
		t.Fatalf("默认构建模型应为 dockerfile, got %q", st.Build.Model)
	}
	if st.Build.DockerfilePath != defaultDockerfilePath {
		t.Fatalf("默认 Dockerfile 路径 = %q", st.Build.DockerfilePath)
	}
	if st.Build.ArtifactType != ArtifactImage {
		t.Fatalf("默认产物应为 image, got %q", st.Build.ArtifactType)
	}
	if st.Build.Vars == nil || len(st.Build.Vars) != 0 {
		t.Fatalf("默认构建变量应为空切片, got %+v", st.Build.Vars)
	}
	if st.Environments == nil || len(st.Environments) != 0 {
		t.Fatalf("默认环境应为空切片, got %+v", st.Environments)
	}

	// 二次 Get 幂等。
	st2, err := svc.Get(context.Background(), projID)
	if err != nil {
		t.Fatalf("Get 2: %v", err)
	}
	if !st2.UpdatedAt.Equal(st.UpdatedAt) {
		t.Fatalf("重复 Get 不应改 updatedAt: %v != %v", st2.UpdatedAt, st.UpdatedAt)
	}
}

func TestSettingsGetProjectNotFound(t *testing.T) {
	svc, _, _, _, _ := newSettingsSvc(t)
	_, err := svc.Get(context.Background(), uuid.NewString())
	if err != ErrProjectNotFound {
		t.Fatalf("err = %v, want ErrProjectNotFound", err)
	}
}

func TestSettingsSaveBuildModelToggleRoundTrip(t *testing.T) {
	svc, _, _, _, projID := newSettingsSvc(t)
	ctx := context.Background()

	// 模型 B + 工具链 + 产物 jar + 明文变量 + 缓存。
	in := SettingsInput{
		Build: BuildConfig{
			Model:        BuildModelToolchain,
			Toolchain:    Toolchain{Language: "node", Version: "22"},
			ArtifactType: ArtifactJAR,
			Vars: []BuildVar{
				{Key: "NODE_ENV", Secret: false, Value: "production"},
			},
			Cache: Cache{Enabled: true, Paths: []string{"node_modules", ".npm"}},
		},
	}
	st, err := svc.Save(ctx, projID, in)
	if err != nil {
		t.Fatalf("Save B: %v", err)
	}
	if st.Build.Model != BuildModelToolchain || st.Build.Toolchain.Version != "22" {
		t.Fatalf("模型 B 往返失败: %+v", st.Build)
	}
	if st.Build.ArtifactType != ArtifactJAR {
		t.Fatalf("产物应为 jar, got %q", st.Build.ArtifactType)
	}
	if len(st.Build.Vars) != 1 || st.Build.Vars[0].Value != "production" || st.Build.Vars[0].ID == "" {
		t.Fatalf("明文变量往返失败(应补 id): %+v", st.Build.Vars)
	}
	if !st.Build.Cache.Enabled || len(st.Build.Cache.Paths) != 2 {
		t.Fatalf("缓存往返失败: %+v", st.Build.Cache)
	}

	// 切回模型 A:DockerfilePath 应补默认。
	st2, err := svc.Save(ctx, projID, SettingsInput{Build: BuildConfig{Model: BuildModelDockerfile, ArtifactType: ArtifactImage}})
	if err != nil {
		t.Fatalf("Save A: %v", err)
	}
	if st2.Build.Model != BuildModelDockerfile || st2.Build.DockerfilePath != defaultDockerfilePath {
		t.Fatalf("模型 A 切换失败: %+v", st2.Build)
	}
}

func TestSettingsSaveInvalidBuildModel(t *testing.T) {
	svc, _, _, _, projID := newSettingsSvc(t)
	_, err := svc.Save(context.Background(), projID, SettingsInput{Build: BuildConfig{Model: "bogus"}})
	if err == nil || !strings.Contains(err.Error(), "invalid build") {
		t.Fatalf("err = %v, want ErrInvalidBuild", err)
	}
}

func TestSettingsSaveInvalidArtifact(t *testing.T) {
	svc, _, _, _, projID := newSettingsSvc(t)
	_, err := svc.Save(context.Background(), projID, SettingsInput{Build: BuildConfig{Model: BuildModelDockerfile, ArtifactType: "bogus"}})
	if err == nil || !strings.Contains(err.Error(), "invalid build") {
		t.Fatalf("err = %v, want ErrInvalidBuild", err)
	}
}

func TestSettingsSaveDuplicateVarKey(t *testing.T) {
	svc, _, _, _, projID := newSettingsSvc(t)
	_, err := svc.Save(context.Background(), projID, SettingsInput{Build: BuildConfig{
		Vars: []BuildVar{
			{Key: "A", Value: "1"},
			{Key: "A", Value: "2"},
		},
	}})
	if err == nil || !strings.Contains(err.Error(), "duplicate key") {
		t.Fatalf("err = %v, want ErrInvalidVar duplicate", err)
	}
}

func TestSettingsSaveEmptyVarKey(t *testing.T) {
	svc, _, _, _, projID := newSettingsSvc(t)
	_, err := svc.Save(context.Background(), projID, SettingsInput{Build: BuildConfig{
		Vars: []BuildVar{{Key: "  ", Value: "x"}},
	}})
	if err == nil || !strings.Contains(err.Error(), "key must not be empty") {
		t.Fatalf("err = %v, want ErrInvalidVar empty", err)
	}
}

func TestSettingsSecretVarRoundTripMasked(t *testing.T) {
	svc, v, _, _, projID := newSettingsSvc(t)
	const plaintext = "ghp_LEAKMARKER_secret_value_zzz"
	credID := seedRealCred(t, v, plaintext)

	st, err := svc.Save(context.Background(), projID, SettingsInput{Build: BuildConfig{
		Vars: []BuildVar{
			{Key: "NPM_TOKEN", Secret: true, CredentialID: credID, Value: plaintext}, // value 应被丢弃
		},
	}})
	if err != nil {
		t.Fatalf("Save secret: %v", err)
	}
	if len(st.Build.Vars) != 1 {
		t.Fatalf("变量数 = %d", len(st.Build.Vars))
	}
	got := st.Build.Vars[0]
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

func TestSettingsSecretVarCredentialNotFound(t *testing.T) {
	svc, _, _, _, projID := newSettingsSvc(t)
	_, err := svc.Save(context.Background(), projID, SettingsInput{Build: BuildConfig{
		Vars: []BuildVar{{Key: "X", Secret: true, CredentialID: uuid.NewString()}},
	}})
	if err == nil || !strings.Contains(err.Error(), "referenced credential not found") {
		t.Fatalf("err = %v, want ErrCredentialNotFound", err)
	}
}

func TestSettingsSecretVarVaultUnconfigured(t *testing.T) {
	db, _ := settingsTestDB(t)
	projID := seedSettingsProject(t, db)
	svc := NewSettingsService(db, vault.New(db, nil)) // 未配置 master key
	_, err := svc.Save(context.Background(), projID, SettingsInput{Build: BuildConfig{
		Vars: []BuildVar{{Key: "X", Secret: true, CredentialID: uuid.NewString()}},
	}})
	if err != ErrSettingsVaultUnconfigured {
		t.Fatalf("err = %v, want ErrSettingsVaultUnconfigured", err)
	}
	// 无 secret 引用时,即便未配置 vault 也应能保存。
	if _, err := svc.Save(context.Background(), projID, SettingsInput{Build: BuildConfig{Model: BuildModelDockerfile}}); err != nil {
		t.Fatalf("无 secret 保存不应需 vault: %v", err)
	}
}

func TestSettingsEnvironmentRoundTrip(t *testing.T) {
	svc, v, _, _, projID := newSettingsSvc(t)
	const dbPass = "registry_user:registry_LEAKMARKER_pass"
	regCredID := seedRealCred(t, v, "ghp_envsecret_marker_aaaa") // git_token 充当通用引用
	regBindCred, _ := v.Create(vault.CreateInput{Name: "reg", Type: vault.TypeRegistry, Secret: dbPass})

	in := SettingsInput{
		Environments: []Environment{
			{
				Name:            "生产",
				TargetServerIDs: []string{"srv-1", "srv-2"},
				EnvVars: []BuildVar{
					{Key: "API_URL", Secret: false, Value: "https://api.acme.io"},
					{Key: "DB_PASS", Secret: true, CredentialID: regCredID},
				},
				ImageRegistry: ImageRegistry{Type: RegistryHarbor, URL: "harbor.acme.com", CredentialID: regBindCred.ID},
			},
		},
	}
	st, err := svc.Save(context.Background(), projID, in)
	if err != nil {
		t.Fatalf("Save env: %v", err)
	}
	if len(st.Environments) != 1 {
		t.Fatalf("环境数 = %d", len(st.Environments))
	}
	e := st.Environments[0]
	if e.Name != "生产" || e.ID == "" {
		t.Fatalf("环境名/id 异常: %+v", e)
	}
	if len(e.TargetServerIDs) != 2 {
		t.Fatalf("目标服务器引用应保留: %+v", e.TargetServerIDs)
	}
	if len(e.EnvVars) != 2 {
		t.Fatalf("环境变量数 = %d", len(e.EnvVars))
	}
	if e.EnvVars[1].Value != "" || e.EnvVars[1].MaskedValue == "" {
		t.Fatalf("secret 环境变量应掩码且无明文: %+v", e.EnvVars[1])
	}
	if e.ImageRegistry.Type != RegistryHarbor || e.ImageRegistry.URL != "harbor.acme.com" {
		t.Fatalf("镜像仓库往返失败: %+v", e.ImageRegistry)
	}
	if e.ImageRegistry.MaskedCredential == "" || strings.Contains(e.ImageRegistry.MaskedCredential, "LEAKMARKER") {
		t.Fatalf("镜像仓库凭据应掩码且不含明文: %q", e.ImageRegistry.MaskedCredential)
	}
}

func TestSettingsEnvEmptyNameRejected(t *testing.T) {
	svc, _, _, _, projID := newSettingsSvc(t)
	_, err := svc.Save(context.Background(), projID, SettingsInput{
		Environments: []Environment{{Name: "  "}},
	})
	if err == nil || !strings.Contains(err.Error(), "environment name must not be empty") {
		t.Fatalf("err = %v, want ErrInvalidEnvironment", err)
	}
}

func TestSettingsInvalidRegistryType(t *testing.T) {
	svc, _, _, _, projID := newSettingsSvc(t)
	_, err := svc.Save(context.Background(), projID, SettingsInput{
		Environments: []Environment{{Name: "e", ImageRegistry: ImageRegistry{Type: "bogus", URL: "x"}}},
	})
	if err == nil || !strings.Contains(err.Error(), "invalid imageRegistry type") {
		t.Fatalf("err = %v, want ErrInvalidEnvironment", err)
	}
}

// TestSettingsSecAfterDBNoPlaintext 断言:存含 secret 引用的 settings 后,整库二进制不含任何明文 secret(AC-SEC)。
func TestSettingsSecAfterDBNoPlaintext(t *testing.T) {
	svc, v, db, dbPath, projID := newSettingsSvc(t)
	const buildSecret = "ghp_BUILDLEAK_marker_value_xyz9"
	const envSecret = "DBPASS_ENVLEAK_marker_value_888"
	buildCred := seedRealCred(t, v, buildSecret)
	envCred := seedRealCred(t, v, envSecret)

	_, err := svc.Save(context.Background(), projID, SettingsInput{
		Build: BuildConfig{
			Vars: []BuildVar{{Key: "NPM_TOKEN", Secret: true, CredentialID: buildCred, Value: buildSecret}},
		},
		Environments: []Environment{
			{Name: "prod", EnvVars: []BuildVar{{Key: "DB_PASS", Secret: true, CredentialID: envCred, Value: envSecret}}},
		},
	})
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	_, _ = db.Exec(`PRAGMA wal_checkpoint(TRUNCATE)`)

	// pipeline_settings 表的 JSON 列裸读:断言不含明文。
	var buildJSON, envsJSON string
	if err := db.QueryRow(`SELECT build_json, environments_json FROM pipeline_settings WHERE project_id = ?`, projID).
		Scan(&buildJSON, &envsJSON); err != nil {
		t.Fatalf("read settings row: %v", err)
	}
	for _, blob := range []string{buildJSON, envsJSON} {
		if strings.Contains(blob, buildSecret) || strings.Contains(blob, envSecret) {
			t.Fatalf("pipeline_settings JSON 含明文 secret!: %s", blob)
		}
		if strings.Contains(blob, "LEAK") {
			t.Fatalf("pipeline_settings JSON 含泄漏标记!: %s", blob)
		}
	}

	// 整库文件(含 WAL/SHM)二进制也不含明文。
	for _, suffix := range []string{"", "-wal", "-shm"} {
		raw := readSettingsFileMaybe(t, dbPath+suffix)
		if strings.Contains(string(raw), buildSecret) || strings.Contains(string(raw), envSecret) {
			t.Fatalf("整库文件 %s 含明文 secret!", dbPath+suffix)
		}
	}
}

func TestSettingsDeleteProjectCascades(t *testing.T) {
	svc, _, db, _, projID := newSettingsSvc(t)
	if _, err := svc.Get(context.Background(), projID); err != nil {
		t.Fatalf("Get: %v", err)
	}
	if _, err := db.Exec(`DELETE FROM projects WHERE id = ?`, projID); err != nil {
		t.Fatalf("delete project: %v", err)
	}
	var n int
	if err := db.QueryRow(`SELECT COUNT(1) FROM pipeline_settings WHERE project_id = ?`, projID).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 0 {
		t.Fatalf("删项目应级联删 settings, 残留 %d 行", n)
	}
}

func readSettingsFileMaybe(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		t.Fatalf("read %s: %v", path, err)
	}
	return b
}
