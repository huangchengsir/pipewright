package build

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/huangchengsir/pipewright/internal/artifactstore"
	"github.com/huangchengsir/pipewright/internal/deploy"
	"github.com/huangchengsir/pipewright/internal/notify"
	"github.com/huangchengsir/pipewright/internal/pipeline"
	"github.com/huangchengsir/pipewright/internal/project"
	"github.com/huangchengsir/pipewright/internal/run"
	"github.com/huangchengsir/pipewright/internal/vault"
)

// 步骤名(Plan 顺序;固定中文以对齐既有 UI/桩风格)。
const (
	stepClone = "拉取源码"
	stepBuild = "构建"
	stepPush  = "推送镜像"
)

// 日志流取值(对齐 run 包 streamStdout/streamStderr;本包独立常量避免跨包耦合)。
const (
	streamStdout = "stdout"
	streamStderr = "stderr"
)

// localTagPrefix 是本地镜像 tag 前缀(local-tag = pipewright/<project-slug>:<commit7>)。
const localTagPrefix = "pipewright"

// Builder 实现 run.Runner:真实隔离构建 + 镜像推送(Story 3-3/3-5)。
//
// 依赖经构造函数注入(仿 RunSecretSource):
//   - projects:取该 run 的项目(RepoURL/凭据)。
//   - settings:取该项目的构建配置(BuildConfig)+ 环境(ImageRegistry)。
//   - vault   :取仓库 token / 构建 secret / 仓库口令的明文(即取即用,绝不入库/日志)。
//   - driver  :容器 CLI 驱动(探测出的 docker/nerdctl/podman);构造时已就绪。
//   - cloner  :磁盘工作区克隆器(SSRF 收口)。
//
// 真实日志/产物/失败日志全经传入的 run.StepSink 喂出,复用既有持久化/SSE/脱敏管道(3-1 地基)。
type Builder struct {
	projects project.Service
	settings pipeline.SettingsService
	vault    vault.Vault
	driver   Driver
	cloner   repoCloner
	// artStore 是制品库(Story 8-16):非 nil 时构建后把 jar/dist 真字节归档,产物 reference 改存句柄;
	// nil 则保持旧行为(reference=文件名占位)。由 main 注入(WithArtifactStore)。
	artStore *artifactstore.Store
	// disableImageGC 关闭「构建后清悬空镜像」(Story 8-17);默认开(false)。由 main 经 env 设置。
	disableImageGC bool
	// recordCommit 在克隆解析出实际 commit 后回写到 run(触发未指定 commit 时补真实值)。
	// nil 则不回写(向后兼容)。由 main 注入(WithCommitRecorder),解耦 build 对 run.Service 的直依赖。
	recordCommit func(ctx context.Context, runID, commit string)
	// deployer/notifier:dag 里 deploy_ssh / notify 节点真实化所需(main 注入 deploy.Service / notify.Service)。
	// nil → 该类节点退化为占位日志(向后兼容;非-dag Builder.Run 路径不用)。
	deployer deploy.Service
	notifier notify.Service
	// buildCache 是构建依赖缓存库(build cache · P0):非 nil 时 script 类 job 执行前后据 job.Config
	// 的 cachePaths/cacheKey 恢复/保存依赖目录(node_modules/.m2/.gradle 等),跨 run 持久化提速。
	// nil 则不缓存(向后兼容)。由 main 注入(WithBuildCache)。缓存问题绝不让构建失败(best-effort)。
	buildCache buildCacheStore
}

// buildCacheStore 抽象「按 key 恢复/保存工作区缓存路径」的能力(便于 fake 单测注入)。
// 实现是 *buildcache.Store。Restore 返回 (命中?, err);err 仅在输入非法时非 nil(缓存未命中
// 不算错误,返回 false,nil)。Save 的 I/O 错误透出供日志,但调用方应忽略(不阻断构建)。
type buildCacheStore interface {
	Restore(ctx context.Context, key string, paths []string, workspace string) (bool, error)
	Save(ctx context.Context, key string, paths []string, workspace string) error
}

// repoCloner 抽象「把仓库在 ref 上克隆到磁盘工作区」的能力(便于 fake 单测注入,
// 避免单测真触网)。默认实现是 *Cloner(go-git PlainClone + SSRF 收口)。
type repoCloner interface {
	Clone(ctx context.Context, repoURL, token, branch, commit, destDir string) (*CloneResolved, error)
}

