package build

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"github.com/google/uuid"
	"github.com/huangchengsir/pipewright/internal/runretry"
	"github.com/huangchengsir/pipewright/internal/target"
	"os/exec"
	"strings"
	"time"
)

type ownedService struct {
	ref      runretry.ResourceRef
	recordID string
}

// knownCLIExit distinguishes a completed local process/remote exit-status from
// a lost receipt. A numeric code alone never overrides transport/cancel errors.
func knownCLIExit(ctx context.Context, code int, err error) bool {
	if code < 0 || ctx.Err() != nil {
		return false
	}
	if err == nil {
		return true
	}
	var exited *exec.ExitError
	return !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) && errors.As(err, &exited) && exited.ExitCode() == code
}

func (d *shellDriver) resourceRuntime(ctx context.Context) (string, error) {
	out, _, code, err := d.cmdr.Run(ctx, d.bin, []string{"info", "--format", "{{.ID}}"}, "")
	if err != nil || code != 0 || strings.TrimSpace(out) == "" || len(out) > 4096 {
		return "", runretry.ErrUnavailable
	}
	sum := sha256.Sum256([]byte(d.bin + "\x00" + strings.TrimSpace(out)))
	return hex.EncodeToString(sum[:]), nil
}
func (d *shellDriver) startOwnedResource(ctx context.Context, kind, name, owner string) (ownedService, error) {
	v := ownedService{ref: runretry.ResourceRef{Kind: kind, Name: name, Owner: owner}}
	if kind == "workspace_container" {
		v.ref.Kind = "container"
		v.ref.RemoveVolumes = true
	}
	if _, ok := runretry.ExecutionFrom(ctx); !ok {
		return v, nil
	}
	runtime, err := d.resourceRuntime(ctx)
	if err != nil {
		return v, err
	}
	v.ref.RuntimeID = runtime
	if remote, ok := d.cmdr.(*sshCommander); ok {
		v.ref.ServerID = remote.serverID
		binding, e := remoteBinding(ctx, remote.execer, remote.serverID)
		if e != nil {
			return v, e
		}
		v.ref.ConnectionFingerprint = binding
	}
	v.recordID, err = runretry.TrackResource(ctx, v.ref)
	return v, err
}
func (d *shellDriver) recordOwnedResource(ctx context.Context, v ownedService, state string) error {
	if v.recordID == "" {
		return nil
	}
	return runretry.UpdateResource(ctx, v.recordID, v.ref, state)
}
func (d *shellDriver) serviceCreated(ctx context.Context, name, id string) error {
	value, ok := d.serviceEvidence.Load(name)
	if !ok {
		return nil
	}
	v := value.(ownedService)
	v.ref.ID = id
	d.serviceEvidence.Store(name, v)
	return d.recordOwnedResource(ctx, v, "active")
}
func serviceMissing(kind, id, stderr string) bool {
	msg := strings.TrimSpace(stderr)
	if kind == "network" {
		return msg == "Error: No such network: "+id || msg == "Error response from daemon: network "+id+" not found" || msg == "network "+id+" not found"
	}
	return msg == "Error: No such object: "+id || msg == "Error: No such container: "+id || msg == "Error response from daemon: No such container: "+id
}
func (d *shellDriver) inspectOwned(ctx context.Context, r runretry.ResourceRef) (absent bool, id string, err error) {
	lookup := r.ID
	if lookup == "" {
		lookup = r.Name
	}
	args := []string{"inspect", "--type", "container", "--format", `{{.Id}} {{index .Config.Labels "io.pipewright.service.owner"}}`, lookup}
	if r.Kind == "network" {
		args = []string{"network", "inspect", "--format", `{{.ID}} {{index .Labels "io.pipewright.service.owner"}}`, lookup}
	}
	out, stderr, code, err := d.cmdr.Run(ctx, d.bin, args, "")
	// Local CLI nonzero exits carry *exec.ExitError; remote Commander reports
	// the same known exit as a nil error plus code. Only a completed exit1 and
	// the exact owned-object missing diagnostic prove absence. Transport errors,
	// cancellation and a different exit code remain unknown.
	knownExit := knownCLIExit(ctx, code, err)
	if knownExit && code == 1 && strings.TrimSpace(out) == "" && serviceMissing(r.Kind, lookup, stderr) {
		return true, "", nil
	}
	if err != nil || code != 0 || len(out) > 4096 {
		return false, "", runretry.ErrUnknown
	}
	parts := strings.Fields(out)
	if len(parts) != 2 || !serviceObjectID.MatchString(parts[0]) || parts[1] != r.Owner || r.ID != "" && r.ID != parts[0] {
		return false, "", runretry.ErrUnknown
	}
	return false, parts[0], nil
}

