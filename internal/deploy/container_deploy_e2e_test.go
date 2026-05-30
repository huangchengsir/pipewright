package deploy

// container_deploy_e2e_test.go 是 Epic 4 部署的**真 e2e**:对一台真 alpine+sshd 容器(当真目标
// 服务器)经真 SSH 真跑部署/零停机/回滚/健康门控,并**进容器内 docker exec 断言**文件真落地、
// current 软链真切换 —— 这是 fake/localhost 证不到的真实路径(localhost e2e 里 target==本机,断言落在
// 本机 FS;容器版断言落在容器内,真传输 + 真远端文件系统都被覆盖)。
//
// 默认 SKIP(PIPEWRIGHT_E2E_DEPLOY=1 启用)。跑法:
//
//	PIPEWRIGHT_E2E_DEPLOY=1 go test ./internal/deploy/ -run E2E -v
//
// 容器内有真 docker?没有。故 image 产物部署不在此验(降级);聚焦 dist/file + 健康 + 零停机 + 回滚。

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/huangchengsir/pipewright/internal/run"
	"github.com/huangchengsir/pipewright/internal/store"
	"github.com/huangchengsir/pipewright/internal/target"
	"github.com/huangchengsir/pipewright/internal/vault"
)

// e2eHarness 把真 vault+target+run+deploy 串起来,登记容器为目标服务器。
type e2eHarness struct {
	db       *sql.DB
	rsvc     run.Service
	dsvc     Service
	serverID string
	c        *sshContainer
}

func newE2EHarness(t *testing.T, c *sshContainer) *e2eHarness {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "e2e.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	var key [32]byte
	copy(key[:], []byte("0123456789abcdef0123456789abcdef"))
	v := vault.New(st.DB, &key)
	cred, err := v.Create(vault.CreateInput{Name: "e2e-ssh", Type: vault.TypeSSHKey, Secret: c.PrivateKey})
	if err != nil {
		t.Fatalf("vault.Create: %v", err)
	}
	tgt := target.New(st.DB, v, nil) // 真 dialer
	srv, err := tgt.Create(context.Background(), target.CreateInput{
		Name: "e2e-target", Host: c.Host, Port: c.Port, User: "root", CredentialID: cred.ID,
	})
	if err != nil {
		t.Fatalf("target.Create: %v", err)
	}
	rsvc := run.New(st.DB)
	return &e2eHarness{db: st.DB, rsvc: rsvc, dsvc: New(tgt, rsvc), serverID: srv.ID, c: c}
}

// containerFileExists 经 docker exec 在**容器内**断言路径存在(真落地证据)。
func (h *e2eHarness) containerFileExists(t *testing.T, path string) bool {
	t.Helper()
	_, err := h.c.Exec(t, "test", "-e", path)
	return err == nil
}

