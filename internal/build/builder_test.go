package build

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/huangchengsir/pipewright/internal/pipeline"
	"github.com/huangchengsir/pipewright/internal/project"
	"github.com/huangchengsir/pipewright/internal/run"
	"github.com/huangchengsir/pipewright/internal/vault"
)

// ---- fakes ----

// fakeProjects 只实现 Get(其余方法嵌入 nil 接口 → 调用即 panic,但测试不触发)。
type fakeProjects struct {
	project.Service
	proj *project.Project
	err  error
}

func (f fakeProjects) Get(_ context.Context, _ string) (*project.Project, error) {
	return f.proj, f.err
}

// fakeSettings 只实现 Get。
type fakeSettings struct {
	pipeline.SettingsService
	settings *pipeline.Settings
	err      error
}

func (f fakeSettings) Get(_ context.Context, _ string) (*pipeline.Settings, error) {
	return f.settings, f.err
}

// fakeVault 只实现 Reveal(按 credID 返回明文);其余方法嵌入 nil 接口。
type fakeVault struct {
	vault.Vault
	secrets map[string]string
}

func (f fakeVault) Reveal(id string) (string, error) {
	if v, ok := f.secrets[id]; ok {
		return v, nil
	}
	return "", vault.ErrNotFound
}

// recordedLog 是一条被 sink 记录的日志行。
type recordedLog struct {
	stream  string
	ordinal int
	line    string
}

// fakeSink 记录 runner 经 StepSink 报告的一切(供断言)。
type fakeSink struct {
	mu         sync.Mutex
	plan       []string
	running    []int
	done       map[int]string
	logs       []recordedLog
	failureLog string
	artifacts  []run.Artifact
}

func newFakeSink() *fakeSink { return &fakeSink{done: map[int]string{}} }

func (s *fakeSink) Plan(_ context.Context, steps []run.StepDecl) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.plan = s.plan[:0]
	for _, st := range steps {
		s.plan = append(s.plan, st.Name)
	}
	return nil
}
func (s *fakeSink) StepRunning(_ context.Context, ordinal int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.running = append(s.running, ordinal)
	return nil
}
func (s *fakeSink) StepDone(_ context.Context, ordinal int, status string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.done[ordinal] = status
	return nil
}
func (s *fakeSink) SetFailureLog(_ context.Context, log string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failureLog = log
	return nil
}
func (s *fakeSink) Log(_ context.Context, stream string, ordinal int, line string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.logs = append(s.logs, recordedLog{stream, ordinal, line})
	return nil
}
func (s *fakeSink) EmitArtifact(_ context.Context, a run.Artifact) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.artifacts = append(s.artifacts, a)
	return nil
}

// allLogText 返回所有日志行拼接(供断言「明文不出现」)。
func (s *fakeSink) allLogText() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var b strings.Builder
	for _, l := range s.logs {
		b.WriteString(l.line)
		b.WriteByte('\n')
	}
	return b.String()
}

// fakeCmd 是 fakeCommander 对一条预期命令的脚本化响应。
type fakeCmd struct {
	stdoutLines []string
	stderrLines []string
	exitCode    int
	startErr    error // 非 nil + exitCode<0 模拟「构建器无法启动」
}

// recordedExec 记录一次被执行的命令(供断言 array 形态 + stdin 不进 argv)。
type recordedExec struct {
	name  string
	args  []string
	stdin string
}

// fakeCommander 脚本化每个子命令(按 args[0] 路由),并记录全部执行。
type fakeCommander struct {
	mu      sync.Mutex
	scripts map[string]fakeCmd // key = 子命令(build/run/tag/login/push/inspect)
	execs   []recordedExec
	// onBuild 在执行 docker build 时回调一次(测试用:模拟构建后取消 ctx 验脚本阶段取消)。
	onBuild func()
}

func newFakeCommander() *fakeCommander {
	return &fakeCommander{scripts: map[string]fakeCmd{}}
}

func (c *fakeCommander) script(sub string, cmd fakeCmd) { c.scripts[sub] = cmd }

