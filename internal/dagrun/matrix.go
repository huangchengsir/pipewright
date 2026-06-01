package dagrun

import (
	"fmt"
	"sort"
	"strings"

	"github.com/huangchengsir/pipewright/internal/pipeline"
)

// matrix.go 是矩阵构建(P1)的纯调度层展开:把一个声明了 Matrix 的阶段展开成笛卡尔积的多个并行
// cell 子阶段。对标 GHA matrix / GitLab parallel:matrix / Jenkins matrix。
//
// 设计(不改 dag/dagrun 的并发与失败语义):
//   - 展开发生在 BuildGraph 之前(ExpandMatrix),产出一份「展开后的阶段列表」,既喂给 BuildGraph
//     建图,也作为 Run 里 stageByID 的权威阶段集——下游调度、上报、执行全走展开后的 cell。
//   - 每个 cell = 原阶段的副本:同 Kind/When/Gate/AllowFailure,jobs 深拷贝并把 axis 值以
//     `MATRIX_<AXIS>` 注入各 script 类 job 的 Config["__matrixEnv"](执行器既有 scriptStepFromJob 消费)。
//   - cell id 形如 `<stageID>__<sortedAxisKey>`,确定性可复现(轴名字典序)。
//   - needs 重映射:下游若 need 一个 matrix 阶段 M,则改为 need M 的**全部 cell**(下游等所有 cell 完成);
//     cell 自身的 needs 同样把指向 matrix 上游的引用展开为该上游的全部 cell。
//   - cell 间并行 / 各自成败 / 失败按 allowFailure+needs 传播,全部复用既有 dag.Schedule(零改并发语义)。
//   - 空 Matrix → 阶段原样保留(行为不变)。
//
// matrixEnvConfigKey 是注入 cell job.Config 的内部键:值为 map[string]string(axis 环境变量),
// 由 build.scriptStepFromJob 读取并并入 step.Env。下划线前缀标记「调度层合成、非用户配置」。
const matrixEnvConfigKey = "__matrixEnv"

// ExpandMatrix 把阶段列表里所有声明了 Matrix 的阶段展开为并行 cell 子阶段,并重映射 needs。
// 无任何 matrix 阶段时原样返回(零开销)。假定 stages 已通过 pipeline 校验(轴/cell 数上限等)。
func ExpandMatrix(stages []pipeline.Stage) []pipeline.Stage {
	hasMatrix := false
	for _, st := range stages {
		if len(st.Matrix) > 0 {
			hasMatrix = true
			break
		}
	}
	if !hasMatrix {
		return stages
	}

	// 原 stageID → 展开后承接其「下游依赖」的 ID 列表:
	//   非 matrix 阶段 → [自身 id];matrix 阶段 → [全部 cell id]。
	expandedIDs := make(map[string][]string, len(stages))
	for _, st := range stages {
		if len(st.Matrix) == 0 {
			expandedIDs[st.ID] = []string{st.ID}
			continue
		}
		ids := make([]string, 0)
		for _, cell := range cartesian(st.Matrix) {
			ids = append(ids, cellID(st.ID, cell))
		}
		expandedIDs[st.ID] = ids
	}

	remapNeeds := func(needs []string) []string {
		if len(needs) == 0 {
			return nil
		}
		out := make([]string, 0, len(needs))
		for _, n := range needs {
			if ids, ok := expandedIDs[n]; ok {
				out = append(out, ids...)
			} else {
				out = append(out, n) // 未知依赖:原样保留(dag.New 会兜底报错)
			}
		}
		return out
	}

	out := make([]pipeline.Stage, 0, len(stages))
	for _, st := range stages {
		if len(st.Matrix) == 0 {
			cp := st
			cp.Needs = remapNeeds(st.Needs)
			out = append(out, cp)
			continue
		}
		needs := remapNeeds(st.Needs)
		for _, cell := range cartesian(st.Matrix) {
			out = append(out, buildCell(st, cell, needs))
		}
	}
	return out
}

