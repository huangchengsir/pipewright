package build

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"path"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/huangchengsir/pipewright/internal/pipeline"
	"github.com/huangchengsir/pipewright/internal/runretry"
)

type recoveryReporterContextKey struct{}
type recoveryRemoteDriver struct {
	Driver
	target    remoteExec
	serverID  string
	mu        sync.Mutex
	shared    map[string]string
	resources map[string]remoteResource
}

func (d *recoveryRemoteDriver) RunToolchain(ctx context.Context, image, hostDir, workdir string, env, cmd []string, res pipeline.Resource, onLine func(string, string)) (code int, runErr error) {
	n, ok := runretry.NodeFrom(ctx)
	if !ok {
		return -1, runretry.ErrInvalid
	}
	ex, ok := runretry.ExecutionFrom(ctx)
	if !ok {
		return -1, runretry.ErrInvalid
	}
	var job pipeline.Job
	post := false
	stages, err := ex.Snapshot().ExecutionStages()
	if err != nil {
		return -1, err
	}
	for _, st := range stages {
		if st.ID == n.ExecutionStageID {
			if strings.HasPrefix(n.ControlKey, "post:") {
				index, parseErr := strconv.Atoi(strings.TrimPrefix(n.ControlKey, "post:"))
				if parseErr != nil || index < 0 || index >= len(st.Post) || n.ControlKey != "post:"+strconv.Itoa(index) {
					return -1, runretry.ErrInvalid
				}
				post = true
			}
			for _, j := range st.Jobs {
				if j.ID == n.ExecutionJobID {
					job = j
				}
			}
		}
	}
	if !post && !isScriptJob(job.Type) {
		return -1, runretry.ErrUnavailable
	}
	group := recoveryShared(ctx) != ""
	remoteWS := ""
	if group {
		d.mu.Lock()
		remoteWS = d.shared[hostDir]
		d.mu.Unlock()
	}
	created := remoteWS == ""
	if created {
		remoteWS = recoveryRemoteWorkspaceRoot + "/" + uuid.NewString()
	}
	remoteTar := path.Join(remoteWS, ".workspace.tar.gz")
	if created {
		resource, err := d.trackWorkspace(ctx, remoteWS, group)
		if err != nil {
			return -1, err
		}
		d.mu.Lock()
		if d.resources == nil {
			d.resources = map[string]remoteResource{}
		}
		d.resources[remoteWS] = resource
		d.mu.Unlock()
	}
	exec := func(ctx context.Context, args ...string) error {
		r, e := d.target.Exec(ctx, d.serverID, args)
		if e != nil || r == nil || r.ExitCode != 0 {
			return runretry.ErrUnavailable
		}
		return nil
	}
	defer func() {
		if group {
			return
		}
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
		defer cancel()
		if err := d.cleanupWorkspace(cleanupCtx, remoteWS); err != nil {
			if onLine != nil {
				onLine(streamStderr, "远程工作区清理尚未核验，新的恢复保持阻止")
			}
		}
	}()
	if created {
		if group {
			d.mu.Lock()
			if d.shared == nil {
				d.shared = map[string]string{}
			}
			d.shared[hostDir] = remoteWS
			d.mu.Unlock()
		}
		if err := d.createWorkspace(ctx, remoteWS); err != nil {
			if onLine != nil {
				onLine(streamStderr, "远程工作区初始化失败：共享父目录不可用或不安全，或专用工作区已存在")
			}
			return -1, err
		}
		if err := exec(ctx, "sh", "-c", `printf '%s' "$1" > "$0"`, path.Join(remoteWS, ".pipewright-owner"), d.resourceOwner(remoteWS)); err != nil {
			return -1, err
		}
		if err := uploadWorkspace(ctx, d.target, d.serverID, hostDir, remoteTar); err != nil {
			return -1, err
		}
		for _, args := range [][]string{{"tar", "-xzf", remoteTar, "-C", remoteWS}, {"rm", "-f", "--", remoteTar}} {
			if err := exec(ctx, args...); err != nil {
				return -1, err
			}
		}
	}
	before, beforeOK := d.workspaceState(ctx, remoteWS)
	code, runErr = d.Driver.RunToolchain(ctx, image, remoteWS, workdir, env, cmd, res, onLine)
	if code < 0 && runErr != nil {
		if r, ok := ctx.Value(recoveryReporterContextKey{}).(*recoveryReporter); ok {
			r.external = "unknown"
		}
	}
	if code != 0 || runErr != nil {
		return code, runErr
	}
	if post {
		return code, nil
	} // Post has its own control receipt, never producer outputs.
	declarations, explicit, err := artifactDeclarations(job.Config)
	if err != nil {
		return code, err
	}
	paths := []string{pipewrightEnvFileName}
	if explicit {
		for _, v := range declarations {
			paths = append(paths, renderTemplate(v.Path, templateContext(job.Config)))
		}
	} else {
		paths = append(paths, splitCommands(renderTemplate(cfgString(job.Config, "artifactPath"), templateContext(job.Config)))...)
	}
	var selected []string
	for _, pattern := range paths {
		if pattern == "" || path.IsAbs(pattern) || strings.ContainsAny(pattern, "\\:\x00") || pattern == ".." || strings.HasPrefix(pattern, "../") || path.Clean(pattern) != pattern {
			return code, runretry.ErrInvalid
		}
		matches, e := d.target.Exec(ctx, d.serverID, []string{"find", remoteWS, "-path", path.Join(remoteWS, pattern), "-print"})
		if e != nil || matches == nil || matches.ExitCode != 0 || len(matches.Stdout) > 60<<10 {
			return code, runretry.ErrUnavailable
		}
		for _, line := range strings.Split(strings.TrimSpace(matches.Stdout), "\n") {
			if line == "" {
				continue
			}
			if !strings.HasPrefix(line, remoteWS+"/") {
				return code, runretry.ErrInvalid
			}
			relative := strings.TrimPrefix(line, remoteWS+"/")
			if _, err := safeRecoveryDestination(hostDir, relative); err != nil {
				return code, err
			}
			selected = append(selected, relative)
			if len(selected) > 512 {
				return code, runretry.ErrUnavailable
			}
		}
	}
	if len(selected) > 0 {
		stream, ok := d.target.(interface {
			ExecStream(context.Context, string, []string) (io.ReadCloser, error)
		})
		if !ok {
			return code, runretry.ErrUnavailable
		}
		args := append([]string{"tar", "-czf", "-", "-C", remoteWS, "--"}, selected...)
		rc, e := stream.ExecStream(ctx, d.serverID, args)
		if e != nil {
			return code, e
		}
		e = extractRecoveryArchive(ctx, rc, hostDir, "")
		closeErr := rc.Close()
		if e != nil {
			return code, e
		}
		if closeErr != nil {
			return code, closeErr
		}
	}
	after, afterOK := d.workspaceState(ctx, remoteWS)
	if r, ok := ctx.Value(recoveryReporterContextKey{}).(*recoveryReporter); ok {
		r.workspaceIncomplete = !beforeOK || !afterOK || filteredRemoteState(before, selected) != filteredRemoteState(after, selected)
	}
	return code, nil
}
func (d *recoveryRemoteDriver) CleanupRecovery(ctx context.Context) error {
	d.mu.Lock()
	var paths []string
	unknown := false
	for ws, r := range d.resources {
		if r.drained {
			continue
		}
		if r.group && !r.attempted {
			paths = append(paths, ws)
		} else {
			unknown = true
		}
	}
	d.mu.Unlock()
	for _, ws := range paths {
		if err := d.cleanupWorkspace(ctx, ws); err != nil {
			return err
		}
	}
	if unknown {
		return runretry.ErrUnknown
	}
	return nil
}
func (d *recoveryRemoteDriver) workspaceState(ctx context.Context, ws string) (string, bool) {
	// No remote workspace bytes are snapshotted. Only bounded metadata is used
	// to detect undeclared state; unverifiable state requests an explicit rebuild.
	out, err := d.target.Exec(ctx, d.serverID, []string{"find", ws, "-name", ".git", "-prune", "-o", "-type", "f", "-printf", "%P\t%s\t%T@\n"})
	if err != nil || out == nil || out.ExitCode != 0 || len(out.Stdout) > 60<<10 {
		return "", false
	}
	return out.Stdout, true
}
func filteredRemoteState(state string, covered []string) string {
	var lines []string
	for _, line := range strings.Split(state, "\n") {
		name, _, ok := strings.Cut(line, "\t")
		if !ok {
			continue
		}
		skip := name == pipewrightEnvFileName
		for _, p := range covered {
			skip = skip || name == p || strings.HasPrefix(name, p+"/")
		}
		if !skip {
			lines = append(lines, line)
		}
	}
	sort.Strings(lines)
	h := sha256.Sum256([]byte(strings.Join(lines, "\n")))
	return hex.EncodeToString(h[:])
}

