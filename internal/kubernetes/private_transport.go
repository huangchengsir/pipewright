package kubernetes

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/huangchengsir/pipewright/internal/target"
)

var ErrTool = errors.New("kubernetes_tool_unavailable")
var ErrPrivateLimit = errors.New("kubernetes_private_output_limit")
var ErrVersion = errors.New("kubernetes_tool_version_incompatible")
var ErrRuntimeIdentity = errors.New("kubernetes_runtime_identity_changed")
var ErrTransportUnknown = errors.New("kubernetes_transport_outcome_unknown")

// PrivateTransport is not exposed through HTTP or repository configuration.
// Implementations must never log inputs, arguments or raw output.
type PrivateTransport interface {
	Docker(context.Context, []string, []byte) (*target.SecretResult, error)
	Pull(context.Context, string, []byte) error
	Snapshot() *target.ConnectionSnapshot
}
type TransportFactory func(context.Context, Executor, *target.ConnectionSnapshot) (PrivateTransport, error)
type RegistryAuthSource func(context.Context, string, func(username, password string) error) error

// NewTransportFactory pins local commands to the default local Unix socket and
// SSH commands to an approved snapshot plus host key. Environment/context-based
// remote Docker selection is deliberately unavailable.
func NewTransportFactory(dockerBinary string, remote target.SecretExecutor) TransportFactory {
	return func(ctx context.Context, x Executor, saved *target.ConnectionSnapshot) (PrivateTransport, error) {
		if !validExecutor(x) {
			return nil, ErrInvalid
		}
		if x.Kind == "local" {
			if saved != nil {
				return nil, ErrRuntimeUnverified
			}
			bin, e := exec.LookPath(dockerBinary)
			if e != nil {
				return nil, ErrTool
			}
			return &localPrivateTransport{bin: bin}, nil
		}
		if remote == nil {
			return nil, ErrTool
		}
		snap, e := remote.Capture(ctx, x.ServerID)
		if e != nil {
			return nil, ErrTool
		}
		if saved != nil && saved.Fingerprint() != snap.Fingerprint() {
			return nil, ErrRuntimeIdentity
		}
		return &sshPrivateTransport{remote: remote, snap: snap, host: x.HostKeySHA256}, nil
	}
}

var dockerPrefix = []string{"--host", localDockerEndpoint, "--config", privateDockerConfig}

func dockerArgs(args []string) []string {
	return append(append([]string(nil), dockerPrefix...), args...)
}
func validatePrivateInput(args []string, input []byte) bool {
	if len(args) == 0 || len(args) > 128 || len(input) > target.SecretInputLimit {
		return false
	}
	for _, a := range args {
		if len(a) > 32768 || strings.ContainsRune(a, 0) {
			return false
		}
	}
	return true
}

type privateBudget struct {
	mu        sync.Mutex
	remaining int
	out, err  bytes.Buffer
	truncated bool
}
type privateWriter struct {
	b      *privateBudget
	stderr bool
}

func (w privateWriter) Write(p []byte) (int, error) {
	w.b.mu.Lock()
	defer w.b.mu.Unlock()
	n := min(len(p), w.b.remaining)
	if n < len(p) {
		w.b.truncated = true
	}
	if w.stderr {
		_, _ = w.b.err.Write(p[:n])
	} else {
		_, _ = w.b.out.Write(p[:n])
	}
	w.b.remaining -= n
	return len(p), nil
}
func privateCommand(ctx context.Context, bin string, args []string, input []byte) (*target.SecretResult, error) {
	if !validatePrivateInput(args, input) {
		return nil, ErrInvalid
	}
	cmd := exec.CommandContext(ctx, bin, args...)
	// No ambient Docker context, auth config, proxy or project variables.
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "LANG=C", "LC_ALL=C"}
	if runtime.GOOS == "windows" {
		cmd.Env = append(cmd.Env, "SYSTEMROOT="+os.Getenv("SYSTEMROOT"))
	}
	cmd.Stdin = bytes.NewReader(input)
	cmd.WaitDelay = 2 * time.Second
	b := &privateBudget{remaining: MaxPrivateResponseBytes}
	cmd.Stdout = privateWriter{b: b}
	cmd.Stderr = privateWriter{b: b, stderr: true}
	e := cmd.Run()
	out := &target.SecretResult{Stdout: append([]byte(nil), b.out.Bytes()...), Stderr: append([]byte(nil), b.err.Bytes()...), Truncated: b.truncated, ExitCode: -1}
	clear(b.out.Bytes())
	clear(b.err.Bytes())
	if e == nil {
		out.ExitCode = 0
		return out, nil
	}
	var exit *exec.ExitError
	if errors.As(e, &exit) && exit.ExitCode() >= 0 {
		out.ExitCode = exit.ExitCode()
		return out, nil
	}
	if cmd.Process == nil {
		return out, target.NotStarted(ErrTool)
	}
	return out, ErrTransportUnknown
}

type localPrivateTransport struct{ bin string }

func (t *localPrivateTransport) Snapshot() *target.ConnectionSnapshot { return nil }
func (t *localPrivateTransport) Docker(ctx context.Context, args []string, input []byte) (*target.SecretResult, error) {
	return privateCommand(ctx, t.bin, dockerArgs(args), input)
}
func (t *localPrivateTransport) Pull(ctx context.Context, image string, auth []byte) error {
	tr := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return dialLocalDocker(ctx)
	}}
	defer tr.CloseIdleConnections()
	if e := target.PullDockerImage(ctx, tr, image, auth); e != nil {
		return ErrTool
	}
	return nil
}

type sshPrivateTransport struct {
	remote target.SecretExecutor
	snap   target.ConnectionSnapshot
	host   string
}

func (t *sshPrivateTransport) Snapshot() *target.ConnectionSnapshot { s := t.snap; return &s }
func (t *sshPrivateTransport) Docker(ctx context.Context, args []string, input []byte) (*target.SecretResult, error) {
	// SSH tools always execute on the approved Linux Engine host, regardless
	// of whether the Pipewright controller itself runs on Windows.
	command := []string{"docker", "--host", "unix:///var/run/docker.sock", "--config", "/dev/null"}
	return t.remote.ExecSecretPinned(ctx, t.snap, t.host, append(command, args...), input)
}
func (t *sshPrivateTransport) Pull(ctx context.Context, image string, auth []byte) error {
	return t.remote.PullSecretPinned(ctx, t.snap, t.host, image, auth)
}
func clearResult(r *target.SecretResult) {
	if r != nil {
		clear(r.Stdout)
		clear(r.Stderr)
	}
}
func privateJSON(raw []byte, v any) error {
	if len(raw) == 0 || len(raw) > MaxPrivateResponseBytes {
		return ErrPrivateLimit
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	if e := d.Decode(v); e != nil {
		return ErrTool
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		return ErrTool
	}
	return nil
}
