package deploy

// Local Docker deployment is an explicit alternative to restartCommand. The
// platform owns build and activation, so an immutable image identity can be
// recorded between those operations. Arbitrary historic scripts are never
// parsed, replayed, or treated as deployment receipts.

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"path"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/huangchengsir/pipewright/internal/deployattempt"
	"github.com/huangchengsir/pipewright/internal/run"
	"github.com/huangchengsir/pipewright/internal/target"
)

var ErrInvalidLocalDockerConfig = errors.New("deploy: invalid local Docker configuration")

var localContainerName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,127}$`)
var localBuildArgument = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*=`)
var localPlatformPart = regexp.MustCompile(`^[a-z0-9][a-z0-9_.-]{0,31}$`)

type localDockerConfig struct {
	name, context, dockerfile, prepare string
	buildArgs, runArgs                 []string
}

type localDockerImageIdentity struct {
	ID           string `json:"id"`
	OS           string `json:"os"`
	Architecture string `json:"architecture"`
	Variant      string `json:"variant"`
}

func validLocalImageIdentity(v localDockerImageIdentity) bool {
	return deploymentImageDigest.MatchString(v.ID) && localPlatformPart.MatchString(v.OS) && localPlatformPart.MatchString(v.Architecture) && (v.Variant == "" || localPlatformPart.MatchString(v.Variant))
}

// LocalDockerEnabled reports the explicit opt-in only; misspellings must be
// rejected by ValidateLocalDockerConfig, rather than silently enabling scripts.
func LocalDockerEnabled(cfg map[string]string) bool { return cfg["localDocker"] == "true" }

// ValidateLocalDockerConfig is pure and may be used before an artifact exists
// by passing its declared type. Runtime additionally requires real stored bytes.
func ValidateLocalDockerConfig(a run.Artifact, cfg map[string]string, strategy string) error {
	_, err := parseLocalDocker(a, cfg, strategy)
	return err
}

func parseLocalDocker(a run.Artifact, cfg map[string]string, strategy string) (localDockerConfig, error) {
	c := localDockerConfig{}
	if cfg["localDocker"] == "" || cfg["localDocker"] == "false" {
		return c, nil
	}
	bad := ErrInvalidLocalDockerConfig
	if !LocalDockerEnabled(cfg) || (a.Type != run.ArtifactArchive && a.Type != run.ArtifactDist) || NormalizeStrategy(strategy) == StrategyBlueGreen || NormalizeStrategy(cfg["strategy"]) == StrategyBlueGreen || strings.TrimSpace(cfg["restartCommand"]) != "" {
		return c, bad
	}
	c.name = cfg["containerName"]
	if !localContainerName.MatchString(c.name) {
		return c, bad
	}
	c.context, c.dockerfile = cfg["localDockerBuildContext"], cfg["localDockerDockerfile"]
	if c.context == "" {
		c.context = "."
	}
	if c.dockerfile == "" {
		c.dockerfile = "Dockerfile"
	}
	for _, value := range []string{c.context, c.dockerfile} {
		if len(value) > 512 || value != strings.TrimSpace(value) || path.IsAbs(value) || strings.ContainsAny(value, "\\:\x00\r\n") || strings.HasPrefix(value, "-") || path.Clean(value) != value || value == ".." || strings.HasPrefix(value, "../") {
			return c, bad
		}
	}
	c.prepare = cfg["localDockerPrepareCommand"]
	if len(c.prepare) > 30<<10 || strings.ContainsRune(c.prepare, 0) {
		return c, bad
	}
	var err error
	c.buildArgs, err = localDockerJSONArgs(cfg["localDockerBuildArgs"])
	if err != nil || len(c.buildArgs) > 122 {
		return c, bad
	}
	for _, arg := range c.buildArgs {
		if !localBuildArgument.MatchString(arg) {
			return c, bad
		}
	}
	if cfg["localDockerRunArgs"] != "" && strings.TrimSpace(cfg["runArgs"]) != "" {
		return c, bad
	}
	c.runArgs, err = localDockerJSONArgs(cfg["localDockerRunArgs"])
	if err != nil {
		return c, bad
	}
	if cfg["localDockerRunArgs"] == "" {
		c.runArgs = strings.Fields(cfg["runArgs"])
	}
	if !validLocalDockerRunArgs(c.runArgs) {
		return c, bad
	}
	ports := splitImageList(cfg["ports"])
	if len(ports) > 32 {
		return c, bad
	}
	for _, port := range ports {
		if len(port) > 128 || strings.HasPrefix(port, "-") || !strings.Contains(port, ":") || strings.ContainsAny(port, "\x00\r\n") {
			return c, bad
		}
		c.runArgs = append(c.runArgs, "-p", port)
	}
	return c, nil
}

