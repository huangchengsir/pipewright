package build

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"github.com/huangchengsir/pipewright/internal/runretry"
	"github.com/huangchengsir/pipewright/internal/target"
	"strings"
	"time"
)

const recoveryRemoteWorkspaceRoot = "/tmp/pipewright-remote"

// Initialize only the shared parent; never chmod/chown/remove an existing one.
// Parent ownership and non-writable group/other bits prevent another runner
// user replacing it between validation and the exclusive child mkdir. A mkdir
// race is accepted only after revalidating the complete parent identity.
const recoveryRemoteWorkspaceCreateScript = `
if [ ! -e "$1" ] && [ ! -L "$1" ]; then
  mkdir -m 700 -- "$1" 2>/dev/null || {
    [ -d "$1" ] && [ ! -L "$1" ] || exit 1
  }
fi
[ -d "$1" ] && [ ! -L "$1" ] || exit 1
[ "$(stat -c %u -- "$1")" = "$(id -u)" ] || exit 1
mode=$(stat -c %a -- "$1") || exit 1
case "$mode" in ''|*[!0-7]*) exit 1;; esac
[ "$((0$mode & 022))" -eq 0 ] || exit 1
mkdir -m 700 -- "$2"
`

func (d *recoveryRemoteDriver) createWorkspace(ctx context.Context, ws string) error {
	prefix := recoveryRemoteWorkspaceRoot + "/"
	if !strings.HasPrefix(ws, prefix) || strings.ContainsAny(ws, "\x00\r\n") || strings.Contains(strings.TrimPrefix(ws, prefix), "/") || strings.TrimPrefix(ws, prefix) == "" || strings.TrimPrefix(ws, prefix) == "." || strings.TrimPrefix(ws, prefix) == ".." {
		return runretry.ErrInvalid
	}
	out, err := d.target.Exec(ctx, d.serverID, []string{"sh", "-c", recoveryRemoteWorkspaceCreateScript, "pw-remote-workspace-create", recoveryRemoteWorkspaceRoot, ws})
	if err != nil || out == nil || out.ExitCode != 0 {
		return runretry.ErrUnavailable
	}
	return nil
}

func remoteBinding(ctx context.Context, t RemoteExecer, id string) (string, error) {
	c, ok := t.(interface {
		Capture(context.Context, string) (target.ConnectionSnapshot, error)
	})
	if !ok {
		return "", runretry.ErrUnavailable
	}
	s, e := c.Capture(ctx, id)
	if e != nil {
		return "", e
	}
	b, _ := json.Marshal(struct {
		ID, Host, User, Credential string
		Port                       int
	}{s.Server.ID, s.Server.Host, s.Server.User, s.Server.CredentialID, s.Server.Port})
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:]), nil
}
func remoteBoot(ctx context.Context, t RemoteExecer, id string) (string, error) {
	out, e := t.Exec(ctx, id, []string{"cat", "/proc/sys/kernel/random/boot_id"})
	if e != nil || out == nil || out.ExitCode != 0 || len(strings.TrimSpace(out.Stdout)) != 36 {
		return "", runretry.ErrUnavailable
	}
	h := sha256.Sum256([]byte(strings.TrimSpace(out.Stdout)))
	return hex.EncodeToString(h[:]), nil
}

