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

	// 唯一目标目录(便于断言落地)。
	dir := "/tmp/pipewright-deploy-real-" + uuid.NewString()[:8]
	t.Cleanup(func() { _ = os.RemoveAll(dir); _ = os.RemoveAll(dir + "-current") })

	dsvc := New(tgt, rsvc)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	res, err := dsvc.Deploy(ctx, DeployInput{
		RunID: runID, ArtifactID: artID, ServerIDs: []string{srv.ID},
		Config: map[string]string{"path": dir},
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

	// 断言文件真落地(SSH 真传输 + 真执行)。
	entries, rerr := os.ReadDir(dir)
	if rerr != nil {
		t.Fatalf("目标目录未创建: %v", rerr)
	}
	if len(entries) == 0 {
		t.Fatalf("目标目录为空,产物未落地")
	}
	t.Logf("落地文件: %v", entries)

	// 断言软链(dist 当前版本)。
	if _, lerr := os.Lstat(dir + "-current"); lerr != nil {
		t.Fatalf("dist 当前版本软链未建: %v", lerr)
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