// BuilderOption 配置 Builder(测试注入 fake driver/cloner)。
type BuilderOption func(*Builder)

// WithDriver 注入 Driver(测试用 fake;生产由 NewBuilder 探测)。
func WithDriver(d Driver) BuilderOption {
	return func(b *Builder) {
		if d != nil {
			b.driver = d
		}
	}
}

// WithCloner 注入克隆器(测试用放行 file:// 的夹具 / stub cloner)。
func WithCloner(c repoCloner) BuilderOption {
	return func(b *Builder) {
		if c != nil {
			b.cloner = c
		}
	}
}

// WithArtifactStore 注入制品库(Story 8-16):构建后把 jar/dist 真字节归档,产物 reference 改存句柄。
// nil 不改(保持旧占位行为)。
func WithArtifactStore(s *artifactstore.Store) BuilderOption {
	return func(b *Builder) {
		if s != nil {
			b.artStore = s
		}
	}
}

// WithImageGC 开关「构建后清悬空镜像」(Story 8-17);默认开。传 false 关闭(如宿主另有镜像 GC 策略)。
func WithImageGC(enabled bool) BuilderOption {
	return func(b *Builder) { b.disableImageGC = !enabled }
}

// WithCommitRecorder 注入「回写实际检出 commit 到 run」的回调(由 main 接 run.Service.SetCommit)。
// 克隆解析出 commit 后调用,补全运行详情里的 COMMIT 字段(触发未指定 commit 时尤其有用)。
func WithCommitRecorder(fn func(ctx context.Context, runID, commit string)) BuilderOption {
	return func(b *Builder) { b.recordCommit = fn }
}

// WithStageDeployer 注入部署服务,使 dag 里的 deploy_ssh 节点真实部署(中途部署,不动 run 终态)。
func WithStageDeployer(d deploy.Service) BuilderOption {
	return func(b *Builder) { b.deployer = d }
}

// WithStageNotifier 注入通知服务,使 dag 里的 notify 节点真实发通知。
func WithStageNotifier(n notify.Service) BuilderOption {
	return func(b *Builder) { b.notifier = n }
}

// WithBuildCache 注入构建依赖缓存库(build cache · P0):script 类 job 配了 cachePaths 时,
// 执行前恢复、执行后保存依赖目录到缓存库(跨 run 持久化提速)。nil 不改(不缓存,向后兼容)。
func WithBuildCache(c buildCacheStore) BuilderOption {
	return func(b *Builder) {
		if c != nil {
			b.buildCache = c
		}
	}
}

// NewBuilder 构造真实 Builder。driver 经 DetectDriver 探测(全无容器 CLI → ErrNoContainerCLI,
// 由 main 据此降级回 StubRunner)。cmdr 为 nil 用默认 execCommander(生产);测试经 Option 覆盖。
func NewBuilder(projects project.Service, settings pipeline.SettingsService, v vault.Vault, opts ...BuilderOption) (*Builder, error) {
	b := &Builder{
		projects: projects,
		settings: settings,
		vault:    v,
		cloner:   NewCloner(),
	}
	for _, opt := range opts {
		opt(b)
	}
	if b.driver == nil {
		drv, err := DetectDriver(nil)
		if err != nil {
			return nil, err
		}
		b.driver = drv
	}
	return b, nil
}

// DriverBinary 返回选中的容器 CLI 名(docker/nerdctl/podman),供 main 启动日志展示。
func (b *Builder) DriverBinary() string {
	if b.driver == nil {
		return ""
	}
	return b.driver.Binary()
}

