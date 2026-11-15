package target

import (
	"context"
	"errors"
	"io"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/huangchengsir/pipewright/internal/store"
)

var ErrWorkspaceCleanup = errors.New("target: remote workspace cleanup unconfirmed")

// WorkspaceCreator is internal execution infrastructure, never an HTTP/MCP DTO.
// It creates a new directory and a cleanup capability in one authorized operation.
type WorkspaceCreator interface {
	CreateWorkspace(context.Context, string) (*RemoteWorkspace, error)
}

// RemoteWorkspace has no exported writable fields. Exec and Upload still require
// live execution permission for its pinned target. Only Cleanup can outlive that
// permission, and only for this freshly created resource. No plaintext credential
// is retained.
type RemoteWorkspace struct {
	service     *service
	approved    ConnectionSnapshot
	root, owner string
	expires     time.Time
	once        sync.Once
	cleanupErr  error
}

func (w *RemoteWorkspace) Directory() string { return w.root + "/work" }
func (w *RemoteWorkspace) Archive() string   { return w.root + "/source.tar.gz" }

const createWorkspaceScript = `set -eu
umask 077
[ -d /tmp ] && [ ! -L /tmp ] || exit 70
mkdir -- "$1"
printf '%s' "$2" > "$1/.pipewright-owner"
mkdir -- "$1/work"`

const cleanupWorkspaceScript = `set -eu
[ -d /tmp ] && [ ! -L /tmp ] || exit 70
[ ! -L "$1" ] || exit 71
[ -d "$1" ] || exit 72
[ ! -L "$1/.pipewright-owner" ] || exit 73
[ -f "$1/.pipewright-owner" ] || exit 74
[ "$(cat -- "$1/.pipewright-owner")" = "$2" ] || exit 75
rm -rf -- "$1"
[ ! -e "$1" ] && [ ! -L "$1" ]`

func (s *service) CreateWorkspace(ctx context.Context, serverID string) (*RemoteWorkspace, error) {
	approved, err := s.Capture(ctx, serverID)
	if err != nil {
		return nil, err
	}
	w := &RemoteWorkspace{service: s, approved: approved, root: "/tmp/pipewright-work-" + uuid.NewString(), owner: uuid.NewString(), expires: time.Now().Add(24 * time.Hour)}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	err = s.withApprovedSSH(ctx, approved, func(addr string, cfg SSHConfig) error {
		res, e := s.dialer.Run(ctx, addr, cfg, []string{"sh", "-c", createWorkspaceScript, "pipewright-workspace", w.root, w.owner})
		if e != nil || res == nil || res.ExitCode != 0 {
			return errors.New("target: remote workspace creation unconfirmed")
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return w, nil
}

func (w *RemoteWorkspace) Cleanup(ctx context.Context) error {
	if w == nil || w.service == nil || w.root == "" || w.owner == "" {
		return ErrWorkspaceCleanup
	}
	w.once.Do(func() { w.cleanupErr = w.cleanup(ctx) })
	return w.cleanupErr
}
func (w *RemoteWorkspace) cleanup(ctx context.Context) error {
	if time.Now().After(w.expires) {
		return ErrWorkspaceCleanup
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	// A changed/deleted target must not redirect a cleanup to another host/user.
	s := w.service
	var host, user, credentialID string
	var port int
	err := s.db.QueryRowContext(ctx, `SELECT host,port,`+store.Identifier(store.DialectOf(s.db), "user")+`,credential_id FROM servers WHERE id=?`, w.approved.Server.ID).Scan(&host, &port, &user, &credentialID)
	a := w.approved.Server
	if err != nil || host != a.Host || port != a.Port || user != a.User || credentialID != a.CredentialID {
		return ErrTargetChanged
	}
	return s.withPinnedCredential(ctx, w.approved, func(addr string, cfg SSHConfig) error {
		res, e := s.dialer.Run(ctx, addr, cfg, []string{"sh", "-c", cleanupWorkspaceScript, "pipewright-workspace-cleanup", w.root, w.owner})
		// Never echo SSH output: scripts or remote data could contain secrets.
		if e != nil || res == nil || res.ExitCode != 0 {
			return ErrWorkspaceCleanup
		}
		return nil
	})
}

// Exec and Upload retain ordinary live execution checks and the immutable target
// snapshot. Only Cleanup carries the narrow post-revocation exception.
func (w *RemoteWorkspace) Exec(ctx context.Context, serverID string, cmd []string) (out *ExecResult, err error) {
	if w == nil || w.service == nil || serverID != w.approved.Server.ID || len(cmd) == 0 {
		return nil, ErrTargetChanged
	}
	err = w.service.withApprovedSSH(ctx, w.approved, func(addr string, cfg SSHConfig) error {
		var e error
		out, e = w.service.dialer.Run(ctx, addr, cfg, cmd)
		return e
	})
	return
}
func (w *RemoteWorkspace) Upload(ctx context.Context, serverID string, content io.Reader, remotePath string) error {
	if w == nil || w.service == nil || serverID != w.approved.Server.ID || remotePath != w.Archive() {
		return ErrTargetChanged
	}
	return w.service.withApprovedSSH(ctx, w.approved, func(addr string, cfg SSHConfig) error {
		out, e := w.service.dialer.RunWithStdin(ctx, addr, cfg, []string{"sh", "-c", `[ -d "$1" ] && [ ! -L "$1" ] && [ ! -L "$2" ] && cat > "$2"`, "pipewright-workspace-upload", w.root, w.Archive()}, content)
		if e != nil || out == nil || out.ExitCode != 0 {
			return errors.New("target: workspace upload unconfirmed")
		}
		return nil
	})
}
