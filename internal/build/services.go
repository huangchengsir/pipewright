package build

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/huangchengsir/pipewright/internal/dagrun"
	"github.com/huangchengsir/pipewright/internal/pipeline"
	"github.com/huangchengsir/pipewright/internal/run"
)

// services.go 实现阶段「旁挂服务」(P1 · 对标 GitLab services / Woodpecker services)。
//
// 阶段声明 services(DB/redis 等)时:建一个临时 docker 网络 → 各服务容器 detached 起在该网络上
// (network-alias=服务名)→ 脚本容器加入同网络(经 Resource.Network)→ 脚本里按服务名互访
// (如 `psql -h testdb`)→ 阶段结束(成败/取消)拆除服务容器 + 网络。
//
// 「建网/起服务/停服务/删网」是**可选** Driver 能力(ServiceRunner):shellDriver 实现;若运行时
// 驱动不具备(如纯 fake),则声明了 services 的阶段直接判失败并明确报错(绝不静默跳过 → 否则测试
// 在缺依赖下假跑出误导结果)。

// ServiceRunner 是旁挂服务所需的可选容器能力。b.driver 类型断言到它;不具备则 services 阶段失败。
type ServiceRunner interface {
	CreateNetwork(ctx context.Context, network string, onLine func(stream, line string)) (int, error)
	RemoveNetwork(ctx context.Context, network string, onLine func(stream, line string)) (int, error)
	// RunService creates a service container, confirms its CID, then starts it.
	// Network alias, environment and fixed host ports retain their declared values.
	RunService(ctx context.Context, containerName, alias, image, network string, env, ports []string, onLine func(stream, line string)) (int, error)
	// StopService removes only the confirmed CID owned by this driver.返回退出码。
	StopService(ctx context.Context, containerName string, onLine func(stream, line string)) (int, error)
}

// ─── shellDriver 实现 ServiceRunner ──────────────────────────────────────────

const serviceOwnerLabel = "io.pipewright.service.owner"

var serviceObjectID = regexp.MustCompile("^[a-f0-9]{64}$")

func serviceOutput(onLine func(string, string), stdout, stderr string) {
	if onLine == nil {
		return
	}
	for _, line := range strings.Split(strings.TrimSpace(stdout), "\n") {
		if line != "" {
			onLine("stdout", line)
		}
	}
	for _, line := range strings.Split(strings.TrimSpace(stderr), "\n") {
		if line != "" {
			onLine("stderr", line)
		}
	}
}
func (d *shellDriver) CreateNetwork(ctx context.Context, network string, onLine func(string, string)) (int, error) {
	owner := rand.Text()
	args := []string{"network", "create", "--label", serviceOwnerLabel + "=" + owner, network}
	emitCmd(onLine, d.bin, args)
	out, stderr, code, err := d.cmdr.Run(ctx, d.bin, args, "")
	serviceOutput(onLine, out, stderr)
	id := strings.TrimSpace(out)
	if err == nil && code == 0 && ctx.Err() == nil && serviceObjectID.MatchString(id) {
		d.serviceNetworks.Store(network, id)
		return 0, nil
	}
	// A named successful Podman receipt is supported, but a lost/error receipt
	// never causes a second create or a subsequent service start.
	named := err == nil && code == 0 && ctx.Err() == nil && id == network
	return d.reconcileServiceCreate(ctx, "network", network, owner, named)
}
func (d *shellDriver) RemoveNetwork(ctx context.Context, network string, onLine func(string, string)) (int, error) {
	value, ok := d.serviceNetworks.Load(network)
	if !ok {
		return -1, errors.New("build: service network ownership unavailable")
	}
	id := value.(string)
	args := []string{"network", "rm", id}
	emitCmd(onLine, d.bin, args)
	code, err := d.cmdr.Stream(ctx, d.bin, args, "", onLine)
	if err == nil && code == 0 {
		d.serviceNetworks.CompareAndDelete(network, id)
	}
	return code, err
}
func (d *shellDriver) RunService(ctx context.Context, containerName, alias, image, network string, env, ports []string, onLine func(string, string)) (int, error) {
	net, ok := d.serviceNetworks.Load(network)
	if !ok {
		return -1, errors.New("build: service network ownership unavailable")
	}
	owner := rand.Text()
	args := []string{"create", "--name", containerName, "--network", net.(string), "--network-alias", alias, "--label", serviceOwnerLabel + "=" + owner}
	display := append([]string(nil), args...)
	for _, kv := range env {
		args = append(args, "-e", kv)
		display = append(display, "-e", maskKV(kv))
	}
	for _, port := range ports {
		args = append(args, "-p", port)
		display = append(display, "-p", port)
	}
	args = append(args, image)
	display = append(display, image)
	emitCmd(onLine, d.bin, display)
	out, stderr, code, err := d.cmdr.Run(ctx, d.bin, args, "")
	serviceOutput(onLine, out, stderr)
	id := strings.TrimSpace(out)
	if err != nil || code != 0 || ctx.Err() != nil || !serviceObjectID.MatchString(id) {
		return d.reconcileServiceCreate(ctx, "container", containerName, owner, false)
	}
	d.serviceContainers.Store(containerName, id)
	start := []string{"start", id}
	emitCmd(onLine, d.bin, start)
	code, err = d.cmdr.Stream(ctx, d.bin, start, "", onLine)
	if err != nil || code != 0 {
		// A failed start (for example an occupied fixed host port) still leaves a
		// created container. Remove only the exact CID returned by this create.
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		defer cancel()
		if c, e := d.StopService(cleanup, containerName, nil); e != nil || c != 0 {
			if onLine != nil {
				onLine("stderr", "service cleanup incomplete for newly created container "+id)
			}
		}
	}
	return code, err
}
func (d *shellDriver) StopService(ctx context.Context, containerName string, onLine func(string, string)) (int, error) {
	value, ok := d.serviceContainers.Load(containerName)
	if !ok {
		return -1, errors.New("build: service container ownership unavailable")
	}
	id := value.(string)
	args := []string{"rm", "-f", id}
	emitCmd(onLine, d.bin, args)
	code, err := d.cmdr.Stream(ctx, d.bin, args, "", onLine)
	if err == nil && code == 0 {
		d.serviceContainers.CompareAndDelete(containerName, id)
	}
	return code, err
}

