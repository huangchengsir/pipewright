package build

// e2e_docker_test.go 是 Story 3-3(隔离构建)+ 3-5(镜像推送)的**真 Docker** 端到端测试。
//
// 与 builder_test.go(fake Commander/Cloner 单测)互补:本测试用**真 shellDriver**(探测出的
// docker)对**真 docker daemon** 跑完整 Builder.Run —— 真 `docker build`(模型 A)→ 真 `tag`
// → 真 `push` 到本地 `registry:2` → 真 `inspect` 取 digest → emit 真实产物;再 `docker pull`
// 拉回确认推送真落地。验证 fake 证不到的真实行为:真实退出码、真实 digest 解析(parseInspect)、
// secret 经 --build-arg 真注入但不泄漏进日志、--password-stdin 路径。
//
// 默认 SKIP:仅当 PIPEWRIGHT_E2E_DOCKER=1 且本机 docker daemon 就绪时运行(CI 无此环境)。
// 跑法:PIPEWRIGHT_E2E_DOCKER=1 go test ./internal/build/ -run E2EDocker -race -v

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/huangchengsir/pipewright/internal/mask"
	"github.com/huangchengsir/pipewright/internal/pipeline"
	"github.com/huangchengsir/pipewright/internal/project"
	"github.com/huangchengsir/pipewright/internal/run"
)

// e2eRegistryPort 是本测试起的本地匿名 registry:2 端口(127.0.0.1 only;避开 5000=AirPlay)。
const e2eRegistryPort = "5050"

// copyDockerfileCloner 是 e2e 克隆替身:把一份真实 Dockerfile 写进工作区(替代真触网克隆),
// 其后的 docker build 走真实路径。返回固定短 sha(产物 tag 用)。
type copyDockerfileCloner struct {
	dockerfile  string
	commitShort string
}

func (c *copyDockerfileCloner) Clone(_ context.Context, _, _, _, _, destDir string) (*CloneResolved, error) {
	if err := os.WriteFile(filepath.Join(destDir, "Dockerfile"), []byte(c.dockerfile), 0o644); err != nil {
		return nil, err
	}
	return &CloneResolved{CommitShort: c.commitShort}, nil
}

// dockerReadyOrSkip 探测真 docker daemon;不可用则 SKIP(不 FAIL,环境前置)。
func dockerReadyOrSkip(t *testing.T) Driver {
	t.Helper()
	if os.Getenv("PIPEWRIGHT_E2E_DOCKER") != "1" {
		t.Skip("设 PIPEWRIGHT_E2E_DOCKER=1 启用真 Docker 构建/推送 e2e")
	}
	drv, err := DetectDriver(nil)
	if err != nil {
		t.Skipf("未探测到容器 CLI: %v", err)
	}
	if out, derr := exec.Command(drv.Binary(), "info").CombinedOutput(); derr != nil {
		t.Skipf("docker daemon 未就绪(先启动 Docker): %v\n%s", derr, tailN(string(out), 3))
	}
	return drv
}

// startLocalRegistry 起一个本地匿名 registry:2(127.0.0.1:port),返回清理函数。
func startLocalRegistry(t *testing.T, bin string) (addr string, cleanup func()) {
	t.Helper()
	name := "pw-e2e-registry"
	_ = exec.Command(bin, "rm", "-f", name).Run() // 清残留
	out, err := exec.Command(bin, "run", "-d", "--name", name,
		"-p", "127.0.0.1:"+e2eRegistryPort+":5000", "registry:2").CombinedOutput()
	if err != nil {
		t.Skipf("起本地 registry 失败(端口被占?): %v\n%s", err, string(out))
	}
	addr = "localhost:" + e2eRegistryPort
	// 等 registry 端口就绪(最多 ~10s)。
	ready := false
	for i := 0; i < 40; i++ {
		if c, derr := exec.Command(bin, "exec", name, "true").CombinedOutput(); derr == nil {
			_ = c
			ready = true
			break
		}
		time.Sleep(250 * time.Millisecond)
	}
	if !ready {
		_ = exec.Command(bin, "rm", "-f", name).Run()
		t.Skip("本地 registry 未在预期时间内就绪")
	}
	return addr, func() { _ = exec.Command(bin, "rm", "-f", name).Run() }
}

