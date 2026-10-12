package build

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/huangchengsir/pipewright/internal/pipeline"
)

const copiedWorkspaceLabel = "io.pipewright.workspace-copy=v1"

var dockerContainerID = regexp.MustCompile(`^[a-f0-9]{12,64}$`)

// Reconcile only old, stopped containers from this lifecycle, never running jobs.
func (d *shellDriver) reconcileCopiedWorkspaces(onLine func(string, string)) {
	d.workspaceReconcileMu.Lock()
	if time.Now().Before(d.workspaceReconcileAfter) {
		d.workspaceReconcileMu.Unlock()
		return
	}
	d.workspaceReconcileAfter = time.Now().Add(15 * time.Minute)
	d.workspaceReconcileMu.Unlock()
	func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		out, _, code, err := d.cmdr.Run(ctx, d.bin, []string{"ps", "-aq", "--filter", "label=" + copiedWorkspaceLabel, "--filter", "status=created", "--filter", "status=exited"}, "")
		if err != nil || code != 0 {
			return
		}
		for i, id := range strings.Fields(out) {
			if i >= 64 || ctx.Err() != nil {
				break
			}
			if !dockerContainerID.MatchString(id) {
				continue
			}
			out, _, code, err = d.cmdr.Run(ctx, d.bin, []string{"inspect", "--format", `{"Status":{{json .State.Status}},"FinishedAt":{{json .State.FinishedAt}},"Created":{{json .Created}}}`, id}, "")
			var state struct{ Status, FinishedAt, Created string }
			if err != nil || code != 0 || json.Unmarshal([]byte(out), &state) != nil || (state.Status != "exited" && state.Status != "created") {
				continue
			}
			ageFrom := state.FinishedAt
			if state.Status == "created" {
				ageFrom = state.Created
			}
			finished, err := time.Parse(time.RFC3339Nano, ageFrom)
			if err != nil || finished.IsZero() || time.Since(finished) < 24*time.Hour {
				continue
			}
			_, _, code, err = d.cmdr.Run(ctx, d.bin, []string{"rm", "-v", id}, "")
			if (err != nil || code != 0) && onLine != nil {
				onLine("stderr", "build: stale workspace cleanup failed")
			}
		}
	}()
}

func toolchainWorkspaceMount(workdir string) string {
	for _, root := range []string{scriptWorkspaceMount, "/src"} {
		if workdir == root || strings.HasPrefix(workdir, root+"/") {
			return root
		}
	}
	return workdir
}

// Docker cp crosses the client/daemon filesystem boundary without host bind paths.
func (d *shellDriver) runCopiedWorkspace(ctx context.Context, image, hostDir, workdir string, env, cmd []string, res pipeline.Resource, onLine func(string, string)) (resultCode int, resultErr error) {
	d.reconcileCopiedWorkspaces(onLine)
	if err := ctx.Err(); err != nil {
		return -1, err
	}
	name := "pipewright-build-" + uuid.NewString()
	mount := toolchainWorkspaceMount(workdir)
	args := []string{"create", "--name", name, "--label", copiedWorkspaceLabel, "--mount", "type=volume,destination=" + mount + ",volume-nocopy", "-w", workdir}
	display := append([]string(nil), args...)
	for _, flag := range []struct{ key, value string }{{"--cpus", res.CPU}, {"--memory", res.Memory}, {"--network", res.Network}} {
		if value := strings.TrimSpace(flag.value); value != "" {
			args = append(args, flag.key, value)
			display = append(display, flag.key, value)
		}
	}
	for _, value := range env {
		args = append(args, "-e", value)
		display = append(display, "-e", maskKV(value))
	}
	args = append(args, image)
	args = append(args, cmd...)
	display = append(display, image)
	display = append(display, cmd...)
	// Removal also runs after cancellation; only this random per-job name is targeted.
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		for attempt := 0; attempt < 2; attempt++ {
			_, _, code, err := d.cmdr.Run(cleanup, d.bin, []string{"rm", "-f", "-v", name}, "")
			if err == nil && code == 0 {
				return
			}
			if cleanup.Err() != nil {
				break
			}
		}
		if onLine != nil {
			onLine("stderr", "build: toolchain cleanup failed; temporary container may remain")
		}
		if resultErr == nil {
			resultCode, resultErr = -1, fmt.Errorf("build: toolchain cleanup failed")
		}
	}()
	emitCmd(onLine, d.bin, display)
	if code, err := d.cmdr.Stream(ctx, d.bin, args, "", onLine); err != nil || code != 0 {
		if ctx.Err() != nil {
			return -1, ctx.Err()
		}
		return code, err
	}
	copyArgs := []string{"cp", "-a", filepath.Clean(hostDir) + string(filepath.Separator) + ".", name + ":" + mount}
	emitCmd(onLine, d.bin, copyArgs)
	if code, err := d.cmdr.Stream(ctx, d.bin, copyArgs, "", onLine); err != nil || code != 0 {
		if ctx.Err() != nil {
			return -1, ctx.Err()
		}
		return code, err
	}
	emitCmd(onLine, d.bin, []string{"start", "-a", name})
	startCode, startErr := d.cmdr.Stream(ctx, d.bin, []string{"start", "-a", name}, "", onLine)
	if err := ctx.Err(); err != nil {
		return -1, err
	}
	stateJSON, _, inspectCode, inspectErr := d.cmdr.Run(ctx, d.bin, []string{"inspect", "--format", "{{json .State}}", name}, "")
	if err := ctx.Err(); err != nil {
		return -1, err
	}
	var state struct {
		Status   string
		ExitCode int
	}
	if inspectErr != nil || inspectCode != 0 || json.Unmarshal([]byte(stateJSON), &state) != nil || state.Status != "exited" {
		return -1, fmt.Errorf("build: unable to verify toolchain exit state")
	}
	if startErr != nil && state.ExitCode == 0 {
		return startCode, startErr
	}
	staging, err := os.MkdirTemp(filepath.Dir(hostDir), "pipewright-result-*")
	if err != nil {
		return -1, fmt.Errorf("build: unable to create result workspace")
	}
	defer os.RemoveAll(staging)
	copyArgs = []string{"cp", "-a", name + ":" + mount + "/.", staging}
	emitCmd(onLine, d.bin, copyArgs)
	if code, err := d.cmdr.Stream(ctx, d.bin, copyArgs, "", onLine); err != nil || code != 0 {
		if ctx.Err() != nil {
			return -1, ctx.Err()
		}
		return code, err
	}
	if err := ctx.Err(); err != nil {
		return -1, err
	}
	if err := replaceWorkspace(hostDir, staging); err != nil {
		return -1, fmt.Errorf("build: unable to restore toolchain workspace")
	}
	return state.ExitCode, nil
}

func replaceWorkspace(destination, source string) error {
	info, err := os.Lstat(destination)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || filepath.Clean(destination) == filepath.VolumeName(destination)+string(filepath.Separator) {
		return fmt.Errorf("invalid workspace destination")
	}
	backup, err := os.MkdirTemp(filepath.Dir(destination), "pipewright-previous-*")
	if err != nil {
		return err
	}
	if err := os.Remove(backup); err != nil {
		return err
	}
	if err := os.Rename(destination, backup); err != nil {
		return err
	}
	if err := os.Rename(source, destination); err != nil {
		if restoreErr := os.Rename(backup, destination); restoreErr != nil {
			return fmt.Errorf("workspace rollback failed")
		}
		return err
	}
	return os.RemoveAll(backup)
}