// ─── 执行器集成 ──────────────────────────────────────────────────────────────

// stageNetworkName 为阶段旁挂服务造一个唯一 docker 网络名(run 短 id + 阶段 id,sanitize)。
func stageNetworkName(runID, stageID string) string {
	short := runID
	if len(short) > 8 {
		short = short[:8]
	}
	readable := sanitizeDockerName(short + "-" + stageID)
	if len(readable) > 48 {
		readable = readable[:48]
	}
	sum := sha256.Sum256([]byte(runID + "\x00" + stageID))
	return "pw-svc-" + readable + "-" + hex.EncodeToString(sum[:8])
}

// sanitizeDockerName 把任意串净化为 docker 名安全(仅字母数字下划线连字符;空 → x)。
func sanitizeDockerName(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_', r == '-':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	out := b.String()
	if out == "" {
		return "x"
	}
	return out
}

// startStageServices 起阶段旁挂服务,返回 (网络名, ok)。ok=false 表示失败(调用方应判阶段失败)。
// 驱动不具备 ServiceRunner → 明确报错失败(不静默跳过)。任一服务起失败 → 拆除已起的 + 失败。
func (b *Builder) startStageServices(ctx context.Context, r *run.Run, stage pipeline.Stage, rep dagrun.StageReporter) (string, bool) {
	runner, ok := b.driver.(ServiceRunner)
	if !ok {
		_ = rep.Log(ctx, streamStderr, "阶段声明了旁挂服务(services),但当前构建驱动不支持容器网络能力,无法运行")
		return "", false
	}
	network := stageNetworkName(r.ID, stage.ID)
	if code, err := runner.CreateNetwork(ctx, network, mkLineLogger(ctx, rep)); err != nil || code != 0 {
		_ = rep.Log(ctx, streamStderr, fmt.Sprintf("创建服务网络失败(code=%d):%v", code, err))
		return "", false
	}
	started := make([]string, 0, len(stage.Services))
	for _, sv := range stage.Services {
		cname := network + "-" + sv.Name
		_ = rep.Log(ctx, streamStdout, fmt.Sprintf("→ 起旁挂服务「%s」(%s)…", sv.Name, sv.Image))
		if code, err := runner.RunService(ctx, cname, sv.Name, sv.Image, network, sv.Env, sv.Ports, mkLineLogger(ctx, rep)); err != nil || code != 0 {
			_ = rep.Log(ctx, streamStderr, fmt.Sprintf("旁挂服务「%s」启动失败(code=%d):%v", sv.Name, code, err))
			// 拆除已起的服务 + 网络,避免泄漏。
			for _, c := range started {
				if cleanupCode, cleanupErr := runner.StopService(context.WithoutCancel(ctx), c, nil); cleanupErr != nil || cleanupCode != 0 {
					_ = rep.Log(context.WithoutCancel(ctx), streamStderr, fmt.Sprintf("清理已启动旁挂服务失败(best-effort, code=%d):%v", cleanupCode, cleanupErr))
				}
			}
			if cleanupCode, cleanupErr := runner.RemoveNetwork(context.WithoutCancel(ctx), network, nil); cleanupErr != nil || cleanupCode != 0 {
				_ = rep.Log(context.WithoutCancel(ctx), streamStderr, fmt.Sprintf("清理服务网络失败(best-effort, code=%d):%v", cleanupCode, cleanupErr))
			}
			return "", false
		}
		started = append(started, cname)
	}
	return network, true
}

// stopStageServices 拆除阶段旁挂服务容器 + 网络(best-effort,失败只记日志)。
func (b *Builder) stopStageServices(ctx context.Context, r *run.Run, stage pipeline.Stage, network string, rep dagrun.StageReporter) {
	runner, ok := b.driver.(ServiceRunner)
	if !ok || network == "" {
		return
	}
	for _, sv := range stage.Services {
		cname := network + "-" + sv.Name
		if code, err := runner.StopService(ctx, cname, nil); err != nil || code != 0 {
			_ = rep.Log(ctx, streamStderr, fmt.Sprintf("清理旁挂服务「%s」失败(best-effort, code=%d):%v", sv.Name, code, err))
		}
	}
	if code, err := runner.RemoveNetwork(ctx, network, nil); err != nil || code != 0 {
		_ = rep.Log(ctx, streamStderr, fmt.Sprintf("清理服务网络失败(best-effort, code=%d):%v", code, err))
	}
}

// mkLineLogger 把 StageReporter 适配成 onLine 回调(服务起停日志流)。
func mkLineLogger(ctx context.Context, rep dagrun.StageReporter) func(stream, line string) {
	return func(stream, line string) { _ = rep.Log(ctx, stream, line) }
}