// TestE2EDockerBuildPushReal 跑完整 Builder.Run:真 build → 真 push 到本地 registry → 拉回确认。
func TestE2EDockerBuildPushReal(t *testing.T) {
	drv := dockerReadyOrSkip(t)
	bin := drv.Binary()
	registryAddr, cleanupReg := startLocalRegistry(t, bin)
	defer cleanupReg()

	const commit = "abc1234def"
	const shortSha = "abc1234"
	dockerfile := "FROM busybox\n" +
		"RUN echo pipewright-e2e > /built.txt\n" +
		"CMD [\"cat\", \"/built.txt\"]\n"

	proj := &project.Project{ID: "p1", Name: "e2e-app", RepoURL: "https://example.com/r.git"}
	registry := &pipeline.ImageRegistry{Type: pipeline.RegistryCustom, URL: registryAddr} // 无 cred → 匿名推送
	settings := imageSettings(registry)

	b := &Builder{
		projects: fakeProjects{proj: proj},
		settings: fakeSettings{settings: settings},
		vault:    fakeVault{secrets: map[string]string{}},
		driver:   drv, // 真 docker 驱动
		cloner:   &copyDockerfileCloner{dockerfile: dockerfile, commitShort: shortSha},
	}

	localTag := "pipewright/e2e-app:" + shortSha
	remoteTag := registryAddr + "/e2e-app:" + shortSha
	// 清理本测试产生的镜像(best-effort)。
	defer func() { _ = exec.Command(bin, "rmi", "-f", localTag, remoteTag).Run() }()

	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()

	r := &run.Run{
		ID: "r1", ProjectID: "p1",
		Trigger: run.Trigger{Type: run.TriggerManual, Branch: "main", Commit: commit, ResolvedEnvironment: "prod"},
	}
	sink := newFakeSink()

	if err := b.Run(ctx, r, sink); err != nil {
		t.Fatalf("Builder.Run 真 docker 构建+推送失败: %v\n失败日志:\n%s\n全部日志:\n%s",
			err, sink.failureLog, tailN(sink.allLogText(), 20))
	}

	// 步骤:拉取源码 / 构建 / 推送镜像 全 success。
	wantSteps := []string{stepClone, stepBuild, stepPush}
	if strings.Join(sink.plan, ",") != strings.Join(wantSteps, ",") {
		t.Fatalf("步骤计划不符,期望 %v 实际 %v", wantSteps, sink.plan)
	}
	for i := range wantSteps {
		if sink.done[i] != run.StepSuccess {
			t.Fatalf("步骤 %d(%s)非 success,实际 %q", i, wantSteps[i], sink.done[i])
		}
	}

	// 产物:image 类型,reference=远端 tag,digest 真实(sha256),size>0。
	if len(sink.artifacts) != 1 {
		t.Fatalf("期望 1 个产物,实际 %d", len(sink.artifacts))
	}
	art := sink.artifacts[0]
	if art.Type != run.ArtifactImage {
		t.Errorf("产物类型应为 image,实际 %q", art.Type)
	}
	if art.Reference != remoteTag {
		t.Errorf("产物 reference 应为远端 tag %q,实际 %q", remoteTag, art.Reference)
	}
	digest, _ := art.Metadata["digest"].(string)
	if !strings.Contains(digest, "sha256:") {
		t.Errorf("产物 digest 应含 sha256,实际 %q", digest)
	}
	if art.SizeBytes <= 0 {
		t.Errorf("产物 size 应 >0,实际 %d", art.SizeBytes)
	}
	t.Logf("✅ 真构建+推送 OK;产物 ref=%s digest=%s size=%d", art.Reference, digest, art.SizeBytes)

	// 终极确认:删本地远端 tag → 从 registry 真 pull 回来,能拉回即证明推送真落地。
	_ = exec.Command(bin, "rmi", "-f", remoteTag).Run()
	if out, perr := exec.Command(bin, "pull", remoteTag).CombinedOutput(); perr != nil {
		t.Fatalf("从本地 registry 拉回 %s 失败 → 推送未真落地: %v\n%s", remoteTag, perr, string(out))
	}
	t.Logf("✅ 从 registry 拉回 %s 成功 → 推送真落地确认", remoteTag)
}

