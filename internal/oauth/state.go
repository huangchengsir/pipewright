package oauth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"sync"
	"time"
)

// stateTTL 是 OAuth state 的有效期。授权来回(用户在 git 平台点同意)通常秒级~分钟级,
// 10 分钟足够且不让陈旧 state 长期可用(限制 CSRF 重放窗口)。
const stateTTL = 10 * time.Minute

// stateEntry 是一条待校验的 state 记录:绑定发起授权的会话 + 过期时刻。
type stateEntry struct {
	sessionID string
	expiresAt time.Time
}

// stateStore 是 OAuth state 的短期内存存储(CSRF 防护)。
//
// 发起 authorize 时生成高熵随机 state、绑定当前会话 ID 存入;callback 校验 state 存在、
// 未过期、且绑定的会话 ID 与当前请求会话一致 —— 三者皆满足才放行 Exchange。这样攻击者
// 即便诱导受害者点开伪造的 callback,其授权码也因 state 不匹配(或不绑该会话)被拒,
// 防止 CSRF 注入伪造授权码把攻击者账号的 token 写进受害者保险库。
//
// 单进程内存即可(平台为单二进制);消费即删(一次性);惰性清理过期项。
type stateStore struct {
	mu      sync.Mutex
	entries map[string]stateEntry
	now     func() time.Time
}

// newStateStore 构造内存 state 存储。
func newStateStore() *stateStore {
	return &stateStore{
		entries: make(map[string]stateEntry),
		now:     time.Now,
	}
}

// issue 生成并登记一个绑定 sessionID 的随机 state,返回供放进 authorize URL。
func (s *stateStore) issue(sessionID string) (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	state := base64.RawURLEncoding.EncodeToString(buf)

	s.mu.Lock()
	defer s.mu.Unlock()
	s.gcLocked()
	s.entries[state] = stateEntry{sessionID: sessionID, expiresAt: s.now().Add(stateTTL)}
	return state, nil
}

// consume 校验并一次性消费 state:存在 + 未过期 + 绑定会话匹配 → true(并删除);否则 false。
// 用常量时间比较 sessionID(防时序侧信道)。
func (s *stateStore) consume(state, sessionID string) bool {
	if state == "" {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.entries[state]
	if !ok {
		return false
	}
	// 无论结果如何都消费掉这条 state(一次性,防重放)。
	delete(s.entries, state)
	if s.now().After(e.expiresAt) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(e.sessionID), []byte(sessionID)) == 1
}

// gcLocked 惰性清理过期项(调用方须持锁)。
func (s *stateStore) gcLocked() {
	now := s.now()
	for k, e := range s.entries {
		if now.After(e.expiresAt) {
			delete(s.entries, k)
		}
	}
}
