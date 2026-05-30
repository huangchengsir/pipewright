package target

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
)

// dialTimeout 是 TCP 拨号 + SSH 握手的兜底超时(ctx 无 deadline 时生效)。
const dialTimeout = 10 * time.Second

// sshDialer 是基于 golang.org/x/crypto/ssh 的默认 SSHDialer 实现。
//
// AC-SEC-02:cmd 以 []string(程序 + 参数)传入,经 quoteArgs 对各参数 POSIX shell 转义
// 后再交 session.Run。调用方**不**拼接原始 shell 字符串,杜绝命令注入(参数中的
// `; rm -rf /`、`$(...)`、反引号等被当作字面量,绝不被远端 shell 解释执行)。
//
// HostKeyCallback:本期 dev 用 InsecureIgnoreHostKey 以便对 localhost 真测。
// ⚠️ DEFERRED(生产硬性):生产必须固定 known_hosts(ssh.FixedHostKey / knownhosts.New),
// 否则无法防 MITM。本机自测场景无 known_hosts,故暂放宽;切勿带此设置上生产。
type sshDialer struct{}

func (sshDialer) Run(ctx context.Context, addr string, cfg SSHConfig, cmd []string) (*ExecResult, error) {
	auth, err := authMethods(cfg)
	if err != nil {
		return nil, err
	}

	clientCfg := &ssh.ClientConfig{
		User: cfg.User,
		Auth: auth,
		// DEFERRED:生产应固定 known_hosts(见上方类型注释)。
		HostKeyCallback: ssh.InsecureIgnoreHostKey(), //nolint:gosec // dev-only;生产固定 known_hosts(deferred)
		Timeout:         resolveTimeout(ctx),
	}

	// 经 net.Dialer 让 TCP 拨号也尊重 ctx 取消/超时。
	d := net.Dialer{Timeout: clientCfg.Timeout}
	conn, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("%w", classifyDialErr(err))
	}

	sshConn, chans, reqs, err := ssh.NewClientConn(conn, addr, clientCfg)
	if err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("%w", classifyHandshakeErr(err))
	}
	client := ssh.NewClient(sshConn, chans, reqs)
	defer func() { _ = client.Close() }()

	session, err := client.NewSession()
	if err != nil {
		return nil, fmt.Errorf("%w", ErrUnreachable)
	}
	defer func() { _ = session.Close() }()

	var stdout, stderr bytes.Buffer
	session.Stdout = &stdout
	session.Stderr = &stderr

	// AC-SEC-02:array → 安全转义的单行命令(各参数 %q POSIX 转义),不让调用方拼 shell。
	runErr := runWithContext(ctx, session, quoteArgs(cmd))

	res := &ExecResult{
		Stdout: stdout.String(),
		Stderr: stderr.String(),
	}

	if runErr != nil {
		var exitErr *ssh.ExitError
		if errors.As(runErr, &exitErr) {
			// 远端命令以非零退出:不是连接错误,exitCode 据实回传。
			res.ExitCode = exitErr.ExitStatus()
			return res, nil
		}
		if errors.Is(runErr, context.DeadlineExceeded) || errors.Is(runErr, context.Canceled) {
			return nil, runErr
		}
		return nil, fmt.Errorf("%w", ErrUnreachable)
	}
	res.ExitCode = 0
	return res, nil
}

// runWithContext 在 session 上跑 cmd,并让 ctx 取消/超时能中断阻塞的 Run。
func runWithContext(ctx context.Context, session *ssh.Session, cmd string) error {
	done := make(chan error, 1)
	go func() { done <- session.Run(cmd) }()
	select {
	case <-ctx.Done():
		_ = session.Signal(ssh.SIGKILL)
		_ = session.Close()
		return ctx.Err()
	case err := <-done:
		return err
	}
}

// authMethods 据 cfg 装配 SSH 认证法(私钥优先,否则口令)。明文仅进程内,不外泄。
func authMethods(cfg SSHConfig) ([]ssh.AuthMethod, error) {
	if cfg.PrivateKey != "" {
		signer, err := ssh.ParsePrivateKey([]byte(cfg.PrivateKey))
		if err != nil {
			// 解析失败:不回显私钥内容,只给干净领域错误。
			return nil, ErrInvalidCredential
		}
		return []ssh.AuthMethod{ssh.PublicKeys(signer)}, nil
	}
	if cfg.Password != "" {
		return []ssh.AuthMethod{ssh.Password(cfg.Password)}, nil
	}
	return nil, ErrInvalidCredential
}

// resolveTimeout 取 ctx 剩余时间作为拨号超时;无 deadline 时用 dialTimeout 兜底。
func resolveTimeout(ctx context.Context) time.Duration {
	if dl, ok := ctx.Deadline(); ok {
		if d := time.Until(dl); d > 0 {
			return d
		}
		return time.Millisecond // 已超时:让拨号立即失败
	}
	return dialTimeout
}

// classifyDialErr 把 TCP 拨号错误映射为干净领域错误(绝不含内部地址/栈)。
func classifyDialErr(err error) error {
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return ErrUnreachable
	}
	return ErrUnreachable
}

// classifyHandshakeErr 把 SSH 握手错误映射:认证类 → ErrAuth,其余 → ErrUnreachable。
// 仅看错误**类别**,绝不透传可能含敏感细节的原始文本。
func classifyHandshakeErr(err error) error {
	msg := strings.ToLower(err.Error())
	if strings.Contains(msg, "unable to authenticate") ||
		strings.Contains(msg, "auth") ||
		strings.Contains(msg, "permission denied") ||
		strings.Contains(msg, "no supported methods") {
		return ErrAuth
	}
	return ErrUnreachable
}

// quoteArgs 把命令 array 转为安全的单行 shell 命令:程序名 + 各参数经 POSIX 单引号转义。
// AC-SEC-02 防注入核心:参数里的特殊字符(空格、`;`、`$()`、反引号、引号)全被当字面量。
func quoteArgs(args []string) string {
	parts := make([]string, len(args))
	for i, a := range args {
		parts[i] = shellQuote(a)
	}
	return strings.Join(parts, " ")
}

// shellQuote 对单个参数做 POSIX 单引号转义。空串 → ”。内部单引号用 '\” 序列拼接。
func shellQuote(s string) string {
	if s == "" {
		return "''"
	}
	// 若全为安全字符,免引号(更可读;不影响安全)。
	safe := true
	for _, r := range s {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' ||
			r == '-' || r == '_' || r == '.' || r == '/' || r == ':' || r == '=' || r == '@') {
			safe = false
			break
		}
	}
	if safe {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
