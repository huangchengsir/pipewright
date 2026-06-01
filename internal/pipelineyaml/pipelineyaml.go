// Package pipelineyaml 是「流水线即代码」(`.pipewright.yml`)与领域模型 pipeline.Spec
// 之间的解析/序列化层(FR-8-12)。
//
// 它把一份人类友好的声明式 YAML 文档(stages → jobs,带 needs/when/gate/allowFailure 与
// 脚本步骤 image/commands/env/workdir)解析为 pipeline.Spec,并可反向序列化回 YAML。
// 解析后**复用 pipeline 包的现有校验**(经 pipeline.NormalizeSpec:阶段名/kind 枚举/任务名/
// type 非空、id 全局唯一、恰一个 source 阶段、needs 存在性/自指/环检测),不另起一套规则。
//
// 设计取向:
//   - 与画布持久化的 JSON 解耦——本包只面向「仓库里手写的 YAML」这一表面,字段顺序确定、
//     省略空字段,读起来像 Jenkins/云效 的流水线文件。
//   - job 的脚本步骤用一个嵌套 `script:` 块表达(image/commands/env/workdir),解析时摊平进
//     pipeline.Job.Config 的字符串 KV——**严格对齐画布 jobConfigSchema 的扁平约定**:
//     image→image、commands→多行命令拼成单个换行连接串、workdir→workDir(驼峰)、env→JSON 对象串;
//     另允许 `config:` 原样透传任意字符串 KV(两者可并存,script 优先级更高、键冲突时覆盖)。
//   - 错误信息人读、含阶段/任务定位线索,但**绝不回显任何 secret 明文**(secret env 仅以
//     credentialId 引用形式出现;本层不接触保险库)。
//
// 本包无 init() 副作用、无包级重对象。
package pipelineyaml

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/huangchengsir/pipewright/internal/pipeline"
	yaml "gopkg.in/yaml.v3"
)

// SchemaVersion 是当前支持的 `.pipewright.yml` 顶层 version。缺省视为 1(向后兼容)。
const SchemaVersion = 1

// ErrParse 表示 YAML 文档无法解析为合法流水线(语法错误 / 结构错误 / 校验失败)。
// 调用方据此回 422;Unwrap 链上可能挂 pipeline.ErrInvalidStage 等领域错误以便精确映射。
var ErrParse = errors.New("pipelineyaml: invalid .pipewright.yml")

// 脚本步骤在 Job.Config 内的约定键(**与前端 jobConfigSchema 的 script 任务严格对齐**)。
//   - configKeyImage:构建镜像(单值字符串)。
//   - configKeyCommands:多行命令拼成单个换行连接串(画布 textarea 即如此存,保序)。
//   - configKeyEnv:非 secret 明文 env(JSON 对象编码进单值)。
//   - configKeyWorkDir:容器内相对工作目录(驼峰,画布约定)。
const (
	configKeyImage    = "image"
	configKeyCommands = "commands"
	configKeyEnv      = "env"
	configKeyWorkDir  = "workDir"
)

// ---- YAML 文档形状(确定性字段顺序;仅用于解析/序列化,与持久化 JSON 解耦) ----

type doc struct {
	Version int         `yaml:"version,omitempty"`
	Stages  []stageNode `yaml:"stages"`
}

type stageNode struct {
	ID           string              `yaml:"id,omitempty"`
	Name         string              `yaml:"name"`
	Kind         string              `yaml:"kind"`
	Needs        []string            `yaml:"needs,omitempty"`
	AllowFailure bool                `yaml:"allowFailure,omitempty"`
	Gate         bool                `yaml:"gate,omitempty"`
	When         *whenNode           `yaml:"when,omitempty"`
	Matrix       map[string][]string `yaml:"matrix,omitempty"`
	Post         []postNode          `yaml:"post,omitempty"`
	Jobs         []jobNode           `yaml:"jobs,omitempty"`
}

type whenNode struct {
	Branches []string `yaml:"branches,omitempty"`
	Events   []string `yaml:"events,omitempty"`
}

