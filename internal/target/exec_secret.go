package target

// Dedicated private transport for trusted tool containers. Existing terminal and
// deployment interfaces retain their established host-key policy.
import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
)

var ErrHostIdentity = errors.New("target: host identity mismatch")
var ErrSecretLimit = errors.New("target: private transport limit exceeded")

const SecretOutputLimit = 1 << 20
const SecretInputLimit = 256 << 10

type SecretResult struct {
	Stdout, Stderr []byte
	ExitCode       int
	Truncated      bool
}

// SecretExecutor is internal only. It has no corresponding execution endpoint.
type SecretExecutor interface {
	Capture(context.Context, string) (ConnectionSnapshot, error)
	ExecSecretPinned(context.Context, ConnectionSnapshot, string, []string, []byte) (*SecretResult, error)
	PullSecretPinned(context.Context, ConnectionSnapshot, string, string, []byte) error
}

type secretSSHRunner interface {
	RunSecret(context.Context, string, SSHConfig, string, []string, []byte) (*SecretResult, error)
	PullSecret(context.Context, string, SSHConfig, string, string, []byte) error
}

func validHostFingerprint(s string) bool {
	if !strings.HasPrefix(s, "SHA256:") {
		return false
	}
	b, e := base64.RawStdEncoding.DecodeString(strings.TrimPrefix(s, "SHA256:"))
	return e == nil && len(b) == 32
}
func secretHostKey(expected string) ssh.HostKeyCallback {
	return func(_ string, _ net.Addr, k ssh.PublicKey) error {
		actual := ssh.FingerprintSHA256(k)
		if subtle.ConstantTimeCompare([]byte(expected), []byte(actual)) != 1 {
			return ErrHostIdentity
		}
		return nil
	}
}
func validSecretCommand(cmd []string, in []byte) bool {
	if len(cmd) == 0 || len(cmd) > 128 || len(in) > SecretInputLimit {
		return false
	}
	for _, a := range cmd {
		if len(a) > 32768 || strings.ContainsRune(a, 0) {
			return false
		}
	}
	return true
}
func (s *service) ExecSecretPinned(ctx context.Context, snap ConnectionSnapshot, hostKey string, cmd []string, in []byte) (*SecretResult, error) {
	if !validHostFingerprint(hostKey) || !validSecretCommand(cmd, in) {
		return nil, NotStarted(ErrSecretLimit)
	}
	runner, ok := s.dialer.(secretSSHRunner)
	if !ok {
		return nil, NotStarted(ErrLimitedUnavailable)
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Minute)
	defer cancel()
	var out *SecretResult
	e := s.withApprovedSSH(ctx, snap, func(addr string, cfg SSHConfig) error {
		var e error
		out, e = runner.RunSecret(ctx, addr, cfg, hostKey, cmd, in)
		return e
	})
	return out, e
}
func (s *service) PullSecretPinned(ctx context.Context, snap ConnectionSnapshot, hostKey, image string, authJSON []byte) error {
	if !validHostFingerprint(hostKey) || len(authJSON) > 16384 || image == "" || len(image) > 512 {
		return NotStarted(ErrSecretLimit)
	}
	runner, ok := s.dialer.(secretSSHRunner)
	if !ok {
		return NotStarted(ErrLimitedUnavailable)
	}
	return s.withApprovedSSH(ctx, snap, func(addr string, cfg SSHConfig) error {
		return runner.PullSecret(ctx, addr, cfg, hostKey, image, authJSON)
	})
}
func privateSSH(ctx context.Context, addr string, cfg SSHConfig, hostKey string) (*ssh.Client, func(), error) {
	if !validHostFingerprint(hostKey) {
		return nil, func() {}, NotStarted(ErrHostIdentity)
	}
	methods, e := authMethods(cfg)
	if e != nil {
		return nil, func() {}, NotStarted(ErrAuth)
	}
	config := &ssh.ClientConfig{User: cfg.User, Auth: methods, HostKeyCallback: secretHostKey(hostKey), Timeout: resolveTimeout(ctx)}
	client, cleanup, e := dialSSH(ctx, addr, config)
	if e != nil {
		return nil, cleanup, NotStarted(e)
	}
	return client, cleanup, nil
}
func (sshDialer) RunSecret(ctx context.Context, addr string, cfg SSHConfig, hostKey string, cmd []string, in []byte) (*SecretResult, error) {
	if !validSecretCommand(cmd, in) {
		return nil, NotStarted(ErrSecretLimit)
	}
	client, cleanup, e := privateSSH(ctx, addr, cfg, hostKey)
	if e != nil {
		return nil, e
	}
	defer cleanup()
	defer client.Close()
	session, e := client.NewSession()
	if e != nil {
		return nil, NotStarted(ErrUnreachable)
	}
	defer session.Close()
	budget := &outputBudget{remaining: SecretOutputLimit, maxLines: SecretOutputLimit}
	session.Stdout = budgetWriter{budget: budget}
	session.Stderr = budgetWriter{budget: budget, stderr: true}
	session.Stdin = bytes.NewReader(in)
	e = runWithContext(ctx, session, quoteArgs(cmd))
	out := &SecretResult{Stdout: append([]byte(nil), budget.stdout.Bytes()...), Stderr: append([]byte(nil), budget.stderr.Bytes()...), Truncated: budget.truncated, ExitCode: -1}
	clear(budget.stdout.Bytes())
	clear(budget.stderr.Bytes())
	budget.stdout.Reset()
	budget.stderr.Reset()
	if e == nil {
		out.ExitCode = 0
		return out, nil
	}
	var exit *ssh.ExitError
	if errors.As(e, &exit) {
		out.ExitCode = exit.ExitStatus()
		return out, nil
	}
	if ctx.Err() != nil {
		return out, OutcomeUnknown(ctx.Err())
	}
	return out, OutcomeUnknown(ErrUnreachable)
}