func (c *fakeCommander) record(name string, args []string, stdin string) fakeCmd {
	c.mu.Lock()
	c.execs = append(c.execs, recordedExec{name: name, args: append([]string(nil), args...), stdin: stdin})
	sub := ""
	if len(args) > 0 {
		sub = args[0]
	}
	hook := c.onBuild
	cmd := c.scripts[sub]
	c.mu.Unlock()
	if sub == "build" && hook != nil {
		hook()
	}
	return cmd
}

func (c *fakeCommander) Run(_ context.Context, name string, args []string, stdin string) (string, string, int, error) {
	cmd := c.record(name, args, stdin)
	return strings.Join(cmd.stdoutLines, "\n"), strings.Join(cmd.stderrLines, "\n"), cmd.exitCode, cmd.startErr
}

func (c *fakeCommander) Stream(_ context.Context, name string, args []string, stdin string, onLine func(stream, line string)) (int, error) {
	cmd := c.record(name, args, stdin)
	if cmd.startErr != nil && cmd.exitCode < 0 {
		return -1, cmd.startErr
	}
	for _, l := range cmd.stdoutLines {
		onLine("stdout", l)
	}
	for _, l := range cmd.stderrLines {
		onLine("stderr", l)
	}
	return cmd.exitCode, nil
}

// allArgsText 拼接所有执行过的 args(供断言「secret 值绝不出现在 argv」)。
func (c *fakeCommander) allArgsText() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	var b strings.Builder
	for _, e := range c.execs {
		b.WriteString(strings.Join(e.args, " "))
		b.WriteByte('\n')
	}
	return b.String()
}

// ---- helpers ----

func newTestBuilder(t *testing.T, cmdr Commander, proj *project.Project, settings *pipeline.Settings, secrets map[string]string) *Builder {
	t.Helper()
	drv, err := DetectDriver(cmdr)
	// 强制选中 docker(测试用 fakeLookPath 总命中)。
	_ = err
	if drv == nil {
		// DetectDriver 在 lookPath 找不到时失败;测试覆盖该路径单独验。此处注入 driver。
		drv = &shellDriver{bin: "docker", cmdr: cmdr}
	}
	b := &Builder{
		projects: fakeProjects{proj: proj},
		settings: fakeSettings{settings: settings},
		vault:    fakeVault{secrets: secrets},
		driver:   &shellDriver{bin: "docker", cmdr: cmdr},
		cloner:   &Cloner{allowInsecure: true}, // 测试不真克隆(下方 imageBuild 走注入 driver,克隆经真 cloner 但用 file:// 夹具时才触发)
	}
	return b
}

func imageSettings(registry *pipeline.ImageRegistry) *pipeline.Settings {
	st := &pipeline.Settings{
		Build: pipeline.BuildConfig{
			Model:          pipeline.BuildModelDockerfile,
			DockerfilePath: "Dockerfile",
			ArtifactType:   pipeline.ArtifactImage,
		},
	}
	if registry != nil {
		st.Environments = []pipeline.Environment{{
			Name:          "prod",
			ImageRegistry: *registry,
		}}
	}
	return st
}

// stubCloner 是不触网的克隆 stub:直接成功、返回固定短 sha(单测隔离网络)。
type stubCloner struct {
	commitShort string
	err         error
}

func newSuccessCloner(commitShort string) *stubCloner { return &stubCloner{commitShort: commitShort} }

func (c *stubCloner) Clone(_ context.Context, _, _, _, _, _ string) (*CloneResolved, error) {
	if c.err != nil {
		return nil, c.err
	}
	return &CloneResolved{CommitShort: c.commitShort}, nil
}

// ---- tests ----

// TestDetectDriverNoCLI 验证探测不到任何容器 CLI 时返回 ErrNoContainerCLI(无 Docker 降级地基)。
func TestDetectDriverNoCLI(t *testing.T) {
	old := lookPath
	defer func() { lookPath = old }()
	lookPath = func(string) (string, error) { return "", errNotFound }
	_, err := DetectDriver(newFakeCommander())
	if err != ErrNoContainerCLI {
		t.Fatalf("expected ErrNoContainerCLI, got %v", err)
	}
}