// postNode 是阶段后置步骤的 YAML 块(P1 · 对标 Jenkins post)。
type postNode struct {
	Condition string   `yaml:"condition,omitempty"`
	Image     string   `yaml:"image"`
	Commands  []string `yaml:"commands,omitempty"`
	WorkDir   string   `yaml:"workDir,omitempty"`
}

type jobNode struct {
	ID      string            `yaml:"id,omitempty"`
	Name    string            `yaml:"name"`
	Type    string            `yaml:"type"`
	Summary string            `yaml:"summary,omitempty"`
	Script  *scriptNode       `yaml:"script,omitempty"`
	Config  map[string]string `yaml:"config,omitempty"`
}

// scriptNode 是脚本步骤的人类友好嵌套块(对标 Jenkins sh / 云效自定义命令)。
type scriptNode struct {
	Image    string            `yaml:"image,omitempty"`
	Commands []string          `yaml:"commands,omitempty"`
	Env      map[string]string `yaml:"env,omitempty"`
	WorkDir  string            `yaml:"workdir,omitempty"`
}

// Parse 把 `.pipewright.yml` 文档字节解析为已校验、已规范化的 pipeline.Config。
//
// 流程:严格 YAML 解码(未知字段报错)→ 摊平 script 块进 Job.Config → 组装 pipeline.Spec →
// 复用 pipeline.NormalizeSpec 做全部领域校验 → 经 pipeline.RenderYAML 回填规范 YAML。
// 任何失败都包成 ErrParse(领域校验错误经 %w 挂在链上,便于上层精确映射状态码)。
func Parse(data []byte) (pipeline.Config, error) {
	if len(strings.TrimSpace(string(data))) == 0 {
		return pipeline.Config{}, fmt.Errorf("%w: 文档为空", ErrParse)
	}

	var d doc
	dec := yaml.NewDecoder(strings.NewReader(string(data)))
	dec.KnownFields(true) // 拒绝未知字段,避免静默吞掉拼错的键
	if err := dec.Decode(&d); err != nil {
		return pipeline.Config{}, fmt.Errorf("%w: YAML 语法/结构错误: %s", ErrParse, sanitizeYAMLErr(err))
	}

	if d.Version != 0 && d.Version != SchemaVersion {
		return pipeline.Config{}, fmt.Errorf("%w: 不支持的 version %d(当前支持 %d)", ErrParse, d.Version, SchemaVersion)
	}
	if len(d.Stages) == 0 {
		return pipeline.Config{}, fmt.Errorf("%w: 至少需要一个 stage", ErrParse)
	}

	stages := make([]pipeline.Stage, 0, len(d.Stages))
	for si, sn := range d.Stages {
		stageLabel := stageLabel(si, sn)
		jobs := make([]pipeline.Job, 0, len(sn.Jobs))
		for ji, jn := range sn.Jobs {
			cfg, err := jobConfig(jn)
			if err != nil {
				return pipeline.Config{}, fmt.Errorf("%w: %s 的任务 #%d(%s): %s", ErrParse, stageLabel, ji+1, jobLabel(jn), err)
			}
			jobs = append(jobs, pipeline.Job{
				ID:      strings.TrimSpace(jn.ID),
				Name:    jn.Name,
				Type:    jn.Type,
				Summary: jn.Summary,
				Config:  cfg,
			})
		}

		var when pipeline.When
		if sn.When != nil {
			when = pipeline.When{Branches: sn.When.Branches, Events: sn.When.Events}
		}
		stages = append(stages, pipeline.Stage{
			ID:           strings.TrimSpace(sn.ID),
			Name:         sn.Name,
			Kind:         sn.Kind,
			Needs:        sn.Needs,
			AllowFailure: sn.AllowFailure,
			When:         when,
			Gate:         sn.Gate,
			Matrix:       sn.Matrix,
			Post:         postsFromNodes(sn.Post),
			Jobs:         jobs,
		})
	}

	// 复用领域校验 + 规范化(单一事实来源:与画布 PUT 走同一套规则)。
	// 用 %w 双层挂链(ErrParse + 领域 ErrInvalidStage/ErrInvalidJob/...),便于上层精确映射状态码。
	spec, err := pipeline.NormalizeSpec(pipeline.Spec{Stages: stages})
	if err != nil {
		return pipeline.Config{}, fmt.Errorf("%w: %w", ErrParse, err)
	}

	renderedYAML, err := pipeline.RenderYAML(spec)
	if err != nil {
		return pipeline.Config{}, fmt.Errorf("%w: 渲染 YAML 失败", ErrParse)
	}
	return pipeline.Config{Spec: spec, YAML: renderedYAML, Status: pipeline.StatusDraft}, nil
}

