package pipelineyaml

import (
	"errors"
	"strings"
	"testing"

	"github.com/huangchengsir/pipewright/internal/pipeline"
)

// multiStageFixture 是一份多阶段 DAG 流水线:source → build(script 步骤)→ deploy(needs + when + gate)
// → notify(allowFailure)。覆盖 needs/when/gate/allowFailure + 脚本步骤 image/commands/env/workdir。
const multiStageFixture = `version: 1
stages:
  - id: stg_src
    name: 流水线源
    kind: source
    jobs:
      - id: job_src
        name: Gitee 源
        type: git_source
        summary: org/repo · main
  - id: stg_build
    name: 构建
    kind: build
    needs:
      - stg_src
    jobs:
      - id: job_test
        name: 运行测试
        type: script
        script:
          image: golang:1.23
          commands:
            - go vet ./...
            - go test ./...
          env:
            CGO_ENABLED: "0"
            GOFLAGS: -count=1
          workdir: src/app
  - id: stg_deploy
    name: 部署
    kind: deploy
    needs:
      - stg_build
    gate: true
    when:
      branches:
        - main
        - release/*
      events:
        - webhook
    jobs:
      - id: job_deploy
        name: SSH 部署
        type: deploy_ssh
        config:
          targetEnv: prod
  - id: stg_notify
    name: 通知
    kind: notify
    needs:
      - stg_deploy
    allowFailure: true
    jobs:
      - id: job_notify
        name: 钉钉通知
        type: notify
`

func TestParseMultiStageDAG(t *testing.T) {
	cfg, err := Parse([]byte(multiStageFixture))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	st := cfg.Spec.Stages
	if len(st) != 4 {
		t.Fatalf("阶段数 = %d, want 4", len(st))
	}

	// 阶段顺序与基本字段。
	if st[0].Kind != pipeline.KindSource || st[1].Kind != pipeline.KindBuild {
		t.Fatalf("阶段 kind 顺序错: %+v", []string{st[0].Kind, st[1].Kind})
	}

	// needs 链。
	if len(st[1].Needs) != 1 || st[1].Needs[0] != "stg_src" {
		t.Fatalf("build needs = %+v, want [stg_src]", st[1].Needs)
	}
	if len(st[2].Needs) != 1 || st[2].Needs[0] != "stg_build" {
		t.Fatalf("deploy needs = %+v", st[2].Needs)
	}

	// gate + when。
	if !st[2].Gate {
		t.Fatalf("deploy gate 应为 true")
	}
	if len(st[2].When.Branches) != 2 || st[2].When.Branches[0] != "main" {
		t.Fatalf("deploy when.branches = %+v", st[2].When.Branches)
	}
	if len(st[2].When.Events) != 1 || st[2].When.Events[0] != "webhook" {
		t.Fatalf("deploy when.events = %+v", st[2].When.Events)
	}

	// allowFailure。
	if !st[3].AllowFailure {
		t.Fatalf("notify allowFailure 应为 true")
	}

	// 脚本步骤摊平进 Job.Config(对齐画布扁平约定)。
	build := st[1].Jobs[0]
	if build.Config["image"] != "golang:1.23" {
		t.Fatalf("script image = %v", build.Config["image"])
	}
	if build.Config["workDir"] != "src/app" {
		t.Fatalf("script workDir = %v", build.Config["workDir"])
	}
	cmds, _ := build.Config["commands"].(string)
	if cmds != "go vet ./...\ngo test ./..." {
		t.Fatalf("script commands = %q", cmds)
	}
	env, _ := build.Config["env"].(string)
	if !strings.Contains(env, `"CGO_ENABLED":"0"`) || !strings.Contains(env, `"GOFLAGS":"-count=1"`) {
		t.Fatalf("script env JSON = %q", env)
	}

	// config: 透传。
	if st[2].Jobs[0].Config["targetEnv"] != "prod" {
		t.Fatalf("deploy job config 透传 = %+v", st[2].Jobs[0].Config)
	}

	// 渲染 YAML 非空(规范回填)。
	if strings.TrimSpace(cfg.YAML) == "" {
		t.Fatalf("回填 YAML 不应为空")
	}
	if cfg.Status != pipeline.StatusDraft {
		t.Fatalf("status = %q, want draft", cfg.Status)
	}
}