// containerReadlink 经 docker exec 读容器内软链指向(零停机切换证据)。
func (h *e2eHarness) containerReadlink(t *testing.T, link string) string {
	t.Helper()
	out, err := h.c.Exec(t, "readlink", link)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

// TestE2EDistDeployLandsInContainer 验 dist 产物经真 SSH 真部署 → 进容器断言文件真落地 + 目录结构对。
func TestE2EDistDeployLandsInContainer(t *testing.T) {
	c := startSSHContainer(t)
	h := newE2EHarness(t, c)
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()

	base := "/opt/pw-e2e-dist-" + uuid.NewString()[:8]
	runID, artID := seedSuccessRunWithArtifact(t, h.db, h.rsvc, run.ArtifactDist, "dist/shop-v1.tar.gz")

	res, err := h.dsvc.Deploy(ctx, DeployInput{
		RunID: runID, ArtifactID: artID, ServerIDs: []string{h.serverID},
		Config: map[string]string{"releaseBase": base},
	})
	if err != nil {
		t.Fatalf("Deploy: %v", err)
	}
	if len(res) != 1 || res[0].Status != run.TargetSuccess {
		t.Fatalf("dist 部署应 success: %+v", res)
	}

	// 关键:进容器内断言发布目录 + 产物文件真落地(经真 SSH 真传输到真远端 FS)。
	releaseDir := base + "/releases/" + runID
	if !h.containerFileExists(t, releaseDir) {
		t.Fatalf("容器内发布目录未落地: %s", releaseDir)
	}
	// 产物文件名 = reference base(shop-v1.tar.gz)。
	artFile := releaseDir + "/shop-v1.tar.gz"
	if !h.containerFileExists(t, artFile) {
		t.Fatalf("容器内产物文件未落地: %s", artFile)
	}
	// current 软链真指向本次发布(原子切换)。
	if link := h.containerReadlink(t, base+"/current"); link != releaseDir {
		t.Fatalf("容器内 current 软链指向异常: %q want %q", link, releaseDir)
	}
	// 落地内容真为 reference 文本(base64 解码经真 SSH 写入)。
	content, _ := h.c.Exec(t, "cat", artFile)
	if !strings.Contains(content, "dist/shop-v1.tar.gz") {
		t.Fatalf("容器内产物内容异常: %q", content)
	}
	if strings.Contains(res[0].Message, "PRIVATE KEY") {
		t.Fatalf("message 泄漏私钥!")
	}
	t.Logf("dist 真落地容器 OK: %s -> current", releaseDir)
}

// TestE2EZeroDowntimeAndRollback 验零停机两版切换 + 健康失败回滚,全程进容器断言软链真切换。
func TestE2EZeroDowntimeAndRollback(t *testing.T) {
	c := startSSHContainer(t)
	h := newE2EHarness(t, c)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	base := "/opt/pw-e2e-zdt-" + uuid.NewString()[:8]

	// v1:健康检查通过(command true)→ current → releases/<run1>。
	run1, art1 := seedSuccessRunWithArtifact(t, h.db, h.rsvc, run.ArtifactDist, "dist/shop-v1.tar.gz")
	res1, err := h.dsvc.Deploy(ctx, DeployInput{
		RunID: run1, ArtifactID: art1, ServerIDs: []string{h.serverID},
		Config:      map[string]string{"releaseBase": base},
		HealthCheck: &HealthCheck{Type: HealthCheckCommand, Command: []string{"true"}, Retries: 1},
	})
	if err != nil {
		t.Fatalf("v1 Deploy: %v", err)
	}
	if res1[0].Status != run.TargetSuccess {
		t.Fatalf("v1 应 success: %+v", res1[0])
	}
	rel1 := base + "/releases/" + run1
	if link := h.containerReadlink(t, base+"/current"); link != rel1 {
		t.Fatalf("v1 后容器内 current 应 -> %q, got %q", rel1, link)
	}

	// v2:健康检查必失败(command false)→ 切 current → releases/<run2> 后健康失败 → 回滚到 rel1。
	run2, art2 := seedSuccessRunWithArtifact(t, h.db, h.rsvc, run.ArtifactDist, "dist/shop-v2.tar.gz")
	res2, err := h.dsvc.Deploy(ctx, DeployInput{
		RunID: run2, ArtifactID: art2, ServerIDs: []string{h.serverID},
		Config:      map[string]string{"releaseBase": base},
		HealthCheck: &HealthCheck{Type: HealthCheckCommand, Command: []string{"false"}, Retries: 1},
	})
	if err != nil {
		t.Fatalf("v2 Deploy: %v", err)
	}
	if res2[0].Status != run.TargetRolledBack {
		t.Fatalf("v2 健康失败应 rolled_back: %+v", res2[0])
	}

	// 关键:进容器断言 current 真切回 v1(零停机回滚),v2 失败发布仍保留供排查。
	if link := h.containerReadlink(t, base+"/current"); link != rel1 {
		t.Fatalf("回滚后容器内 current 应切回 v1 %q, got %q", rel1, link)
	}
	if !h.containerFileExists(t, base+"/releases/"+run2) {
		t.Fatalf("失败发布 v2 应保留供排查")
	}
	// run 终态:全部目标回滚 → failed。
	rn, _ := h.rsvc.Get(ctx, run2)
	if rn.Status != run.StatusFailed {
		t.Fatalf("v2 run 终态应 failed(全目标回滚),got %s", rn.Status)
	}
	t.Logf("零停机回滚 OK: current 真切回 %s", rel1)
}

// TestE2EHealthGate 验健康门控:通过型(test -f 落地文件)→ success;必失败型 → 首次部署无可回滚 → failed。
func TestE2EHealthGate(t *testing.T) {
	c := startSSHContainer(t)
	h := newE2EHarness(t, c)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// 通过型:健康命令在容器内真跑 `test -f <落地文件>`,文件由本次部署真落地 → 通过。
	basePass := "/opt/pw-e2e-hpass-" + uuid.NewString()[:8]
	runP, artP := seedSuccessRunWithArtifact(t, h.db, h.rsvc, run.ArtifactDist, "dist/shop.tar.gz")
	landed := basePass + "/releases/" + runP + "/shop.tar.gz"
	resP, err := h.dsvc.Deploy(ctx, DeployInput{
		RunID: runP, ArtifactID: artP, ServerIDs: []string{h.serverID},
		Config:      map[string]string{"releaseBase": basePass},
		HealthCheck: &HealthCheck{Type: HealthCheckCommand, Command: []string{"test", "-f", landed}, Retries: 2, IntervalSeconds: 1},
	})
	if err != nil {
		t.Fatalf("通过型 Deploy: %v", err)
	}
	if resP[0].Status != run.TargetSuccess {
		t.Fatalf("健康门控应通过(文件已真落地): %+v", resP[0])
	}
	if !strings.Contains(resP[0].Message, "健康检查通过") {
		t.Fatalf("成功 message 应含『健康检查通过』: %q", resP[0].Message)
	}

	// 必失败型(首次部署,无上一发布可回滚)→ failed。
	baseFail := "/opt/pw-e2e-hfail-" + uuid.NewString()[:8]
	runF, artF := seedSuccessRunWithArtifact(t, h.db, h.rsvc, run.ArtifactDist, "dist/shop.tar.gz")
	resF, err := h.dsvc.Deploy(ctx, DeployInput{
		RunID: runF, ArtifactID: artF, ServerIDs: []string{h.serverID},
		Config:      map[string]string{"releaseBase": baseFail},
		HealthCheck: &HealthCheck{Type: HealthCheckCommand, Command: []string{"test", "-f", "/nonexistent/pw-e2e-missing"}, Retries: 1},
	})
	if err != nil {
		t.Fatalf("必失败型 Deploy: %v", err)
	}
	if resF[0].Status != run.TargetFailed {
		t.Fatalf("首次部署健康失败应 failed(无可回滚): %+v", resF[0])
	}
	if !strings.Contains(resF[0].Message, "健康检查") {
		t.Fatalf("失败 message 应含健康检查原因: %q", resF[0].Message)
	}
	t.Logf("健康门控 OK: 通过型 success、必失败型 failed")
}