// Run 实现 run.Runner:拉取源码 → 构建 →(image 且配仓库时)推送。
// 任一步骤命令非零退出 → 该步 failed + SetFailureLog + 返回 ErrBuildFailed;ctx 取消 → run.ErrCanceled。
func (b *Builder) Run(ctx context.Context, r *run.Run, sink run.StepSink) error {
	// 解析项目 + 构建配置(经注入服务;缺失即诚实失败,不假装)。
	proj, settings, perr := b.resolve(ctx, r)
	if perr != nil {
		return b.failPlan(ctx, sink, stepClone, "无法加载项目构建配置:"+perr.Error())
	}

	// 是否需要推送:产物为 image 且解析环境的 ImageRegistry 非空。
	registry := b.resolveRegistry(settings, r.Trigger.ResolvedEnvironment)
	artifactType := settings.Build.ArtifactType
	willPush := artifactType == pipeline.ArtifactImage && registry != nil

	// 自定义脚本步骤(Epic 8 · Story 8-2):配置里的有序 script 步骤,接在构建/推送之后。
	customSteps := scriptSteps(settings)

	// 步骤计划:拉取源码 → 构建 →(可选)推送镜像 → 自定义脚本步骤(按声明顺序)。
	steps := []string{stepClone, stepBuild}
	if willPush {
		steps = append(steps, stepPush)
	}
	scriptStepBase := len(steps) // 第一个脚本步骤在计划里的序号
	for _, cs := range customSteps {
		steps = append(steps, cs.Name)
	}
	if err := sink.Plan(ctx, steps); err != nil {
		return err
	}

	// ---- 步骤 0:拉取源码到临时工作区(即用即销,FR-5)----
	if canceled(ctx) {
		return b.cancelAt(ctx, sink, 0)
	}
	if err := sink.StepRunning(ctx, 0); err != nil {
		return err
	}
	workspace, mkErr := mkTempWorkspace()
	if mkErr != nil {
		return b.failStep(ctx, sink, 0, "创建临时工作区失败:"+mkErr.Error())
	}
	defer func() { _ = os.RemoveAll(workspace) }() // 宿主零污染

	token := b.revealToken(proj.CredentialID)
	resolved, cerr := b.cloner.Clone(ctx, proj.RepoURL, token, r.Trigger.Branch, r.Trigger.Commit, workspace)
	token = "" // 明文用完即弃
	_ = token
	if cerr != nil {
		if errors.Is(ctx.Err(), context.Canceled) {
			return b.cancelAt(ctx, sink, 0)
		}
		_ = sink.Log(ctx, streamStderr, 0, "源码克隆失败(鉴权/网络/ref 不存在或被 SSRF 拒绝)")
		return b.failStep(ctx, sink, 0, "源码克隆失败:"+cerr.Error())
	}
	if resolved != nil && resolved.CommitShort != "" && b.recordCommit != nil {
		b.recordCommit(ctx, r.ID, resolved.CommitShort)
	}
	commitTag := resolved.CommitShort
	if commitTag == "" {
		commitTag = "latest"
	}
	_ = sink.Log(ctx, streamStdout, 0, "已克隆 "+proj.RepoURL+" @ "+commitTag)
	if err := sink.StepDone(ctx, 0, run.StepSuccess); err != nil {
		return err
	}

	// ---- 步骤 1:构建(image/jar/dist)----
	if canceled(ctx) {
		return b.cancelAt(ctx, sink, 1)
	}
	if err := sink.StepRunning(ctx, 1); err != nil {
		return err
	}
	localTag, artifact, berr := b.build(ctx, sink, 1, proj, settings.Build, workspace, commitTag)
	if berr != nil {
		if errors.Is(ctx.Err(), context.Canceled) {
			return b.cancelAt(ctx, sink, 1)
		}
		return b.failStep(ctx, sink, 1, "构建失败:"+berr.Error())
	}
	if err := sink.StepDone(ctx, 1, run.StepSuccess); err != nil {
		return err
	}

	// ---- 步骤 2(可选):推送镜像 ----
	if willPush {
		if canceled(ctx) {
			return b.cancelAt(ctx, sink, 2)
		}
		if err := sink.StepRunning(ctx, 2); err != nil {
			return err
		}
		remoteTag, pdigest, perr := b.push(ctx, sink, 2, localTag, registry, proj, commitTag)
		if perr != nil {
			if errors.Is(ctx.Err(), context.Canceled) {
				return b.cancelAt(ctx, sink, 2)
			}
			return b.failStep(ctx, sink, 2, "镜像推送失败:"+perr.Error())
		}
		// 推送后产物引用改为远端 tag,digest 用推送后(repoDigest)的。
		artifact.Reference = remoteTag
		if pdigest != "" {
			if artifact.Metadata == nil {
				artifact.Metadata = map[string]any{}
			}
			artifact.Metadata["digest"] = pdigest
		}
		if err := sink.StepDone(ctx, 2, run.StepSuccess); err != nil {
			return err
		}
	}

	// ---- 自定义脚本步骤(Story 8-2):按声明顺序在隔离容器内逐个执行 ----
	for i, cs := range customSteps {
		ordinal := scriptStepBase + i
		if canceled(ctx) {
			return b.cancelAt(ctx, sink, ordinal)
		}
		if err := sink.StepRunning(ctx, ordinal); err != nil {
			return err
		}
		serr := b.runScriptStep(ctx, sink, ordinal, cs, workspace)
		if serr != nil {
			if errors.Is(serr, run.ErrCanceled) || errors.Is(ctx.Err(), context.Canceled) {
				return b.cancelAt(ctx, sink, ordinal)
			}
			return b.failStep(ctx, sink, ordinal, "自定义步骤「"+cs.Name+"」失败")
		}
		if err := sink.StepDone(ctx, ordinal, run.StepSuccess); err != nil {
			return err
		}
	}

	// 成功路径:emit 真实产物(去掉 stub 标记)。best-effort:落库失败仅忽略。
	if artifact != nil {
		_ = sink.EmitArtifact(ctx, *artifact)
	}

	// 构建后清悬空镜像(Story 8-17):防中控机镜像缓存随重复构建无限增长。
	// best-effort、只清 dangling、绝不影响构建结果(已 success);经构建步骤序号喂日志。
	b.pruneImagesBestEffort(ctx, b.lineSink(sink, len(steps)-1))
	return nil
}

