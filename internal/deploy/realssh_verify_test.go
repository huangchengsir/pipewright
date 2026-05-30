//go:build realssh

// 真验:登记 localhost 服务器 + dist 产物 → 真部署(经真实 SSH 到 localhost:22)→ targets 回填。
// 默认不跑(需 -tags realssh + 本机 SSH 到 localhost 可用 + DEPLOY_SSH_KEY 指向私钥)。
//
//	export PATH="$HOME/sdk/go/bin:$PATH"
//	DEPLOY_SSH_KEY=$HOME/.ssh/id_ed25519 DEPLOY_SSH_USER=$USER \
//	  go test -tags realssh -run TestRealLocalhostDistDeploy ./internal/deploy/ -v
package deploy

import (
	"context"
	"os"
	"os/user"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/huangchengsir/pipewright/internal/run"
	"github.com/huangchengsir/pipewright/internal/target"
	"github.com/huangchengsir/pipewright/internal/vault"
)

func TestRealLocalhostDistDeploy(t *testing.T) {
	keyPath := os.Getenv("DEPLOY_SSH_KEY")
	if keyPath == "" {
		t.Skip("DEPLOY_SSH_KEY 未设置;跳过真验")
	}
	privKey, err := os.ReadFile(keyPath)
	if err != nil {
		t.Fatalf("读私钥: %v", err)
	}
	sshUser := os.Getenv("DEPLOY_SSH_USER")
	if sshUser == "" {
		if u, uerr := user.Current(); uerr == nil {
			sshUser = u.Username
		}
	}

	db := testDB(t)

	// 真 vault(32 字节 key)+ 真 target(nil dialer → 真实 x/crypto/ssh)。
	var key [32]byte
	copy(key[:], []byte("0123456789abcdef0123456789abcdef"))
	v := vault.New(db, &key)
	cred, err := v.Create(vault.CreateInput{Name: "loopback-ssh", Type: "ssh_key", Secret: string(privKey)})
	if err != nil {
		t.Fatalf("vault.Create: %v", err)
	}

	tgt := target.New(db, v, nil)
	srv, err := tgt.Create(context.Background(), target.CreateInput{
		Name: "localhost-deploy", Host: "127.0.0.1", Port: 22, User: sshUser, CredentialID: cred.ID,
	})
	if err != nil {
		t.Fatalf("target.Create: %v", err)
	}

	rsvc := run.New(db)
	runID, artID := seedSuccessRunWithArtifact(t, db, rsvc, run.ArtifactDist, "dist/shop-v1.tar.gz")

	// 唯一发布根目录(便于断言落地);dist 走 release 模式:<base>/releases/<runId> + <base>/current。
	base := "/tmp/pipewright-deploy-real-" + uuid.NewString()[:8]
	t.Cleanup(func() { _ = os.RemoveAll(base) })

	dsvc := New(tgt, rsvc)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	res, err := dsvc.Deploy(ctx, DeployInput{
		RunID: runID, ArtifactID: artID, ServerIDs: []string{srv.ID},
		Config: map[string]string{"releaseBase": base},
	})
	if err != nil {
		t.Fatalf("Deploy: %v", err)
	}
	if len(res) != 1 {
		t.Fatalf("want 1 target, got %d", len(res))
	}
	if res[0].Status != run.TargetSuccess {
		t.Fatalf("真部署应 success,got %s: %s", res[0].Status, res[0].Message)
	}
	t.Logf("真部署结果: status=%s message=%q", res[0].Status, res[0].Message)

	// 断言发布目录 <base>/releases/<runId> 真落地(SSH 真传输 + 真执行)。
	releaseDir := base + "/releases/" + runID
	entries, rerr := os.ReadDir(releaseDir)
	if rerr != nil {
		t.Fatalf("发布目录未创建: %v", rerr)
	}
	if len(entries) == 0 {
		t.Fatalf("发布目录为空,产物未落地")
	}
	t.Logf("落地文件: %v", entries)

	// 断言 current 软链真指向本次发布(原子切换)。
	link, lerr := os.Readlink(base + "/current")
	if lerr != nil {
		t.Fatalf("current 软链未建: %v", lerr)
	}
	if link != releaseDir {
		t.Fatalf("current 软链指向异常: %q want %q", link, releaseDir)
	}

	// run-detail targets slot 回填。
	targets, _ := rsvc.ListDeployTargets(context.Background(), runID)
	if len(targets) != 1 || targets[0].Status != run.TargetSuccess {
		t.Fatalf("targets 回填异常: %+v", targets)
	}

	// 断言 message / 内容无凭据明文。
	if strings.Contains(res[0].Message, "PRIVATE KEY") || strings.Contains(res[0].Message, "BEGIN OPENSSH") {
		t.Fatalf("message 泄漏私钥!")
	}
}

