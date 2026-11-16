package target

import (
	"context"
	"regexp"
	"slices"
	"time"

	"github.com/huangchengsir/pipewright/internal/store"
)

var dockerToolID = regexp.MustCompile(`^[a-f0-9]{64}$`)

// DockerToolCleanupIssuer is internal infrastructure. The Kubernetes caller
// binds a newly created tool to its lease before asking for this handle.
type DockerToolCleanupIssuer interface {
	OpenDockerToolCleanup(context.Context, ConnectionSnapshot, string, string) (*DockerToolCleanup, error)
}

// DockerToolCleanup can only inspect and dispose of one already bound tool. It
// keeps the connection identity and credential version, not plaintext secrets.
type DockerToolCleanup struct {
	service   *service
	snapshot  ConnectionSnapshot
	hostKey   string
	container string
	expires   time.Time
}

func (s *service) OpenDockerToolCleanup(ctx context.Context, snap ConnectionSnapshot, hostKey, container string) (*DockerToolCleanup, error) {
	if !dockerToolID.MatchString(container) || !validHostFingerprint(hostKey) || snap.ExecutionBindingVersion == "" || snap.ExecutionHostKey != hostKey {
		return nil, ErrTargetChanged
	}
	if _, ok := s.dialer.(secretSSHRunner); !ok {
		return nil, ErrLimitedUnavailable
	}
	if err := s.AuthorizePinned(ctx, snap); err != nil {
		return nil, err
	}
	// The longest release is 30 minutes. Keep the handle for that run and its
	// final bounded cleanup, without renewing it after authorization is lost.
	return &DockerToolCleanup{service: s, snapshot: snap, hostKey: hostKey, container: container, expires: time.Now().Add(32 * time.Minute)}, nil
}

func (c *DockerToolCleanup) Docker(ctx context.Context, args []string, input []byte) (*SecretResult, error) {
	if c == nil || c.service == nil || !time.Now().Before(c.expires) || len(input) != 0 {
		return nil, ErrTargetChanged
	}
	id := c.container
	if !slices.Equal(args, []string{"info", "--format", "{{json .}}"}) &&
		!slices.Equal(args, []string{"inspect", "--type", "container", "--format", "{{json .}}", id}) &&
		!slices.Equal(args, []string{"stop", "--time", "2", id}) &&
		!slices.Equal(args, []string{"rm", id}) {
		return nil, ErrTargetChanged
	}

	ctx, cancel := context.WithDeadline(ctx, c.expires)
	defer cancel()
	s := c.service
	pinned := c.snapshot.Server
	var host, user, credentialID string
	var port int
	query := "SELECT host,port," + store.Identifier(store.DialectOf(s.db), "user") + ",credential_id FROM servers WHERE id=?"
	if err := s.db.QueryRowContext(ctx, query, pinned.ID).Scan(&host, &port, &user, &credentialID); err != nil ||
		host != pinned.Host || port != pinned.Port || user != pinned.User || credentialID != pinned.CredentialID {
		return nil, ErrTargetChanged
	}
	_, version, err := s.readExecutionAssignment(ctx, pinned.ID)
	if err != nil || version != c.snapshot.ExecutionBindingVersion {
		return nil, ErrTargetChanged
	}
	runner, ok := s.dialer.(secretSSHRunner)
	if !ok {
		return nil, ErrLimitedUnavailable
	}
	command := append([]string{"docker", "--host", "unix:///var/run/docker.sock", "--config", "/dev/null"}, args...)
	var result *SecretResult
	err = s.withPinnedCredential(ctx, c.snapshot, func(addr string, cfg SSHConfig) error {
		var runErr error
		result, runErr = runner.RunSecret(ctx, addr, cfg, c.hostKey, command, nil)
		return runErr
	})
	return result, err
}
