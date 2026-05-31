package deploy

import (
	"context"
	"testing"

	"github.com/huangchengsir/pipewright/internal/run"
)

// stage_image_test.go 覆盖「流水线部署节点」(DeployForStage)对 **镜像产物** 的部署编排:
//   - 仅镜像产物时,DeployForStage 选中镜像并 docker pull → rm → run(不再 ErrArtifactNotFound)。
//   - 镜像 + 文件产物并存时的优先级(默认文件优先;cfg["artifactType"]=image 选镜像)。
//   - cfg 驱动的容器名 / 端口 / runArgs 原样进 docker run(array 化,不拼 shell)。
//   - 蓝绿策略下镜像走 stage(pull)→ cutover(run);健康失败回滚上一镜像。
//   - 文件产物路径不受影响(回归保护)。
// 全部用 stubTarget / touchRecorder 捕获命令断言,不触真实 SSH / docker。

// addArtifact 给已有 run 追加一件产物(用于「镜像+文件并存」场景)。
func addArtifact(t *testing.T, rsvc run.Service, runID, artType, name, ref string) {
	t.Helper()
	if _, err := rsvc.AddArtifact(context.Background(), run.Artifact{
		RunID: runID, Type: artType, Name: name, Reference: ref,
	}); err != nil {
		t.Fatalf("AddArtifact(%s): %v", artType, err)
	}
}

// hasCmd 报告命令序列里是否出现「以 prefix 开头」的命令。
func hasCmd(calls [][]string, prefix ...string) bool {
	for _, c := range calls {
		if len(c) < len(prefix) {
			continue
		}
		ok := true
		for i := range prefix {
			if c[i] != prefix[i] {
				ok = false
				break
			}
		}
		if ok {
			return true
		}
	}
	return false
}

// runCmd 取序列里首个 `docker run` 命令(无则 nil)。
func runCmd(calls [][]string) []string {
	for _, c := range calls {
		if len(c) >= 2 && c[0] == "docker" && c[1] == "run" {
			return c
		}
	}
	return nil
}

// TestStageDeploysImageArtifact 仅镜像产物 → DeployForStage 部署镜像(此前被跳过 → ErrArtifactNotFound)。
func TestStageDeploysImageArtifact(t *testing.T) {
	db := testDB(t)
	rsvc := run.New(db)
	tgt := &stubTarget{}
	srv := seedServer(t, tgt, "web-1")
	runID, _ := seedSuccessRunWithArtifact(t, db, rsvc, run.ArtifactImage, "registry/shop:1.0")

	svc := New(tgt, rsvc)
	res, err := svc.DeployForStage(context.Background(), runID, []string{srv.ID}, nil, "")
	if err != nil {
		t.Fatalf("DeployForStage: %v", err)
	}
	if len(res) != 1 || res[0].Status != run.TargetSuccess {
		t.Fatalf("want 1 success, got %+v", res)
	}
	if !hasCmd(tgt.calls, "docker", "pull", "registry/shop:1.0") {
		t.Fatalf("应有 docker pull 镜像: %v", tgt.calls)
	}
	if !hasCmd(tgt.calls, "docker", "run") {
		t.Fatalf("应有 docker run 起容器: %v", tgt.calls)
	}
	// 不应走文件发布(无 mkdir releases / ln -sfn)。
	if hasCmd(tgt.calls, "ln", "-sfn") {
		t.Fatalf("镜像部署不应出现软链切换: %v", tgt.calls)
	}
}

// TestStagePrefersFileArtifactByDefault 镜像+文件并存,默认(cfg 空)优先文件发布(保持既有行为)。
func TestStagePrefersFileArtifactByDefault(t *testing.T) {
	db := testDB(t)
	rsvc := run.New(db)
	tgt := &stubTarget{}
	srv := seedServer(t, tgt, "web-1")
	runID, _ := seedSuccessRunWithArtifact(t, db, rsvc, run.ArtifactImage, "registry/shop:1.0")
	addArtifact(t, rsvc, runID, run.ArtifactDist, "web", "dist/shop.tar.gz")

	svc := New(tgt, rsvc)
	res, err := svc.DeployForStage(context.Background(), runID, []string{srv.ID}, nil, "")
	if err != nil {
		t.Fatalf("DeployForStage: %v", err)
	}
	if len(res) != 1 || res[0].Status != run.TargetSuccess {
		t.Fatalf("want 1 success, got %+v", res)
	}
	// 默认应走文件发布(dist → ln -sfn current),不应 docker pull。
	if !hasCmd(tgt.calls, "ln", "-sfn") {
		t.Fatalf("默认应走文件发布(软链切换): %v", tgt.calls)
	}
	if hasCmd(tgt.calls, "docker", "pull") {
		t.Fatalf("默认不应部署镜像: %v", tgt.calls)
	}
}

