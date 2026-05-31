// Package testreport 解析测试报告(JUnit XML)与代码覆盖率(Cobertura XML),产出汇总
// (通过/失败/跳过计数 + 可选覆盖率%),供「测试报告展示 + 质量门禁」消费(Epic 8 · FR-8-6)。
//
// 设计取舍(不过度扩张):
//   - 测试报告:仅支持 JUnit XML(行业事实标准;pytest/jest/go-junit-report/maven-surefire 皆可产)。
//     聚合 <testsuites>/<testsuite> 的 tests/failures/errors/skipped/time 计数,不深挖单条用例。
//   - 覆盖率:仅支持 Cobertura XML 的根 line-rate(0~1 → 百分比;jacoco/gocover-cobertura/coverage.py
//     皆可产 Cobertura)。lcov/jacoco 原生格式本期不支持(文档已注明)。
//
// 解析铁律:报告内容是测试输出,可能含路径/堆栈,但**不含 secret 责任在本层**——调用方在
// 落库/出网前仍走既有 per-run Masker;本层只把 XML 形状解析成数字汇总,不回显原文。
package testreport

import (
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// ErrEmptyReport 表示报告内容为空(无可解析的 testsuite)。
var ErrEmptyReport = errors.New("testreport: empty or no testsuite found")

// Summary 是一份测试报告的聚合结果(展示 + 门禁共用)。
//
//   - Total/Passed/Failed/Skipped:用例计数(Passed = Total - Failed - Skipped,非负)。
//   - DurationSeconds:套件总耗时(秒;无则 0)。
//   - Coverage:行覆盖率百分比(0~100;-1 表示「未提供覆盖率」)。
type Summary struct {
	Total           int
	Passed          int
	Failed          int
	Skipped         int
	DurationSeconds float64
	Coverage        float64
}

// CoverageUnknown 是 Summary.Coverage 的「未提供」哨兵值。
const CoverageUnknown = -1.0

// junitTestSuites 对应 JUnit XML 的 <testsuites> 根(部分产物直接以 <testsuite> 为根)。
type junitTestSuites struct {
	XMLName  xml.Name         `xml:"testsuites"`
	Tests    *int             `xml:"tests,attr"`
	Failures *int             `xml:"failures,attr"`
	Errors   *int             `xml:"errors,attr"`
	Skipped  *int             `xml:"skipped,attr"`
	Time     string           `xml:"time,attr"`
	Suites   []junitTestSuite `xml:"testsuite"`
}

// junitTestSuite 对应单个 <testsuite>。
type junitTestSuite struct {
	XMLName  xml.Name `xml:"testsuite"`
	Tests    int      `xml:"tests,attr"`
	Failures int      `xml:"failures,attr"`
	Errors   int      `xml:"errors,attr"`
	Skipped  int      `xml:"skipped,attr"`
	Time     string   `xml:"time,attr"`
}

// ParseJUnit 解析 JUnit XML 字节流为 Summary(不含覆盖率;Coverage = CoverageUnknown)。
//
// 兼容两种根形态:<testsuites> 聚合根、或直接 <testsuite> 单套件根。
// 聚合规则:
//   - 优先用 <testsuites> 根属性的 tests/failures/errors/skipped(若产物给了);
//   - 否则把各 <testsuite> 的计数逐项相加。
//   - failures + errors 统一计入 Failed(errors = 异常/未捕获失败,门禁同等看待)。
//
// 无任何 testsuite → ErrEmptyReport;XML 非法 → 解析错误。
func ParseJUnit(data []byte) (Summary, error) {
	trimmed := strings.TrimSpace(string(data))
	if trimmed == "" {
		return Summary{}, ErrEmptyReport
	}

	// 先按 <testsuites> 根尝试。
	var suites junitTestSuites
	if err := xml.Unmarshal([]byte(trimmed), &suites); err == nil && suites.XMLName.Local == "testsuites" {
		return summarizeSuites(suites)
	}

	// 退化:根是单个 <testsuite>。
	var single junitTestSuite
	if err := xml.Unmarshal([]byte(trimmed), &single); err != nil {
		// EOF = 只有 XML 声明/无根元素;根元素不是 testsuite → 视为空报告(非解析错误)。
		if err == io.EOF {
			return Summary{}, ErrEmptyReport
		}
		if _, ok := err.(*xml.UnmarshalError); ok {
			return Summary{}, ErrEmptyReport
		}
		return Summary{}, fmt.Errorf("testreport: parse junit xml: %w", err)
	}
	if single.XMLName.Local != "testsuite" {
		return Summary{}, ErrEmptyReport
	}
	return summarizeSuites(junitTestSuites{Suites: []junitTestSuite{single}})
}

// summarizeSuites 把 <testsuites> 聚合为 Summary。
func summarizeSuites(s junitTestSuites) (Summary, error) {
	if len(s.Suites) == 0 && s.Tests == nil {
		return Summary{}, ErrEmptyReport
	}

	var total, failures, errs, skipped int
	var duration float64

	// 根属性给全了 → 直接采信(避免与子套件重复累加)。
	if s.Tests != nil {
		total = *s.Tests
		if s.Failures != nil {
			failures = *s.Failures
		}
		if s.Errors != nil {
			errs = *s.Errors
		}
		if s.Skipped != nil {
			skipped = *s.Skipped
		}
		duration = parseSeconds(s.Time)
	} else {
		for _, sub := range s.Suites {
			total += sub.Tests
			failures += sub.Failures
			errs += sub.Errors
			skipped += sub.Skipped
			duration += parseSeconds(sub.Time)
		}
	}

	failed := failures + errs
	passed := total - failed - skipped
	if passed < 0 {
		passed = 0
	}
	return Summary{
		Total:           total,
		Passed:          passed,
		Failed:          failed,
		Skipped:         skipped,
		DurationSeconds: duration,
		Coverage:        CoverageUnknown,
	}, nil
}

// parseSeconds 把 time 属性(秒,可空/非法)解析为 float(失败 → 0)。
func parseSeconds(s string) float64 {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil || v < 0 {
		return 0
	}
	return v
}
