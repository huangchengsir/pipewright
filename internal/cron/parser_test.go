package cron

import (
	"testing"
	"time"
)

func mustParse(t *testing.T, expr string) *Schedule {
	t.Helper()
	s, err := Parse(expr)
	if err != nil {
		t.Fatalf("Parse(%q): %v", expr, err)
	}
	return s
}

// 固定时区(UTC)便于断言。
func at(y int, mo time.Month, d, h, mi int) time.Time {
	return time.Date(y, mo, d, h, mi, 0, 0, time.UTC)
}

func TestParseErrors(t *testing.T) {
	bad := []string{
		"",            // 空
		"* * * *",     // 4 字段
		"* * * * * *", // 6 字段
		"60 * * * *",  // 分越界
		"* 24 * * *",  // 时越界
		"* * 0 * *",   // 日越界(下限 1)
		"* * * 13 *",  // 月越界
		"* * * * 8",   // 周越界(上限 7)
		"a * * * *",   // 非数字
		"5-2 * * * *", // 逆范围
		"*/0 * * * *", // 步进 0
	}
	for _, expr := range bad {
		if err := Valid(expr); err == nil {
			t.Errorf("Valid(%q) = nil, 期望错误", expr)
		}
	}
}

func TestMatchesBasic(t *testing.T) {
	// 每天 02:30
	s := mustParse(t, "30 2 * * *")
	if !s.Matches(at(2026, 5, 31, 2, 30)) {
		t.Error("应命中 02:30")
	}
	if s.Matches(at(2026, 5, 31, 2, 31)) {
		t.Error("不应命中 02:31")
	}
	if s.Matches(at(2026, 5, 31, 3, 30)) {
		t.Error("不应命中 03:30")
	}
}

func TestMatchesStepAndList(t *testing.T) {
	// 每 15 分钟
	s := mustParse(t, "*/15 * * * *")
	for _, m := range []int{0, 15, 30, 45} {
		if !s.Matches(at(2026, 5, 31, 10, m)) {
			t.Errorf("*/15 应命中分钟 %d", m)
		}
	}
	if s.Matches(at(2026, 5, 31, 10, 7)) {
		t.Error("*/15 不应命中分钟 7")
	}
	// 列表
	l := mustParse(t, "0,30 9-17 * * *")
	if !l.Matches(at(2026, 5, 31, 9, 0)) || !l.Matches(at(2026, 5, 31, 17, 30)) {
		t.Error("列表/范围应命中工作时间整点/半点")
	}
	if l.Matches(at(2026, 5, 31, 18, 0)) {
		t.Error("不应命中 18:00")
	}
}

func TestDowSunday7And0(t *testing.T) {
	// 2026-05-31 是周日。
	sun := at(2026, 5, 31, 0, 0)
	if sun.Weekday() != time.Sunday {
		t.Fatalf("测试前提错误:2026-05-31 应为周日,实际 %v", sun.Weekday())
	}
	for _, expr := range []string{"0 0 * * 0", "0 0 * * 7"} {
		if !mustParse(t, expr).Matches(sun) {
			t.Errorf("%q 应命中周日", expr)
		}
	}
	if mustParse(t, "0 0 * * 1").Matches(sun) {
		t.Error("周一表达式不应命中周日")
	}
}

func TestDomDowOrSemantics(t *testing.T) {
	// 日=1 或 周=一(均限定 → 或)。2026-06-01 是周一且是 1 号。
	s := mustParse(t, "0 0 1 * 1")
	if !s.Matches(at(2026, 6, 15, 0, 0)) { // 6-15 周一 → dow 命中
		t.Error("周一应命中(或语义)")
	}
	if !s.Matches(at(2026, 7, 1, 0, 0)) { // 7-1 周三 → dom 命中
		t.Error("1 号应命中(或语义)")
	}
	if s.Matches(at(2026, 6, 16, 0, 0)) { // 周二且非 1 号
		t.Error("既非 1 号又非周一不应命中")
	}
}

func TestNext(t *testing.T) {
	s := mustParse(t, "0 3 * * *") // 每天 03:00
	next := s.Next(at(2026, 5, 31, 4, 0))
	want := at(2026, 6, 1, 3, 0)
	if !next.Equal(want) {
		t.Errorf("Next = %v, 期望 %v", next, want)
	}
	// 严格晚于:正好在命中分钟时,返回下一次。
	n2 := s.Next(at(2026, 5, 31, 3, 0))
	if !n2.Equal(at(2026, 6, 1, 3, 0)) {
		t.Errorf("Next 应严格晚于 after,得 %v", n2)
	}
}