// TestDetectDriverPicksFirst 验证按 docker→nerdctl→podman 顺序取首个可用。
func TestDetectDriverPicksFirst(t *testing.T) {
	old := lookPath
	defer func() { lookPath = old }()
	// docker 不可用、nerdctl 可用 → 应选 nerdctl。
	lookPath = func(name string) (string, error) {
		if name == "nerdctl" {
			return "/usr/bin/nerdctl", nil
		}
		return "", errNotFound
	}
	drv, err := DetectDriver(newFakeCommander())
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if drv.Binary() != "nerdctl" {
		t.Fatalf("expected nerdctl, got %q", drv.Binary())
	}
}

var errNotFound = &lookErr{}

type lookErr struct{}

func (*lookErr) Error() string { return "not found" }

// TestBuildImageSuccessEmitsArtifact 验证成功 image 构建:emit 真实产物(无 stub),步骤全 success。
// 用注入 cloner 跳过真实网络:把 cloner 替换成放行夹具 + 直接让 Clone 成功(经覆盖 builder.cloner)。
func TestBuildImageSuccessEmitsArtifact(t *testing.T) {
	cmdr := newFakeCommander()
	cmdr.script("build", fakeCmd{stdoutLines: []string{"Step 1/3 : FROM alpine", "Successfully built abc123"}, exitCode: 0})
	cmdr.script("inspect", fakeCmd{stdoutLines: []string{`{"Id":"sha256:abc","RepoDigests":[],"Size":48210432}`}, exitCode: 0})

	proj := &project.Project{ID: "p1", Name: "My App", RepoURL: "https://example.com/r.git"}
	b := newTestBuilder(t, cmdr, proj, imageSettings(nil), nil)
	b.cloner = newSuccessCloner("abc1234")

	sink := newFakeSink()
	r := &run.Run{ID: "run1", ProjectID: "p1", Trigger: run.Trigger{Commit: "abc1234deadbeef"}}
	if err := b.Run(context.Background(), r, sink); err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	if len(sink.artifacts) != 1 {
		t.Fatalf("expected 1 artifact, got %d", len(sink.artifacts))
	}
	a := sink.artifacts[0]
	if a.Type != run.ArtifactImage {
		t.Fatalf("expected image artifact, got %q", a.Type)
	}
	if a.Reference != "pipewright/my-app:abc1234" {
		t.Fatalf("unexpected reference %q", a.Reference)
	}
	if a.SizeBytes != 48210432 {
		t.Fatalf("expected real size, got %d", a.SizeBytes)
	}
	if _, ok := a.Metadata["stub"]; ok {
		t.Fatalf("real artifact must not carry stub=true")
	}
	if sink.done[0] != run.StepSuccess || sink.done[1] != run.StepSuccess {
		t.Fatalf("expected both steps success, got %v", sink.done)
	}
}

// TestBuildFailureSetsFailureLog 验证构建命令非零退出 → 步骤 failed + SetFailureLog + 返回错误。
func TestBuildFailureSetsFailureLog(t *testing.T) {
	cmdr := newFakeCommander()
	cmdr.script("build", fakeCmd{
		stdoutLines: []string{"Step 1/3 : FROM alpine"},
		stderrLines: []string{"error: COPY failed: file not found"},
		exitCode:    1,
	})
	proj := &project.Project{ID: "p1", Name: "app", RepoURL: "https://example.com/r.git"}
	b := newTestBuilder(t, cmdr, proj, imageSettings(nil), nil)
	b.cloner = newSuccessCloner("c0ffee0")

	sink := newFakeSink()
	r := &run.Run{ID: "run1", ProjectID: "p1", Trigger: run.Trigger{Commit: "c0ffee0aaa"}}
	err := b.Run(context.Background(), r, sink)
	if err != ErrBuildFailed {
		t.Fatalf("expected ErrBuildFailed, got %v", err)
	}
	if sink.done[1] != run.StepFailed {
		t.Fatalf("expected build step failed, got %v", sink.done)
	}
	if strings.TrimSpace(sink.failureLog) == "" {
		t.Fatalf("expected failure log to be set")
	}
	if len(sink.artifacts) != 0 {
		t.Fatalf("failed build must not emit artifacts")
	}
}