// Marshal 把 pipeline.Spec 序列化为人类友好的 `.pipewright.yml` 文档字节。
// 与 Parse 往返一致:script 块从 Job.Config 的约定键重建;非约定键落回 config: 透传。
func Marshal(spec pipeline.Spec) ([]byte, error) {
	d := doc{Version: SchemaVersion, Stages: make([]stageNode, 0, len(spec.Stages))}
	for _, st := range spec.Stages {
		sn := stageNode{
			ID:           strings.TrimSpace(st.ID),
			Name:         st.Name,
			Kind:         st.Kind,
			Needs:        nonEmpty(st.Needs),
			AllowFailure: st.AllowFailure,
			Gate:         st.Gate,
			Matrix:       st.Matrix,
			Post:         postsToNodes(st.Post),
		}
		if !st.When.IsEmpty() {
			sn.When = &whenNode{Branches: nonEmpty(st.When.Branches), Events: nonEmpty(st.When.Events)}
		}
		for _, jb := range st.Jobs {
			jn := jobNode{
				ID:      strings.TrimSpace(jb.ID),
				Name:    jb.Name,
				Type:    jb.Type,
				Summary: jb.Summary,
			}
			script, rest := splitConfig(jb.Config)
			jn.Script = script
			if len(rest) > 0 {
				jn.Config = rest
			}
			sn.Jobs = append(sn.Jobs, jn)
		}
		d.Stages = append(d.Stages, sn)
	}
	out, err := yaml.Marshal(d)
	if err != nil {
		return nil, fmt.Errorf("pipelineyaml: marshal: %w", err)
	}
	return out, nil
}

// jobConfig 把一个 YAML job 节点的 script 块 + config 透传合并为 pipeline.Job.Config
// (字符串 KV;commands/env 以 JSON 编码进单值,与画布 jobConfigSchema 对齐)。
func jobConfig(jn jobNode) (map[string]any, error) {
	cfg := map[string]any{}
	// 先放 config: 原样透传(字符串 KV),再让 script 块覆盖约定键。
	for k, v := range jn.Config {
		k = strings.TrimSpace(k)
		if k == "" {
			return nil, errors.New("config 键不能为空")
		}
		cfg[k] = v
	}
	if jn.Script != nil {
		s := jn.Script
		if img := strings.TrimSpace(s.Image); img != "" {
			cfg[configKeyImage] = img
		}
		if wd := strings.TrimSpace(s.WorkDir); wd != "" {
			cfg[configKeyWorkDir] = wd
		}
		if len(s.Commands) > 0 {
			cmds := make([]string, 0, len(s.Commands))
			for _, c := range s.Commands {
				if strings.TrimSpace(c) != "" {
					cmds = append(cmds, c)
				}
			}
			if len(cmds) > 0 {
				// 画布把多行命令存为单个换行连接串(textarea);此处对齐,保序拼接。
				cfg[configKeyCommands] = strings.Join(cmds, "\n")
			}
		}
		if len(s.Env) > 0 {
			cfg[configKeyEnv] = encodeJSON(s.Env)
		}
	}
	if len(cfg) == 0 {
		return cfg, nil
	}
	return cfg, nil
}

// splitConfig 从 Job.Config 里把脚本步骤的约定键重建为 scriptNode,其余键作为透传 config 返回。
// 用于 Marshal 往返;commands/env 若为 JSON 编码则解码回结构,否则退化为透传(不丢数据)。
func splitConfig(cfg map[string]any) (*scriptNode, map[string]string) {
	rest := map[string]string{}
	var script scriptNode
	hasScript := false

	for k, v := range cfg {
		sv := asString(v)
		switch k {
		case configKeyImage:
			if sv != "" {
				script.Image = sv
				hasScript = true
			}
		case configKeyWorkDir:
			if sv != "" {
				script.WorkDir = sv
				hasScript = true
			}
		case configKeyCommands:
			if sv != "" {
				script.Commands = strings.Split(sv, "\n")
				hasScript = true
			}
		case configKeyEnv:
			if env, ok := decodeStringMap(sv); ok {
				script.Env = env
				hasScript = true
			} else if sv != "" {
				rest[k] = sv
			}
		default:
			rest[k] = sv
		}
	}
	if !hasScript {
		return nil, rest
	}
	return &script, rest
}

