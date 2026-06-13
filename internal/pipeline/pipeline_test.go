package pipeline

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/huangchengsir/pipewright/internal/storetest"
)

// testDB 打开临时 SQLite(含全部迁移)。
func testDB(t *testing.T) *sql.DB {
	return storetest.OpenDB(t)
}

// seedProject 直接插一个项目(满足外键),返回 project id。
func seedProject(t *testing.T, db *sql.DB) string {
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
		 VALUES (?, 'p', 'https://gitee.com/acme/shop.git', 'main', ?, ?, ?)`,
		projID, credID, now, now,
	)
	if err != nil {
		t.Fatalf("seed project: %v", err)
	}
	return projID
}

func newSvc(t *testing.T) (Service, *sql.DB, string) {
	t.Helper()
	db := testDB(t)
	svc := New(db)
	projID := seedProject(t, db)
	return svc, db, projID
}

func TestGetLazyDefaultShape(t *testing.T) {
	svc, _, projID := newSvc(t)
	cfg, err := svc.Get(context.Background(), projID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	// 默认种子只预置 源 + 构建 两阶段;部署/通知等按需由用户「+添加阶段」动态加入,
	// 不再写死空阶段(空阶段不冒充已配置)。
	if len(cfg.Spec.Stages) != 2 {
		t.Fatalf("默认应有 2 阶段(源+构建), got %d", len(cfg.Spec.Stages))
	}
	wantKinds := []string{KindSource, KindBuild}
	for i, k := range wantKinds {
		if cfg.Spec.Stages[i].Kind != k {
			t.Fatalf("阶段 %d kind = %q, want %q", i, cfg.Spec.Stages[i].Kind, k)
		}
	}
	src := cfg.Spec.Stages[0]
	if len(src.Jobs) != 1 {
		t.Fatalf("源阶段应有 1 任务, got %d", len(src.Jobs))
	}
	if src.Jobs[0].Type != "git_source" {
		t.Fatalf("源任务 type = %q, want git_source", src.Jobs[0].Type)
	}
	if !strings.Contains(src.Jobs[0].Summary, "gitee.com/acme/shop.git") {
		t.Fatalf("源任务 summary 应含仓库地址, got %q", src.Jobs[0].Summary)
	}
	for _, st := range cfg.Spec.Stages[1:] {
		if len(st.Jobs) != 0 {
			t.Fatalf("阶段 %q 应为空任务, got %d", st.Kind, len(st.Jobs))
		}
	}
	if cfg.Status != "draft" {
		t.Fatalf("status = %q, want draft", cfg.Status)
	}
	if strings.TrimSpace(cfg.YAML) == "" {
		t.Fatal("yaml 不应为空")
	}
}

func TestGetProjectNotFound(t *testing.T) {
	svc, _, _ := newSvc(t)
	_, err := svc.Get(context.Background(), "nope")
	if err != ErrProjectNotFound {
		t.Fatalf("err = %v, want ErrProjectNotFound", err)
	}
}

func TestGetIdempotent(t *testing.T) {
	svc, _, projID := newSvc(t)
	ctx := context.Background()
	a, err := svc.Get(ctx, projID)
	if err != nil {
		t.Fatalf("Get a: %v", err)
	}
	b, err := svc.Get(ctx, projID)
	if err != nil {
		t.Fatalf("Get b: %v", err)
	}
	if a.Spec.Stages[0].Jobs[0].Summary != b.Spec.Stages[0].Jobs[0].Summary {
		t.Fatal("重复 Get 应返回同一权威行(惰性默认仅生成一次)")
	}
}

func validSpec() Spec {
	return Spec{Stages: []Stage{
		{Name: "流水线源", Kind: KindSource, Jobs: []Job{
			{Name: "Gitee 源", Type: "git_source", Summary: "main", Config: map[string]any{}},
		}},
		{Name: "构建", Kind: KindBuild, Jobs: []Job{
			{Name: "打镜像", Type: "build_image", Summary: "docker build", Config: map[string]any{"dockerfile": "Dockerfile"}},
		}},
		{Name: "部署", Kind: KindDeploy, Jobs: []Job{}},
	}}
}

func TestSaveValid(t *testing.T) {
	svc, _, projID := newSvc(t)
	ctx := context.Background()
	cfg, err := svc.Save(ctx, projID, validSpec())
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	if len(cfg.Spec.Stages) != 3 {
		t.Fatalf("阶段数 = %d, want 3", len(cfg.Spec.Stages))
	}
	if cfg.Status != "draft" {
		t.Fatalf("status = %q, want draft", cfg.Status)
	}
}

// TestSaveRejectsNoSourceStage 验证源阶段不变式:无 source 阶段的 spec 被拒(防抹除仓库引用)。
func TestSaveRejectsNoSourceStage(t *testing.T) {
	svc, _, projID := newSvc(t)
	spec := Spec{Stages: []Stage{{Name: "构建", Kind: KindBuild, Jobs: []Job{}}}}
	if _, err := svc.Save(context.Background(), projID, spec); !errors.Is(err, ErrInvalidStage) {
		t.Fatalf("无源阶段 Save err = %v, want ErrInvalidStage", err)
	}
	// 空 stages 同样被拒。
	if _, err := svc.Save(context.Background(), projID, Spec{Stages: []Stage{}}); !errors.Is(err, ErrInvalidStage) {
		t.Fatalf("空 stages Save err = %v, want ErrInvalidStage", err)
	}
}

func TestSaveInvalidStageName(t *testing.T) {
	svc, _, projID := newSvc(t)
	spec := Spec{Stages: []Stage{{Name: "  ", Kind: KindBuild}}}
	_, err := svc.Save(context.Background(), projID, spec)
	if !errors.Is(err, ErrInvalidStage) {
		t.Fatalf("err = %v, want ErrInvalidStage", err)
	}
}

func TestSaveInvalidKind(t *testing.T) {
	svc, _, projID := newSvc(t)
	spec := Spec{Stages: []Stage{{Name: "x", Kind: "bogus"}}}
	_, err := svc.Save(context.Background(), projID, spec)
	if !errors.Is(err, ErrInvalidStage) {
		t.Fatalf("err = %v, want ErrInvalidStage", err)
	}
}

func TestSaveInvalidJobName(t *testing.T) {
	svc, _, projID := newSvc(t)
	spec := Spec{Stages: []Stage{{Name: "构建", Kind: KindBuild, Jobs: []Job{{Name: "", Type: "t"}}}}}
	_, err := svc.Save(context.Background(), projID, spec)
	if !errors.Is(err, ErrInvalidJob) {
		t.Fatalf("err = %v, want ErrInvalidJob", err)
	}
}

func TestSaveInvalidJobType(t *testing.T) {
	svc, _, projID := newSvc(t)
	spec := Spec{Stages: []Stage{{Name: "构建", Kind: KindBuild, Jobs: []Job{{Name: "j", Type: "  "}}}}}
	_, err := svc.Save(context.Background(), projID, spec)
	if !errors.Is(err, ErrInvalidJob) {
		t.Fatalf("err = %v, want ErrInvalidJob", err)
	}
}

// TestSaveValidJobDAG 验证阶段内 job 级依赖(横串竖并):合法 needs 被保存且无损往返。
func TestSaveValidJobDAG(t *testing.T) {
	svc, _, projID := newSvc(t)
	spec := Spec{Stages: []Stage{
		{Name: "源", Kind: KindSource, Jobs: []Job{{Name: "src", Type: "git_source"}}},
		{ID: "s_build", Name: "构建", Kind: KindBuild, Jobs: []Job{
			{ID: "j_fe", Name: "前端构建", Type: "build_frontend"},                               // 无依赖(与 j_be 并行)
			{ID: "j_be", Name: "后端构建", Type: "build_backend"},                                // 无依赖(与 j_fe 并行)
			{ID: "j_img", Name: "打镜像", Type: "build_image", Needs: []string{"j_fe", "j_be"}}, // 依赖二者(串行其后)
		}},
	}}
	cfg, err := svc.Save(context.Background(), projID, spec)
	if err != nil {
		t.Fatalf("合法 job DAG Save: %v", err)
	}
	var img Job
	for _, jb := range cfg.Spec.Stages[1].Jobs {
		if jb.ID == "j_img" {
			img = jb
		}
	}
	if len(img.Needs) != 2 {
		t.Fatalf("打镜像 needs 应往返保留 2 项, got %v", img.Needs)
	}
}

// TestSaveJobSelfDep 验证 job 自指被拒。
func TestSaveJobSelfDep(t *testing.T) {
	svc, _, projID := newSvc(t)
	spec := Spec{Stages: []Stage{
		{Name: "源", Kind: KindSource, Jobs: []Job{{Name: "src", Type: "git_source"}}},
		{Name: "构建", Kind: KindBuild, Jobs: []Job{{ID: "j1", Name: "j1", Type: "t", Needs: []string{"j1"}}}},
	}}
	if _, err := svc.Save(context.Background(), projID, spec); !errors.Is(err, ErrInvalidJob) {
		t.Fatalf("job 自指 err = %v, want ErrInvalidJob", err)
	}
}

// TestSaveJobCycle 验证 job 依赖成环被拒。
func TestSaveJobCycle(t *testing.T) {
	svc, _, projID := newSvc(t)
	spec := Spec{Stages: []Stage{
		{Name: "源", Kind: KindSource, Jobs: []Job{{Name: "src", Type: "git_source"}}},
		{Name: "构建", Kind: KindBuild, Jobs: []Job{
			{ID: "a", Name: "a", Type: "t", Needs: []string{"b"}},
			{ID: "b", Name: "b", Type: "t", Needs: []string{"a"}},
		}},
	}}
	if _, err := svc.Save(context.Background(), projID, spec); !errors.Is(err, ErrInvalidJob) {
		t.Fatalf("job 成环 err = %v, want ErrInvalidJob", err)
	}
}

// TestSaveJobUnknownDep 验证 job needs 引用阶段内不存在的 job 被拒(跨阶段引用也算未知)。
func TestSaveJobUnknownDep(t *testing.T) {
	svc, _, projID := newSvc(t)
	spec := Spec{Stages: []Stage{
		{Name: "源", Kind: KindSource, Jobs: []Job{{Name: "src", Type: "git_source"}}},
		{Name: "构建", Kind: KindBuild, Jobs: []Job{
			{ID: "a", Name: "a", Type: "t", Needs: []string{"nonexistent"}},
		}},
	}}
	if _, err := svc.Save(context.Background(), projID, spec); !errors.Is(err, ErrInvalidJob) {
		t.Fatalf("job 未知依赖 err = %v, want ErrInvalidJob", err)
	}
}

func TestSaveDuplicateStageID(t *testing.T) {
	svc, _, projID := newSvc(t)
	spec := Spec{Stages: []Stage{
		{ID: "dup", Name: "a", Kind: KindBuild},
		{ID: "dup", Name: "b", Kind: KindDeploy},
	}}
	_, err := svc.Save(context.Background(), projID, spec)
	if !errors.Is(err, ErrDuplicateID) {
		t.Fatalf("err = %v, want ErrDuplicateID", err)
	}
}

func TestSaveDuplicateJobID(t *testing.T) {
	svc, _, projID := newSvc(t)
	spec := Spec{Stages: []Stage{
		{ID: "s1", Name: "a", Kind: KindBuild, Jobs: []Job{{ID: "jdup", Name: "j1", Type: "t"}}},
		{ID: "s2", Name: "b", Kind: KindDeploy, Jobs: []Job{{ID: "jdup", Name: "j2", Type: "t"}}},
	}}
	_, err := svc.Save(context.Background(), projID, spec)
	if !errors.Is(err, ErrDuplicateID) {
		t.Fatalf("err = %v, want ErrDuplicateID", err)
	}
}

func TestSaveNormalizesMissingIDsAndConfig(t *testing.T) {
	svc, _, projID := newSvc(t)
	spec := Spec{Stages: []Stage{
		{Name: " 构建 ", Kind: KindBuild, Jobs: []Job{{Name: " j ", Type: " t ", Config: nil}}},
		{Name: "源", Kind: KindSource, Jobs: []Job{{Name: "src", Type: "git_source"}}}, // 满足源阶段不变式
	}}
	cfg, err := svc.Save(context.Background(), projID, spec)
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	st := cfg.Spec.Stages[0]
	if st.ID == "" {
		t.Fatal("阶段 id 应被补全")
	}
	if st.Name != "构建" {
		t.Fatalf("阶段名应被 trim, got %q", st.Name)
	}
	jb := st.Jobs[0]
	if jb.ID == "" {
		t.Fatal("任务 id 应被补全")
	}
	if jb.Name != "j" || jb.Type != "t" {
		t.Fatalf("任务名/type 应被 trim, got name=%q type=%q", jb.Name, jb.Type)
	}
	if jb.Config == nil {
		t.Fatal("nil config 应补全为 {}")
	}
}

func TestSaveGetRoundTrip(t *testing.T) {
	svc, _, projID := newSvc(t)
	ctx := context.Background()
	saved, err := svc.Save(ctx, projID, validSpec())
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, err := svc.Get(ctx, projID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if len(got.Spec.Stages) != len(saved.Spec.Stages) {
		t.Fatalf("往返阶段数不一致: %d vs %d", len(got.Spec.Stages), len(saved.Spec.Stages))
	}
	for i := range saved.Spec.Stages {
		if got.Spec.Stages[i].Name != saved.Spec.Stages[i].Name ||
			got.Spec.Stages[i].Kind != saved.Spec.Stages[i].Kind ||
			got.Spec.Stages[i].ID != saved.Spec.Stages[i].ID {
			t.Fatalf("往返阶段 %d 不一致: %+v vs %+v", i, got.Spec.Stages[i], saved.Spec.Stages[i])
		}
	}
	if got.YAML != saved.YAML {
		t.Fatal("往返 yaml 应一致")
	}
	if strings.TrimSpace(got.YAML) == "" {
		t.Fatal("yaml 不应为空")
	}
	if !strings.Contains(got.YAML, "build_image") {
		t.Fatalf("yaml 应含任务 type, got:\n%s", got.YAML)
	}
}

func TestRenderYAMLDeterministic(t *testing.T) {
	a, err := renderYAML(validSpec())
	if err != nil {
		t.Fatalf("render a: %v", err)
	}
	b, err := renderYAML(validSpec())
	if err != nil {
		t.Fatalf("render b: %v", err)
	}
	if a != b {
		t.Fatalf("yaml 渲染应确定性:\n%q\nvs\n%q", a, b)
	}
}

// TestGetFillsSourceDefaults 验证:git_source 任务 config 为空时,Get 用项目绑定的
// 仓库/分支/凭据预填(展示用),并补「仓库 · 分支」摘要(修复源节点不显示源码信息)。
func TestGetFillsSourceDefaults(t *testing.T) {
	svc, db, projID := newSvc(t)
	var credID string
	if err := db.QueryRow(`SELECT credential_id FROM projects WHERE id = ?`, projID).Scan(&credID); err != nil {
		t.Fatalf("read project cred: %v", err)
	}
	// 存一条 source 任务 config/summary 全空的流水线。
	spec := Spec{Stages: []Stage{
		{Name: "源码", Kind: KindSource, Jobs: []Job{
			{Name: "拉取", Type: "git_source", Config: map[string]any{}},
		}},
	}}
	if _, err := svc.Save(context.Background(), projID, spec); err != nil {
		t.Fatalf("Save: %v", err)
	}
	cfg, err := svc.Get(context.Background(), projID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	job := cfg.Spec.Stages[0].Jobs[0]
	if got, _ := job.Config["repoUrl"].(string); got != "https://gitee.com/acme/shop.git" {
		t.Fatalf("repoUrl 未预填项目仓库, got %q", got)
	}
	if got, _ := job.Config["branch"].(string); got != "main" {
		t.Fatalf("branch 未预填项目默认分支, got %q", got)
	}
	if got, _ := job.Config["credentialId"].(string); got != credID {
		t.Fatalf("credentialId 未预填项目凭据, got %q want %q", got, credID)
	}
	if !strings.Contains(job.Summary, "gitee.com/acme/shop.git") || !strings.Contains(job.Summary, "main") {
		t.Fatalf("summary 应含仓库 · 分支, got %q", job.Summary)
	}
}

// TestGetDoesNotOverrideExplicitSource 验证:用户显式填了仓库/分支时,Get 不覆盖。
func TestGetDoesNotOverrideExplicitSource(t *testing.T) {
	svc, _, projID := newSvc(t)
	spec := Spec{Stages: []Stage{
		{Name: "源码", Kind: KindSource, Jobs: []Job{
			{Name: "拉取", Type: "git_source", Config: map[string]any{
				"repoUrl": "https://gitee.com/other/repo.git",
				"branch":  "dev",
			}},
		}},
	}}
	if _, err := svc.Save(context.Background(), projID, spec); err != nil {
		t.Fatalf("Save: %v", err)
	}
	cfg, err := svc.Get(context.Background(), projID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	job := cfg.Spec.Stages[0].Jobs[0]
	if got, _ := job.Config["repoUrl"].(string); got != "https://gitee.com/other/repo.git" {
		t.Fatalf("显式 repoUrl 被覆盖, got %q", got)
	}
	if got, _ := job.Config["branch"].(string); got != "dev" {
		t.Fatalf("显式 branch 被覆盖, got %q", got)
	}
}