// Preserve optional capabilities of the frozen remote driver. Missing support
// remains an explicit failure rather than silently omitting declared services.
func (d *recoveryRemoteDriver) CreateNetwork(ctx context.Context, name string, line func(string, string)) (int, error) {
	if v, ok := d.Driver.(ServiceRunner); ok {
		return v.CreateNetwork(ctx, name, line)
	}
	return -1, runretry.ErrUnavailable
}
func (d *recoveryRemoteDriver) RemoveNetwork(ctx context.Context, name string, line func(string, string)) (int, error) {
	if v, ok := d.Driver.(ServiceRunner); ok {
		return v.RemoveNetwork(ctx, name, line)
	}
	return -1, runretry.ErrUnavailable
}
func (d *recoveryRemoteDriver) RunService(ctx context.Context, name, alias, image, network string, env, ports []string, line func(string, string)) (int, error) {
	if v, ok := d.Driver.(ServiceRunner); ok {
		return v.RunService(ctx, name, alias, image, network, env, ports, line)
	}
	return -1, runretry.ErrUnavailable
}
func (d *recoveryRemoteDriver) StopService(ctx context.Context, name string, line func(string, string)) (int, error) {
	if v, ok := d.Driver.(ServiceRunner); ok {
		return v.StopService(ctx, name, line)
	}
	return -1, runretry.ErrUnavailable
}
func (d *recoveryRemoteDriver) ReadyService(ctx context.Context, name string) error {
	if v, ok := d.Driver.(interface {
		ReadyService(context.Context, string) error
	}); ok {
		return v.ReadyService(ctx, name)
	}
	return runretry.ErrUnavailable
}
