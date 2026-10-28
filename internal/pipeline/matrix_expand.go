package pipeline

import (
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strings"
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
func ExpandMatrix(stages []Stage) []Stage {
	originals := map[string]Stage{}
	for _, stage := range stages {
		originals[stage.ID] = stage
	}
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

	out := make([]Stage, 0, len(stages))
	for _, st := range stages {
		if len(st.Matrix) == 0 {
			cp := st
			cp.Needs = remapNeeds(st.Needs)
			cp = remapMatrixArtifactSelectors(cp, nil, originals)
			out = append(out, cp)
			continue
		}
		needs := remapNeeds(st.Needs)
		for _, cell := range cartesian(st.Matrix) {
			out = append(out, remapMatrixArtifactSelectors(buildCell(st, cell, needs), cell, originals))
		}
	}
	return out
}

// buildCell 构造一个 cell 子阶段:复制原阶段元信息,jobs 深拷贝并注入 axis 环境变量。
func buildCell(st Stage, cell map[string]string, needs []string) Stage {
	env := make(map[string]string, len(cell))
	for axis, val := range cell {
		env[MatrixEnvKey(axis)] = val
	}
	suffix := cellSuffixFromCell(cell)
	jobIDs := make(map[string]string, len(st.Jobs))
	for _, job := range st.Jobs {
		jobIDs[job.ID] = job.ID + "__" + suffix
	}
	cp := st
	cp.MatrixParentID = st.ID
	cp.ID, cp.Name, cp.Matrix = cellID(st.ID, cell), st.Name+" "+cellLabel(cell), nil
	cp.Needs = append([]string(nil), needs...)
	cp.When.Branches = append([]string(nil), st.When.Branches...)
	cp.When.Events = append([]string(nil), st.When.Events...)
	cp.Jobs = make([]Job, len(st.Jobs))
	for i, job := range st.Jobs {
		cp.Jobs[i] = cloneJobWithMatrixEnv(job, env, suffix)
		cp.Jobs[i].Needs = append([]string(nil), job.Needs...)
		for j, id := range cp.Jobs[i].Needs {
			if scoped, ok := jobIDs[id]; ok {
				cp.Jobs[i].Needs[j] = scoped
			}
		}
		if text, ok := cp.Jobs[i].Config["artifactSource"].(string); ok && text != "" {
			var selector map[string]json.RawMessage
			if json.Unmarshal([]byte(text), &selector) == nil {
				var stageID, jobID string
				_ = json.Unmarshal(selector["stageId"], &stageID)
				_ = json.Unmarshal(selector["jobId"], &jobID)
				if stageID == st.ID {
					if mapped, exists := jobIDs[jobID]; exists {
						selector["stageId"], _ = json.Marshal(cp.ID)
						selector["jobId"], _ = json.Marshal(mapped)
						if data, e := json.Marshal(selector); e == nil {
							cp.Jobs[i].Config["artifactSource"] = string(data)
						}
					}
				}
			}
		}
	}
	cp.Post = append([]PostStep(nil), st.Post...)
	for i := range cp.Post {
		cp.Post[i].Commands = append([]string(nil), st.Post[i].Commands...)
	}
	cp.Services = append([]ServiceSpec(nil), st.Services...)
	for i := range cp.Services {
		cp.Services[i].Env = append([]string(nil), st.Services[i].Env...)
		cp.Services[i].Ports = append([]string(nil), st.Services[i].Ports...)
	}
	return cp
}

// StageDependencies preserves the legacy linear order between original stages,
// while treating a matrix expansion as one parallel group with a join barrier.
// Both the scheduler and frozen recovery compiler use this exact projection.
func StageDependencies(stages []Stage) map[string][]string {
	out := make(map[string][]string, len(stages))
	explicit := false
	for _, st := range stages {
		explicit = explicit || len(st.Needs) > 0
	}
	if explicit {
		for _, st := range stages {
			out[st.ID] = append([]string(nil), st.Needs...)
		}
		return out
	}
	var previous, current []string
	group := ""
	for _, st := range stages {
		id := st.ID
		if st.MatrixParentID != "" {
			id = st.MatrixParentID
		}
		if group != "" && id != group {
			previous = current
			current = nil
		}
		group = id
		out[st.ID] = append([]string(nil), previous...)
		current = append(current, st.ID)
	}
	return out
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

// cloneJobWithMatrixEnv copies validated JSON-compatible configuration,
// including typed maps/slices used by internal callers, before adding cell env.
func cloneJobWithMatrixEnv(jb Job, env map[string]string, suffix string) Job {
	cfg := make(map[string]any, len(jb.Config)+1)
	for k, v := range jb.Config {
		if v == nil {
			cfg[k] = nil
		} else {
			cfg[k] = cloneMatrixValue(reflect.ValueOf(v)).Interface()
		}
	}
	envCopy := make(map[string]string, len(env))
	for k, v := range env {
		envCopy[k] = v
	}
	cfg[matrixEnvConfigKey] = envCopy
	jb.Config = cfg
	jb.Needs = append([]string(nil), jb.Needs...)
	if suffix != "" {
		jb.ID += "__" + suffix
	}
	return jb
}
func cloneMatrixValue(v reflect.Value) reflect.Value {
	switch v.Kind() {
	case reflect.Interface:
		if v.IsNil() {
			return v
		}
		cp := reflect.New(v.Type()).Elem()
		cp.Set(cloneMatrixValue(v.Elem()))
		return cp
	case reflect.Map:
		if v.IsNil() {
			return v
		}
		cp := reflect.MakeMapWithSize(v.Type(), v.Len())
		it := v.MapRange()
		for it.Next() {
			cp.SetMapIndex(it.Key(), cloneMatrixValue(it.Value()))
		}
		return cp
	case reflect.Slice:
		if v.IsNil() {
			return v
		}
		cp := reflect.MakeSlice(v.Type(), v.Len(), v.Len())
		for i := 0; i < v.Len(); i++ {
			cp.Index(i).Set(cloneMatrixValue(v.Index(i)))
		}
		return cp
	case reflect.Array:
		cp := reflect.New(v.Type()).Elem()
		for i := 0; i < v.Len(); i++ {
			cp.Index(i).Set(cloneMatrixValue(v.Index(i)))
		}
		return cp
	default:
		return v
	}
}

// cartesian 返回矩阵所有轴的笛卡尔积(每个 cell = axis→值 的一种取值组合)。
// 轴序按字典序固定(MatrixAxisOrder),保证 cell 顺序与命名可复现。
func cartesian(matrix map[string][]string) []map[string]string {
	axes := MatrixAxisOrder(matrix)
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

// MatrixCells returns raw axes in the same order used by ExpandMatrix.
func MatrixCells(matrix map[string][]string) []map[string]string { return cartesian(matrix) }

// MatrixCellID returns the existing display/execution ID; recovery identity uses raw axes.
func MatrixCellID(stageID string, cell map[string]string) string { return cellID(stageID, cell) }
