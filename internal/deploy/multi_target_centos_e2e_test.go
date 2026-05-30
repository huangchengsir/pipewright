package deploy

// multi_target_centos_e2e_test.go 是 Epic 4-5(多机并行部署 + 部分失败 · FR-13)的**真多机 e2e**:
// 用 docker 起**多台 CentOS Stream 9 + sshd 容器**当一个「服务器集群」,经真 SSH 把一份产物**并行**
// 部署到多台,验证 fake/单机证不到的真实多机语义:
//   - 全成功 → run 终态 success,每台都真落地(进各容器断言)。
//   - 一台不可达(模拟宕机)→ 该台 failed、**其余台不受连累仍真部署成功**、整体 partial_failed
//     (FR-13:并行、结果独立、非事务)。
//
// CentOS dnf 装 openssh 慢,故**先 build 一个预装 sshd 的镜像(全程一次)**,再从该镜像快起 N 台,
// 每台运行时注入同一测试 key。默认 SKIP(PIPEWRIGHT_E2E_DEPLOY=1 启用)。容器全程 t.Cleanup 清理。
// 跑法:PIPEWRIGHT_E2E_DEPLOY=1 go test ./internal/deploy/ -run E2EMultiTarget -v

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/huangchengsir/pipewright/internal/run"
	"github.com/huangchengsir/pipewright/internal/store"
	"github.com/huangchengsir/pipewright/internal/target"
	"github.com/huangchengsir/pipewright/internal/vault"
)

const centosBaseImage = "quay.io/centos/centos:stream9"

var (
	centosImgOnce sync.Once
	centosImgName string
	centosImgErr  error
)

// buildCentOSSSHImage 全程构建一次预装 openssh-server 的 CentOS 镜像(dnf 慢,只做一次)。
func buildCentOSSSHImage(t *testing.T, dockerBin string) string {
	t.Helper()
	centosImgOnce.Do(func() {
		dir, err := os.MkdirTemp("", "pw-centos-img-*")
		if err != nil {
			centosImgErr = err
			return
		}
		defer os.RemoveAll(dir)
		dockerfile := "FROM " + centosBaseImage + "\n" +
			"RUN dnf install -y openssh-server openssh-clients >/dev/null 2>&1 && ssh-keygen -A && \\\n" +
			"    sed -i 's/^#\\?PermitRootLogin.*/PermitRootLogin yes/' /etc/ssh/sshd_config && \\\n" +
			"    sed -i 's/^#\\?PubkeyAuthentication.*/PubkeyAuthentication yes/' /etc/ssh/sshd_config && \\\n" +
			"    mkdir -p /root/.ssh && chmod 700 /root/.ssh\n" +
			"CMD [\"/usr/sbin/sshd\",\"-D\",\"-e\"]\n"
		if werr := os.WriteFile(filepath.Join(dir, "Dockerfile"), []byte(dockerfile), 0o644); werr != nil {
			centosImgErr = werr
			return
		}
		centosImgName = "pw-e2e-centos-ssh:test"
		out, berr := exec.Command(dockerBin, "build", "-t", centosImgName, dir).CombinedOutput()
		if berr != nil {
			centosImgErr = fmt.Errorf("build centos ssh image: %v\n%s", berr, tailStr(string(out), 8))
		}
	})
	if centosImgErr != nil {
		t.Skipf("构建 CentOS+sshd 镜像失败(网络/dnf 不可达?): %v", centosImgErr)
	}
	return centosImgName
}