// resolve 取该 run 的项目 + 构建配置(经注入服务)。任一依赖缺失即返回错误(诚实失败)。
func (b *Builder) resolve(ctx context.Context, r *run.Run) (*project.Project, *pipeline.Settings, error) {
	if b.projects == nil || b.settings == nil {
		return nil, nil, errors.New("builder 依赖未装配")
	}
	if r == nil || strings.TrimSpace(r.ProjectID) == "" {
		return nil, nil, errors.New("运行缺少项目引用")
	}
	proj, err := b.projects.Get(ctx, r.ProjectID)
	if err != nil {
		return nil, nil, fmt.Errorf("项目不存在或不可读")
	}
	settings, err := b.settings.Get(ctx, r.ProjectID)
	if err != nil {
		return nil, nil, fmt.Errorf("构建配置不可读")
	}
	return proj, settings, nil
}

// resolveRegistry 取解析环境名对应的 ImageRegistry(非空且 Type 非空才返回)。
// 环境名为空(手动触发)或无匹配/未绑定 → nil(不推送)。
func (b *Builder) resolveRegistry(settings *pipeline.Settings, envName string) *pipeline.ImageRegistry {
	envName = strings.TrimSpace(envName)
	if envName == "" {
		return nil
	}
	for i := range settings.Environments {
		e := &settings.Environments[i]
		if e.Name == envName {
			if strings.TrimSpace(e.ImageRegistry.Type) == "" || strings.TrimSpace(e.ImageRegistry.URL) == "" {
				return nil
			}
			reg := e.ImageRegistry
			return &reg
		}
	}
	return nil
}