// stageLabel / jobLabel 给错误信息提供人读定位(不含 secret)。
func stageLabel(idx int, sn stageNode) string {
	if name := strings.TrimSpace(sn.Name); name != "" {
		return fmt.Sprintf("阶段「%s」", name)
	}
	if id := strings.TrimSpace(sn.ID); id != "" {
		return fmt.Sprintf("阶段「%s」", id)
	}
	return fmt.Sprintf("阶段 #%d", idx+1)
}

func jobLabel(jn jobNode) string {
	if name := strings.TrimSpace(jn.Name); name != "" {
		return name
	}
	if id := strings.TrimSpace(jn.ID); id != "" {
		return id
	}
	return "<未命名>"
}

// nonEmpty 把 nil/空切片归一为 nil(让 omitempty 生效),否则原样返回。
func nonEmpty(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	return in
}

// postsFromNodes / postsToNodes 在 YAML postNode 与领域 pipeline.PostStep 间转换(校验由 NormalizeSpec 统一做)。
func postsFromNodes(in []postNode) []pipeline.PostStep {
	if len(in) == 0 {
		return nil
	}
	out := make([]pipeline.PostStep, 0, len(in))
	for _, pn := range in {
		out = append(out, pipeline.PostStep{Condition: pn.Condition, Image: pn.Image, Commands: pn.Commands, WorkDir: pn.WorkDir})
	}
	return out
}

func postsToNodes(in []pipeline.PostStep) []postNode {
	if len(in) == 0 {
		return nil
	}
	out := make([]postNode, 0, len(in))
	for _, ps := range in {
		out = append(out, postNode{Condition: ps.Condition, Image: ps.Image, Commands: ps.Commands, WorkDir: ps.WorkDir})
	}
	return out
}

// asString 把 Job.Config 的 any 值尽量转字符串(画布存的恒为字符串;JSON 回读亦如此)。
func asString(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case nil:
		return ""
	default:
		return fmt.Sprintf("%v", t)
	}
}

// encodeJSON 把 env map 编码为确定性 JSON 串(键排序,跨次稳定),供存入 Job.Config 单值。
func encodeJSON(m map[string]string) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	b.WriteByte('{')
	for i, k := range keys {
		if i > 0 {
			b.WriteByte(',')
		}
		b.Write(mustJSON(k))
		b.WriteByte(':')
		b.Write(mustJSON(m[k]))
	}
	b.WriteByte('}')
	return b.String()
}

// mustJSON 把字符串安全编码为 JSON 字面量(转义引号/控制符)。
func mustJSON(s string) []byte {
	out, err := json.Marshal(s)
	if err != nil {
		// string 编码不会失败;兜底退化为空串字面量。
		return []byte(`""`)
	}
	return out
}

// decodeStringMap 把 Job.Config 里的 JSON 对象单值解码回 map[string]string;非 JSON 对象返回 ok=false。
func decodeStringMap(s string) (map[string]string, bool) {
	s = strings.TrimSpace(s)
	if s == "" || s[0] != '{' {
		return nil, false
	}
	var m map[string]string
	if err := json.Unmarshal([]byte(s), &m); err != nil {
		return nil, false
	}
	return m, true
}

// sanitizeYAMLErr 收敛 yaml 解码错误为一行人读串(去多行/去内部前缀,不外泄实现细节)。
func sanitizeYAMLErr(err error) string {
	msg := err.Error()
	msg = strings.TrimPrefix(msg, "yaml: ")
	if i := strings.IndexByte(msg, '\n'); i >= 0 {
		msg = msg[:i]
	}
	return strings.TrimSpace(msg)
}