// TestSecretNotInVisibleLogs 验证 secret 构建变量明文绝不出现在可见日志/回显命令行,但确实注入了子进程 argv。
func TestSecretNotInVisibleLogs(t *testing.T) {
	const secretVal = "SUPER_SECRET_TOKEN_zzz"
	cmdr := newFakeCommander()
	cmdr.script("build", fakeCmd{stdoutLines: []string{"building..."}, exitCode: 0})
	cmdr.script("inspect", fakeCmd{stdoutLines: []string{`{"Id":"sha256:x","Size":10}`}, exitCode: 0})

	proj := &project.Project{ID: "p1", Name: "app", RepoURL: "https://example.com/r.git"}
	st := imageSettings(nil)
	st.Build.Vars = []pipeline.BuildVar{
		{Key: "API_TOKEN", Secret: true, CredentialID: "cred-secret"},
		{Key: "PUBLIC_VAR", Secret: false, Value: "hello"},
	}
	b := newTestBuilder(t, cmdr, proj, st, map[string]string{"cred-secret": secretVal})
	b.cloner = newSuccessCloner("abc1234")

	sink := newFakeSink()
	r := &run.Run{ID: "run1", ProjectID: "p1", Trigger: run.Trigger{Commit: "abc1234x"}}
	if err := b.Run(context.Background(), r, sink); err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	// 可见日志(含命令回显)绝不含 secret 明文。
	if strings.Contains(sink.allLogText(), secretVal) {
		t.Fatalf("secret plaintext leaked into visible logs:\n%s", sink.allLogText())
	}
	// 命令回显里 secret 只列 key(API_TOKEN=***),非 secret 变量可见。
	logs := sink.allLogText()
	if !strings.Contains(logs, "API_TOKEN=***") {
		t.Fatalf("expected masked secret key in command echo, got:\n%s", logs)
	}
	if !strings.Contains(logs, "PUBLIC_VAR=hello") {
		t.Fatalf("expected non-secret var visible, got:\n%s", logs)
	}
	// 但 secret 确实经 --build-arg 注入了真实子进程 argv(构建需要它)。
	if !strings.Contains(cmdr.allArgsText(), "API_TOKEN="+secretVal) {
		t.Fatalf("secret should be injected into real argv")
	}
}

// TestCancelReturnsErrCanceled 验证 ctx 取消时返回 run.ErrCanceled。
func TestCancelReturnsErrCanceled(t *testing.T) {
	cmdr := newFakeCommander()
	proj := &project.Project{ID: "p1", Name: "app", RepoURL: "https://example.com/r.git"}
	b := newTestBuilder(t, cmdr, proj, imageSettings(nil), nil)
	b.cloner = newSuccessCloner("abc1234")

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // 立即取消

	sink := newFakeSink()
	r := &run.Run{ID: "run1", ProjectID: "p1", Trigger: run.Trigger{Commit: "abc1234"}}
	if err := b.Run(ctx, r, sink); err != run.ErrCanceled {
		t.Fatalf("expected run.ErrCanceled, got %v", err)
	}
}