// build 执行构建步骤,逐行喂日志。返回 local-tag(image 产物)+ 待 emit 的产物。
//
//   - 模型 A(dockerfile)或产物=image:docker build -f <DockerfilePath> -t <local-tag> .
//   - 模型 B(toolchain)产 jar/dist:docker run --rm -v ws:/src -w /src <toolchain:ver> <build-cmd>;
//     对 image 产物退化为要求 Dockerfile(走模型 A 路径)。
func (b *Builder) build(ctx context.Context, sink run.StepSink, ordinal int, proj *project.Project, cfg pipeline.BuildConfig, workspace, commitTag string) (string, *run.Artifact, error) {
	onLine := b.lineSink(sink, ordinal)

	// 构建变量:非 secret → 明文 K=V;secret → vault.Reveal 明文 K=V(注入子进程,绝不回显值)。
	buildArgs, secretArgs := b.buildArgs(cfg.Vars)

	slug := slugify(proj.Name)

	switch cfg.ArtifactType {
	case pipeline.ArtifactImage:
		// image 产物:无论 model A/B 都要求 Dockerfile(模型 B 对 image 退化为 Dockerfile)。
		dockerfile := strings.TrimSpace(cfg.DockerfilePath)
		if dockerfile == "" {
			dockerfile = "Dockerfile"
		}
		// 构建上下文:默认仓库根(workspace);配了 Context(monorepo 子目录)则用该子目录。
		// dockerfilePath 与 context 都相对仓库根:故 context 非空时把 dockerfile 解析为相对
		// workspace 的绝对路径,避免 driver 把它再 join 到子目录 contextDir 下(致路径翻倍)。
		contextDir := workspace
		if c := strings.TrimSpace(cfg.Context); c != "" && c != "." {
			contextDir = filepath.Join(workspace, c)
			if !filepath.IsAbs(dockerfile) {
				dockerfile = filepath.Join(workspace, dockerfile)
			}
		}
		localTag := localTagPrefix + "/" + slug + ":" + commitTag
		code, err := b.driver.Build(ctx, contextDir, dockerfile, localTag, buildArgs, secretArgs, onLine)
		if err != nil && code < 0 {
			return "", nil, fmt.Errorf("构建器无法启动")
		}
		if code != 0 {
			return "", nil, ErrBuildFailed
		}
		digest, size, _ := b.driver.InspectImage(ctx, localTag)
		art := &run.Artifact{
			Type:      run.ArtifactImage,
			Name:      slug,
			Reference: localTag,
			SizeBytes: size,
			Metadata: map[string]any{
				"builder": b.driver.Binary(),
			},
		}
		if digest != "" {
			art.Metadata["digest"] = digest
		}
		return localTag, art, nil

	case pipeline.ArtifactJAR, pipeline.ArtifactDist:
		// 模型 B:工具链镜像挂载工作区跑构建命令。
		image := toolchainImage(cfg.Toolchain)
		if image == "" {
			return "", nil, fmt.Errorf("未指定工具链镜像(toolchain.language/version)")
		}
		env := append(buildArgs, secretArgs...) // 工具链构建经 -e 注入(secret 回显只列 key)
		buildCmd := toolchainBuildCmd(cfg.ArtifactType)
		code, err := b.driver.RunToolchain(ctx, image, workspace, "/src", env, buildCmd, pipeline.Resource{}, onLine)
		if err != nil && code < 0 {
			return "", nil, fmt.Errorf("构建器无法启动")
		}
		if code != 0 {
			return "", nil, ErrBuildFailed
		}
		art := b.locateFileArtifact(cfg.ArtifactType, slug, workspace, onLine)
		return "", art, nil

	default:
		return "", nil, fmt.Errorf("不支持的产物类型 %q", cfg.ArtifactType)
	}
}

// push 执行镜像推送(3-5):login(口令经 stdin)→ tag → push → inspect 取推送后 digest。
func (b *Builder) push(ctx context.Context, sink run.StepSink, ordinal int, localTag string, registry *pipeline.ImageRegistry, proj *project.Project, commitTag string) (string, string, error) {
	onLine := b.lineSink(sink, ordinal)

	user, pass := b.revealRegistryCred(registry.CredentialID)
	// 远端 tag:<registry-url>/<project-slug>:<commit7>(URL 去协议/尾斜杠)。
	remoteTag := strings.TrimRight(stripScheme(registry.URL), "/") + "/" + slugify(proj.Name) + ":" + commitTag

	// 登录(口令经 stdin,绝不进 argv/日志)。凭据缺失(user 空)时跳过登录,直接 tag/push
	// (匿名/已登录场景);仓库要求鉴权则 push 自身会失败并落失败日志。
	if user != "" {
		code, err := b.driver.Login(ctx, registry.URL, user, pass, onLine)
		pass = "" // 口令用完即弃
		_ = pass
		if err != nil && code < 0 {
			return "", "", fmt.Errorf("构建器无法启动")
		}
		if code != 0 {
			return "", "", fmt.Errorf("仓库登录失败")
		}
	}

	if code, err := b.driver.Tag(ctx, localTag, remoteTag, onLine); err != nil && code < 0 {
		return "", "", fmt.Errorf("构建器无法启动")
	} else if code != 0 {
		return "", "", ErrBuildFailed
	}
	if code, err := b.driver.Push(ctx, remoteTag, onLine); err != nil && code < 0 {
		return "", "", fmt.Errorf("构建器无法启动")
	} else if code != 0 {
		return "", "", ErrBuildFailed
	}
	digest, _, _ := b.driver.InspectImage(ctx, remoteTag)
	return remoteTag, digest, nil
}

