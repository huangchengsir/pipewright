// Package qualitygate 是质量门禁的纯评估层(Epic 8 · FR-8-6)。
//
// 输入:一份测试报告汇总(testreport.Summary)+ 一组门禁阈值(Thresholds)。
// 输出:门禁裁决(Verdict:通过 / 阻断 + 人读原因)。阻断时,调用方(阶段执行器)据此
// 令该阶段失败,从而**阻断下游部署阶段**——复用既有「阶段失败 → 下游不执行」语义,不另造机制。
//
// 本包零 I/O、零依赖运行时,便于穷举单测;原因串不含 secret(只含计数/阈值数字)。
package qualitygate

import (
	"fmt"
	"strings"

	"github.com/huangchengsir/pipewright/internal/testreport"
)

// Thresholds 是质量门禁阈值(取自 script job 的 config)。
//
//   - MaxFailures   : 允许的最大失败用例数(含 errors);超过即阻断。-1 = 不检查失败数。
//   - MinCoverage   : 要求的最小行覆盖率百分比(0~100);低于即阻断。-1 = 不检查覆盖率。
//
// 任一阈值为「不检查」哨兵(-1)时跳过该维度;两维都不检查 → 门禁恒通过(等于无门禁)。
type Thresholds struct {
	MaxFailures int
	MinCoverage float64
}

// NoCheck 是「该维度不检查」的哨兵值。
const NoCheck = -1

// Enabled 报告是否声明了任一门禁维度(否则评估恒通过,可省略门禁展示)。
func (t Thresholds) Enabled() bool {
	return t.MaxFailures != NoCheck || t.MinCoverage != float64(NoCheck)
}

// Verdict 是门禁裁决结果。
//
//   - Passed     : 是否通过(未声明任何阈值 → 视为通过)。
//   - Violations : 未通过的具体原因(人读;通过时为空)。
type Verdict struct {
	Passed     bool
	Violations []string
}

// Reason 返回违规原因的单行汇总(通过时为空串)。不含 secret(仅数字)。
func (v Verdict) Reason() string {
	return strings.Join(v.Violations, "; ")
}

// Evaluate 据阈值评估一份测试报告,返回裁决。
//
// 失败数检查:Summary.Failed > MaxFailures → 违规。
// 覆盖率检查:仅当报告提供了覆盖率(Coverage != CoverageUnknown);若声明了 MinCoverage
// 但报告**未提供**覆盖率 → 视为违规(声明了要求却拿不到证据,宁阻断不放过)。
func Evaluate(s testreport.Summary, t Thresholds) Verdict {
	v := Verdict{Passed: true}

	if t.MaxFailures != NoCheck && s.Failed > t.MaxFailures {
		v.Passed = false
		v.Violations = append(v.Violations, fmt.Sprintf(
			"失败用例 %d 个,超过门禁上限 %d", s.Failed, t.MaxFailures))
	}

	if t.MinCoverage != float64(NoCheck) {
		if s.Coverage == testreport.CoverageUnknown {
			v.Passed = false
			v.Violations = append(v.Violations, fmt.Sprintf(
				"门禁要求覆盖率 ≥ %.1f%%,但本次未产出覆盖率报告", t.MinCoverage))
		} else if s.Coverage < t.MinCoverage {
			v.Passed = false
			v.Violations = append(v.Violations, fmt.Sprintf(
				"覆盖率 %.1f%%,低于门禁要求 %.1f%%", s.Coverage, t.MinCoverage))
		}
	}

	return v
}