// startCentOSFleet 起 n 台 CentOS+sshd 容器(共用一把测试 key),全部就绪后返回。
func startCentOSFleet(t *testing.T, n int) ([]*sshContainer, string) {
	t.Helper()
	requireE2EDeploy(t)
	dockerBin := dockerBinOrSkip(t)
	img := buildCentOSSSHImage(t, dockerBin)

	privPEM, authLine := genTestSSHKey(t)
	bootstrap := `set -e
printf '%s\n' "$0" > /root/.ssh/authorized_keys
chmod 600 /root/.ssh/authorized_keys
exec /usr/sbin/sshd -D -e`

	fleet := make([]*sshContainer, 0, n)
	for i := 0; i < n; i++ {
		name := fmt.Sprintf("pw-e2e-mt-%d-%d-%d", os.Getpid(), time.Now().UnixNano()%100000, i)
		_ = exec.Command(dockerBin, "rm", "-f", name).Run()
		args := []string{"run", "-d", "--name", name, "-p", "0:22", img, "sh", "-c", bootstrap, authLine}
		if out, err := exec.Command(dockerBin, args...).CombinedOutput(); err != nil {
			t.Skipf("起 CentOS 容器 %s 失败: %v\n%s", name, err, string(out))
		}
		c := &sshContainer{Name: name, Host: "127.0.0.1", PrivateKey: privPEM, dockerBin: dockerBin}
		t.Cleanup(func() { _ = exec.Command(dockerBin, "rm", "-f", name).Run() })

		portOut, err := exec.Command(dockerBin, "port", name, "22/tcp").Output()
		if err != nil {
			c.dumpLogs(t)
			t.Fatalf("取容器 %s 端口失败: %v", name, err)
		}
		line := strings.TrimSpace(strings.Split(string(portOut), "\n")[0])
		if idx := strings.LastIndex(line, ":"); idx >= 0 {
			_, _ = fmt.Sscanf(line[idx+1:], "%d", &c.Port)
		}
		if c.Port == 0 {
			c.dumpLogs(t)
			t.Fatalf("解析容器 %s 端口失败: %q", name, line)
		}
		fleet = append(fleet, c)
	}
	// 全部就绪(sshd 预装,启动快;给 45s 足够)。
	for _, c := range fleet {
		c.waitReady(t, 45*time.Second)
	}
	return fleet, privPEM
}

// multiHarness 把真 vault+target+run+deploy 串起来,把整个容器集群登记为多台目标服务器。
type multiHarness struct {
	db        *sql.DB
	rsvc      run.Service
	dsvc      Service
	serverIDs []string
}

func newMultiHarness(t *testing.T, fleet []*sshContainer, privKey string) *multiHarness {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "mt.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	var key [32]byte
	copy(key[:], []byte("0123456789abcdef0123456789abcdef"))
	v := vault.New(st.DB, &key)
	cred, err := v.Create(vault.CreateInput{Name: "mt-ssh", Type: vault.TypeSSHKey, Secret: privKey})
	if err != nil {
		t.Fatalf("vault.Create: %v", err)
	}
	tgt := target.New(st.DB, v, nil) // 真 dialer
	ids := make([]string, 0, len(fleet))
	for i, c := range fleet {
		srv, cerr := tgt.Create(context.Background(), target.CreateInput{
			Name: fmt.Sprintf("web-%d", i+1), Host: c.Host, Port: c.Port, User: "root", CredentialID: cred.ID,
		})
		if cerr != nil {
			t.Fatalf("注册目标 web-%d: %v", i+1, cerr)
		}
		ids = append(ids, srv.ID)
	}
	rsvc := run.New(st.DB)
	return &multiHarness{db: st.DB, rsvc: rsvc, dsvc: New(tgt, rsvc), serverIDs: ids}
}

