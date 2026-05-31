package pipeline

import "strings"

// stage_when.go 是阶段条件执行(Epic 8 · Story 8-5)的领域逻辑:When 规则 + 匹配。
//
// When 决定一个阶段在某次运行里是否执行——不满足则该阶段(及其下游)被 skipped(非失败)。
// 「上游成功」条件已由 DAG 依赖(needs)天然表达,故 When 只覆盖「分支匹配」与「触发类型」。

// When 是阶段条件执行规则。空(无 Branches 且无 Events)= 无条件,总执行。
//   - Branches:仅当触发分支匹配其一(glob `*`,跨 `/`)时执行;空 = 任意分支。
//   - Events:仅当触发类型 ∈ 其中(manual/webhook/schedule)时执行;空 = 任意触发。
//
// 两组皆设时按「与」:分支命中 **且** 触发类型命中才执行;任一不满足 → skipped。
type When struct {
	Branches []string `json:"branches,omitempty"`
	Events   []string `json:"events,omitempty"`
}

// IsEmpty 报告该 When 是否无任何条件(总执行)。
func (w When) IsEmpty() bool { return len(w.Branches) == 0 && len(w.Events) == 0 }

// Matches 报告给定触发分支/类型是否满足本条件(空条件恒真)。
func (w When) Matches(branch, event string) bool {
	if len(w.Branches) > 0 {
		hit := false
		for _, p := range w.Branches {
			if globMatch(strings.TrimSpace(p), branch) {
				hit = true
				break
			}
		}
		if !hit {
			return false
		}
	}
	if len(w.Events) > 0 {
		hit := false
		for _, e := range w.Events {
			if strings.TrimSpace(e) == event {
				hit = true
				break
			}
		}
		if !hit {
			return false
		}
	}
	return true
}

// normalizeWhen 规范化 When:trim、剔空、去重(保留序);返回的零值表示无条件。
func normalizeWhen(in When) When {
	return When{Branches: dedupTrim(in.Branches), Events: dedupTrim(in.Events)}
}

func dedupTrim(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, v := range in {
		v = strings.TrimSpace(v)
		if v == "" {
			continue
		}
		if _, ok := seen[v]; ok {
			continue
		}
		seen[v] = struct{}{}
		out = append(out, v)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// globMatch 是 `*`(匹配任意字符,含 `/`)的简易 glob;无 `*` 精确匹配;`*`/空 patten 匹配一切。
// 仿 trigger.globMatch 语义(分支模式 release/* 命中 release/1.2)。
func globMatch(pattern, s string) bool {
	if pattern == "" || pattern == "*" {
		return true
	}
	if !strings.Contains(pattern, "*") {
		return pattern == s
	}
	parts := strings.Split(pattern, "*")
	pos := 0
	for i, part := range parts {
		if part == "" {
			continue
		}
		idx := strings.Index(s[pos:], part)
		if idx < 0 {
			return false
		}
		if i == 0 && idx != 0 {
			return false // 非通配前缀须从头匹配
		}
		pos += idx + len(part)
	}
	// 末段非空且 pattern 不以 `*` 结尾时,须匹配到字符串结尾。
	if last := parts[len(parts)-1]; last != "" && !strings.HasSuffix(s, last) {
		return false
	}
	return true
}
