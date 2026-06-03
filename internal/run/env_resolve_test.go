package run

import (
	"context"
	"database/sql"
	"testing"
)

// stubEnvResolver 是受控的 EnvResolver 测试替身:记录是否被调用 + 返回固定结果。
type stubEnvResolver struct {
	env     string
	servers []string
	called  bool
	gotBr   string
}

func (r *stubEnvResolver) ResolveEnv(_ context.Context, _, branch string) (string, []string) {
	r.called = true
	r.gotBr = branch
	return r.env, r.servers
}

// readResolved 直接读运行内部列(不进冻结 DTO)。
func readResolved(t *testing.T, db *sql.DB, runID string) (env, targets string) {
	t.Helper()
	if err := db.QueryRow(
		`SELECT resolved_environment, resolved_target_server_ids FROM pipeline_runs WHERE id = ?`, runID,
	).Scan(&env, &targets); err != nil {
		t.Fatalf("read resolved cols: %v", err)
	}
	return env, targets
}

// TestCreateResolvesEnvForManualTrigger 证 #56:手动触发未带环境时,Create 经注入的
// EnvResolver 据分支补解析环境 + 目标服务器并落库。
func TestCreateResolvesEnvForManualTrigger(t *testing.T) {
	db := testDB(t)
	res := &stubEnvResolver{env: "staging", servers: []string{"srv-1", "srv-2"}}
	svc := New(db, WithEnvResolver(res))
	projID := seedProject(t, db)

	rn, err := svc.Create(context.Background(), projID, Trigger{Type: TriggerManual, Branch: "release/1.2"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if !res.called || res.gotBr != "release/1.2" {
		t.Fatalf("resolver 未按分支调用:called=%v br=%q", res.called, res.gotBr)
	}
	env, targets := readResolved(t, db, rn.ID)
	if env != "staging" {
		t.Errorf("resolved_environment = %q, want staging", env)
	}
	if targets != `["srv-1","srv-2"]` {
		t.Errorf("resolved_target_server_ids = %q, want srv-1/srv-2", targets)
	}
}

// TestCreateResolvesEnvUsesDefaultBranch 证补解析发生在默认分支回退之后:分支留空 →
// 取项目默认分支(main)→ 据其解析环境。
func TestCreateResolvesEnvUsesDefaultBranch(t *testing.T) {
	db := testDB(t)
	res := &stubEnvResolver{env: "prod"}
	svc := New(db, WithEnvResolver(res))
	projID := seedProject(t, db) // default_branch = main

	rn, err := svc.Create(context.Background(), projID, Trigger{Type: TriggerManual})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if res.gotBr != "main" {
		t.Errorf("resolver 应收到默认分支 main,实收 %q", res.gotBr)
	}
	if env, _ := readResolved(t, db, rn.ID); env != "prod" {
		t.Errorf("resolved_environment = %q, want prod", env)
	}
}

// TestCreateSkipsResolutionWhenEnvAlreadySet 证 webhook 路径(已显式带 ResolvedEnvironment)
// 不被二次解析:resolver 不被调用,环境保持原值。
func TestCreateSkipsResolutionWhenEnvAlreadySet(t *testing.T) {
	db := testDB(t)
	res := &stubEnvResolver{env: "should-not-be-used"}
	svc := New(db, WithEnvResolver(res))
	projID := seedProject(t, db)

	rn, err := svc.Create(context.Background(), projID, Trigger{
		Type:                TriggerWebhook,
		Branch:              "main",
		ResolvedEnvironment: "production",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if res.called {
		t.Error("已带环境时不应调用 resolver(避免二次解析)")
	}
	if env, _ := readResolved(t, db, rn.ID); env != "production" {
		t.Errorf("resolved_environment = %q, want production(原值不变)", env)
	}
}

// TestGetHydratesResolvedEnv 证 Get 把已落库的 resolved_environment / resolved_target_server_ids
// 回填进返回的 Run.Trigger。此前 Get 的 SELECT 漏读这两列 → 构建/部署执行器(经 Get 取 run)
// 恒拿到空环境 → resolveRegistry 返回 nil → 镜像永不推送、deploy_ssh pull 不到(image
// build→push→deploy 全链路在 Get 路径下形同虚设)。真 docker-in-docker 走页面部署时暴露。
func TestGetHydratesResolvedEnv(t *testing.T) {
	db := testDB(t)
	res := &stubEnvResolver{env: "prod", servers: []string{"srv-a", "srv-b"}}
	svc := New(db, WithEnvResolver(res))
	projID := seedProject(t, db)

	rn, err := svc.Create(context.Background(), projID, Trigger{Type: TriggerManual, Branch: "main"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	got, err := svc.Get(context.Background(), rn.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Trigger.ResolvedEnvironment != "prod" {
		t.Errorf("Get 应回填 ResolvedEnvironment=prod,实得 %q", got.Trigger.ResolvedEnvironment)
	}
	if len(got.Trigger.ResolvedTargetServerIDs) != 2 ||
		got.Trigger.ResolvedTargetServerIDs[0] != "srv-a" ||
		got.Trigger.ResolvedTargetServerIDs[1] != "srv-b" {
		t.Errorf("Get 应回填 ResolvedTargetServerIDs=[srv-a srv-b],实得 %v", got.Trigger.ResolvedTargetServerIDs)
	}
}

// TestCreateNoResolverKeepsEmptyEnv 证无 resolver(未注入)时行为不变:手动触发环境保持空。
func TestCreateNoResolverKeepsEmptyEnv(t *testing.T) {
	db := testDB(t)
	svc := New(db) // 不注入 resolver
	projID := seedProject(t, db)

	rn, err := svc.Create(context.Background(), projID, Trigger{Type: TriggerManual, Branch: "main"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if env, targets := readResolved(t, db, rn.ID); env != "" || targets != "[]" {
		t.Errorf("无 resolver 应保持空环境:env=%q targets=%q", env, targets)
	}
}
