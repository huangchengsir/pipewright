package approval

import (
	"strings"
	"testing"
	"time"
)

func testKey() []byte {
	k := make([]byte, 32)
	for i := range k {
		k[i] = byte(i + 1)
	}
	return k
}

// 往返:签名后能校验回原始 runID/stageID。
func TestSignerRoundTrip(t *testing.T) {
	s := NewSigner(testKey())
	if !s.Enabled() {
		t.Fatal("signer should be enabled with a key")
	}
	tok := s.Sign("run-123", "stage-deploy", time.Now().Add(time.Hour))
	if tok == "" {
		t.Fatal("Sign returned empty token for enabled signer")
	}
	runID, stageID, ok := s.Verify(tok)
	if !ok {
		t.Fatal("Verify failed for a valid round-trip token")
	}
	if runID != "run-123" || stageID != "stage-deploy" {
		t.Fatalf("round-trip mismatch: got run=%q stage=%q", runID, stageID)
	}
}

// 篡改 payload → ok=false。
func TestSignerTamperedPayload(t *testing.T) {
	s := NewSigner(testKey())
	tok := s.Sign("run-1", "stage-1", time.Now().Add(time.Hour))
	dot := strings.IndexByte(tok, '.')
	// 用另一组 (runID,stageID) 的合法 payload 替换,但保留原签名 → 签名不符。
	other := s.Sign("run-EVIL", "stage-EVIL", time.Now().Add(time.Hour))
	otherDot := strings.IndexByte(other, '.')
	forged := other[:otherDot] + tok[dot:]
	if _, _, ok := s.Verify(forged); ok {
		t.Fatal("Verify accepted a token with mismatched payload/signature")
	}
}

// 篡改签名 → ok=false。
func TestSignerTamperedSig(t *testing.T) {
	s := NewSigner(testKey())
	tok := s.Sign("run-1", "stage-1", time.Now().Add(time.Hour))
	// 翻转签名段**首字符**:base64 首字符编码首字节高 6 位,翻转必改解码字节。
	// (末字符含非有效尾比特,翻转可能解出同字节 → 与原签名等价、不可靠;曾致 CI 偶发假失败。)
	dot := strings.IndexByte(tok, '.')
	sigFirst := tok[dot+1]
	repl := byte('A')
	if sigFirst == 'A' {
		repl = 'B'
	}
	forged := tok[:dot+1] + string(repl) + tok[dot+2:]
	if _, _, ok := s.Verify(forged); ok {
		t.Fatal("Verify accepted a token with tampered signature")
	}
}

// 过期 → ok=false。
func TestSignerExpired(t *testing.T) {
	s := NewSigner(testKey())
	tok := s.Sign("run-1", "stage-1", time.Now().Add(-time.Minute))
	if _, _, ok := s.Verify(tok); ok {
		t.Fatal("Verify accepted an expired token")
	}
}

// 畸形 token → ok=false(无 panic)。
func TestSignerMalformed(t *testing.T) {
	s := NewSigner(testKey())
	for _, bad := range []string{"", ".", "abc", "abc.", ".abc", "not-base64!.sig", "$$.$$"} {
		if _, _, ok := s.Verify(bad); ok {
			t.Fatalf("Verify accepted malformed token %q", bad)
		}
	}
}

// 不同 key 签发的 token → 校验失败(签名隔离)。
func TestSignerWrongKey(t *testing.T) {
	a := NewSigner(testKey())
	other := make([]byte, 32)
	for i := range other {
		other[i] = 0xFF
	}
	b := NewSigner(other)
	tok := a.Sign("run-1", "stage-1", time.Now().Add(time.Hour))
	if _, _, ok := b.Verify(tok); ok {
		t.Fatal("Verify with a different key accepted the token")
	}
}

// 禁用态(nil key):Sign 返回 ""、Verify 恒 false。
func TestSignerDisabled(t *testing.T) {
	s := NewSigner(nil)
	if s.Enabled() {
		t.Fatal("nil-key signer must be disabled")
	}
	if tok := s.Sign("run-1", "stage-1", time.Now().Add(time.Hour)); tok != "" {
		t.Fatalf("disabled signer Sign should return empty, got %q", tok)
	}
	// 即便给一个看似合法的 token,禁用态也恒 false。
	enabled := NewSigner(testKey())
	realTok := enabled.Sign("run-1", "stage-1", time.Now().Add(time.Hour))
	if _, _, ok := s.Verify(realTok); ok {
		t.Fatal("disabled signer Verify should always be false")
	}
}
