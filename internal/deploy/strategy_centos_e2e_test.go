package deploy

// strategy_centos_e2e_test.go 是部署策略(Story 8-8 / FR-8-8:金丝雀 / 蓝绿)的**真多机 e2e**:
// 复用 multi_target_centos_e2e 的 CentOS+sshd 容器集群 + 真 SSH,验 fake 证不到的真实策略语义:
//   - 金丝雀:金丝雀台不可达 → 其余台**绝不被部署**(真容器内断言无发布目录,仍运行旧版本)。
//   - 蓝绿成功:全机先就绪、统一切换 → 每台真落地 + current 软链。
//   - 蓝绿机群回滚:已有 v1 上一发布,蓝绿发 v2 + 健康检查仅一台失败 → **整个机群回滚到 v1**
//     (真容器内断言每台 current 都切回 v1,机群级「要么全切要么全退」)。
//
// 默认 SKIP(PIPEWRIGHT_E2E_DEPLOY=1 启用)。跑法:
//   PIPEWRIGHT_E2E_DEPLOY=1 go test ./internal/deploy/ -run E2EStrategy -v

import (
	"context"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/huangchengsir/pipewright/internal/run"
)

// TestE2EStrategyCanaryAbort:3 台,金丝雀(第 1 台)不可达 → 金丝雀 failed、其余 2 台**未被部署**。
func TestE2EStrategyCanaryAbort(t *testing.T) {
	fleet, key := startCentOSFleet(t, 3)
	h := newMultiHarness(t, fleet, key)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	base := "/opt/pw-canary-" + uuid.NewString()[:8]
	runID, artID := seedSuccessRunWithArtifact(t, h.db, h.rsvc, run.ArtifactDist, "dist/shop-v1.tar.gz")

	// 金丝雀(第 1 台,serverIDs[0])部署前宕机 → SSH 不可达 → 金丝雀失败。
	canary := fleet[0]
	if out, err := exec.Command(canary.dockerBin, "stop", "-t", "1", canary.Name).CombinedOutput(); err != nil {
		t.Fatalf("停金丝雀台失败: %v\n%s", err, out)
	}

	res, err := h.dsvc.Deploy(ctx, DeployInput{
		RunID: runID, ArtifactID: artID, ServerIDs: h.serverIDs,
		Strategy: "canary",
		Config:   map[string]string{"releaseBase": base},
	})
	if err != nil {
		t.Fatalf("金丝雀 Deploy: %v", err)
	}
	// 金丝雀台 failed;其余 2 台被中止 → 也 failed(含「未部署」)。
	if res[0].Status != run.TargetFailed {
		t.Fatalf("金丝雀台应 failed,实际 %s", res[0].Status)
	}
	for i := 1; i < 3; i++ {
		if res[i].Status != run.TargetFailed || !strings.Contains(res[i].Message, "未部署") {
			t.Fatalf("其余台 %d 应 failed+未部署,实际 %s / %q", i, res[i].Status, res[i].Message)
		}
	}
	// 关键:其余 2 台真容器内**绝无发布目录**(金丝雀门控拦住了,仍运行旧版本)。
	releaseDir := base + "/releases/" + runID
	for _, idx := range []int{1, 2} {
		if _, err := fleet[idx].Exec(t, "test", "-e", releaseDir); err == nil {
			t.Fatalf("金丝雀失败后第 %d 台仍被部署(发布目录存在),金丝雀门控失效", idx+1)
		}
	}
	rn, _ := h.rsvc.Get(ctx, runID)
	if rn.Status != run.StatusFailed {
		t.Fatalf("金丝雀中止 run 终态应 failed,实际 %s", rn.Status)
	}
	t.Logf("✅ 金丝雀台不可达 → 其余 2 台真容器内零发布目录(门控拦截,未部署)")
}