// TestE2EDockerSecretBuildArgReal 验 secret 构建变量经 --build-arg 真注入 docker build 的**两层防御**:
//
// 层 1(build 包自身):命令回显只列 key(`API_TOKEN=***`,emitCmd 脱敏)——build 包做到。
// 层 2(下游 per-run Masker,红线修的 WithLogMaskerFunc):BuildKit 会回显 RUN 里被替换的 arg
//
//	值(Docker 亦警告 SecretsUsedInArgOrEnv),build 包拦不住裸输出;真实管道靠 pool 的 logMasker
//	(登记该 run 真实凭据明文)在落库/出网前 Scrub 成 [MASKED]。本测试把裸日志过一遍真 mask.Masker
//	复现该层,断言明文被抹掉。
//
// 改进项(deferred):secret 宜走 BuildKit `--secret`(RUN --mount=type=secret)而非 --build-arg,
// 从源头不进构建输出;v1 取 --build-arg + 下游脱敏的务实组合,已在 deferred-work 记录。
func TestE2EDockerSecretBuildArgReal(t *testing.T) {
	drv := dockerReadyOrSkip(t)
	bin := drv.Binary()

	const secretVal = "s3cr3t_e2e_TOKEN_zzz"
	const shortSha = "sec1234"
	// Dockerfile 用 ARG 接收 secret 并落到镜像内文件(证明值真注入);构建日志不打印它。
	dockerfile := "FROM busybox\n" +
		"ARG API_TOKEN\n" +
		"RUN test -n \"$API_TOKEN\" && echo ok > /tokchk.txt\n" +
		"CMD [\"cat\", \"/tokchk.txt\"]\n"

	proj := &project.Project{ID: "p1", Name: "sec-app", RepoURL: "https://example.com/r.git"}
	settings := imageSettings(nil) // 不推送
	settings.Build.Vars = []pipeline.BuildVar{{Key: "API_TOKEN", Secret: true, CredentialID: "cred-1"}}

	b := &Builder{
		projects: fakeProjects{proj: proj},
		settings: fakeSettings{settings: settings},
		vault:    fakeVault{secrets: map[string]string{"cred-1": secretVal}},
		driver:   drv,
		cloner:   &copyDockerfileCloner{dockerfile: dockerfile, commitShort: shortSha},
	}
	localTag := "pipewright/sec-app:" + shortSha
	defer func() { _ = exec.Command(bin, "rmi", "-f", localTag).Run() }()

	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	r := &run.Run{ID: "r2", ProjectID: "p1", Trigger: run.Trigger{Type: run.TriggerManual, Branch: "main", Commit: "sec1234aa"}}
	sink := newFakeSink()

	if err := b.Run(ctx, r, sink); err != nil {
		t.Fatalf("带 secret 的真构建失败(secret 应真注入使 `test -n` 通过): %v\n%s", err, tailN(sink.allLogText(), 15))
	}

	logText := sink.allLogText()

	// 层 1:build 包命令回显只列 key(emitCmd 脱敏),证明走了 secretArgs 路径。
	if !strings.Contains(logText, "API_TOKEN=***") {
		t.Errorf("命令回显应脱敏为 API_TOKEN=***,实际:\n%s", tailN(logText, 10))
	}
	// build 包**裸**输出含 BuildKit 回显的明文(预期:这正是下游 masker 存在的理由)。
	if !strings.Contains(logText, secretVal) {
		t.Logf("注:本次 BuildKit 未回显 arg 明文(Dockerfile 写法相关);层 2 仍验。")
	}

	// 层 2:把裸日志过一遍真 mask.Masker(复现 pool 的 per-run logMasker)→ 明文必被 [MASKED]。
	m := mask.NewMasker()
	m.RegisterSecret(secretVal)
	scrubbed := m.Scrub(logText)
	if strings.Contains(scrubbed, secretVal) {
		t.Fatalf("❌ 经 per-run Masker 后 secret 明文仍泄漏(下游脱敏失效)!\n%s", tailN(scrubbed, 12))
	}
	if !strings.Contains(scrubbed, "[MASKED]") {
		t.Errorf("脱敏后应含 [MASKED] 占位")
	}
	t.Logf("✅ 两层防御 OK:build 包回显脱敏 API_TOKEN=***;裸日志经 per-run Masker 明文→[MASKED]")
}

// tailN 取文本末 n 行(日志友好)。
func tailN(s string, n int) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}
