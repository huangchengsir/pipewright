package mask

import (
	"strings"
	"testing"
)

// TestScrubMasksRegisteredSecret 是 AC-SEC-04 核心回归:注册 secret 后,
// Scrub("echo "+secret) 含 [MASKED] 且不含明文。
func TestScrubMasksRegisteredSecret(t *testing.T) {
	const secret = "ghp_supersecrettoken1234567890"
	m := NewMasker()
	m.RegisterSecret(secret)

	line := "echo " + secret
	out := m.Scrub(line)

	if strings.Contains(out, secret) {
		t.Fatalf("脱敏后仍含明文: %q", out)
	}
	if !strings.Contains(out, Placeholder) {
		t.Fatalf("脱敏后应含 %s: %q", Placeholder, out)
	}
	if out != "echo "+Placeholder {
		t.Fatalf("脱敏结果 = %q, want %q", out, "echo "+Placeholder)
	}
}

// TestScrubMultipleOccurrences 同一 secret 多次出现全部替换。
func TestScrubMultipleOccurrences(t *testing.T) {
	const secret = "whsec_abcdef0123456789"
	m := NewMasker()
	m.RegisterSecret(secret)

	line := secret + " middle " + secret + " end " + secret
	out := m.Scrub(line)

	if strings.Contains(out, secret) {
		t.Fatalf("仍残留明文: %q", out)
	}
	if n := strings.Count(out, Placeholder); n != 3 {
		t.Fatalf("应替换 3 处, got %d: %q", n, out)
	}
}

// TestScrubMultipleSecrets 多个不同 secret 都被替换。
func TestScrubMultipleSecrets(t *testing.T) {
	const s1 = "git_token_aaaaaaaa"
	const s2 = "registry_pass_bbbbbbbb"
	m := NewMasker()
	m.RegisterSecret(s1)
	m.RegisterSecret(s2)

	out := m.Scrub("user=" + s2 + " token=" + s1)
	if strings.Contains(out, s1) || strings.Contains(out, s2) {
		t.Fatalf("仍残留明文: %q", out)
	}
	if strings.Count(out, Placeholder) != 2 {
		t.Fatalf("应替换 2 处: %q", out)
	}
}

// TestScrubLongestFirst 短 secret 是长 secret 子串时,长串优先替换,不破坏匹配。
func TestScrubLongestFirst(t *testing.T) {
	const long = "secretvalue_full_token"
	const short = "secretvalue"
	m := NewMasker()
	m.RegisterSecret(long)
	m.RegisterSecret(short)

	out := m.Scrub("X=" + long)
	if strings.Contains(out, long) {
		t.Fatalf("长 secret 未被完整替换: %q", out)
	}
	// 整个长串应被一个 [MASKED] 取代,而非「[MASKED]_full_token」残留尾巴。
	if out != "X="+Placeholder {
		t.Fatalf("长串优先替换失败, got %q, want %q", out, "X="+Placeholder)
	}
}

// TestShortSecretNotRegistered 过短串不登记,避免误伤正常文本。
func TestShortSecretNotRegistered(t *testing.T) {
	m := NewMasker()
	m.RegisterSecret("")    // 空
	m.RegisterSecret("ab")  // 太短
	m.RegisterSecret("abc") // 太短(< 4)

	out := m.Scrub("the cab has abc and ab inside")
	if strings.Contains(out, Placeholder) {
		t.Fatalf("过短/空 secret 不应触发脱敏: %q", out)
	}
	if out != "the cab has abc and ab inside" {
		t.Fatalf("正常文本被误改: %q", out)
	}
}

// TestScrubNoSecrets 未登记任何 secret 时原样返回。
func TestScrubNoSecrets(t *testing.T) {
	m := NewMasker()
	const line = "nothing secret here"
	if got := m.Scrub(line); got != line {
		t.Fatalf("无登记时应原样返回, got %q", got)
	}
}

// TestScrubMapRecursive 验证 detail map(含嵌套)中的 secret 被深度脱敏,非串值保留。
func TestScrubMapRecursive(t *testing.T) {
	const secret = "supersecret_master_key_value"
	m := NewMasker()
	m.RegisterSecret(secret)

	detail := map[string]any{
		"name":    "cred",
		"value":   secret,
		"count":   42,
		"enabled": true,
		"nested": map[string]any{
			"key": "prefix-" + secret + "-suffix",
		},
		"list": []any{secret, "clean", 7},
	}
	out := m.ScrubMap(detail)

	if out["value"] != Placeholder {
		t.Fatalf("顶层 secret 未脱敏: %v", out["value"])
	}
	if out["count"] != 42 || out["enabled"] != true {
		t.Fatalf("非串值不应被改: %v", out)
	}
	nested, _ := out["nested"].(map[string]any)
	if nested == nil || strings.Contains(nested["key"].(string), secret) {
		t.Fatalf("嵌套 secret 未脱敏: %v", out["nested"])
	}
	list, _ := out["list"].([]any)
	if list == nil || list[0] != Placeholder || list[1] != "clean" {
		t.Fatalf("列表内 secret 未脱敏: %v", out["list"])
	}
	// 原始 detail 不被原地修改(深拷贝)。
	if detail["value"] != secret {
		t.Fatalf("ScrubMap 不应原地修改输入")
	}
}

// TestNilMaskerSafe nil Masker 上调用安全。
func TestNilMaskerSafe(t *testing.T) {
	var m *Masker
	m.RegisterSecret("anything")
	if got := m.Scrub("line"); got != "line" {
		t.Fatalf("nil Masker Scrub 应原样返回, got %q", got)
	}
	if got := m.ScrubMap(map[string]any{"a": "b"}); got["a"] != "b" {
		t.Fatalf("nil Masker ScrubMap 应原样返回, got %v", got)
	}
}

// TestConcurrentRegisterScrub 并发登记 + 脱敏不触发竞态(-race 下断言)。
func TestConcurrentRegisterScrub(t *testing.T) {
	m := NewMasker()
	done := make(chan struct{})
	go func() {
		for i := 0; i < 1000; i++ {
			m.RegisterSecret("secret_value_xyz")
		}
		close(done)
	}()
	for i := 0; i < 1000; i++ {
		_ = m.Scrub("echo secret_value_xyz")
	}
	<-done
}