// TestStageSelectsImageWhenConfigured 镜像+文件并存,cfg["artifactType"]=image → 选镜像。
func TestStageSelectsImageWhenConfigured(t *testing.T) {
	db := testDB(t)
	rsvc := run.New(db)
	tgt := &stubTarget{}
	srv := seedServer(t, tgt, "web-1")
	runID, _ := seedSuccessRunWithArtifact(t, db, rsvc, run.ArtifactDist, "dist/shop.tar.gz")
	addArtifact(t, rsvc, runID, run.ArtifactImage, "shop", "registry/shop:2.0")

	svc := New(tgt, rsvc)
	res, err := svc.DeployForStage(context.Background(), runID, []string{srv.ID},
		map[string]string{"artifactType": "image"}, "")
	if err != nil {
		t.Fatalf("DeployForStage: %v", err)
	}
	if len(res) != 1 || res[0].Status != run.TargetSuccess {
		t.Fatalf("want 1 success, got %+v", res)
	}
	if !hasCmd(tgt.calls, "docker", "pull", "registry/shop:2.0") {
		t.Fatalf("cfg image 偏好应部署镜像: %v", tgt.calls)
	}
	if hasCmd(tgt.calls, "ln", "-sfn") {
		t.Fatalf("选镜像时不应走文件发布: %v", tgt.calls)
	}
}

// TestStageImageHonorsContainerNamePortsRunArgs 验证 cfg 的容器名 / 端口 / runArgs 原样进 docker run。
func TestStageImageHonorsContainerNamePortsRunArgs(t *testing.T) {
	db := testDB(t)
	rsvc := run.New(db)
	tgt := &stubTarget{}
	srv := seedServer(t, tgt, "web-1")
	runID, _ := seedSuccessRunWithArtifact(t, db, rsvc, run.ArtifactImage, "registry/shop:1.0")

	svc := New(tgt, rsvc)
	cfg := map[string]string{
		"containerName": "shopapp",
		"ports":         "8080:80, 9000:9000",
		"runArgs":       "-e KEY=value --restart always",
	}
	res, err := svc.DeployForStage(context.Background(), runID, []string{srv.ID}, cfg, "")
	if err != nil {
		t.Fatalf("DeployForStage: %v", err)
	}
	if res[0].Status != run.TargetSuccess {
		t.Fatalf("want success, got %+v", res)
	}
	rc := runCmd(tgt.calls)
	if rc == nil {
		t.Fatalf("无 docker run 命令: %v", tgt.calls)
	}
	// 期望:docker run -d --name shopapp -p 8080:80 -p 9000:9000 -e KEY=value --restart always registry/shop:1.0
	want := []string{
		"docker", "run", "-d", "--name", "shopapp",
		"-p", "8080:80", "-p", "9000:9000",
		"-e", "KEY=value", "--restart", "always",
		"registry/shop:1.0",
	}
	if len(rc) != len(want) {
		t.Fatalf("docker run 参数不符\n got: %v\nwant: %v", rc, want)
	}
	for i := range want {
		if rc[i] != want[i] {
			t.Fatalf("docker run[%d]=%q, want %q\n got: %v", i, rc[i], want[i], rc)
		}
	}
	// rm 旧容器也应用 cfg 容器名(幂等替换)。
	if !hasCmd(tgt.calls, "docker", "rm", "-f", "shopapp") {
		t.Fatalf("应按 cfg 容器名移除旧容器: %v", tgt.calls)
	}
}

// TestStageImageBlueGreen 蓝绿策略下镜像走 stage(pull)→ cutover(run),且 run 带 cfg runArgs。
func TestStageImageBlueGreen(t *testing.T) {
	db := testDB(t)
	rsvc := run.New(db)
	rec := &touchRecorder{}
	tgt := &stubTarget{execFn: rec.exec}
	s1 := seedServer(t, tgt, "web-1")
	s2 := seedServer(t, tgt, "web-2")
	runID, _ := seedSuccessRunWithArtifact(t, db, rsvc, run.ArtifactImage, "registry/shop:2.0")

	svc := New(tgt, rsvc)
	cfg := map[string]string{"containerName": "shop", "ports": "80:80"}
	res, err := svc.DeployForStage(context.Background(), runID, []string{s1.ID, s2.ID}, cfg, "blue_green")
	if err != nil {
		t.Fatalf("DeployForStage: %v", err)
	}
	for i, r := range res {
		if r.Status != run.TargetSuccess {
			t.Fatalf("蓝绿镜像目标 %d 应 success,实际 %s / %q", i, r.Status, r.Message)
		}
	}
	if !hasCmd(rec.calls2, "docker", "pull", "registry/shop:2.0") {
		t.Fatalf("蓝绿应有 pull(预备): %v", rec.calls2)
	}
	rc := runCmd(rec.calls2)
	if rc == nil {
		t.Fatalf("蓝绿应有 docker run(切换): %v", rec.calls2)
	}
	// cutover 的 run 应带 cfg 容器名与端口。
	if !hasCmd(rec.calls2, "docker", "run", "-d", "--name", "shop", "-p", "80:80", "registry/shop:2.0") {
		t.Fatalf("蓝绿切换 run 应带 cfg 容器名 + 端口: %v", rec.calls2)
	}
}