func localDockerJSONArgs(raw string) ([]string, error) {
	if raw == "" {
		return nil, nil
	}
	if len(raw) > 32<<10 {
		return nil, ErrInvalidLocalDockerConfig
	}
	var args []string
	if json.Unmarshal([]byte(raw), &args) != nil || args == nil || len(args) > 128 {
		return nil, ErrInvalidLocalDockerConfig
	}
	for _, arg := range args {
		if arg == "" || len(arg) > 4096 || strings.ContainsAny(arg, "\x00\r\n") {
			return nil, ErrInvalidLocalDockerConfig
		}
	}
	return args, nil
}

// Every token belongs to a known option. Positional images/commands, detach,
// name/cidfile/pull/platform overrides and automatic removal are forbidden.
func validLocalDockerRunArgs(args []string) bool {
	if len(args) > 128 {
		return false
	}
	values := map[string]bool{
		"-e": true, "--env": true, "--env-file": true, "--restart": true,
		"-p": true, "--publish": true, "--network": true, "--add-host": true,
		"--hostname": true, "-u": true, "--user": true, "-w": true, "--workdir": true,
		"-v": true, "--volume": true, "--mount": true, "-m": true, "--memory": true,
		"--cpus": true, "--cpu-shares": true, "--memory-swap": true, "--entrypoint": true,
		"--health-cmd": true, "--health-interval": true, "--health-timeout": true,
		"--health-start-period": true, "--health-retries": true, "--stop-timeout": true,
		"--cap-add": true, "--cap-drop": true, "--security-opt": true, "--ulimit": true,
		"--dns": true, "--dns-search": true, "--log-driver": true, "--log-opt": true,
		"--tmpfs": true, "--shm-size": true,
	}
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if len(arg) > 4096 || strings.ContainsAny(arg, "\x00\r\n") {
			return false
		}
		if arg == "--init" || arg == "--read-only" {
			continue
		}
		key, value, equal := strings.Cut(arg, "=")
		if !values[key] {
			return false
		}
		if !equal {
			i++
			if i >= len(args) {
				return false
			}
			value = args[i]
		}
		if value == "" || len(value) > 4096 || strings.ContainsAny(value, "\x00\r\n") {
			return false
		}
	}
	return true
}

func (s *service) validateLocalDockerRuntime(a run.Artifact, cfg map[string]string, strategy string) error {
	if err := ValidateLocalDockerConfig(a, cfg, strategy); err != nil {
		return err
	}
	if LocalDockerEnabled(cfg) && (!isStoredArtifact(a) || (artifactFormat(a) != "tar.gz" && artifactFormat(a) != "file") || s.artStore == nil || s.attempts == nil) {
		return ErrInvalidLocalDockerConfig
	}
	if LocalDockerEnabled(cfg) {
		if _, ok := s.targets.(localDockerPinnedExecutor); !ok {
			return ErrInvalidLocalDockerConfig
		}
	}
	return nil
}

