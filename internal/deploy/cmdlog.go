// cmdlog.go:把部署链路在目标机真实执行的命令 + 其 stdout/stderr 实时回流到运行步骤日志,
// 让 deploy_ssh 步骤像 Jenkins 控制台一样看得到「具体执行了什么」(此前仅一行结果摘要)。
//
// 脱敏分两道:① 命令回显时对紧跟 -e/--env/--build-arg 的 K=V、--password 的值就地打码(第一道);
// ② 文本最终经 sink 侧 per-run Masker 兜底脱敏(与构建日志同一机制,绝无明文 secret 落库/出网)。
package deploy

import (
	"context"
	"fmt"
	"strings"

	"github.com/huangchengsir/pipewright/internal/target"
)

const (
	cmdStreamStdout = "stdout"
	cmdStreamStderr = "stderr"
)

// CmdLogFunc 接收一行待写入运行日志的部署命令/输出(stream = stdout | stderr)。
type CmdLogFunc func(stream, text string)

type cmdLogKey struct{}

// WithCmdLog 把命令日志回调挂到 ctx:部署链路每条远程命令 + 其输出经它实时回流。
// fn 为 nil → 原样返回(不需要命令级日志的调用方,如 /runs/{id}/deploy 端点,无副作用)。
func WithCmdLog(ctx context.Context, fn CmdLogFunc) context.Context {
	if fn == nil {
		return ctx
	}
	return context.WithValue(ctx, cmdLogKey{}, fn)
}

func cmdLogFrom(ctx context.Context) CmdLogFunc {
	if fn, ok := ctx.Value(cmdLogKey{}).(CmdLogFunc); ok && fn != nil {
		return fn
	}
	return func(string, string) {}
}

// exec 包裹 targets.Exec:执行前回显命令、执行后回流 stdout/stderr 到 ctx 的命令日志(若挂了)。
// 返回值与 targets.Exec 完全一致,不改变任何控制流(无 ctx 日志时 = 纯透传)。
func (s *service) exec(ctx context.Context, serverID string, cmd []string) (*target.ExecResult, error) {
	lg := cmdLogFrom(ctx)
	lg(cmdStreamStdout, "$ "+displayCmd(cmd))
	out, err := s.targets.Exec(ctx, serverID, cmd)
	if err != nil {
		lg(cmdStreamStderr, "  ✗ "+humanExecError(err))
		return out, err
	}
	if out != nil {
		if t := strings.Trim(out.Stdout, "\r\n"); strings.TrimSpace(t) != "" {
			lg(cmdStreamStdout, t)
		}
		if t := strings.Trim(out.Stderr, "\r\n"); strings.TrimSpace(t) != "" {
			lg(cmdStreamStderr, t)
		}
		if out.ExitCode != 0 {
			lg(cmdStreamStderr, fmt.Sprintf("  ✗ 退出码 %d", out.ExitCode))
		}
	}
	return out, err
}

// displayCmd 把 array 命令拼为可读单行;对敏感参数值就地打码(第一道脱敏)。
//   - sh -c <script> [args...]:直接展示脚本本体(docker build/run 等,可读性最佳;
//     脚本内 secret 由 sink 侧 Masker 兜底)。
//   - 其余:逐 token 拼接,紧跟 -e/--env/--build-arg 的 K=V → K=***,--password 的值 → ***。
func displayCmd(cmd []string) string {
	if len(cmd) >= 3 && cmd[0] == "sh" && cmd[1] == "-c" {
		return strings.TrimSpace(cmd[2])
	}
	out := make([]string, len(cmd))
	for i, tok := range cmd {
		out[i] = tok
		if i == 0 {
			continue
		}
		switch cmd[i-1] {
		case "-e", "--env", "--build-arg", "--arg":
			out[i] = maskAssign(tok)
		case "--password", "--password-stdin":
			out[i] = "***"
		}
	}
	return strings.Join(out, " ")
}

// maskAssign 把 "K=V" → "K=***"(只暴露 key);无 '=' 原样返回。
func maskAssign(kv string) string {
	if i := strings.IndexByte(kv, '='); i >= 0 {
		return kv[:i+1] + "***"
	}
	return kv
}