// TestPushFlow 验证 image + 配 registry 时:计划含推送步骤;login 口令经 stdin(不进 argv);
// tag/push 执行;产物 reference 改为远端 tag。
func TestPushFlow(t *testing.T) {
	const regPass = "registry-password-xyz"
	cmdr := newFakeCommander()
	cmdr.script("build", fakeCmd{stdoutLines: []string{"built"}, exitCode: 0})
	cmdr.script("inspect", fakeCmd{stdoutLines: []string{`{"Id":"sha256:x","RepoDigests":["reg.example.com/app@sha256:deadbeef"],"Size":100}`}, exitCode: 0})
	cmdr.script("login", fakeCmd{stdoutLines: []string{"Login Succeeded"}, exitCode: 0})
	cmdr.script("tag", fakeCmd{exitCode: 0})
	cmdr.script("push", fakeCmd{stdoutLines: []string{"pushed"}, exitCode: 0})

	registry := &pipeline.ImageRegistry{Type: pipeline.RegistryHarbor, URL: "https://reg.example.com", CredentialID: "cred-reg"}
	proj := &project.Project{ID: "p1", Name: "app", RepoURL: "https://example.com/r.git"}
	b := newTestBuilder(t, cmdr, proj, imageSettings(registry), map[string]string{"cred-reg": "robot$ci:" + regPass})
	b.cloner = newSuccessCloner("abc1234")

	sink := newFakeSink()
	r := &run.Run{ID: "run1", ProjectID: "p1", Trigger: run.Trigger{Commit: "abc1234", ResolvedEnvironment: "prod"}}
	if err := b.Run(context.Background(), r, sink); err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	if len(sink.plan) != 3 || sink.plan[2] != stepPush {
		t.Fatalf("expected 3 steps incl push, got %v", sink.plan)
	}
	if sink.done[2] != run.StepSuccess {
		t.Fatalf("expected push step success, got %v", sink.done)
	}
	// 口令经 stdin,绝不进 argv。
	if strings.Contains(cmdr.allArgsText(), regPass) {
		t.Fatalf("registry password leaked into argv")
	}
	var loginStdin string
	for _, e := range cmdr.execs {
		if len(e.args) > 0 && e.args[0] == "login" {
			loginStdin = e.stdin
		}
	}
	if loginStdin != regPass {
		t.Fatalf("expected password via stdin, got %q", loginStdin)
	}
	// 产物 reference 为远端 tag,digest 取推送后 repoDigest。
	if len(sink.artifacts) != 1 {
		t.Fatalf("expected 1 artifact")
	}
	a := sink.artifacts[0]
	if a.Reference != "reg.example.com/app:abc1234" {
		t.Fatalf("expected remote tag reference, got %q", a.Reference)
	}
	if a.Metadata["digest"] != "sha256:deadbeef" {
		t.Fatalf("expected pushed digest, got %v", a.Metadata["digest"])
	}
}

// TestPushFailureSetsFailureLog 验证推送命令失败 → push 步骤 failed + 失败日志。
func TestPushFailureSetsFailureLog(t *testing.T) {
	cmdr := newFakeCommander()
	cmdr.script("build", fakeCmd{stdoutLines: []string{"built"}, exitCode: 0})
	cmdr.script("inspect", fakeCmd{stdoutLines: []string{`{"Id":"sha256:x","Size":1}`}, exitCode: 0})
	cmdr.script("login", fakeCmd{exitCode: 0})
	cmdr.script("tag", fakeCmd{exitCode: 0})
	cmdr.script("push", fakeCmd{stderrLines: []string{"denied: requested access to the resource is denied"}, exitCode: 1})

	registry := &pipeline.ImageRegistry{Type: pipeline.RegistryHarbor, URL: "https://reg.example.com", CredentialID: "cred-reg"}
	proj := &project.Project{ID: "p1", Name: "app", RepoURL: "https://example.com/r.git"}
	b := newTestBuilder(t, cmdr, proj, imageSettings(registry), map[string]string{"cred-reg": "u:p"})
	b.cloner = newSuccessCloner("abc1234")

	sink := newFakeSink()
	r := &run.Run{ID: "run1", ProjectID: "p1", Trigger: run.Trigger{Commit: "abc1234", ResolvedEnvironment: "prod"}}
	if err := b.Run(context.Background(), r, sink); err != ErrBuildFailed {
		t.Fatalf("expected ErrBuildFailed, got %v", err)
	}
	if sink.done[2] != run.StepFailed {
		t.Fatalf("expected push step failed, got %v", sink.done)
	}
	if strings.TrimSpace(sink.failureLog) == "" {
		t.Fatalf("expected push failure log")
	}
}

// TestSSRFBlocksMetadataIP 验证 SSRF 收口拒绝云元数据 IP(生产 cloner)。
func TestSSRFBlocksMetadataIP(t *testing.T) {
	if validRepoURL("http://169.254.169.254/latest/meta-data/") {
		t.Fatalf("metadata IP must be blocked")
	}
	if validRepoURL("http://127.0.0.1/repo.git") {
		t.Fatalf("loopback must be blocked")
	}
	if !validRepoURL("https://gitee.com/u/r.git") {
		t.Fatalf("public host must be allowed")
	}
	if validRepoURL("file:///tmp/repo") {
		t.Fatalf("file scheme must be blocked")
	}
}
