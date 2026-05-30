package auth

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"time"
)

const (
	// sessionTTL 会话有效期(7 天)。
	sessionTTL = 7 * 24 * time.Hour
	// tokenBytes 会话 token 随机字节数(32B → 64 hex chars)。
	tokenBytes = 32
)

// ErrSessionNotFound 表示会话不存在或已过期。
var ErrSessionNotFound = errors.New("auth: session not found or expired")

// Session 代表一个服务端会话。
type Session struct {
	Token      string
	CSRFToken  string
	CreatedAt  time.Time
	ExpiresAt  time.Time
	LastSeenAt time.Time
}

// SessionStore 操作 sessions 表。
type SessionStore struct {
	db *sql.DB
}

// NewSessionStore 构造 SessionStore。
func NewSessionStore(db *sql.DB) *SessionStore {
	return &SessionStore{db: db}
}

// Create 创建新会话并持久化到 DB。返回 Session(含 Token 与 CSRFToken)。
func (ss *SessionStore) Create() (*Session, error) {
	token, err := randHex(tokenBytes)
	if err != nil {
		return nil, fmt.Errorf("auth: generate session token: %w", err)
	}
	csrfToken, err := randHex(tokenBytes)
	if err != nil {
		return nil, fmt.Errorf("auth: generate csrf token: %w", err)
	}
	now := time.Now().UTC()
	exp := now.Add(sessionTTL)

	_, err = ss.db.Exec(
		`INSERT INTO sessions (token, csrf_token, created_at, expires_at, last_seen_at)
		 VALUES (?, ?, ?, ?, ?)`,
		token,
		csrfToken,
		now.Format(time.RFC3339),
		exp.Format(time.RFC3339),
		now.Format(time.RFC3339),
	)
	if err != nil {
		return nil, fmt.Errorf("auth: persist session: %w", err)
	}
	return &Session{
		Token:      token,
		CSRFToken:  csrfToken,
		CreatedAt:  now,
		ExpiresAt:  exp,
		LastSeenAt: now,
	}, nil
}

// Get 按 token 查找会话;不存在或已过期返回 ErrSessionNotFound。
// 若会话有效则滑动更新 last_seen_at。
func (ss *SessionStore) Get(token string) (*Session, error) {
	var s Session
	var createdStr, expiresStr, lastSeenStr string
	err := ss.db.QueryRow(
		`SELECT token, csrf_token, created_at, expires_at, last_seen_at
		 FROM sessions WHERE token = ?`,
		token,
	).Scan(&s.Token, &s.CSRFToken, &createdStr, &expiresStr, &lastSeenStr)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrSessionNotFound
		}
		return nil, fmt.Errorf("auth: get session: %w", err)
	}

	if s.CreatedAt, err = time.Parse(time.RFC3339, createdStr); err != nil {
		return nil, fmt.Errorf("auth: parse created_at: %w", err)
	}
	if s.ExpiresAt, err = time.Parse(time.RFC3339, expiresStr); err != nil {
		return nil, fmt.Errorf("auth: parse expires_at: %w", err)
	}
	if s.LastSeenAt, err = time.Parse(time.RFC3339, lastSeenStr); err != nil {
		return nil, fmt.Errorf("auth: parse last_seen_at: %w", err)
	}

	now := time.Now().UTC()
	if now.After(s.ExpiresAt) {
		// 惰性清理过期会话。
		_, _ = ss.db.Exec(`DELETE FROM sessions WHERE token = ?`, token)
		return nil, ErrSessionNotFound
	}

	// 滑动空闲超时:命中有效会话时把 expires_at 前移为 now+TTL,
	// 并同步刷新 last_seen_at。如此会话在连续活跃下不会到期,
	// 仅在空闲满 sessionTTL 后才过期(名实相符的滑动过期)。
	newExp := now.Add(sessionTTL)
	_, _ = ss.db.Exec(
		`UPDATE sessions SET last_seen_at = ?, expires_at = ? WHERE token = ?`,
		now.Format(time.RFC3339), newExp.Format(time.RFC3339), token,
	)
	s.LastSeenAt = now
	s.ExpiresAt = newExp
	return &s, nil
}

// PurgeExpired 删除所有已过期会话,返回删除行数。
// 在启动时调用一次以清理 DB 中惰性删除遗漏的过期会话。
func (ss *SessionStore) PurgeExpired() (int64, error) {
	now := time.Now().UTC().Format(time.RFC3339)
	res, err := ss.db.Exec(`DELETE FROM sessions WHERE expires_at < ?`, now)
	if err != nil {
		return 0, fmt.Errorf("auth: purge expired sessions: %w", err)
	}
	n, _ := res.RowsAffected()
	return n, nil
}

// Delete 删除指定 token 对应的会话(登出)。
func (ss *SessionStore) Delete(token string) error {
	_, err := ss.db.Exec(`DELETE FROM sessions WHERE token = ?`, token)
	if err != nil {
		return fmt.Errorf("auth: delete session: %w", err)
	}
	return nil
}

// randHex 生成 n 字节随机数并返回 hex 编码字串。
func randHex(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
