// Package mask 是 secret 脱敏工具(AC-SEC-04)。
//
// 把运行期已知的敏感串(凭据明文、webhook 签名密钥、master key 等)登记进 Masker,
// 之后对任意输出行调用 Scrub,把所有已登记 secret 的子串替换为占位 [MASKED]。
// 用于:① 审计 detail 落库前脱敏(绝不把密钥写进 detail_json);② 运行日志落盘前
// 脱敏(3-6 落地时接入其写日志路径,本期工具独立可测)。
//
// 无 init 副作用、无包级重对象:NewMasker 返回轻量空 Masker(避免抬高空载内存)。
// 并发安全:RegisterSecret 与 Scrub 可被多 goroutine 并发调用(读写以 RWMutex 守护)。
package mask

import (
	"sort"
	"strings"
	"sync"
)

// Placeholder 是脱敏后替换占位文本。
const Placeholder = "[MASKED]"

// minSecretLen 是登记 secret 的最短长度:过短的串(如 "a"、"12")作为子串极易
// 误伤正常文本(把无害字符也打码),故不登记。4 字符为经验下限。
const minSecretLen = 4

// Masker 持有已登记 secret 集合,提供按子串脱敏。零值不可用,经 NewMasker 构造。
type Masker struct {
	mu sync.RWMutex
	// secrets 去重存放已登记 secret;scrub 时按长度降序替换(长串优先,
	// 避免短 secret 先替换破坏长 secret 的匹配)。
	secrets map[string]struct{}
}

// NewMasker 构造空 Masker。不做任何重计算(无包级重对象)。
func NewMasker() *Masker {
	return &Masker{secrets: make(map[string]struct{})}
}

// RegisterSecret 登记一个敏感串。空串或过短串(< minSecretLen)被忽略以避免误伤;
// 重复登记幂等。登记后 Scrub 会把该串的所有出现替换为 [MASKED]。
func (m *Masker) RegisterSecret(s string) {
	if m == nil {
		return
	}
	if len(s) < minSecretLen {
		return
	}
	m.mu.Lock()
	if m.secrets == nil {
		m.secrets = make(map[string]struct{})
	}
	m.secrets[s] = struct{}{}
	m.mu.Unlock()
}

// Scrub 返回 line 的脱敏副本:每个已登记 secret 的所有出现都替换为 [MASKED]。
// 未登记任何 secret 时原样返回。多 secret、同一 secret 多次出现均全部替换。
//
// 替换按 secret 长度降序进行:先替长串,避免「短 secret 是长 secret 子串」时
// 短串先打码破坏长串匹配,从而漏掉长串的剩余部分。
func (m *Masker) Scrub(line string) string {
	if m == nil || line == "" {
		return line
	}
	m.mu.RLock()
	if len(m.secrets) == 0 {
		m.mu.RUnlock()
		return line
	}
	ordered := make([]string, 0, len(m.secrets))
	for s := range m.secrets {
		ordered = append(ordered, s)
	}
	m.mu.RUnlock()

	sort.Slice(ordered, func(i, j int) bool {
		return len(ordered[i]) > len(ordered[j])
	})

	out := line
	for _, s := range ordered {
		if strings.Contains(out, s) {
			out = strings.ReplaceAll(out, s, Placeholder)
		}
	}
	return out
}

// ScrubMap 返回 detail 的脱敏深拷贝:对每个字符串值/嵌套字符串递归 Scrub。
// 供审计 detail 落库前过滤,确保 detail_json 绝不含明文 secret。
// 非字符串值(数字/布尔/nil)原样保留;嵌套 map[string]any 与 []any 递归处理。
func (m *Masker) ScrubMap(detail map[string]any) map[string]any {
	if detail == nil {
		return nil
	}
	out := make(map[string]any, len(detail))
	for k, v := range detail {
		out[k] = m.scrubValue(v)
	}
	return out
}

// scrubValue 递归脱敏任意值。
func (m *Masker) scrubValue(v any) any {
	switch val := v.(type) {
	case string:
		return m.Scrub(val)
	case map[string]any:
		return m.ScrubMap(val)
	case []any:
		out := make([]any, len(val))
		for i, e := range val {
			out[i] = m.scrubValue(e)
		}
		return out
	default:
		return v
	}
}