const localDockerCWD = `cd "$1" && shift && exec "$@"`
const localDockerImageProjection = `{"id":{{json .Id}},"os":{{json .Os}},"architecture":{{json .Architecture}},"variant":{{with index . "Variant"}}{{json .}}{{else}}""{{end}}}`
const localDockerContainerProjection = `{"containerId":{{json .Id}},"configId":{{json .Image}},"createdAt":{{json .Created}},"startedAt":{{json .State.StartedAt}}}`

type localDockerPinnedExecutor interface {
	target.PinnedExecutor
	Capture(context.Context, string) (target.ConnectionSnapshot, error)
}

// Only this target's private service copy uses the snapshot. Other concurrent
// fanout targets retain their own service and cannot switch this connection.
type localDockerPinnedTarget struct {
	target.Service
	executor localDockerPinnedExecutor
	snapshot target.ConnectionSnapshot
}

func (p *localDockerPinnedTarget) Capture(_ context.Context, id string) (target.ConnectionSnapshot, error) {
	if id != p.snapshot.Server.ID {
		return target.ConnectionSnapshot{}, target.ErrTargetChanged
	}
	return p.snapshot, nil
}

func (p *localDockerPinnedTarget) Exec(ctx context.Context, id string, cmd []string) (*target.ExecResult, error) {
	if id != p.snapshot.Server.ID {
		return nil, target.ErrTargetChanged
	}
	out, err := p.executor.ExecPinned(ctx, p.snapshot, cmd, target.ExecutionLimits{Bytes: 64 << 10, Lines: 1000})
	if err != nil || out == nil {
		if err != nil {
			return nil, err
		}
		return nil, target.OutcomeUnknown(target.ErrLimitedUnavailable)
	}
	if len(cmd) >= 4 && cmd[0] == "sh" && cmd[1] == "-c" && cmd[3] == "pw-local-docker" {
		// Build/preparation output is untrusted and can contain credentials. It
		// is drained within a fixed memory budget, then deliberately discarded.
		return &target.ExecResult{ExitCode: out.ExitCode}, nil
	}
	if out.Truncated {
		return nil, errors.New("deploy: pinned local Docker response truncated")
	}
	return &target.ExecResult{Stdout: out.Stdout, Stderr: out.Stderr, ExitCode: out.ExitCode}, nil
}

func (p *localDockerPinnedTarget) Upload(ctx context.Context, id string, content io.Reader, remotePath string) error {
	if id != p.snapshot.Server.ID {
		return target.ErrTargetChanged
	}
	return p.executor.UploadPinned(ctx, p.snapshot, content, remotePath)
}

// Script text, argv and raw build output can contain secrets. Unlike the legacy
// restart path this mode logs only fixed operation names and exit status.
func (s *service) execLocalDocker(ctx context.Context, serverID, label string, cmd []string) (*target.ExecResult, error) {
	ctx, cancel := context.WithTimeout(ctx, policyFrom(ctx).command)
	defer cancel()
	cmdLogFrom(ctx)(cmdStreamStdout, "[local-docker] "+label)
	out, err := s.targets.Exec(ctx, serverID, cmd)
	if err != nil || out == nil || out.ExitCode != 0 {
		if err != nil {
			return out, err
		}
		return out, errors.New("deploy: local Docker operation failed")
	}
	return out, nil
}

func decodeLocalProjection(text string, value any) bool {
	if len(text) > 2048 {
		return false
	}
	if strings.HasPrefix(strings.TrimSpace(text), "{") {
		keys := json.NewDecoder(strings.NewReader(text))
		if _, err := keys.Token(); err != nil {
			return false
		}
		seen := map[string]bool{}
		for keys.More() {
			tok, err := keys.Token()
			key, ok := tok.(string)
			if err != nil || !ok || seen[key] || keys.Decode(new(json.RawMessage)) != nil {
				return false
			}
			seen[key] = true
		}
	}
	d := json.NewDecoder(strings.NewReader(text))
	d.DisallowUnknownFields()
	return d.Decode(value) == nil && d.Decode(new(any)) == io.EOF
}