// Pull uses the authenticated SSH stream-local channel to the fixed Docker
// socket. Registry auth is an HTTP header in protected memory, not argv/env or
// a Docker config file. No arbitrary Engine API paths are exposed.
func (sshDialer) PullSecret(ctx context.Context, addr string, cfg SSHConfig, hostKey, image string, authJSON []byte) error {
	client, cleanup, e := privateSSH(ctx, addr, cfg, hostKey)
	if e != nil {
		return e
	}
	defer cleanup()
	defer client.Close()
	transport := &http.Transport{DialContext: func(context.Context, string, string) (net.Conn, error) {
		return client.Dial("unix", "/var/run/docker.sock")
	}}
	defer transport.CloseIdleConnections()
	return PullDockerImage(ctx, transport, image, authJSON)
}

// PullDockerImage is fixed-purpose and also used by the local Unix-socket path.
// It never follows redirects or returns raw daemon diagnostics.
func PullDockerImage(ctx context.Context, transport http.RoundTripper, image string, authJSON []byte) error {
	if len(image) > 512 || image == "" || len(authJSON) > 16384 {
		return ErrSecretLimit
	}
	req, e := http.NewRequestWithContext(ctx, http.MethodPost, "http://docker/images/create", nil)
	if e != nil {
		return ErrUnreachable
	}
	q := req.URL.Query()
	q.Set("fromImage", image)
	req.URL.RawQuery = q.Encode()
	if len(authJSON) > 0 {
		req.Header.Set("X-Registry-Auth", base64.URLEncoding.EncodeToString(authJSON))
	}
	response, e := transport.RoundTrip(req)
	req.Header.Del("X-Registry-Auth")
	if e != nil {
		return ErrUnreachable
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		return ErrUnreachable
	}
	raw, e := io.ReadAll(io.LimitReader(response.Body, SecretOutputLimit+1))
	defer clear(raw)
	if e != nil || len(raw) > SecretOutputLimit {
		return ErrSecretLimit
	}
	// The engine may emit a JSON error with HTTP 200. Decode every progress record.
	return checkPullRecords(raw)
}

func checkPullRecords(raw []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	count := 0
	for {
		var msg struct {
			Error       string `json:"error"`
			ErrorDetail *struct {
				Message string `json:"message"`
			} `json:"errorDetail"`
		}
		e := decoder.Decode(&msg)
		if e == io.EOF {
			break
		}
		if e != nil || msg.Error != "" || msg.ErrorDetail != nil {
			return ErrUnreachable
		}
		count++
	}
	if count == 0 {
		return ErrUnreachable
	}
	return nil
}