// TestRoundTrip 验证 Parse → Marshal → Parse 语义不变(往返一致)。
func TestRoundTrip(t *testing.T) {
	cfg1, err := Parse([]byte(multiStageFixture))
	if err != nil {
		t.Fatalf("first Parse: %v", err)
	}
	out, err := Marshal(cfg1.Spec)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	cfg2, err := Parse(out)
	if err != nil {
		t.Fatalf("re-Parse marshaled: %v\n---\n%s", err, out)
	}

	if len(cfg1.Spec.Stages) != len(cfg2.Spec.Stages) {
		t.Fatalf("往返阶段数变化: %d → %d", len(cfg1.Spec.Stages), len(cfg2.Spec.Stages))
	}
	for i := range cfg1.Spec.Stages {
		a, b := cfg1.Spec.Stages[i], cfg2.Spec.Stages[i]
		if a.ID != b.ID || a.Name != b.Name || a.Kind != b.Kind || a.Gate != b.Gate || a.AllowFailure != b.AllowFailure {
			t.Fatalf("阶段 %d 往返字段不一致:\n a=%+v\n b=%+v", i, a, b)
		}
		if strings.Join(a.Needs, ",") != strings.Join(b.Needs, ",") {
			t.Fatalf("阶段 %d needs 往返不一致: %+v vs %+v", i, a.Needs, b.Needs)
		}
		if strings.Join(a.When.Branches, ",") != strings.Join(b.When.Branches, ",") ||
			strings.Join(a.When.Events, ",") != strings.Join(b.When.Events, ",") {
			t.Fatalf("阶段 %d when 往返不一致", i)
		}
		if len(a.Jobs) != len(b.Jobs) {
			t.Fatalf("阶段 %d 任务数往返不一致", i)
		}
		for j := range a.Jobs {
			ja, jb := a.Jobs[j], b.Jobs[j]
			if ja.ID != jb.ID || ja.Name != jb.Name || ja.Type != jb.Type || ja.Summary != jb.Summary {
				t.Fatalf("阶段 %d 任务 %d 往返字段不一致:\n a=%+v\n b=%+v", i, j, ja, jb)
			}
			// Config 关键键往返一致。
			for _, k := range []string{"image", "commands", "workDir", "env", "targetEnv"} {
				if asString(ja.Config[k]) != asString(jb.Config[k]) {
					t.Fatalf("阶段 %d 任务 %d config[%q] 往返不一致: %q vs %q", i, j, k, asString(ja.Config[k]), asString(jb.Config[k]))
				}
			}
		}
	}
}

func TestParseEmpty(t *testing.T) {
	if _, err := Parse([]byte("   \n  ")); !errors.Is(err, ErrParse) {
		t.Fatalf("空文档应回 ErrParse, got %v", err)
	}
}

func TestParseUnknownField(t *testing.T) {
	doc := `stages:
  - name: src
    kind: source
    bogusKey: 1
    jobs:
      - name: j
        type: git_source
`
	if _, err := Parse([]byte(doc)); !errors.Is(err, ErrParse) {
		t.Fatalf("未知字段应回 ErrParse, got %v", err)
	}
}

func TestParseBadKind(t *testing.T) {
	doc := `stages:
  - name: src
    kind: source
    jobs:
      - name: j
        type: git_source
  - name: weird
    kind: nonsense
    jobs:
      - name: j2
        type: script
        script:
          image: node:20
          commands: [echo hi]
`
	_, err := Parse([]byte(doc))
	if !errors.Is(err, ErrParse) || !errors.Is(err, pipeline.ErrInvalidStage) {
		t.Fatalf("非法 kind 应回 ErrParse∧ErrInvalidStage, got %v", err)
	}
}