// VerifyRemoteResource is a bounded observation adapter for main's verifier.
// It never runs rm/kill/create and rejects changed server/daemon identities.
func VerifyRemoteResource(ctx context.Context, t RemoteExecer, r runretry.ResourceRef) (bool, error) {
	if r.CreationUnknown {
		return false, runretry.ErrUnknown
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if r.ServerID == "" || r.Owner == "" {
		return false, runretry.ErrUnavailable
	}
	binding, e := remoteBinding(ctx, t, r.ServerID)
	if e != nil || r.ConnectionFingerprint == "" || binding != r.ConnectionFingerprint {
		return false, runretry.ErrUnavailable
	}
	if r.Kind != "remote_directory" {
		driver := NewRemoteDriver(t, r.ServerID, "docker").(*shellDriver)
		return driver.VerifyResource(ctx, r)
	}
	if !strings.HasPrefix(r.Path, "/tmp/pipewright-remote/") || strings.ContainsAny(r.Path, "\x00\r\n") || strings.Count(strings.TrimPrefix(r.Path, "/tmp/pipewright-remote/"), "/") != 0 {
		return false, runretry.ErrInvalid
	}
	boot, e := remoteBoot(ctx, t, r.ServerID)
	if e != nil || boot != r.RuntimeID {
		return false, runretry.ErrUnavailable
	}
	out, e := t.Exec(ctx, r.ServerID, []string{"sh", "-c", `if [ ! -e "$1" ] && [ ! -L "$1" ]; then exit 0; fi; if [ -d "$1" ] && [ ! -L "$1" ] && [ "$(cat "$1/.pipewright-owner" 2>/dev/null)" = "$2" ]; then exit 1; fi; exit 2`, "pw-resource-inspect", r.Path, r.Owner})
	if e != nil || out == nil {
		return false, runretry.ErrUnknown
	}
	if out.ExitCode == 0 {
		return true, nil
	}
	if out.ExitCode == 1 {
		return false, nil
	}
	return false, runretry.ErrUnknown
}

type remoteResource struct {
	owned                     ownedService
	group, attempted, drained bool
}

func (d *recoveryRemoteDriver) resourceOwner(ws string) string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.resources[ws].owned.ref.Owner
}

func (d *recoveryRemoteDriver) trackWorkspace(ctx context.Context, ws string, group bool) (remoteResource, error) {
	r := remoteResource{group: group}
	binding, e := remoteBinding(ctx, d.target, d.serverID)
	if e != nil {
		return r, e
	}
	boot, e := remoteBoot(ctx, d.target, d.serverID)
	if e != nil {
		return r, e
	}
	r.owned.ref = runretry.ResourceRef{Kind: "remote_directory", Name: strings.TrimPrefix(ws, "/tmp/pipewright-remote/"), Path: ws, Owner: rand.Text(), ServerID: d.serverID, ConnectionFingerprint: binding, RuntimeID: boot}
	r.owned.recordID, e = runretry.TrackResource(ctx, r.owned.ref)
	return r, e
}
func (d *recoveryRemoteDriver) cleanupWorkspace(ctx context.Context, ws string) error {
	d.mu.Lock()
	resource, ok := d.resources[ws]
	if !ok {
		d.mu.Unlock()
		return runretry.ErrUnavailable
	}
	if resource.drained {
		d.mu.Unlock()
		return nil
	}
	if resource.attempted {
		d.mu.Unlock()
		return runretry.ErrUnknown
	}
	resource.attempted = true
	d.resources[ws] = resource
	d.mu.Unlock()
	// Prove ownership before deleting, including a failed upload or script.
	absent, e := VerifyRemoteResource(ctx, d.target, resource.owned.ref)
	if e != nil {
		return e
	}
	if !absent {
		out, e := d.target.Exec(ctx, d.serverID, []string{"sh", "-c", `[ -d "$1" ] && [ ! -L "$1" ] && [ "$(cat "$1/.pipewright-owner" 2>/dev/null)" = "$2" ] && rm -rf -- "$1"`, "pw-resource-cleanup", ws, resource.owned.ref.Owner})
		if e != nil || out == nil || out.ExitCode != 0 {
			return runretry.ErrUnknown
		}
		absent, e = VerifyRemoteResource(ctx, d.target, resource.owned.ref)
		if e != nil || !absent {
			return runretry.ErrUnknown
		}
	}
	if e = runretry.UpdateResource(ctx, resource.owned.recordID, resource.owned.ref, "drained"); e != nil {
		return e
	}
	d.mu.Lock()
	resource.drained = true
	d.resources[ws] = resource
	d.mu.Unlock()
	return nil
}
