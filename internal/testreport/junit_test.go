package testreport

import (
	"errors"
	"math"
	"testing"
)

// 夹具:全部通过(<testsuites> 聚合根)。
const junitAllPass = `<?xml version="1.0" encoding="UTF-8"?>
<testsuites tests="5" failures="0" errors="0" skipped="0" time="1.250">
  <testsuite name="pkg/foo" tests="3" failures="0" errors="0" skipped="0" time="0.500"/>
  <testsuite name="pkg/bar" tests="2" failures="0" errors="0" skipped="0" time="0.750"/>
</testsuites>`

// 夹具:含失败 + 错误 + 跳过(无根属性,靠子套件累加)。
const junitWithFailures = `<?xml version="1.0" encoding="UTF-8"?>
<testsuites>
  <testsuite name="pkg/foo" tests="4" failures="1" errors="1" skipped="1" time="0.300">
    <testcase name="ok"/>
    <testcase name="bad"><failure message="boom"/></testcase>
    <testcase name="err"><error message="panic"/></testcase>
    <testcase name="skip"><skipped/></testcase>
  </testsuite>
  <testsuite name="pkg/bar" tests="2" failures="0" errors="0" skipped="0" time="0.200"/>
</testsuites>`

// 夹具:根直接是单个 <testsuite>(maven-surefire 单文件常见形态)。
const junitSingleSuite = `<?xml version="1.0"?>
<testsuite name="solo" tests="2" failures="1" errors="0" skipped="0" time="0.42">
  <testcase name="a"/>
  <testcase name="b"><failure/></testcase>
</testsuite>`

func almostEqual(a, b float64) bool { return math.Abs(a-b) < 1e-6 }

func TestParseJUnit_AllPass(t *testing.T) {
	s, err := ParseJUnit([]byte(junitAllPass))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if s.Total != 5 || s.Passed != 5 || s.Failed != 0 || s.Skipped != 0 {
		t.Errorf("counts = %+v, want total=5 passed=5 failed=0 skipped=0", s)
	}
	if !almostEqual(s.DurationSeconds, 1.250) {
		t.Errorf("duration = %v, want 1.250", s.DurationSeconds)
	}
	if s.Coverage != CoverageUnknown {
		t.Errorf("coverage = %v, want CoverageUnknown", s.Coverage)
	}
}

func TestParseJUnit_WithFailures(t *testing.T) {
	s, err := ParseJUnit([]byte(junitWithFailures))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// 子套件累加:tests=6, failures=1, errors=1, skipped=1 → failed=2, passed=3.
	if s.Total != 6 {
		t.Errorf("total = %d, want 6", s.Total)
	}
	if s.Failed != 2 {
		t.Errorf("failed = %d, want 2 (failures+errors)", s.Failed)
	}
	if s.Skipped != 1 {
		t.Errorf("skipped = %d, want 1", s.Skipped)
	}
	if s.Passed != 3 {
		t.Errorf("passed = %d, want 3", s.Passed)
	}
	if !almostEqual(s.DurationSeconds, 0.500) {
		t.Errorf("duration = %v, want 0.500", s.DurationSeconds)
	}
}

func TestParseJUnit_SingleSuiteRoot(t *testing.T) {
	s, err := ParseJUnit([]byte(junitSingleSuite))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if s.Total != 2 || s.Failed != 1 || s.Passed != 1 {
		t.Errorf("counts = %+v, want total=2 failed=1 passed=1", s)
	}
}

func TestParseJUnit_Empty(t *testing.T) {
	for _, in := range []string{"", "   ", `<?xml version="1.0"?>`} {
		if _, err := ParseJUnit([]byte(in)); !errors.Is(err, ErrEmptyReport) {
			t.Errorf("ParseJUnit(%q) err = %v, want ErrEmptyReport", in, err)
		}
	}
}

func TestParseJUnit_Malformed(t *testing.T) {
	if _, err := ParseJUnit([]byte(`<testsuite tests="oops</bad`)); err == nil {
		t.Error("expected error for malformed xml, got nil")
	}
}

func TestParseCoberturaCoverage(t *testing.T) {
	const cobertura = `<?xml version="1.0"?>
<coverage line-rate="0.8342" branch-rate="0.5" version="1.9">
  <packages/>
</coverage>`
	pct, err := ParseCoberturaCoverage([]byte(cobertura))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !almostEqual(pct, 83.42) {
		t.Errorf("coverage = %v, want 83.42", pct)
	}
}

func TestParseCoberturaCoverage_Missing(t *testing.T) {
	for _, in := range []string{"", `<coverage version="1"/>`, `<other line-rate="0.5"/>`} {
		if _, err := ParseCoberturaCoverage([]byte(in)); !errors.Is(err, ErrNoCoverage) {
			t.Errorf("ParseCoberturaCoverage(%q) err = %v, want ErrNoCoverage", in, err)
		}
	}
}
