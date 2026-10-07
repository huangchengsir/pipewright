package target

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/huangchengsir/pipewright/internal/vault"
	"golang.org/x/crypto/ssh"
)

var (
	ErrTargetChanged      = errors.New("target: approved configuration changed")
	ErrLimitedUnavailable = errors.New("target: bounded execution unavailable")
)

type ExecutionLimits struct {
	Bytes int
	Lines int
}
type LimitedResult struct {
	Stdout    string
	Stderr    string
	ExitCode  int
	Truncated bool
}

// The snapshot is server-created and must never be accepted from a browser.
type ConnectionSnapshot struct {
	Server            Server
	CredentialVersion string
}

func (s ConnectionSnapshot) Fingerprint() string {
	data, _ := json.Marshal(s)
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}

// An additive interface leaves the existing deployment/terminal contract intact.
type LimitedExecutor interface {
	Capture(context.Context, string) (ConnectionSnapshot, error)
	ExecLimited(context.Context, ConnectionSnapshot, []string, ExecutionLimits) (*LimitedResult, error)
}

type limitedSSHRunner interface {
	RunLimited(context.Context, string, SSHConfig, []string, ExecutionLimits) (*LimitedResult, error)
}

func (s *service) Capture(ctx context.Context, id string) (ConnectionSnapshot, error) {
	server, err := s.Get(ctx, id)
	if err != nil {
		return ConnectionSnapshot{}, err
	}
	var ciphertext []byte
	if err := s.db.QueryRowContext(ctx, "SELECT ciphertext FROM credentials WHERE id = ?", server.CredentialID).Scan(&ciphertext); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ConnectionSnapshot{}, ErrCredentialNotFound
		}
		return ConnectionSnapshot{}, errors.New("target: credential version unavailable")
	}
	digest := sha256.Sum256(ciphertext)
	return ConnectionSnapshot{Server: *server, CredentialVersion: hex.EncodeToString(digest[:])}, nil
}

func normalizeExecutionLimits(l ExecutionLimits) (ExecutionLimits, error) {
	if l.Bytes == 0 {
		l.Bytes = 64 << 10
	}
	if l.Lines == 0 {
		l.Lines = 1000
	}
	if l.Bytes < 1 || l.Bytes > 64<<10 || l.Lines < 1 || l.Lines > 1000 {
		return ExecutionLimits{}, errors.New("target: invalid execution limits")
	}
	return l, nil
}

func (s *service) ExecLimited(ctx context.Context, approved ConnectionSnapshot, cmd []string, limits ExecutionLimits) (*LimitedResult, error) {
	limits, err := normalizeExecutionLimits(limits)
	if err != nil {
		return nil, err
	}
	if len(cmd) == 0 || len(cmd) > 64 {
		return nil, errors.New("target: invalid command")
	}
	for _, arg := range cmd {
		if len(arg) > 16<<10 || strings.ContainsRune(arg, 0) {
			return nil, errors.New("target: invalid command")
		}
	}
	runner, ok := s.dialer.(limitedSSHRunner)
	if !ok {
		return nil, ErrLimitedUnavailable
	}
	current, err := s.Capture(ctx, approved.Server.ID)
	if err != nil {
		return nil, err
	}
	if approved.Fingerprint() != current.Fingerprint() {
		return nil, ErrTargetChanged
	}
	if s.vault == nil {
		return nil, ErrVaultUnconfigured
	}
	var sealed []byte
	err = s.db.QueryRowContext(ctx, "SELECT ciphertext FROM credentials WHERE id = ?", approved.Server.CredentialID).Scan(&sealed)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrCredentialNotFound
	}
	if err != nil {
		return nil, ErrAuth
	}
	version := sha256.Sum256(sealed)
	if hex.EncodeToString(version[:]) != approved.CredentialVersion {
		return nil, ErrTargetChanged
	}
	plaintext, err := s.vault.OpenSecret(sealed)
	if err != nil {
		switch {
		case errors.Is(err, vault.ErrVaultUnconfigured):
			return nil, ErrVaultUnconfigured
		case errors.Is(err, vault.ErrNotFound):
			return nil, ErrCredentialNotFound
		default:
			return nil, ErrAuth
		}
	}
	defer func() {
		for i := range plaintext {
			plaintext[i] = 0
		}
	}()
	secret := string(plaintext)
	cfg := SSHConfig{User: approved.Server.User}
	if looksLikePEM(secret) {
		cfg.PrivateKey = secret
	} else {
		cfg.Password = secret
	}
	secret = ""
	defer func() { cfg.Password = ""; cfg.PrivateKey = "" }()
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	// Use the approved address, not another lookup after validation.
	addr := net.JoinHostPort(approved.Server.Host, strconv.Itoa(approved.Server.Port))
	return runner.RunLimited(ctx, addr, cfg, cmd, limits)
}

