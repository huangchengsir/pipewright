package testreport

import (
	"encoding/xml"
	"errors"
	"strconv"
	"strings"
)

// coverage.go 解析 Cobertura XML 的行覆盖率(line-rate)。
//
// 只取根 <coverage line-rate="0.83"> 的 line-rate(0~1),换算成百分比(0~100)。
// jacoco/gocover-cobertura/coverage.py 均能产 Cobertura 格式;lcov/jacoco 原生格式本期不支持。

// ErrNoCoverage 表示 XML 里找不到可用的 line-rate。
var ErrNoCoverage = errors.New("testreport: no line-rate found in coverage xml")

// coberturaCoverage 对应 Cobertura 根 <coverage>。
type coberturaCoverage struct {
	XMLName  xml.Name `xml:"coverage"`
	LineRate string   `xml:"line-rate,attr"`
}

// ParseCoberturaCoverage 解析 Cobertura XML,返回行覆盖率百分比(0~100)。
// 无 <coverage> 根或 line-rate 缺失/非法 → ErrNoCoverage;XML 非法 → 解析错误。
func ParseCoberturaCoverage(data []byte) (float64, error) {
	trimmed := strings.TrimSpace(string(data))
	if trimmed == "" {
		return 0, ErrNoCoverage
	}
	var cov coberturaCoverage
	if err := xml.Unmarshal([]byte(trimmed), &cov); err != nil {
		// 覆盖率是次要项(失败软处理):根非 <coverage> / 空根 / 形状不符 → 一律视为「无覆盖率」,
		// 不把次要解析问题升级为硬错误。语法层完全无法解析也归此(由门禁层据「未提供覆盖率」处置)。
		return 0, ErrNoCoverage
	}
	if cov.XMLName.Local != "coverage" || strings.TrimSpace(cov.LineRate) == "" {
		return 0, ErrNoCoverage
	}
	rate, err := strconv.ParseFloat(strings.TrimSpace(cov.LineRate), 64)
	if err != nil || rate < 0 {
		return 0, ErrNoCoverage
	}
	pct := rate * 100
	if pct > 100 {
		pct = 100
	}
	return pct, nil
}
