package build

import (
	"context"
	"errors"
	"testing"

	"github.com/huangchengsir/pipewright/internal/pipeline"
	"github.com/huangchengsir/pipewright/internal/target"
)

// fakeRemoteExecer 是可控的 RemoteExecer:记录被投递的命令,按注入返回结果/错误。
type fakeRemoteExecer struct {
	gotServerID string
	gotCmds     [][]string
	result      *target.ExecResult
	err         error
}

func (f *fakeRemoteExecer) Exec(_ context.Context, serverID string, cmd []string) (*target.ExecResult, error) {
	f.gotServerID = serverID
	f.gotCmds = append(f.gotCmds, cmd)
	return f.result, f.err
}

func TestSSHCommanderRunMapsResult(t *testing.T) {
	f := &fakeRemoteExecer{result: &target.ExecResult{Stdout: "out", Stderr: "err", ExitCode: 7}}
	c := NewRemoteCommander(f, "srv-1")

	stdout, stderr, code, err := c.Run(context.Background(), "docker", []string{"build", "."}, "")
	if err != nil {
		t.Fatalf("Run err: %v", err)
	}
	if stdout != "out" || stderr != "err" || code != 7 {
		t.Fatalf("got (%q,%q,%d), want (out,err,7)", stdout, stderr, code)
	}
	// 命令必须 array 投递(program + args),且打到正确的远程机。
	if f.gotServerID != "srv-1" {
		t.Fatalf("serverID = %q, want srv-1", f.gotServerID)
	}
	if len(f.gotCmds) != 1 || len(f.gotCmds[0]) != 3 ||
		f.gotCmds[0][0] != "docker" || f.gotCmds[0][1] != "build" || f.gotCmds[0][2] != "." {
		t.Fatalf("cmd = %v, want [docker build .]", f.gotCmds)
	}
}

func TestSSHCommanderRunRejectsStdin(t *testing.T) {
	f := &fakeRemoteExecer{result: &target.ExecResult{}}
	c := NewRemoteCommander(f, "srv-1")
	_, _, code, err := c.Run(context.Background(), "docker", []string{"login"}, "secret-password")
	if !errors.Is(err, ErrRemoteStdinUnsupported) {
		t.Fatalf("err = %v, want ErrRemoteStdinUnsupported", err)
	}
	if code != -1 {
		t.Fatalf("code = %d, want -1", code)
	}
	// 关键:带 stdin(口令)的命令**绝不投递到远程**(避免 secret 处理面)。
	if len(f.gotCmds) != 0 {
		t.Fatalf("stdin 命令不应投递,实际投了 %v", f.gotCmds)
	}
}

func TestSSHCommanderRunSurfacesExecError(t *testing.T) {
	f := &fakeRemoteExecer{err: errors.New("ssh: connection refused")}
	c := NewRemoteCommander(f, "srv-1")
	_, _, code, err := c.Run(context.Background(), "docker", []string{"ps"}, "")
	if err == nil {
		t.Fatal("want exec error surfaced")
	}
	if code != -1 {
		t.Fatalf("code = %d, want -1 (无法在远程启动)", code)
	}
}

func TestSSHCommanderStreamReplaysLines(t *testing.T) {
	f := &fakeRemoteExecer{result: &target.ExecResult{
		Stdout:   "line1\nline2\n",
		Stderr:   "warn1\n",
		ExitCode: 0,
	}}
	c := NewRemoteCommander(f, "srv-1")

	type ev struct {
		stream, line string
	}
	var got []ev
	code, err := c.Stream(context.Background(), "sh", []string{"-c", "echo line1; echo line2"}, "", func(stream, line string) {
		got = append(got, ev{stream, line})
	})
	if err != nil {
		t.Fatalf("Stream err: %v", err)
	}
	if code != 0 {
		t.Fatalf("code = %d, want 0", code)
	}
	want := []ev{{"stdout", "line1"}, {"stdout", "line2"}, {"stderr", "warn1"}}
	if len(got) != len(want) {
		t.Fatalf("got %d lines %v, want %v", len(got), got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("line %d = %v, want %v", i, got[i], want[i])
		}
	}
}

func TestSSHCommanderStreamReturnsRemoteExitCode(t *testing.T) {
	f := &fakeRemoteExecer{result: &target.ExecResult{Stdout: "", Stderr: "boom", ExitCode: 2}}
	c := NewRemoteCommander(f, "srv-1")
	code, err := c.Stream(context.Background(), "false", nil, "", func(string, string) {})
	if err != nil {
		t.Fatalf("Stream err: %v", err)
	}
	if code != 2 {
		t.Fatalf("远程非零退出应原样取回,code = %d want 2", code)
	}
}

// NewRemoteDriver 应产出一个 Binary() 为指定 CLI 的 Driver,且其命令经远程 Commander 投递。
func TestNewRemoteDriverUsesRemoteCommander(t *testing.T) {
	f := &fakeRemoteExecer{result: &target.ExecResult{ExitCode: 0}}
	drv := NewRemoteDriver(f, "srv-1", "")
	if drv.Binary() != "docker" {
		t.Fatalf("默认 Binary = %q, want docker", drv.Binary())
	}
	// 触发一次 RunToolchain:应经远程机执行 `docker run ...`(命令打到 srv-1);
	// 资源规格(cpu/memory)应透传为 --cpus/--memory 投到远程 docker。
	_, err := drv.RunToolchain(context.Background(), "node:20", "/ws", "/src", nil, []string{"sh", "-c", "npm ci"}, pipeline.Resource{CPU: "2", Memory: "1g"}, func(string, string) {})
	if err != nil {
		t.Fatalf("RunToolchain err: %v", err)
	}
	if f.gotServerID != "srv-1" || len(f.gotCmds) == 0 || f.gotCmds[0][0] != "docker" {
		t.Fatalf("远程工具链命令未经 srv-1/docker 投递:server=%q cmds=%v", f.gotServerID, f.gotCmds)
	}
	if !cmdContainsPair(f.gotCmds[0], "--cpus", "2") || !cmdContainsPair(f.gotCmds[0], "--memory", "1g") {
		t.Fatalf("远程 docker run 未透传资源规格:%v", f.gotCmds[0])
	}

	// podman:Binary 与命令前缀应随之变化。
	drv2 := NewRemoteDriver(f, "srv-2", "podman")
	if drv2.Binary() != "podman" {
		t.Fatalf("Binary = %q, want podman", drv2.Binary())
	}
}

// cmdContainsPair 判断 args 中存在相邻的 (flag, value) 对。
func cmdContainsPair(args []string, flag, value string) bool {
	for i := 0; i+1 < len(args); i++ {
		if args[i] == flag && args[i+1] == value {
			return true
		}
	}
	return false
}