func TestParseMultipleSourceStages(t *testing.T) {
	doc := `stages:
  - name: src1
    kind: source
    jobs: [{name: a, type: git_source}]
  - name: src2
    kind: source
    jobs: [{name: b, type: git_source}]
`
	_, err := Parse([]byte(doc))
	if !errors.Is(err, ErrParse) || !errors.Is(err, pipeline.ErrInvalidStage) {
		t.Fatalf("双 source 应回 ErrParse∧ErrInvalidStage, got %v", err)
	}
}

func TestParseUnknownNeeds(t *testing.T) {
	doc := `stages:
  - name: src
    kind: source
    jobs: [{name: a, type: git_source}]
  - id: b
    name: build
    kind: build
    needs: [nonexistent]
    jobs: [{name: j, type: script, script: {image: node:20, commands: [echo]}}]
`
	_, err := Parse([]byte(doc))
	if !errors.Is(err, ErrParse) || !errors.Is(err, pipeline.ErrInvalidStage) {
		t.Fatalf("needs 引用不存在阶段应回 ErrParse∧ErrInvalidStage, got %v", err)
	}
}

func TestParseCycle(t *testing.T) {
	doc := `stages:
  - name: src
    kind: source
    jobs: [{name: a, type: git_source}]
  - id: x
    name: x
    kind: build
    needs: [y]
    jobs: [{name: j, type: script, script: {image: node:20, commands: [echo]}}]
  - id: y
    name: y
    kind: custom
    needs: [x]
    jobs: [{name: k, type: script, script: {image: node:20, commands: [echo]}}]
`
	_, err := Parse([]byte(doc))
	if !errors.Is(err, ErrParse) || !errors.Is(err, pipeline.ErrInvalidStage) {
		t.Fatalf("成环应回 ErrParse∧ErrInvalidStage, got %v", err)
	}
}

func TestParseEmptyJobName(t *testing.T) {
	doc := `stages:
  - name: src
    kind: source
    jobs:
      - name: "  "
        type: git_source
`
	_, err := Parse([]byte(doc))
	if !errors.Is(err, ErrParse) || !errors.Is(err, pipeline.ErrInvalidJob) {
		t.Fatalf("空任务名应回 ErrParse∧ErrInvalidJob, got %v", err)
	}
}

// TestParseErrorNoSecretLeak 断言:即便 env 里写了疑似 secret 值,解析错误信息也不回显它。
func TestParseErrorNoSecretLeak(t *testing.T) {
	doc := `stages:
  - name: src
    kind: badkind
    jobs:
      - name: j
        type: script
        script:
          image: node:20
          commands: [echo]
          env:
            TOKEN: SUPERSECRET_LEAKMARKER_999
`
	_, err := Parse([]byte(doc))
	if err == nil {
		t.Fatalf("应解析失败")
	}
	if strings.Contains(err.Error(), "LEAKMARKER") {
		t.Fatalf("错误信息泄漏了 env 值: %v", err)
	}
}

// TestMarshalDeterministic 验证同一 spec 序列化两次字节一致(确定性)。
func TestMarshalDeterministic(t *testing.T) {
	cfg, err := Parse([]byte(multiStageFixture))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	a, err := Marshal(cfg.Spec)
	if err != nil {
		t.Fatalf("Marshal a: %v", err)
	}
	b, err := Marshal(cfg.Spec)
	if err != nil {
		t.Fatalf("Marshal b: %v", err)
	}
	if string(a) != string(b) {
		t.Fatalf("序列化非确定性:\n--- a ---\n%s\n--- b ---\n%s", a, b)
	}
}

// TestParseAutoFillsStageID 验证缺省 id 时由领域规范化补 uuid(不报错)。
func TestParseAutoFillsStageID(t *testing.T) {
	doc := `stages:
  - name: 源
    kind: source
    jobs:
      - name: src
        type: git_source
`
	cfg, err := Parse([]byte(doc))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if strings.TrimSpace(cfg.Spec.Stages[0].ID) == "" {
		t.Fatalf("缺省阶段 id 应被补全")
	}
	if strings.TrimSpace(cfg.Spec.Stages[0].Jobs[0].ID) == "" {
		t.Fatalf("缺省任务 id 应被补全")
	}
}
