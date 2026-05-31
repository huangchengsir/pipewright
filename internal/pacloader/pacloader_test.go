package pacloader

import (
	"context"
	"errors"
	"testing"

	"github.com/huangchengsir/pipewright/internal/pipeline"
)

// ─── 测试替身 ──────────────────────────────────────────────────────────────────

// storedLoader 是被装饰的「库内」loader 替身:返回一份可辨识的 stored 配置。
type storedLoader struct {
	cfg    *pipeline.Config
	err    error
	called int
}

func (s *storedLoader) Get(_ context.Context, _ string) (*pipeline.Config, error) {
	s.called++
	return s.cfg, s.err
}

type fakeProjects struct {
	info ProjectInfo
	err  error
}

func (f fakeProjects) Lookup(_ context.Context, _ string) (ProjectInfo, error) {
	return f.info, f.err
}

type fakeTokens struct {
	token string
	err   error
	gotID string
}

func (f *fakeTokens) Reveal(id string) (string, error) {
	f.gotID = id
	return f.token, f.err
}

type fakeBlobs struct {
	content  string
	degraded bool
	err      error
	gotRef   string
	gotFile  string
	gotToken string
}

func (f *fakeBlobs) FetchBlob(_ context.Context, _, token, ref, file string) (string, bool, error) {
	f.gotToken = token
	f.gotRef = ref
	f.gotFile = file
	return f.content, f.degraded, f.err
}

// ─── 夹具 ──────────────────────────────────────────────────────────────────────

func storedCfg() *pipeline.Config {
	return &pipeline.Config{Spec: pipeline.Spec{Stages: []pipeline.Stage{
		{ID: "stg_stored", Name: "stored-pipeline", Kind: pipeline.KindBuild},
	}}}
}

// firstStage 取配置首个阶段名(测试用「stored」/「from-repo」辨识来源)。
func firstStage(cfg *pipeline.Config) string {
	if cfg == nil || len(cfg.Spec.Stages) == 0 {
		return ""
	}
	return cfg.Spec.Stages[0].Name
}

// validYAML 是一份最小合法 `.pipewright.yml`(经 pipelineyaml.Parse 校验通过:须恰好一个 source 阶段)。
// 首阶段名「from-repo」用于在测试中辨识「来自仓库 YAML」。
const validYAML = `version: 1
stages:
  - id: stg_src
    name: from-repo
    kind: source
    jobs:
      - id: job_src
        name: 源
        type: git_source
  - id: stg_build
    name: 构建
    kind: build
    needs:
      - stg_src
    jobs:
      - id: job_compile
        name: compile
        type: script
        script:
          commands:
            - echo hi
`

func newLoader(stored *storedLoader, p ProjectLookup, t TokenRevealer, b BlobFetcher) *Loader {
	return New(stored, p, t, b)
}

func defaultInfo() ProjectInfo {
	return ProjectInfo{RepoURL: "https://git.example.com/acme/app.git", CredentialID: "cred-1", DefaultBranch: "main"}
}

// ─── (a) 合法 .pipewright.yml → 用其 spec ───────────────────────────────────────

func TestGet_ValidRepoYAML_UsesRepoSpec(t *testing.T) {
	stored := &storedLoader{cfg: storedCfg()}
	tokens := &fakeTokens{token: "secret-token"}
	blobs := &fakeBlobs{content: validYAML}
	l := newLoader(stored, fakeProjects{info: defaultInfo()}, tokens, blobs)

	cfg, err := l.Get(context.Background(), "proj-1")
	if err != nil {
		t.Fatalf("意外错误: %v", err)
	}
	if cfg == nil || firstStage(cfg) != "from-repo" {
		t.Fatalf("应使用仓库 YAML 的 spec(name=from-repo),实际: %+v", cfg)
	}
	if stored.called != 0 {
		t.Fatalf("命中仓库 YAML 时不应回退库内 loader,called=%d", stored.called)
	}
	// 从默认分支拉取正确文件,并把凭据明文透传给拉取器。
	if blobs.gotRef != "main" {
		t.Errorf("应从默认分支 main 拉取,实际 ref=%q", blobs.gotRef)
	}
	if blobs.gotFile != DefaultFile {
		t.Errorf("应拉取 %s,实际 file=%q", DefaultFile, blobs.gotFile)
	}
	if blobs.gotToken != "secret-token" {
		t.Errorf("应把凭据明文透传给拉取器")
	}
	if tokens.gotID != "cred-1" {
		t.Errorf("应按项目 CredentialID 解密,实际 id=%q", tokens.gotID)
	}
}

// ─── (b) 文件不存在 → 用库内配置 ────────────────────────────────────────────────

func TestGet_FileMissing_FallsBackToStored(t *testing.T) {
	stored := &storedLoader{cfg: storedCfg()}
	// 拉取器对「文件不存在」返回错误(SourceReader 语义:path not found)。
	blobs := &fakeBlobs{err: errors.New("source: path not found")}
	l := newLoader(stored, fakeProjects{info: defaultInfo()}, &fakeTokens{}, blobs)

	cfg, err := l.Get(context.Background(), "proj-1")
	if err != nil {
		t.Fatalf("文件缺失不应报错(应回退): %v", err)
	}
	if cfg == nil || firstStage(cfg) != "stored-pipeline" {
		t.Fatalf("应回退库内配置,实际: %+v", cfg)
	}
	if stored.called != 1 {
		t.Fatalf("应恰好回退一次库内 loader,called=%d", stored.called)
	}
}