// A shared budget bounds stdout and stderr while SSH drains both concurrently.
type outputBudget struct {
	mu        sync.Mutex
	remaining int
	lines     int
	maxLines  int
	openLine  [2]bool
	truncated bool
	stdout    bytes.Buffer
	stderr    bytes.Buffer
}
type budgetWriter struct {
	budget *outputBudget
	stderr bool
}

func (w budgetWriter) Write(p []byte) (int, error) {
	b := w.budget
	b.mu.Lock()
	defer b.mu.Unlock()
	n := min(len(p), b.remaining)
	stream := 0
	if w.stderr {
		stream = 1
	}
	for i := 0; i < n; i++ {
		if !b.openLine[stream] {
			if b.lines >= b.maxLines {
				n = i
				break
			}
			b.lines++
			b.openLine[stream] = true
		}
		if p[i] == '\n' {
			b.openLine[stream] = false
		}
	}
	if n < len(p) {
		b.truncated = true
	}
	if w.stderr {
		_, _ = b.stderr.Write(p[:n])
	} else {
		_, _ = b.stdout.Write(p[:n])
	}
	b.remaining -= n
	// Reporting a full write drains excess bytes instead of hanging the remote process.
	return len(p), nil
}

func (b *outputBudget) result() *LimitedResult {
	b.mu.Lock()
	defer b.mu.Unlock()
	return &LimitedResult{
		Stdout:    strings.ToValidUTF8(b.stdout.String(), ""),
		Stderr:    strings.ToValidUTF8(b.stderr.String(), ""),
		Truncated: b.truncated,
	}
}

func (sshDialer) RunLimited(ctx context.Context, addr string, cfg SSHConfig, cmd []string, limits ExecutionLimits) (*LimitedResult, error) {
	limits, err := normalizeExecutionLimits(limits)
	if err != nil {
		return nil, err
	}
	auth, err := authMethods(cfg)
	if err != nil {
		return nil, err
	}
	clientCfg := &ssh.ClientConfig{
		User: cfg.User, Auth: auth,
		HostKeyCallback: ssh.InsecureIgnoreHostKey(), //nolint:gosec // Existing target trust policy; not a host identity guarantee.
		Timeout:         resolveTimeout(ctx),
	}
	client, cleanup, err := dialSSH(ctx, addr, clientCfg)
	if err != nil {
		return nil, err
	}
	defer cleanup()
	defer func() { _ = client.Close() }()
	session, err := client.NewSession()
	if err != nil {
		return nil, ErrUnreachable
	}
	defer func() { _ = session.Close() }()
	budget := &outputBudget{remaining: limits.Bytes, maxLines: limits.Lines}
	session.Stdout = budgetWriter{budget: budget}
	session.Stderr = budgetWriter{budget: budget, stderr: true}
	runErr := runWithContext(ctx, session, quoteArgs(cmd))
	res := budget.result()
	if runErr == nil {
		return res, nil
	}
	var exitErr *ssh.ExitError
	if errors.As(runErr, &exitErr) {
		res.ExitCode = exitErr.ExitStatus()
		return res, nil
	}
	if ctx.Err() != nil {
		return res, ctx.Err()
	}
	if errors.Is(runErr, io.EOF) {
		return res, ErrUnreachable
	}
	return res, fmt.Errorf("%w", ErrUnreachable)
}
