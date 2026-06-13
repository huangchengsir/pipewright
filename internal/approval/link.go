package approval

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"strconv"
	"strings"
	"time"
)

// link.go 实现「从通知直接审批」的签名链接(token-based auth):通知里携带一个签名 token,
// 打开 {PUBLIC_URL}/approvals?token=<signed> 即可在不登录的前提下批准/拒绝该审批门。
//
// token 即认证(无会话):任何人持有有效 token 即可代审批,故签名必须用 master key 做 HMAC、
// 带过期、常量时间比较;token 不入日志、不入页面。master key 缺失时签名器进入「禁用」态:
// Sign 返回空串、Verify 恒 false——功能优雅关闭(不发链接 / 链接不可用),平台其余不受影响。
//
// token 形态(URL-safe,无 padding):
//
//	base64url(payload) + "." + base64url(HMAC-SHA256(key, payloadBytes))
//
// 其中 payload = "runID|stageID|expUnix"(expUnix 为过期时刻的 Unix 秒)。

// Signer 用 master key 对审批链接 token 签名 / 校验。零值不可用,经 NewSigner 构造。
type Signer struct {
	key []byte
}

// NewSigner 构造签名器。key 为空(len==0)→ 禁用态:Sign 返回 ""、Verify 恒 ok=false。
// 调用方(main)在 master key 缺失时传 nil 即可优雅关闭本功能。
func NewSigner(key []byte) *Signer {
	if len(key) == 0 {
		return &Signer{key: nil}
	}
	// 复制一份,避免调用方持有的底层数组被外部改动影响签名稳定性。
	cp := make([]byte, len(key))
	copy(cp, key)
	return &Signer{key: cp}
}

// Enabled 报告签名器是否可用(配置了 master key)。
func (s *Signer) Enabled() bool {
	return s != nil && len(s.key) > 0
}

// Sign 为 (runID, stageID) 签发一个在 exp 过期的 token。禁用态 → 返回空串。
func (s *Signer) Sign(runID, stageID string, exp time.Time) string {
	if !s.Enabled() {
		return ""
	}
	payload := runID + "|" + stageID + "|" + strconv.FormatInt(exp.Unix(), 10)
	payloadB := []byte(payload)
	sig := s.mac(payloadB)
	return b64(payloadB) + "." + b64(sig)
}

// Verify 校验 token,返回其承载的 runID/stageID 及是否有效。
// 任何畸形(格式错、base64 错、签名不符、已过期)→ ok=false(返回的 runID/stageID 为空)。
// 禁用态(无 key)→ 恒 ok=false。签名比较用 hmac.Equal(常量时间,防计时旁路)。
func (s *Signer) Verify(token string) (runID, stageID string, ok bool) {
	if !s.Enabled() {
		return "", "", false
	}
	dot := strings.IndexByte(token, '.')
	if dot <= 0 || dot == len(token)-1 {
		return "", "", false
	}
	payloadB, err := unb64(token[:dot])
	if err != nil {
		return "", "", false
	}
	gotSig, err := unb64(token[dot+1:])
	if err != nil {
		return "", "", false
	}
	wantSig := s.mac(payloadB)
	if !hmac.Equal(gotSig, wantSig) {
		return "", "", false
	}
	// 校验通过后再解析 payload(签名先验,防解析未认证内容)。
	parts := strings.Split(string(payloadB), "|")
	if len(parts) != 3 {
		return "", "", false
	}
	expUnix, perr := strconv.ParseInt(parts[2], 10, 64)
	if perr != nil {
		return "", "", false
	}
	if time.Now().After(time.Unix(expUnix, 0)) {
		return "", "", false // 已过期。
	}
	return parts[0], parts[1], true
}

// mac 计算 payload 的 HMAC-SHA256(key 为 master key)。
func (s *Signer) mac(payload []byte) []byte {
	h := hmac.New(sha256.New, s.key)
	h.Write(payload)
	return h.Sum(nil)
}

// b64 / unb64 用 URL-safe 无 padding 的 base64(token 可直接进 URL query)。
func b64(b []byte) string {
	return base64.RawURLEncoding.EncodeToString(b)
}

func unb64(s string) ([]byte, error) {
	return base64.RawURLEncoding.DecodeString(s)
}
