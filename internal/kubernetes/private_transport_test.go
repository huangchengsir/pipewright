package kubernetes

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/huangchengsir/pipewright/internal/target"
)

type inspectResponseTransport struct {
	PrivateTransport
	result target.SecretResult
	err    error
}

func (t inspectResponseTransport) Docker(context.Context, []string, []byte) (*target.SecretResult, error) {
	out := t.result
	out.Stdout = append([]byte(nil), out.Stdout...)
	out.Stderr = append([]byte(nil), out.Stderr...)
	return &out, t.err
}
func TestMissingRuntimeRequiresExactDockerEvidence(t *testing.T) {
	id := strings.Repeat("a", 64)
	warning := "WARNING: Error loading config file: open /dev/null/config.json: not a directory\n"
	missing := "Error response from daemon: No such container: " + id
	for _, tc := range []struct {
		name, stderr, stdout string
		exit                 int
		truncated            bool
		err                  error
		absent               bool
	}{
		{name: "plain", stderr: missing, exit: 1, absent: true},
		{name: "inert_config_warning", stderr: warning + missing + "\n", stdout: "\n", exit: 1, absent: true},
		{name: "wrong_container", stderr: warning + "Error response from daemon: No such container: other", exit: 1},
		{name: "extra_error", stderr: warning + missing + "\npermission denied", exit: 1},
		{name: "unknown_warning", stderr: "WARNING: endpoint changed\n" + missing, exit: 1},
		{name: "repeated_warning", stderr: warning + warning + missing, exit: 1},
		{name: "unexpected_stdout", stderr: missing, stdout: "{}", exit: 1},
		{name: "wrong_exit", stderr: missing, exit: 125},
		{name: "truncated", stderr: missing, exit: 1, truncated: true},
		{name: "transport_failed", stderr: missing, exit: 1, err: ErrTransportUnknown},
	} {
		t.Run(tc.name, func(t *testing.T) {
			transport := inspectResponseTransport{result: target.SecretResult{Stdout: []byte(tc.stdout), Stderr: []byte(tc.stderr), ExitCode: tc.exit, Truncated: tc.truncated}, err: tc.err}
			_, absent, err := inspectContainer(context.Background(), transport, RuntimeLease{ContainerID: id, ContainerName: "owned"})
			if absent != tc.absent || tc.absent && err != nil || !tc.absent && err == nil {
				t.Fatalf("absent=%v err=%v, expected absent=%v", absent, err, tc.absent)
			}
		})
	}
}

type capturedSecretExecutor struct {
	target.SecretExecutor
	command []string
	host    string
}

func (x *capturedSecretExecutor) ExecSecretPinned(_ context.Context, _ target.ConnectionSnapshot, host string, command []string, _ []byte) (*target.SecretResult, error) {
	x.command = append([]string(nil), command...)
	x.host = host
	return &target.SecretResult{}, nil
}
func TestSSHRuntimeUsesLinuxEndpointRegardlessOfControllerOS(t *testing.T) {
	t.Setenv("DOCKER_HOST", "tcp://untrusted.invalid:2375")
	t.Setenv("DOCKER_CONFIG", "untrusted-config")
	x := &capturedSecretExecutor{}
	transport := sshPrivateTransport{remote: x, host: "approved-host-key"}
	_, err := transport.Docker(context.Background(), []string{"info", "--format", "{{json .}}"}, nil)
	want := []string{"docker", "--host", "unix:///var/run/docker.sock", "--config", "/dev/null", "info", "--format", "{{json .}}"}
	if err != nil || !reflect.DeepEqual(x.command, want) || x.host != "approved-host-key" {
		t.Fatalf("remote endpoint or host pin changed: command=%q host=%q err=%v", x.command, x.host, err)
	}
}
