package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"time"
)

const (
	// sessionTTL 会话有效期(7 天)。
	sessionTTL = 7 * 24 * time.Hour
	// sessionRefreshInterval 是滑动过期的最小写库间隔:Get 命中有效会话后,
	// 仅当距上次 last_seen 超过此间隔才写库刷新,避免每请求一次 UPDATE 的写放大。
	sessionRefreshInterval = 60 * time.Second
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

	// 滑动空闲超时:命中有效会话时把 expires_at 前移为 now+TTL,并刷新 last_seen_at。
	// **写节流**:仅当距上次 last_seen 超过 sessionRefreshInterval 才写库。否则每个
	// 认证请求(列表/SSE 等)都触发一次 UPDATE,在单写 modernc/sqlite 上造成写放大与
	// 锁竞争(SSE 并发下易 SQLITE_BUSY)。TTL(7d)远大于刷新间隔(1min),跳过几次刷新
	// 不影响滑动过期语义。
	if now.Sub(s.LastSeenAt) >= sessionRefreshInterval {
		newExp := now.Add(sessionTTL)
		if _, err := ss.db.Exec(
			`UPDATE sessions SET last_seen_at = ?, expires_at = ? WHERE token = ?`,
			now.Format(time.RFC3339), newExp.Format(time.RFC3339), token,
		); err == nil {
			s.LastSeenAt = now
			s.ExpiresAt = newExp
		}
	}
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

// sessionIDPrefixLen 是会话对外 id 的长度(token 的 sha256 hex 前缀字符数)。
// 16 hex = 64 bit,足够在单实例会话集合内唯一定位,且**绝不回传原 token**。
const sessionIDPrefixLen = 16

// SessionID 返回 token 的 sha256 hex 前 16 位,作为对外可定位 id(绝不暴露原 token)。
func SessionID(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])[:sessionIDPrefixLen]
}

// SessionMeta 是会话的对外只读元数据(用于列表;不含原 token)。
type SessionMeta struct {
	ID         string // token 的 sha256 hex 前缀
	CreatedAt  time.Time
	LastSeenAt time.Time
	ExpiresAt  time.Time
	Current    bool // 是否为当前请求所属会话
}

// List 返回所有未过期会话的元数据(不含原 token)。currentToken 用于标识 current。
// 按 created_at DESC 排序(最新在前)。
func (ss *SessionStore) List(currentToken string) ([]SessionMeta, error) {
	now := time.Now().UTC().Format(time.RFC3339)
	rows, err := ss.db.Query(
		`SELECT token, created_at, last_seen_at, expires_at
		 FROM sessions WHERE expires_at >= ? ORDER BY created_at DESC`,
		now,
	)
	if err != nil {
		return nil, fmt.Errorf("auth: list sessions: %w", err)
	}
	defer func() { _ = rows.Close() }()

	currentID := SessionID(currentToken)
	var out []SessionMeta
	for rows.Next() {
		var token, createdStr, lastSeenStr, expiresStr string
		if err := rows.Scan(&token, &createdStr, &lastSeenStr, &expiresStr); err != nil {
			return nil, fmt.Errorf("auth: scan session: %w", err)
		}
		m := SessionMeta{ID: SessionID(token)}
		if m.CreatedAt, err = time.Parse(time.RFC3339, createdStr); err != nil {
			return nil, fmt.Errorf("auth: parse created_at: %w", err)
		}
		if m.LastSeenAt, err = time.Parse(time.RFC3339, lastSeenStr); err != nil {
			return nil, fmt.Errorf("auth: parse last_seen_at: %w", err)
		}
		if m.ExpiresAt, err = time.Parse(time.RFC3339, expiresStr); err != nil {
			return nil, fmt.Errorf("auth: parse expires_at: %w", err)
		}
		m.Current = m.ID == currentID
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("auth: iterate sessions: %w", err)
	}
	return out, nil
}

// DeleteByIDPrefix 删除 sha256 前缀匹配 id 的会话。返回删除行数。
// 因 id 为 token 自身 sha256 即时算(未存库),逐行比对 token 的 sha256 定位。
func (ss *SessionStore) DeleteByIDPrefix(id string) (int64, error) {
	rows, err := ss.db.Query(`SELECT token FROM sessions`)
	if err != nil {
		return 0, fmt.Errorf("auth: scan tokens: %w", err)
	}
	var target string
	for rows.Next() {
		var token string
		if err := rows.Scan(&token); err != nil {
			_ = rows.Close()
			return 0, fmt.Errorf("auth: scan token: %w", err)
		}
		if SessionID(token) == id {
			target = token
			break
		}
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return 0, fmt.Errorf("auth: iterate tokens: %w", err)
	}
	_ = rows.Close()
	if target == "" {
		return 0, nil
	}
	res, err := ss.db.Exec(`DELETE FROM sessions WHERE token = ?`, target)
	if err != nil {
		return 0, fmt.Errorf("auth: delete session by id: %w", err)
	}
	n, _ := res.RowsAffected()
	return n, nil
}

// DeleteOthers 删除除 keepToken 外的所有会话(改口令后使其它会话失效)。返回删除行数。
//
// keepToken 为空时**拒绝**(返回错误):空 token 会让 `token != ”` 匹配所有真实会话,
// 误删包括当前会话在内的全部,与「当前会话保留」语义相悖。这是危险原语的兜底护栏
// (调用方应保证传入有效的当前会话 token)。
func (ss *SessionStore) DeleteOthers(keepToken string) (int64, error) {
	if keepToken == "" {
		return 0, errors.New("auth: DeleteOthers 拒绝空 keepToken(避免误删全部会话)")
	}
	res, err := ss.db.Exec(`DELETE FROM sessions WHERE token != ?`, keepToken)
	if err != nil {
		return 0, fmt.Errorf("auth: delete other sessions: %w", err)
	}
	n, _ := res.RowsAffected()
	return n, nil
}

// randHex 生成 n 字节随机数并返回 hex 编码字串。
func randHex(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
