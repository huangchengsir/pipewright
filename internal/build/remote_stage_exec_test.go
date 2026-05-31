package build

import (
	"bytes"
	"context"
	"io"
	"strings"
	"testing"

	"github.com/huangchengsir/pipewright/internal/pipeline"
	"github.com/huangchengsir/pipewright/internal/project"
	"github.com/huangchengsir/pipewright/internal/run"
	"github.com/huangchengsir/pipewright/internal/target"
)

// fakeRemoteTarget 记录远程 Exec 命令 + Upload 内容(校验派发 + 密钥安全)。
type fakeRemoteTarget struct {
	serverIDs map[string]bool
	cmds      [][]string
	uploaded  []byte
}

func (f *fakeRemoteTarget) Exec(_ context.Context, serverID string, cmd []string) (*target.ExecResult, error) {
	if f.serverIDs == nil {
		f.serverIDs = map[string]bool{}
	}
	f.serverIDs[serverID] = true
	f.cmds = append(f.cmds, cmd)
	return &target.ExecResult{ExitCode: 0}, nil
}

func (f *fakeRemoteTarget) Upload(_ context.Context, serverID string, content io.Reader, _ string) error {
	if f.serverIDs == nil {
		f.serverIDs = map[string]bool{}
	}
	f.serverIDs[serverID] = true
	b, _ := io.ReadAll(content)
	f.uploaded = append(f.uploaded, b...)
	return nil
}

type fakeRunnerLookup struct {
	serverID string
}

func (l fakeRunnerLookup) RunnerFor(_ context.Context, _ string) (string, bool) {
	return l.serverID, l.serverID != ""
}

// 带 git token 的测试 Builder(校验 token 绝不上远程)。
func newRemoteTestBuilder(drv Driver) *Builder {
	return &Builder{
		projects: fakeProjects{proj: &project.Project{ID: "p1", RepoURL: "https://example.com/r.git", CredentialID: "cred"}},
		settings: fakeSettings{settings: &pipeline.Settings{}},
		vault:    fakeVault{secrets: map[string]string{"cred": gitTokenSecret}},
		driver:   drv,
		cloner:   &markerCloner{file: "README.md", content: "hi"},
	}
}

const gitTokenSecret = "SUPERSECRET-GIT-TOKEN-xyz"

func TestStageExecutorDispatchesToRemote(t *testing.T) {
	local := &recordingDriver{}
	b := newRemoteTestBuilder(local)
	tgt := &fakeRemoteTarget{}
	exec := NewStageExecutorWithRunner(b, nil, fakeRunnerLookup{serverID: "srv-1"}, tgt)

	rep := &fakeReporter{}
	r := &run.Run{ID: "run-1", ProjectID: "p1", Trigger: run.Trigger{Branch: "main"}}
	stage := scriptStage(scriptJob("build", "node:20", "npm ci"))
	if err := exec(context.Background(), r, stage, rep); err != nil {
		t.Fatalf("remote exec: %v", err)
	}

	// 命令应打到远程 runner(srv-1),且本地 driver 绝不被调(构建下沉了)。
	if !tgt.serverIDs["srv-1"] {
		t.Fatalf("应派发到 srv-1;serverIDs=%v", tgt.serverIDs)
	}
	if local.callCount != 0 {
		t.Fatalf("远程派发时本地 driver 不应被调,实际 %d 次", local.callCount)
	}
	// 应上传工作区 tar.gz + 远端 untar + 远端 docker run。
	if len(tgt.uploaded) == 0 {
		t.Fatal("应上传工作区 tar.gz 到远程")
	}
	var sawUntar, sawDockerRun bool
	for _, c := range tgt.cmds {
		joined := strings.Join(c, " ")
		if strings.Contains(joined, "tar -xzf") {
			sawUntar = true
		}
		if len(c) >= 2 && c[0] == "docker" && c[1] == "run" {
			sawDockerRun = true
		}
	}
	if !sawUntar || !sawDockerRun {
		t.Fatalf("远程应 untar + docker run;untar=%v run=%v cmds=%v", sawUntar, sawDockerRun, tgt.cmds)
	}

	// 安全铁律:git token 绝不出现在任何远程命令 / 上传内容里(token 全程只在控制机)。
	for _, c := range tgt.cmds {
		if strings.Contains(strings.Join(c, " "), gitTokenSecret) {
			t.Fatalf("git token 泄漏进远程命令!%v", c)
		}
	}
	if bytes.Contains(tgt.uploaded, []byte(gitTokenSecret)) {
		t.Fatal("git token 泄漏进上传内容!")
	}
}

func TestStageExecutorLocalWhenNoRunner(t *testing.T) {
	local := &recordingDriver{}
	b := newRemoteTestBuilder(local)
	tgt := &fakeRemoteTarget{}
	// 无 runner(空)→ 走本地。
	exec := NewStageExecutorWithRunner(b, nil, fakeRunnerLookup{serverID: ""}, tgt)

	rep := &fakeReporter{}
	r := &run.Run{ID: "run-1", ProjectID: "p1", Trigger: run.Trigger{Branch: "main"}}
	if err := exec(context.Background(), r, scriptStage(scriptJob("build", "node:20", "echo hi")), rep); err != nil {
		t.Fatalf("local exec: %v", err)
	}
	if local.callCount != 1 {
		t.Fatalf("无 runner 应本地执行(driver 调 1 次),实际 %d", local.callCount)
	}
	if len(tgt.cmds) != 0 || len(tgt.uploaded) != 0 {
		t.Fatal("无 runner 不应有任何远程动作")
	}
}

// lookup/tgt 为 nil → 退化纯本地(不 panic)。
func TestStageExecutorNilRunnerIsLocal(t *testing.T) {
	local := &recordingDriver{}
	b := newRemoteTestBuilder(local)
	exec := NewStageExecutorWithRunner(b, nil, nil, nil)
	rep := &fakeReporter{}
	r := &run.Run{ID: "run-1", ProjectID: "p1", Trigger: run.Trigger{Branch: "main"}}
	if err := exec(context.Background(), r, scriptStage(scriptJob("b", "alpine", "true")), rep); err != nil {
		t.Fatalf("exec: %v", err)
	}
	if local.callCount != 1 {
		t.Fatalf("nil runner 应纯本地,实际 driver 调 %d 次", local.callCount)
	}
}