// buildArgs 把构建变量拆成非 secret(明文 K=V)与 secret(vault.Reveal 明文 K=V)。
// secret 明文即取即用,登记进 Masker 的脱敏由 sink 侧兜底;本包仅注入子进程不回显值。
func (b *Builder) buildArgs(vars []pipeline.BuildVar) (plain []string, secret []string) {
	for _, v := range vars {
		if v.Secret {
			if v.CredentialID == "" || b.vault == nil {
				continue
			}
			pt, err := b.vault.Reveal(v.CredentialID)
			if err != nil || pt == "" {
				continue
			}
			secret = append(secret, v.Key+"="+pt)
		} else {
			plain = append(plain, v.Key+"="+v.Value)
		}
	}
	return plain, secret
}

// revealToken 取项目主凭据(git token)明文(即取即用;失败返回空 → 匿名克隆,公开仓库友好)。
func (b *Builder) revealToken(credID string) string {
	if credID == "" || b.vault == nil {
		return ""
	}
	pt, err := b.vault.Reveal(credID)
	if err != nil {
		return ""
	}
	return pt
}

// revealRegistryCred 取仓库凭据明文并解析为 user/password。凭据约定以 "user:password" 存储;
// 无冒号时整串作为口令、user 空(匿名/token 场景由调用方处理)。失败返回空。
func (b *Builder) revealRegistryCred(credID string) (string, string) {
	if credID == "" || b.vault == nil {
		return "", ""
	}
	pt, err := b.vault.Reveal(credID)
	if err != nil || pt == "" {
		return "", ""
	}
	if i := strings.Index(pt, ":"); i >= 0 {
		return pt[:i], pt[i+1:]
	}
	return "", pt
}

// locateFileArtifact 在工作区定位 jar/dist 产物文件/目录,算 size,构造产物。定位不到时
// 退化为工作区根引用、size=0(产物仍 emit,不致命)。
func (b *Builder) locateFileArtifact(artifactType, slug, workspace string, onLine func(stream, line string)) *run.Artifact {
	switch artifactType {
	case pipeline.ArtifactJAR:
		if p := findFirst(workspace, ".jar"); p != "" {
			size := fileSize(p)
			onLine(streamStdout, "产物:"+filepath.Base(p)+"("+itoa(size)+" bytes)")
			art := &run.Artifact{
				Type: run.ArtifactJar, Name: slug, Reference: filepath.Base(p), SizeBytes: size,
				Metadata: map[string]any{"builder": b.driver.Binary(), "path": filepath.Base(p)},
			}
			// 制品库:把 jar 真字节归档,reference 改存句柄(供部署取真字节);未配则保持占位。
			b.storeJarBytes(art, p, onLine)
			return art
		}
	case pipeline.ArtifactDist:
		// 常见 dist 输出目录:dist / build / out。
		for _, d := range []string{"dist", "build", "out"} {
			full := filepath.Join(workspace, d)
			if isDir(full) {
				size := dirSize(full)
				onLine(streamStdout, "产物目录:"+d+"/("+itoa(size)+" bytes)")
				art := &run.Artifact{
					Type: run.ArtifactDist, Name: slug + "-dist", Reference: d + "/", SizeBytes: size,
					Metadata: map[string]any{"builder": b.driver.Binary(), "path": d + "/"},
				}
				// 制品库:把 dist 目录打 tar.gz 归档,reference 改存句柄;未配则保持占位。
				b.storeDistDir(art, full, onLine)
				return art
			}
		}
	}
	// 退化:未定位到产物(仍 emit 一条记录,metadata 标注 located=false 便于诊断)。
	t := run.ArtifactJar
	if artifactType == pipeline.ArtifactDist {
		t = run.ArtifactDist
	}
	return &run.Artifact{
		Type: t, Name: slug, Reference: slug, SizeBytes: 0,
		Metadata: map[string]any{"builder": b.driver.Binary(), "located": false},
	}
}

// lineSink 返回一个把 (stream, line) 转喂 sink.Log 的回调(绑定步骤 ordinal)。best-effort。
func (b *Builder) lineSink(sink run.StepSink, ordinal int) func(stream, line string) {
	return func(stream, line string) {
		_ = sink.Log(context.Background(), stream, ordinal, line)
	}
}

// ---- 失败/取消归一辅助 ----

