package pipeline

import (
	"fmt"
	"sort"
	"strings"
)

// stage_matrix.go 是矩阵构建(P1,对标 GHA matrix / GitLab parallel:matrix / Jenkins matrix)的领域规则:
// 一个阶段可声明多维 axes(axisName→值列表),调度时展开成笛卡尔积的多个并行 cell(各跑该阶段 jobs)。
//
// 本文件只管「校验 + 规范化 + 命名」:
//   - 轴名合法(标识符:字母/数字/下划线,首字符非数字)、轴名唯一(map 天然)、值非空且去重;
//   - 维度数 ≤ MatrixMaxAxes、cell 数(笛卡尔积) ≤ MatrixMaxCells —— 防组合爆炸;
//   - axis 值注入容器的环境变量键 = `MATRIX_<大写轴名>`(MatrixEnvKey)。
//
// 真正的「1 stage → N cell stage」展开在调度层(dagrun.ExpandMatrix),不改 dag/dagrun 的并发与失败语义。

const (
	// MatrixMaxAxes 是单阶段矩阵维度(轴)数上限。
	MatrixMaxAxes = 8
	// MatrixMaxCells 是单阶段矩阵展开后的 cell(笛卡尔积)数上限,防组合爆炸。
	MatrixMaxCells = 50
	// MatrixEnvPrefix 是注入 cell 容器的 axis 环境变量前缀(键 = 前缀 + 大写轴名)。
	MatrixEnvPrefix = "MATRIX_"
)

// MatrixEnvKey 返回 axis 注入容器的环境变量键(`MATRIX_<大写轴名>`)。
// 轴名已在校验期保证为合法标识符,故大写即安全的 env 键。
func MatrixEnvKey(axis string) string {
	return MatrixEnvPrefix + strings.ToUpper(strings.TrimSpace(axis))
}

// normalizeMatrix 校验并规范化阶段矩阵声明。
//   - 空(nil / 全空轴)→ 返回 nil(阶段行为不变,不展开);
//   - 轴名非法 / 值全空 / 维度超限 / cell 数超限 → ErrInvalidStage(附简短原因)。
//
// 规范化:trim 轴名与值、剔空值、同轴内去重(保留首现序)。
func normalizeMatrix(in map[string][]string) (map[string][]string, error) {
	if len(in) == 0 {
		return nil, nil
	}
	out := make(map[string][]string, len(in))
	for rawAxis, rawVals := range in {
		axis := strings.TrimSpace(rawAxis)
		if axis == "" {
			// 空轴名 + 空值:整体视作未声明,跳过;非空值却无轴名:报错(防静默丢值)。
			if len(dedupTrim(rawVals)) == 0 {
				continue
			}
			return nil, fmt.Errorf("%w: matrix axis name must not be empty", ErrInvalidStage)
		}
		if !isValidMatrixAxis(axis) {
			return nil, fmt.Errorf("%w: invalid matrix axis name %q (须为标识符:字母/下划线起,后接字母/数字/下划线)", ErrInvalidStage, axis)
		}
		vals := dedupTrim(rawVals)
		if len(vals) == 0 {
			return nil, fmt.Errorf("%w: matrix axis %q must have at least one value", ErrInvalidStage, axis)
		}
		out[axis] = vals
	}
	if len(out) == 0 {
		return nil, nil
	}
	if len(out) > MatrixMaxAxes {
		return nil, fmt.Errorf("%w: matrix has %d axes, exceeds limit %d", ErrInvalidStage, len(out), MatrixMaxAxes)
	}
	cells := 1
	for _, vals := range out {
		cells *= len(vals)
	}
	if cells > MatrixMaxCells {
		return nil, fmt.Errorf("%w: matrix expands to %d cells, exceeds limit %d", ErrInvalidStage, cells, MatrixMaxCells)
	}
	return out, nil
}

// isValidMatrixAxis 报告轴名是否为合法标识符(首字符字母/下划线,后接字母/数字/下划线)。
func isValidMatrixAxis(s string) bool {
	if s == "" {
		return false
	}
	for i, r := range s {
		isLetter := (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || r == '_'
		isDigit := r >= '0' && r <= '9'
		if i == 0 {
			if !isLetter {
				return false
			}
			continue
		}
		if !isLetter && !isDigit {
			return false
		}
	}
	return true
}

// MatrixAxisOrder 返回矩阵轴名的确定性序(字典序),供展开 cell 时稳定笛卡尔积与命名。
// map 迭代序不确定,展开必须经此取序以保证 cell id / 注入 env 的可复现性。
func MatrixAxisOrder(matrix map[string][]string) []string {
	axes := make([]string, 0, len(matrix))
	for a := range matrix {
		axes = append(axes, a)
	}
	sort.Strings(axes)
	return axes
}
