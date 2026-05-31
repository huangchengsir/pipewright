// Package cron 是定时触发的最小实现(Epic 8 · Story 8-6):一个 5 字段 cron 解析器 +
// 分钟粒度调度器,无第三方依赖(项目刻意保持依赖精简)。
//
// 字段(标准 Vixie cron):分 时 日 月 周。
//
//	┌───────── 分钟 minute (0-59)
//	│ ┌─────── 小时 hour   (0-23)
//	│ │ ┌───── 日   dom    (1-31)
//	│ │ │ ┌─── 月   month  (1-12)
//	│ │ │ │ ┌─ 周   dow    (0-6;0=周日,7 亦为周日)
//	* * * * *
//
// 每字段支持:`*`、单值 `n`、范围 `a-b`、步进 `*/n` 或 `a-b/n`、逗号列表 `a,b,c`。
// 日(dom)与周(dow)同时被限定(均非 `*`)时按「或」匹配(Vixie 经典语义)。
package cron

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// field 是一个解析后的 cron 字段:allowed 为命中值集合;restricted 表示该字段非 `*`。
type field struct {
	allowed    map[int]bool
	restricted bool
}

func (f field) match(v int) bool { return f.allowed[v] }

// Schedule 是解析后的 cron 计划(5 字段)。
type Schedule struct {
	minute field
	hour   field
	dom    field
	month  field
	dow    field
}

const (
	minMinute, maxMinute = 0, 59
	minHour, maxHour     = 0, 23
	minDom, maxDom       = 1, 31
	minMonth, maxMonth   = 1, 12
	minDow, maxDow       = 0, 6
)

// Parse 解析一个 5 字段 cron 表达式。多余空白容忍;字段数不为 5 → 错误。
func Parse(expr string) (*Schedule, error) {
	parts := strings.Fields(strings.TrimSpace(expr))
	if len(parts) != 5 {
		return nil, fmt.Errorf("cron: 表达式须为 5 个字段(分 时 日 月 周),实际 %d 个", len(parts))
	}
	minute, err := parseField(parts[0], minMinute, maxMinute, "minute")
	if err != nil {
		return nil, err
	}
	hour, err := parseField(parts[1], minHour, maxHour, "hour")
	if err != nil {
		return nil, err
	}
	dom, err := parseField(parts[2], minDom, maxDom, "day-of-month")
	if err != nil {
		return nil, err
	}
	month, err := parseField(parts[3], minMonth, maxMonth, "month")
	if err != nil {
		return nil, err
	}
	dow, err := parseDow(parts[4])
	if err != nil {
		return nil, err
	}
	return &Schedule{minute: minute, hour: hour, dom: dom, month: month, dow: dow}, nil
}

// Valid 是「仅校验」便捷封装。
func Valid(expr string) error {
	_, err := Parse(expr)
	return err
}

// parseField 解析单字段为 [min,max] 内的命中集合(支持 *、n、a-b、*/n、a-b/n、逗号列表)。
func parseField(spec string, min, max int, name string) (field, error) {
	f := field{allowed: make(map[int]bool)}
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return f, fmt.Errorf("cron: %s 字段为空", name)
	}
	if spec == "*" {
		for v := min; v <= max; v++ {
			f.allowed[v] = true
		}
		return f, nil // restricted=false
	}
	f.restricted = true
	for _, term := range strings.Split(spec, ",") {
		if err := parseTerm(term, min, max, name, f.allowed); err != nil {
			return field{}, err
		}
	}
	return f, nil
}

// parseTerm 解析单个项(`*`、`n`、`a-b`、`*/n`、`a-b/n`),写入 allowed。
func parseTerm(term string, min, max int, name string, allowed map[int]bool) error {
	term = strings.TrimSpace(term)
	if term == "" {
		return fmt.Errorf("cron: %s 字段含空项", name)
	}

	rangePart := term
	step := 1
	if slash := strings.IndexByte(term, '/'); slash >= 0 {
		rangePart = term[:slash]
		s, err := strconv.Atoi(strings.TrimSpace(term[slash+1:]))
		if err != nil || s <= 0 {
			return fmt.Errorf("cron: %s 字段步进非法 %q", name, term)
		}
		step = s
	}

	lo, hi := min, max
	switch {
	case rangePart == "*":
		// 全域 + 步进
	case strings.ContainsRune(rangePart, '-'):
		bounds := strings.SplitN(rangePart, "-", 2)
		a, err1 := strconv.Atoi(strings.TrimSpace(bounds[0]))
		b, err2 := strconv.Atoi(strings.TrimSpace(bounds[1]))
		if err1 != nil || err2 != nil {
			return fmt.Errorf("cron: %s 字段范围非法 %q", name, term)
		}
		lo, hi = a, b
	default:
		v, err := strconv.Atoi(rangePart)
		if err != nil {
			return fmt.Errorf("cron: %s 字段值非法 %q", name, term)
		}
		lo, hi = v, v
	}

	if lo < min || hi > max || lo > hi {
		return fmt.Errorf("cron: %s 字段越界 %q(允许 %d-%d)", name, term, min, max)
	}
	for v := lo; v <= hi; v += step {
		allowed[v] = true
	}
	return nil
}

// parseDow 解析周字段:7 归一为 0(周日)。
func parseDow(spec string) (field, error) {
	spec = strings.TrimSpace(spec)
	// 允许 0-7 输入,内部 7→0;先按 0-7 解析再归一。
	raw, err := parseField(spec, 0, 7, "day-of-week")
	if err != nil {
		return field{}, err
	}
	out := field{allowed: make(map[int]bool), restricted: raw.restricted}
	for v := range raw.allowed {
		if v == 7 {
			v = 0
		}
		out.allowed[v] = true
	}
	return out, nil
}

// Matches 报告时刻 t(取分钟精度)是否命中本计划。
// 日/周同时被限定时按「或」匹配(Vixie 经典语义);否则用被限定者(或全放行)。
func (s *Schedule) Matches(t time.Time) bool {
	if !s.minute.match(t.Minute()) || !s.hour.match(t.Hour()) || !s.month.match(int(t.Month())) {
		return false
	}
	domHit := s.dom.match(t.Day())
	dowHit := s.dow.match(int(t.Weekday())) // time.Weekday: 0=Sunday
	switch {
	case s.dom.restricted && s.dow.restricted:
		return domHit || dowHit
	case s.dom.restricted:
		return domHit
	case s.dow.restricted:
		return dowHit
	default:
		return true // 日、周皆 *
	}
}

// Next 返回严格晚于 after 的下一个命中时刻(分钟精度;同一时区)。
// 一年内无命中(如不存在的日期组合)→ 返回零值 time.Time。
func (s *Schedule) Next(after time.Time) time.Time {
	// 进位到下一整分钟。
	t := after.Truncate(time.Minute).Add(time.Minute)
	limit := t.Add(366 * 24 * time.Hour)
	for t.Before(limit) {
		if s.Matches(t) {
			return t
		}
		t = t.Add(time.Minute)
	}
	return time.Time{}
}