func (s *service) deployLocalDockerOne(ctx context.Context, srv *target.Server, a run.Artifact, cfg map[string]string, hc *HealthCheck) TargetResult {
	started := time.Now().UTC()
	res := TargetResult{ServerID: srv.ID, ServerName: srv.Name, StartedAt: started}
	c, err := parseLocalDocker(a, cfg, "")
	if err != nil || s.validateLocalDockerRuntime(a, cfg, "") != nil {
		return finishFailed(res, "本地 Docker 部署配置无效，未执行远端部署")
	}
	executor := s.targets.(localDockerPinnedExecutor)
	snapshot, err := executor.Capture(ctx, srv.ID)
	if err != nil {
		return finishFailed(res, "目标连接身份不可用，未执行远端部署")
	}
	local := *s
	local.targets = &localDockerPinnedTarget{Service: s.targets, executor: executor, snapshot: snapshot}
	s = &local
	st := imageState{name: c.name}
	defer func() {
		if rec := recover(); rec != nil {
			s.imageEvent(ctx, st, deployattempt.OutcomeUnknown, "not_run", "local_docker_interrupted")
			panic(rec)
		}
	}()
	release, _, ok := s.stageReleaseOne(ctx, srv, a, cfg)
	if !ok {
		return finishFailed(res, "本地 Docker 制品准备失败，未切换目标容器")
	}
	if strings.TrimSpace(c.prepare) != "" {
		// Existing deploy-script authorization applies. The documented contract
		// prohibits activating/replacing the workload from this preparation step.
		script := "set -e\ncd \"$1\"\n" + c.prepare
		if _, err := s.execLocalDocker(ctx, srv.ID, "prepare", []string{"sh", "-c", script, "pw-local-docker", release.release}); err != nil {
			return finishFailed(res, "本地 Docker 构建准备失败；脚本结果可能不完整，未重放脚本或执行平台容器切换")
		}
	}
	identityDir := path.Join(release.releasesDir, ".pw-build-"+uuid.NewString())
	if _, err := s.execLocalDocker(ctx, srv.ID, "prepare image identity", []string{"mkdir", "-m", "700", identityDir}); err != nil {
		return finishFailed(res, "本地 Docker 镜像身份目录创建失败，未切换目标容器")
	}
	iidfile := path.Join(identityDir, "image.id")
	defer func() {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		_, _ = s.execLocalDocker(cleanup, srv.ID, "remove image identity file", []string{"rm", "-f", iidfile})
		_, _ = s.execLocalDocker(cleanup, srv.ID, "remove image identity directory", []string{"rmdir", identityDir})
	}()
	// Docker resolves -f from the command's working directory (the release
	// root), whereas the configured Dockerfile is relative to the build context.
	build := []string{"sh", "-c", localDockerCWD, "pw-local-docker", release.release, "docker", "build", "--iidfile", iidfile, "-f", path.Join(c.context, c.dockerfile)}
	for _, arg := range c.buildArgs {
		build = append(build, "--build-arg", arg)
	}
	build = append(build, c.context)
	if _, err := s.execLocalDocker(ctx, srv.ID, "build image", build); err != nil {
		return finishFailed(res, "本地 Docker 镜像构建失败，未切换目标容器")
	}
	iid, err := s.execLocalDocker(ctx, srv.ID, "read build identity", []string{"cat", iidfile})
	if err != nil || !deploymentImageDigest.MatchString(strings.TrimSpace(iid.Stdout)) {
		return finishFailed(res, "本地 Docker 构建身份缺失或无效，未切换目标容器")
	}
	// iidfile is a build result reference, not presumed to be a config digest.
	// Independently inspect the loaded image and use the daemon's .Id identity.
	image, err := s.execLocalDocker(ctx, srv.ID, "inspect built image", []string{"docker", "image", "inspect", "--format", localDockerImageProjection, strings.TrimSpace(iid.Stdout)})
	var identity localDockerImageIdentity
	if err != nil || !decodeLocalProjection(image.Stdout, &identity) || !validLocalImageIdentity(identity) {
		return finishFailed(res, "本地 Docker 镜像检查无有效 Config ID 或平台信息，未切换目标容器")
	}
	st.ref = identity.ID
	expected := deployattempt.ExpectedImage{Reference: identity.ID, Digest: identity.ID, DigestKind: deployattempt.DigestConfig, IdentitySource: deployattempt.IdentityScriptPreActivation, Platform: deployattempt.Platform{OS: identity.OS, Architecture: identity.Architecture, Variant: identity.Variant}}
	if err := s.startImageReceiptWithExpected(ctx, srv, a, &st, expected); err != nil {
		return finishFailed(res, "部署身份账本不可用，未切换目标容器")
	}
	// Resolve the old full ID before activation. Never delete a replacement that
	// appears under the same name after this lookup, and never infer a receipt
	// by inspecting the name after activation.
	old, err := s.localDockerPreviousContainer(ctx, srv.ID, c.name)
	if err != nil {
		s.imageEvent(ctx, st, deployattempt.DeployFailed, "not_run", "previous_container_unavailable")
		return finishFailed(res, "无法确定旧容器身份，未切换目标容器")
	}
	s.beforeImage(ctx, st)
	if !s.imageEvent(ctx, st, deployattempt.ActivationStarted, "not_run", "") {
		return finishFailed(res, "部署身份账本不可用，未切换目标容器")
	}
	fail := func(reason, message string) TargetResult {
		s.imageEvent(ctx, st, deployattempt.OutcomeUnknown, "not_run", reason)
		return finishFailed(res, message+"；未自动重试或回放脚本")
	}
	for _, cmd := range atomicSymlinkCmds(release.release, release.current) {
		if _, err := s.execLocalDocker(ctx, srv.ID, "activate release directory", cmd); err != nil {
			return fail("release_activation_unknown", "发布目录切换结果未确认")
		}
	}
	if old != "" {
		if _, err := s.execLocalDocker(ctx, srv.ID, "remove previous container", []string{"docker", "container", "rm", "-f", old}); err != nil {
			return fail("container_removal_unknown", "旧容器移除结果未确认")
		}
	}
	start := []string{"docker", "run", "-d", "--pull=never", "--name", c.name,
		// Activation ownership follows its durable current attempt. The artifact
		// keeps the original producer RunID/ID when a recovery reuses its bytes.
		"--label", "pipewright.run=" + st.receipt.target.Source.RunID, "--label", "pipewright.server=" + srv.ID,
		"--label", "pipewright.deployment-target=" + st.receipt.target.ID}
	start = append(start, c.runArgs...)
	start = append(start, st.ref)
	activated, err := s.execLocalDocker(ctx, srv.ID, "start pinned local image", start)
	if err != nil {
		return fail("container_activation_unknown", "容器启动结果未确认")
	}
	id := strings.TrimSpace(activated.Stdout)
	if !deployattempt.ValidContainerID(id) {
		return fail("container_id_unavailable", "容器启动未返回完整 Container ID")
	}
	st.receipt.actual.ContainerID = id
	actual, err := s.execLocalDocker(ctx, srv.ID, "inspect activated container", []string{"docker", "container", "inspect", "--format", localDockerContainerProjection, id})
	var observed struct {
		ContainerID string `json:"containerId"`
		ConfigID    string `json:"configId"`
		CreatedAt   string `json:"createdAt"`
		StartedAt   string `json:"startedAt"`
	}
	if err != nil || !decodeLocalProjection(actual.Stdout, &observed) || observed.ContainerID != id || !deploymentImageDigest.MatchString(observed.ConfigID) || len(observed.CreatedAt) > 64 || len(observed.StartedAt) > 64 {
		return fail("container_identity_unavailable", "已取得容器 ID，但未确认实际镜像身份")
	}
	st.receipt.actual.ConfigID = observed.ConfigID
	st.receipt.actual.ContainerCreatedAt, st.receipt.actual.ContainerStartedAt = observed.CreatedAt, observed.StartedAt
	if observed.ConfigID == expected.Digest {
		st.receipt.actual.Platform = expected.Platform
	} else {
		// Retain the actual mismatching identity and independently inspect its
		// platform. Never copy the expected platform onto a different image.
		actualImage, inspectErr := s.execLocalDocker(ctx, srv.ID, "inspect mismatching image", []string{"docker", "image", "inspect", "--format", localDockerImageProjection, observed.ConfigID})
		var actualIdentity localDockerImageIdentity
		if inspectErr == nil && decodeLocalProjection(actualImage.Stdout, &actualIdentity) && validLocalImageIdentity(actualIdentity) && actualIdentity.ID == observed.ConfigID {
			st.receipt.actual.Platform = deployattempt.Platform{OS: actualIdentity.OS, Architecture: actualIdentity.Architecture, Variant: actualIdentity.Variant}
		}
	}
	if !s.imageEvent(ctx, st, deployattempt.Activated, "not_run", "") {
		return finishFailed(res, "容器已启动但回执持久化失败；未自动重试或回放脚本")
	}
	s.afterImage(ctx, st)
	if observed.ConfigID != expected.Digest {
		s.imageEvent(ctx, st, deployattempt.DeployFailed, "not_run", "image_config_id_mismatch")
		return finishFailed(res, "容器实际镜像与构建后固定的 Config ID 不一致；未自动回滚或重放脚本")
	}
	gate := "not_run"
	if hc.enabled() {
		cmdLogFrom(ctx)(cmdStreamStdout, "[local-docker] health check")
		healthCtx := WithCmdLog(ctx, func(string, string) {})
		if err := s.runHealthCheck(healthCtx, srv.ID, hc); err != nil {
			s.imageEvent(ctx, st, deployattempt.DeployFailed, "failed", "local_docker_health_failed")
			return finishFailed(res, "本地 Docker 容器健康检查失败；未自动回滚或重放脚本")
		}
		gate = "passed"
	}
	if !s.imageEvent(ctx, st, deployattempt.Succeeded, gate, "") {
		return finishFailed(res, "容器已启动但完成回执未确认；未自动重试或回放脚本")
	}
	cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
	defer cancel()
	s.pruneReleases(cleanup, srv.ID, release.releasesDir, path.Base(release.release), release.prev, keepReleases(cfg))
	finish := time.Now().UTC()
	res.Status, res.Message, res.FinishedAt = run.TargetSuccess, "本地 Docker 部署完成，已核对本次完整容器 ID 与构建后固定的镜像 Config ID", &finish
	return res
}

func (s *service) localDockerPreviousContainer(ctx context.Context, serverID, name string) (string, error) {
	out, err := s.execLocalDocker(ctx, serverID, "inspect previous container", []string{"docker", "container", "inspect", "--format", "{{json .Id}}", name})
	if err != nil {
		if out != nil && out.ExitCode == 1 && strings.TrimSpace(out.Stdout) == "" {
			msg := strings.TrimSpace(out.Stderr)
			if msg == "Error: No such container: "+name || msg == "Error response from daemon: No such container: "+name || msg == "Error: No such object: "+name {
				return "", nil
			}
		}
		return "", errors.New("deploy: previous container unavailable")
	}
	var id string
	if !decodeLocalProjection(out.Stdout, &id) || !deployattempt.ValidContainerID(id) {
		return "", errors.New("deploy: previous container identity unavailable")
	}
	return id, nil
}