// ─── (c) 拉取/克隆失败 → 用库内配置(不冒泡错误)─────────────────────────────────

func TestGet_FetchError_FallsBackNoError(t *testing.T) {
	stored := &storedLoader{cfg: storedCfg()}
	blobs := &fakeBlobs{err: errors.New("source: clone failed")}
	l := newLoader(stored, fakeProjects{info: defaultInfo()}, &fakeTokens{}, blobs)

	cfg, err := l.Get(context.Background(), "proj-1")
	if err != nil {
		t.Fatalf("拉取失败绝不应冒泡给调用方: %v", err)
	}
	if firstStage(cfg) != "stored-pipeline" {
		t.Fatalf("应回退库内配置,实际 name=%q", firstStage(cfg))
	}
}

// degraded 克隆降级(SourceReader 对 tree/blob 的优雅降级)同样回退。
func TestGet_DegradedClone_FallsBack(t *testing.T) {
	stored := &storedLoader{cfg: storedCfg()}
	blobs := &fakeBlobs{content: "", degraded: true}
	l := newLoader(stored, fakeProjects{info: defaultInfo()}, &fakeTokens{}, blobs)

	cfg, err := l.Get(context.Background(), "proj-1")
	if err != nil {
		t.Fatalf("降级不应报错: %v", err)
	}
	if firstStage(cfg) != "stored-pipeline" {
		t.Fatalf("降级应回退库内配置,实际 name=%q", firstStage(cfg))
	}
}

// ─── (d) 非法 YAML → 用库内配置 ─────────────────────────────────────────────────

func TestGet_InvalidYAML_FallsBackToStored(t *testing.T) {
	stored := &storedLoader{cfg: storedCfg()}
	blobs := &fakeBlobs{content: "this: : is not valid pipewright yaml\n  - broken"}
	l := newLoader(stored, fakeProjects{info: defaultInfo()}, &fakeTokens{}, blobs)

	cfg, err := l.Get(context.Background(), "proj-1")
	if err != nil {
		t.Fatalf("非法 YAML 绝不应中断运行: %v", err)
	}
	if firstStage(cfg) != "stored-pipeline" {
		t.Fatalf("非法 YAML 应回退库内配置,实际 name=%q", firstStage(cfg))
	}
}

// ─── 边界:项目查询失败 / 空仓库 / 空内容 / 缺依赖 → 回退 ────────────────────────

func TestGet_ProjectLookupError_FallsBack(t *testing.T) {
	stored := &storedLoader{cfg: storedCfg()}
	l := newLoader(stored, fakeProjects{err: errors.New("not found")}, &fakeTokens{}, &fakeBlobs{content: validYAML})

	cfg, _ := l.Get(context.Background(), "proj-1")
	if firstStage(cfg) != "stored-pipeline" {
		t.Fatalf("项目查询失败应回退库内,实际 name=%q", firstStage(cfg))
	}
}

func TestGet_EmptyRepoURL_FallsBack(t *testing.T) {
	stored := &storedLoader{cfg: storedCfg()}
	info := defaultInfo()
	info.RepoURL = "  "
	l := newLoader(stored, fakeProjects{info: info}, &fakeTokens{}, &fakeBlobs{content: validYAML})

	cfg, _ := l.Get(context.Background(), "proj-1")
	if firstStage(cfg) != "stored-pipeline" {
		t.Fatalf("空仓库地址应回退库内,实际 name=%q", firstStage(cfg))
	}
}

func TestGet_EmptyContent_FallsBack(t *testing.T) {
	stored := &storedLoader{cfg: storedCfg()}
	l := newLoader(stored, fakeProjects{info: defaultInfo()}, &fakeTokens{}, &fakeBlobs{content: "   \n"})

	cfg, _ := l.Get(context.Background(), "proj-1")
	if firstStage(cfg) != "stored-pipeline" {
		t.Fatalf("空文件应回退库内,实际 name=%q", firstStage(cfg))
	}
}

func TestGet_NilDeps_PurePassthrough(t *testing.T) {
	stored := &storedLoader{cfg: storedCfg()}
	l := New(stored, nil, nil, nil)

	cfg, err := l.Get(context.Background(), "proj-1")
	if err != nil {
		t.Fatalf("意外错误: %v", err)
	}
	if firstStage(cfg) != "stored-pipeline" || stored.called != 1 {
		t.Fatalf("缺依赖应纯透传库内 loader,name=%q called=%d", firstStage(cfg), stored.called)
	}
}

// 凭据取不到(Reveal 失败)→ 以空 token 继续尝试公开仓库(不直接回退)。
func TestGet_TokenRevealError_TriesPublicWithEmptyToken(t *testing.T) {
	stored := &storedLoader{cfg: storedCfg()}
	tokens := &fakeTokens{err: errors.New("vault: not found")}
	blobs := &fakeBlobs{content: validYAML}
	l := newLoader(stored, fakeProjects{info: defaultInfo()}, tokens, blobs)

	cfg, err := l.Get(context.Background(), "proj-1")
	if err != nil {
		t.Fatalf("意外错误: %v", err)
	}
	if firstStage(cfg) != "from-repo" {
		t.Fatalf("凭据取不到应以空 token 尝试公开仓库,实际 name=%q", firstStage(cfg))
	}
	if blobs.gotToken != "" {
		t.Errorf("Reveal 失败时应以空 token 拉取,实际 token=%q", blobs.gotToken)
	}
}