// VerifyResource performs only info/inspect. A foreign label, changed daemon or
// missing ownership proof never permits deletion or a synthetic drain result.
func (d *shellDriver) VerifyResource(ctx context.Context, r runretry.ResourceRef) (bool, error) {
	if r.CreationUnknown {
		return false, runretry.ErrUnknown
	}
	if r.Kind != "container" && r.Kind != "network" {
		return false, runretry.ErrUnavailable
	}
	if r.Owner == "" || r.Name == "" || r.RuntimeID == "" {
		return false, runretry.ErrUnavailable
	}
	runtime, err := d.resourceRuntime(ctx)
	if err != nil || runtime != r.RuntimeID {
		return false, runretry.ErrUnknown
	}
	absent, _, err := d.inspectOwned(ctx, r)
	return absent, err
}
func (b *Builder) VerifyResource(ctx context.Context, r runretry.ResourceRef) (bool, error) {
	if r.ServerID != "" {
		return false, runretry.ErrUnavailable
	}
	v, ok := b.driver.(interface {
		VerifyResource(context.Context, runretry.ResourceRef) (bool, error)
	})
	if !ok {
		return false, runretry.ErrUnavailable
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	return v.VerifyResource(ctx, r)
}
func (d *shellDriver) removeOwnedService(ctx context.Context, name, kind string, onLine func(string, string)) (int, error) {
	table := &d.serviceContainers
	if kind == "network" {
		table = &d.serviceNetworks
	}
	value, ok := table.Load(name)
	if !ok {
		return -1, runretry.ErrUnknown
	}
	id := value.(string)
	evidence, has := d.serviceEvidence.Load(name)
	v := ownedService{ref: runretry.ResourceRef{Kind: kind, Name: name, ID: id}}
	if has {
		v = evidence.(ownedService)
		v.ref.ID = id
	}
	// Older in-memory receipts remain compatible; recovery receipts require the
	// random owner label and exact runtime before any destructive operation.
	if v.recordID != "" {
		if v.ref.CreationUnknown {
			return -1, runretry.ErrUnknown
		}
		runtime, err := d.resourceRuntime(ctx)
		if err != nil || runtime != v.ref.RuntimeID {
			return -1, runretry.ErrUnknown
		}
		absent, actual, err := d.inspectOwned(ctx, v.ref)
		if err != nil {
			return -1, err
		}
		if absent {
			if err = d.recordOwnedResource(ctx, v, "drained"); err != nil {
				return -1, err
			}
			table.CompareAndDelete(name, id)
			return 0, nil
		}
		if actual != id {
			return -1, runretry.ErrUnknown
		}
	}
	args := []string{"rm", "-f", id}
	if v.ref.RemoveVolumes {
		args = []string{"rm", "-f", "-v", id}
	}
	if kind == "network" {
		args = []string{"network", "rm", id}
	}
	emitCmd(onLine, d.bin, args)
	code, err := d.cmdr.Stream(ctx, d.bin, args, "", onLine)
	if err != nil || code != 0 {
		return code, err
	}
	if v.recordID != "" {
		absent, _, err := d.inspectOwned(ctx, v.ref)
		if err != nil || !absent {
			return -1, errors.Join(runretry.ErrUnknown, err)
		}
		if err = d.recordOwnedResource(ctx, v, "drained"); err != nil {
			return -1, err
		}
	}
	table.CompareAndDelete(name, id)
	d.serviceEvidence.Delete(name)
	return 0, nil
}

func (d *shellDriver) serviceCreateFailed(ctx context.Context, name string, cause error, code int) {
	value, ok := d.serviceEvidence.Load(name)
	if !ok {
		return
	}
	v := value.(ownedService)
	if v.recordID == "" {
		return
	}
	if target.IsNotStarted(cause) {
		_ = d.recordOwnedResource(context.WithoutCancel(ctx), v, "drained")
		return
	}
	v.ref.CreationUnknown = !knownCLIExit(ctx, code, cause)
	d.serviceEvidence.Store(name, v)
	_ = d.recordOwnedResource(context.WithoutCancel(ctx), v, "outcome_unknown")
}
func (d *shellDriver) runOwnedToolchain(ctx context.Context, args, display []string, onLine func(string, string)) (int, error) {
	name := "pw-task-" + uuid.NewString()
	owner := rand.Text()
	v, err := d.startOwnedResource(ctx, "container", name, owner)
	if err != nil {
		return -1, err
	}
	args = append([]string{"run", "--name", name, "--label", serviceOwnerLabel + "=" + owner}, args[1:]...)
	display = append([]string{"run", "--name", name}, display[1:]...)
	emitCmd(onLine, d.bin, display)
	code, runErr := d.cmdr.Stream(ctx, d.bin, args, "", onLine)
	clean, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if !knownCLIExit(ctx, code, runErr) {
		if target.IsNotStarted(runErr) {
			_ = d.recordOwnedResource(clean, v, "drained")
			return code, runErr
		}
		v.ref.CreationUnknown = true
		_ = d.recordOwnedResource(clean, v, "outcome_unknown")
		return code, runErr
	}
	absent, id, inspectErr := d.inspectOwned(clean, v.ref)
	if inspectErr == nil && absent {
		_ = d.recordOwnedResource(clean, v, "drained")
	} else {
		v.ref.ID = id
		_ = d.recordOwnedResource(clean, v, "outcome_unknown")
	}
	return code, runErr
}