// buildCell 构造一个 cell 子阶段:复制原阶段元信息,jobs 深拷贝并注入 axis 环境变量。
func buildCell(st pipeline.Stage, cell map[string]string, needs []string) pipeline.Stage {
	env := make(map[string]string, len(cell))
	for axis, val := range cell {
		env[pipeline.MatrixEnvKey(axis)] = val
	}
	jobs := make([]pipeline.Job, 0, len(st.Jobs))
	for _, jb := range st.Jobs {
		jobs = append(jobs, cloneJobWithMatrixEnv(jb, env, cellSuffixFromCell(cell)))
	}
	return pipeline.Stage{
		ID:           cellID(st.ID, cell),
		Name:         st.Name + " " + cellLabel(cell),
		Kind:         st.Kind,
		Needs:        needs,
		AllowFailure: st.AllowFailure,
		When:         st.When,
		Gate:         st.Gate,
		// Matrix 故意清空:cell 已是展开后的具体实例,不可再被二次展开。
		Jobs: jobs,
	}
}

// cellLabel 返回供展示的 cell 标签,如 `[go=1.21, os=linux]`(轴字典序)。
func cellLabel(cell map[string]string) string {
	axes := make([]string, 0, len(cell))
	for a := range cell {
		axes = append(axes, a)
	}
	sort.Strings(axes)
	parts := make([]string, 0, len(axes))
	for _, a := range axes {
		parts = append(parts, fmt.Sprintf("%s=%s", a, cell[a]))
	}
	return "[" + strings.Join(parts, ", ") + "]"
}

// cloneJobWithMatrixEnv 深拷贝 job(含 Config 浅拷贝足够——只新增键),写入 __matrixEnv,
// 并给 job id 追加 cell 后缀以保证全局唯一(同阶段多 cell 的同名 job 不撞 id)。
func cloneJobWithMatrixEnv(jb pipeline.Job, env map[string]string, suffix string) pipeline.Job {
	cfg := make(map[string]any, len(jb.Config)+1)
	for k, v := range jb.Config {
		cfg[k] = v
	}
	// 拷贝一份 env,避免多 job 共享同一 map 引用。
	envCopy := make(map[string]string, len(env))
	for k, v := range env {
		envCopy[k] = v
	}
	cfg[matrixEnvConfigKey] = envCopy
	jb.Config = cfg
	if suffix != "" {
		jb.ID = jb.ID + "__" + suffix
	}
	return jb
}

// cartesian 返回矩阵所有轴的笛卡尔积(每个 cell = axis→值 的一种取值组合)。
// 轴序按字典序固定(MatrixAxisOrder),保证 cell 顺序与命名可复现。
func cartesian(matrix map[string][]string) []map[string]string {
	axes := pipeline.MatrixAxisOrder(matrix)
	cells := []map[string]string{{}}
	for _, axis := range axes {
		vals := matrix[axis]
		next := make([]map[string]string, 0, len(cells)*len(vals))
		for _, base := range cells {
			for _, v := range vals {
				nc := make(map[string]string, len(base)+1)
				for k, ov := range base {
					nc[k] = ov
				}
				nc[axis] = v
				next = append(next, nc)
			}
		}
		cells = next
	}
	return cells
}

// cellID 返回 cell 子阶段的全局唯一 id(`<原阶段id>__<cellSuffix>`)。
func cellID(stageID string, cell map[string]string) string {
	return stageID + "__" + cellSuffixFromCell(cell)
}

// cellSuffixFromCell 不依赖原 matrix(cell 自身即含全部轴),按轴名字典序连接。
func cellSuffixFromCell(cell map[string]string) string {
	axes := make([]string, 0, len(cell))
	for a := range cell {
		axes = append(axes, a)
	}
	sort.Strings(axes)
	parts := make([]string, 0, len(axes))
	for _, a := range axes {
		parts = append(parts, sanitizeIDPart(a)+"-"+sanitizeIDPart(cell[a]))
	}
	return strings.Join(parts, "_")
}

// sanitizeIDPart 把任意轴值清洗成可安全嵌入 id 的片段(非字母/数字/下划线/连字符/点 → `-`)。
// 允许点(.):版本值常含点(如 1.21),保留更可读。
func sanitizeIDPart(s string) string {
	s = strings.TrimSpace(s)
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch {
		case (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' || r == '-' || r == '.':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	if b.Len() == 0 {
		return "x"
	}
	return b.String()
}