// TestStageImageBlueGreenRollback 蓝绿镜像:一机健康失败 → 已切换成功的机回滚到上一镜像。
func TestStageImageBlueGreenRollback(t *testing.T) {
	db := testDB(t)
	rsvc := run.New(db)
	badID := ""
	rec := &touchRecorder{
		inspectImage: "registry/shop:1.0", // 模拟上一镜像存在 → 可回滚
		failOn: func(serverID string, cmd []string) bool {
			return serverID == badID && len(cmd) > 0 && cmd[0] == "true" // bad 机健康失败
		},
	}
	tgt := &stubTarget{execFn: rec.exec}
	good := seedServer(t, tgt, "web-good")
	bad := seedServer(t, tgt, "web-bad")
	badID = bad.ID
	runID, _ := seedSuccessRunWithArtifact(t, db, rsvc, run.ArtifactImage, "registry/shop:2.0")

	svc := New(tgt, rsvc)
	// DeployForStage 不带 HealthCheck 入参,这里直接调内部蓝绿编排验证回滚(健康门控经 hc)。
	hc := &HealthCheck{Type: HealthCheckCommand, Command: []string{"true"}, Retries: 1}
	res, err := svc.Deploy(context.Background(), DeployInput{
		RunID: runID, ArtifactID: mustArtID(t, rsvc, runID), ServerIDs: []string{good.ID, bad.ID},
		Strategy: "blue_green", HealthCheck: hc,
		Config: map[string]string{"containerName": "shop"},
	})
	if err != nil {
		t.Fatalf("Deploy: %v", err)
	}
	for i, r := range res {
		if r.Status != run.TargetRolledBack {
			t.Fatalf("镜像机群回滚:目标 %d (%s) 应 rolled_back,实际 %s / %q", i, r.ServerName, r.Status, r.Message)
		}
	}
	// 回滚应 docker run 上一镜像(带 cfg 容器名)。
	if !hasCmd(rec.calls2, "docker", "run", "-d", "--name", "shop", "registry/shop:1.0") {
		t.Fatalf("回滚应以 cfg 容器名起上一镜像: %v", rec.calls2)
	}
}

// mustArtID 取该 run 首件产物 id(测试便捷)。
func mustArtID(t *testing.T, rsvc run.Service, runID string) string {
	t.Helper()
	arts, err := rsvc.ListArtifacts(context.Background(), runID)
	if err != nil || len(arts) == 0 {
		t.Fatalf("ListArtifacts: %v / %d", err, len(arts))
	}
	return arts[0].ID
}

// TestPickStageArtifact 单测产物选取优先级矩阵。
func TestPickStageArtifact(t *testing.T) {
	img := run.Artifact{Type: run.ArtifactImage, Reference: "img:1"}
	dist := run.Artifact{Type: run.ArtifactDist, Reference: "dist"}
	jar := run.Artifact{Type: run.ArtifactJar, Reference: "app.jar"}

	cases := []struct {
		name   string
		arts   []run.Artifact
		prefer string
		want   string // 期望选中的 Type;"" = nil
	}{
		{"empty", nil, "", ""},
		{"image only / no prefer", []run.Artifact{img}, "", run.ArtifactImage},
		{"file only / no prefer", []run.Artifact{dist}, "", run.ArtifactDist},
		{"both / no prefer → file first", []run.Artifact{img, dist}, "", run.ArtifactDist},
		{"both / prefer image", []run.Artifact{img, dist}, "image", run.ArtifactImage},
		{"both / prefer image (order swapped)", []run.Artifact{dist, img}, "image", run.ArtifactImage},
		{"prefer image but none → fall back file", []run.Artifact{dist}, "image", run.ArtifactDist},
		{"prefer jar exact", []run.Artifact{dist, jar}, "jar", run.ArtifactJar},
		{"prefer dist but only jar → any release", []run.Artifact{jar}, "dist", run.ArtifactJar},
		{"unknown type skipped", []run.Artifact{{Type: "weird"}}, "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := pickStageArtifact(tc.arts, tc.prefer)
			if tc.want == "" {
				if got != nil {
					t.Fatalf("want nil, got %+v", got)
				}
				return
			}
			if got == nil || got.Type != tc.want {
				t.Fatalf("want %s, got %+v", tc.want, got)
			}
		})
	}
}