// TestE2EStrategyBlueGreenSuccess:2 台蓝绿,全机就绪 → 统一切换 → 每台真落地 + current 软链。
func TestE2EStrategyBlueGreenSuccess(t *testing.T) {
	fleet, key := startCentOSFleet(t, 2)
	h := newMultiHarness(t, fleet, key)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	base := "/opt/pw-bg-" + uuid.NewString()[:8]
	runID, artID := seedSuccessRunWithArtifact(t, h.db, h.rsvc, run.ArtifactDist, "dist/shop-v1.tar.gz")

	res, err := h.dsvc.Deploy(ctx, DeployInput{
		RunID: runID, ArtifactID: artID, ServerIDs: h.serverIDs,
		Strategy: "blue_green",
		Config:   map[string]string{"releaseBase": base},
	})
	if err != nil {
		t.Fatalf("蓝绿 Deploy: %v", err)
	}
	for i, r := range res {
		if r.Status != run.TargetSuccess {
			t.Fatalf("蓝绿目标 %d 应 success,实际 %s / %q", i, r.Status, r.Message)
		}
	}
	releaseDir := base + "/releases/" + runID
	for i, c := range fleet {
		out, _ := c.Exec(t, "readlink", base+"/current")
		if strings.TrimSpace(out) != releaseDir {
			t.Fatalf("第 %d 台蓝绿后 current 应 → %s,实际 %q", i+1, releaseDir, strings.TrimSpace(out))
		}
	}
	t.Logf("✅ 蓝绿:2 台先就绪、统一切换,每台真落地 + current 软链")
}

// TestE2EStrategyBlueGreenFleetRollback:已有 v1,蓝绿发 v2 + 健康检查仅 1 台失败 → 整个机群回滚到 v1。
func TestE2EStrategyBlueGreenFleetRollback(t *testing.T) {
	fleet, key := startCentOSFleet(t, 2)
	h := newMultiHarness(t, fleet, key)
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	base := "/opt/pw-bgrb-" + uuid.NewString()[:8]

	// 1) 先滚动部署 v1 到两台,建立 current → v1(作为回滚目标)。
	runV1, artV1 := seedSuccessRunWithArtifact(t, h.db, h.rsvc, run.ArtifactDist, "dist/shop-v1.tar.gz")
	if _, err := h.dsvc.Deploy(ctx, DeployInput{
		RunID: runV1, ArtifactID: artV1, ServerIDs: h.serverIDs,
		Config: map[string]string{"releaseBase": base},
	}); err != nil {
		t.Fatalf("v1 预部署: %v", err)
	}
	v1Dir := base + "/releases/" + runV1
	for i, c := range fleet {
		out, _ := c.Exec(t, "readlink", base+"/current")
		if strings.TrimSpace(out) != v1Dir {
			t.Fatalf("v1 预部署后第 %d 台 current 应 → v1,实际 %q", i+1, strings.TrimSpace(out))
		}
	}

	// 2) 健康检查:test -f /tmp/pw-healthy。仅第 1 台(good)创建该文件 → 第 2 台(bad)健康必失败。
	if _, err := fleet[0].Exec(t, "touch", "/tmp/pw-healthy"); err != nil {
		t.Fatalf("good 台创建健康标记失败: %v", err)
	}

	// 3) 蓝绿发 v2 + 命令健康检查。bad 台健康失败 → 自身回滚;机群级 → good 台也回滚 → 两台都回 v1。
	runV2, artV2 := seedSuccessRunWithArtifact(t, h.db, h.rsvc, run.ArtifactDist, "dist/shop-v2.tar.gz")
	hc := &HealthCheck{Type: HealthCheckCommand, Command: []string{"test", "-f", "/tmp/pw-healthy"}, Retries: 1}
	res, err := h.dsvc.Deploy(ctx, DeployInput{
		RunID: runV2, ArtifactID: artV2, ServerIDs: h.serverIDs,
		Strategy:    "blue_green",
		HealthCheck: hc,
		Config:      map[string]string{"releaseBase": base},
	})
	if err != nil {
		t.Fatalf("v2 蓝绿 Deploy: %v", err)
	}
	// 两台都应 rolled_back(bad 健康失败自回滚;good 机群级回滚)。
	for i, r := range res {
		if r.Status != run.TargetRolledBack {
			t.Fatalf("蓝绿机群回滚:目标 %d 应 rolled_back,实际 %s / %q", i, r.Status, r.Message)
		}
	}
	// 关键:两台真容器内 current 都切回 v1(机群级原子性:要么全切要么全退)。
	for i, c := range fleet {
		out, _ := c.Exec(t, "readlink", base+"/current")
		if strings.TrimSpace(out) != v1Dir {
			t.Fatalf("机群回滚后第 %d 台 current 应切回 v1(%s),实际 %q", i+1, v1Dir, strings.TrimSpace(out))
		}
	}
	t.Logf("✅ 蓝绿机群回滚:1 台健康失败 → 两台真容器 current 全切回 v1(机群级原子性)")
}