// failPlan 在尚未 Plan 时的早失败:声明单步、置 failed、写失败日志、返回错误。
func (b *Builder) failPlan(ctx context.Context, sink run.StepSink, stepName, msg string) error {
	if err := sink.Plan(ctx, []string{stepName}); err == nil {
		_ = sink.StepRunning(ctx, 0)
		_ = sink.Log(ctx, streamStderr, 0, msg)
		_ = sink.StepDone(ctx, 0, run.StepFailed)
	}
	_ = sink.SetFailureLog(ctx, msg)
	return ErrBuildFailed
}

// failStep 置某步 failed + 写真实尾部失败日志(供 AI 诊断;脱敏由消费侧)+ 返回错误。
func (b *Builder) failStep(ctx context.Context, sink run.StepSink, ordinal int, msg string) error {
	_ = sink.StepDone(ctx, ordinal, run.StepFailed)
	_ = sink.SetFailureLog(ctx, msg)
	return ErrBuildFailed
}

// cancelAt 在 ctx 取消时置某步 failed 并返回 run.ErrCanceled(worker 归一为 failed 终态)。
func (b *Builder) cancelAt(ctx context.Context, sink run.StepSink, ordinal int) error {
	_ = sink.StepDone(context.Background(), ordinal, run.StepFailed)
	return run.ErrCanceled
}

// canceled 报告 ctx 是否已取消。
func canceled(ctx context.Context) bool {
	select {
	case <-ctx.Done():
		return true
	default:
		return false
	}
}

// ---- 纯函数辅助 ----

// toolchainImage 据工具链(language/version)推容器镜像名(language:version);language 空 → ""。
func toolchainImage(tc pipeline.Toolchain) string {
	lang := strings.TrimSpace(tc.Language)
	if lang == "" {
		return ""
	}
	ver := strings.TrimSpace(tc.Version)
	if ver == "" {
		ver = "latest"
	}
	return lang + ":" + ver
}

// toolchainBuildCmd 据产物类型给一个保守的默认构建命令(模型 B)。真实构建命令未来可由配置驱动;
// 本期按产物类型给业界最常见命令(jar=mvn package;dist=npm run build),容器内执行。
func toolchainBuildCmd(artifactType string) []string {
	switch artifactType {
	case pipeline.ArtifactJAR:
		return []string{"mvn", "-B", "-DskipTests", "package"}
	case pipeline.ArtifactDist:
		return []string{"sh", "-c", "npm install && npm run build"}
	default:
		return []string{"true"}
	}
}

// stripScheme 去掉 URL 的 http(s):// 前缀(镜像 tag 不含协议)。
func stripScheme(u string) string {
	u = strings.TrimSpace(u)
	u = strings.TrimPrefix(u, "https://")
	u = strings.TrimPrefix(u, "http://")
	return u
}

// slugify 把项目名归一为引用安全的小写 slug(非字母数字折成 '-';空 → "app")。
func slugify(s string) string {
	var b strings.Builder
	prevDash := false
	for _, r := range strings.ToLower(strings.TrimSpace(s)) {
		switch {
		case (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9'):
			b.WriteRune(r)
			prevDash = false
		default:
			if !prevDash && b.Len() > 0 {
				b.WriteByte('-')
				prevDash = true
			}
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		return "app"
	}
	return out
}

// findFirst 递归查工作区下首个以 suffix 结尾的文件,返回绝对路径(未找到 → "")。
func findFirst(root, suffix string) string {
	var found string
	_ = filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err != nil || found != "" || info == nil {
			return nil
		}
		if !info.IsDir() && strings.HasSuffix(info.Name(), suffix) {
			found = p
			return filepath.SkipDir
		}
		return nil
	})
	return found
}

// fileSize 取文件字节大小(失败 → 0)。
func fileSize(p string) int64 {
	if fi, err := os.Stat(p); err == nil {
		return fi.Size()
	}
	return 0
}

// isDir 报告路径是否为目录。
func isDir(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.IsDir()
}

// dirSize 递归累加目录下所有文件字节大小(失败的项跳过)。
func dirSize(root string) int64 {
	var total int64
	_ = filepath.Walk(root, func(_ string, info os.FileInfo, err error) error {
		if err == nil && info != nil && !info.IsDir() {
			total += info.Size()
		}
		return nil
	})
	return total
}