// TestE2EMultiTargetDeployAllSuccess 把一份 dist 并行部署到 3 台 CentOS,验全成功 + 每台真落地。
func TestE2EMultiTargetDeployAllSuccess(t *testing.T) {
	fleet, key := startCentOSFleet(t, 3)
	h := newMultiHarness(t, fleet, key)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	base := "/opt/pw-mt-" + uuid.NewString()[:8]
	runID, artID := seedSuccessRunWithArtifact(t, h.db, h.rsvc, run.ArtifactDist, "dist/shop-v1.tar.gz")

	res, err := h.dsvc.Deploy(ctx, DeployInput{
		RunID: runID, ArtifactID: artID, ServerIDs: h.serverIDs,
		Config: map[string]string{"releaseBase": base},
	})
	if err != nil {
		t.Fatalf("多机 Deploy: %v", err)
	}
	if len(res) != 3 {
		t.Fatalf("应有 3 台目标结果,实际 %d", len(res))
	}
	for _, r := range res {
		if r.Status != run.TargetSuccess {
			t.Fatalf("目标 %s 应 success,实际 %s", r.ServerID, r.Status)
		}
	}
	// 每台真落地(进各容器断言发布目录 + current 软链)。
	releaseDir := base + "/releases/" + runID
	for i, c := range fleet {
		if _, err := c.Exec(t, "test", "-e", releaseDir); err != nil {
			t.Fatalf("第 %d 台 web-%d 容器内发布目录未落地: %s", i+1, i+1, releaseDir)
		}
		out, _ := c.Exec(t, "readlink", base+"/current")
		if strings.TrimSpace(out) != releaseDir {
			t.Fatalf("第 %d 台 current 软链异常: %q", i+1, strings.TrimSpace(out))
		}
	}
	// run 终态:全成功 → success。
	rn, _ := h.rsvc.Get(ctx, runID)
	if rn.Status != run.StatusSuccess {
		t.Fatalf("全成功 run 终态应 success,实际 %s", rn.Status)
	}
	t.Logf("✅ dist 并行真部署到 3 台 CentOS 全成功,每台真落地 + current 软链切换")
}

// TestE2EMultiTargetPartialFailure 部署到 3 台,其中 1 台部署前宕机 → 该台 failed、其余真成功、整体 partial_failed(FR-13)。
func TestE2EMultiTargetPartialFailure(t *testing.T) {
	fleet, key := startCentOSFleet(t, 3)
	h := newMultiHarness(t, fleet, key)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	base := "/opt/pw-mtp-" + uuid.NewString()[:8]
	runID, artID := seedSuccessRunWithArtifact(t, h.db, h.rsvc, run.ArtifactDist, "dist/shop-v1.tar.gz")

	// 模拟第 2 台宕机:部署前 docker stop(SSH 将不可达)。
	down := fleet[1]
	if out, err := exec.Command(down.dockerBin, "stop", "-t", "1", down.Name).CombinedOutput(); err != nil {
		t.Fatalf("停第 2 台失败: %v\n%s", err, out)
	}

	res, err := h.dsvc.Deploy(ctx, DeployInput{
		RunID: runID, ArtifactID: artID, ServerIDs: h.serverIDs,
		Config: map[string]string{"releaseBase": base},
	})
	if err != nil {
		t.Fatalf("多机 Deploy(含宕机): %v", err)
	}

	// 结果:第 2 台(宕机)failed,其余两台 success。
	byID := map[string]TargetResult{}
	for _, r := range res {
		byID[r.ServerID] = r
	}
	if byID[h.serverIDs[1]].Status != run.TargetFailed {
		t.Fatalf("宕机台应 failed,实际 %s", byID[h.serverIDs[1]].Status)
	}
	for _, idx := range []int{0, 2} {
		if byID[h.serverIDs[idx]].Status != run.TargetSuccess {
			t.Fatalf("健康台 web-%d 应 success(失败不连累其余 · FR-13),实际 %s", idx+1, byID[h.serverIDs[idx]].Status)
		}
	}
	// 关键:健康两台**真落地**(失败被隔离,其余非事务地完成)。
	releaseDir := base + "/releases/" + runID
	for _, idx := range []int{0, 2} {
		if _, err := fleet[idx].Exec(t, "test", "-e", releaseDir); err != nil {
			t.Fatalf("健康台 web-%d 未真落地(失败隔离失效): %s", idx+1, releaseDir)
		}
	}
	// run 终态:有失败 + 有成功 → partial_failed。
	rn, _ := h.rsvc.Get(ctx, runID)
	if rn.Status != run.StatusPartialFailed {
		t.Fatalf("部分失败 run 终态应 partial_failed,实际 %s", rn.Status)
	}
	// 失败 message 不泄漏私钥。
	if strings.Contains(byID[h.serverIDs[1]].Message, "PRIVATE KEY") {
		t.Fatal("失败 message 泄漏私钥!")
	}
	t.Logf("✅ 3 台中 1 台宕机:该台 failed、其余 2 台真落地成功、整体 partial_failed(FR-13 失败隔离 + 非事务)")
}

// tailStr 取文本末 n 行(日志友好)。
func tailStr(s string, n int) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}