// TestRealLocalhostRollback 经真实 SSH 到 localhost 真验零停机切换 + 失败回滚全链路:
//
//	v1 部署(health=true)→ current → releases/<run1>(成功保留)
//	v2 部署(health=false,探测必失败)→ 切 current → releases/<run2> 后健康失败 →
//	  回滚 current → releases/<run1> + status=rolled_back。
//
// 默认不跑(需 -tags realssh + DEPLOY_SSH_KEY)。
func TestRealLocalhostRollback(t *testing.T) {
	keyPath := os.Getenv("DEPLOY_SSH_KEY")
	if keyPath == "" {
		t.Skip("DEPLOY_SSH_KEY 未设置;跳过真验")
	}
	privKey, err := os.ReadFile(keyPath)
	if err != nil {
		t.Fatalf("读私钥: %v", err)
	}
	sshUser := os.Getenv("DEPLOY_SSH_USER")
	if sshUser == "" {
		if u, uerr := user.Current(); uerr == nil {
			sshUser = u.Username
		}
	}

	db := testDB(t)
	var key [32]byte
	copy(key[:], []byte("0123456789abcdef0123456789abcdef"))
	v := vault.New(db, &key)
	cred, err := v.Create(vault.CreateInput{Name: "loopback-ssh", Type: "ssh_key", Secret: string(privKey)})
	if err != nil {
		t.Fatalf("vault.Create: %v", err)
	}
	tgt := target.New(db, v, nil)
	srv, err := tgt.Create(context.Background(), target.CreateInput{
		Name: "localhost-deploy", Host: "127.0.0.1", Port: 22, User: sshUser, CredentialID: cred.ID,
	})
	if err != nil {
		t.Fatalf("target.Create: %v", err)
	}

	rsvc := run.New(db)
	dsvc := New(tgt, rsvc)
	base := "/tmp/pipewright-rollback-real-" + uuid.NewString()[:8]
	t.Cleanup(func() { _ = os.RemoveAll(base) })

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// v1:健康检查必通过(true),current → releases/<run1>。
	run1, art1 := seedSuccessRunWithArtifact(t, db, rsvc, run.ArtifactDist, "dist/shop-v1.tar.gz")
	res1, err := dsvc.Deploy(ctx, DeployInput{
		RunID: run1, ArtifactID: art1, ServerIDs: []string{srv.ID},
		Config:      map[string]string{"releaseBase": base},
		HealthCheck: &HealthCheck{Type: HealthCheckCommand, Command: []string{"true"}, Retries: 1},
	})
	if err != nil {
		t.Fatalf("v1 Deploy: %v", err)
	}
	if res1[0].Status != run.TargetSuccess {
		t.Fatalf("v1 应 success, got %s: %s", res1[0].Status, res1[0].Message)
	}
	rel1 := base + "/releases/" + run1
	if link, _ := os.Readlink(base + "/current"); link != rel1 {
		t.Fatalf("v1 后 current 应指向 %q, got %q", rel1, link)
	}

	// v2:健康检查必失败(false),切 current → releases/<run2> 后健康失败 → 回滚到 rel1。
	run2, art2 := seedSuccessRunWithArtifact(t, db, rsvc, run.ArtifactDist, "dist/shop-v2.tar.gz")
	res2, err := dsvc.Deploy(ctx, DeployInput{
		RunID: run2, ArtifactID: art2, ServerIDs: []string{srv.ID},
		Config:      map[string]string{"releaseBase": base},
		HealthCheck: &HealthCheck{Type: HealthCheckCommand, Command: []string{"false"}, Retries: 1},
	})
	if err != nil {
		t.Fatalf("v2 Deploy: %v", err)
	}
	if res2[0].Status != run.TargetRolledBack {
		t.Fatalf("v2 健康失败应 rolled_back, got %s: %s", res2[0].Status, res2[0].Message)
	}
	t.Logf("v2 回滚结果: status=%s message=%q", res2[0].Status, res2[0].Message)

	// 关键断言:current 真切回 v1 的发布(零停机回滚)。
	link, lerr := os.Readlink(base + "/current")
	if lerr != nil {
		t.Fatalf("回滚后 current 软链丢失: %v", lerr)
	}
	if link != rel1 {
		t.Fatalf("回滚后 current 应切回 v1 %q, got %q", rel1, link)
	}
	// v2 失败发布仍保留(供排查),v1 发布仍在。
	if _, serr := os.Stat(base + "/releases/" + run2); serr != nil {
		t.Fatalf("失败发布 v2 应保留供排查: %v", serr)
	}
	// message 无凭据明文。
	if strings.Contains(res2[0].Message, "PRIVATE KEY") || strings.Contains(res2[0].Message, "BEGIN OPENSSH") {
		t.Fatalf("回滚 message 泄漏私钥!")
	}
}
