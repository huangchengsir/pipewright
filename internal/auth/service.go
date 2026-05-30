package auth

import (
	"crypto/subtle"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"time"
)

// Authenticator 定义认证领域对外接口。(-er 命名约定)
type Authenticator interface {
	// Login 以用户名/口令认证;成功返回新 Session,失败或锁定返回相应错误。
	Login(username, password string) (*Session, error)
	// Verify 校验会话 token 是否有效;有效返回 Session。
	Verify(token string) (*Session, error)
	// Logout 使指定 token 的会话失效。
	Logout(token string) error
	// AdminUsername 返回当前存储的管理员用户名(用于回显)。
	AdminUsername() (string, error)
}

// ErrInvalidCredentials 用户名或口令错误。
var ErrInvalidCredentials = errors.New("auth: invalid credentials")

// ErrInvalidCurrentPassword 改口令时当前口令校验失败。
var ErrInvalidCurrentPassword = errors.New("auth: invalid current password")

// ErrWeakPassword 新口令不满足强度规则(至少 8 位、非空)。
var ErrWeakPassword = errors.New("auth: weak password")

// minPasswordLen 新口令最小长度。
const minPasswordLen = 8

// ErrLockedOut 登录失败次数过多被锁定。
type ErrLockedOut struct {
	RetryAfter time.Duration
}

func (e *ErrLockedOut) Error() string {
	mins := int(e.RetryAfter.Minutes()) + 1
	return fmt.Sprintf("auth: locked out, retry after %d minutes", mins)
}

// dummyHash 是一个**预计算的静态** argon2id PHC 串(参数与真实哈希一致:
// m=64MB,t=1,p=4),用于在用户名不匹配 / admin 不存在时仍执行一次口令校验,
// 使「用户名错」与「口令错」两条失败路径耗时一致,消除时序枚举侧信道。
// 其明文不可知且永不匹配真实输入。
//
// 必须用静态常量、而非在 init() 里跑 argon2 生成:argon2id 单次需 ~64MB,
// init 期生成会让进程**空载常驻内存**永久抬高 ~64MB(实测 18MB→82MB),
// 逼近 NFR-4 的 ≤100MB 红线。静态常量下 64MB 仅在实际登录校验时**瞬时**分配。
const dummyHash = "$argon2id$v=19$m=65536,t=1,p=4$WyWRpzFnab6vlgFLECjL9g$ZJhWU5lKWEo1T7NhxBxQf70Gk+We+sUQ8SO52b8nnxQ"

// Service 实现 Authenticator,依赖 *sql.DB(经构造函数注入)。
type Service struct {
	db       *sql.DB
	sessions *SessionStore
	lockout  *LockoutManager
}

// NewService 构造 Service;clock 为 nil 时使用 RealClock。
func NewService(db *sql.DB, clock Clock) *Service {
	return &Service{
		db:       db,
		sessions: NewSessionStore(db),
		lockout:  NewLockoutManager(clock),
	}
}

// Bootstrap 首次启动引导:若 admin_user 为空且提供了口令则 argon2id 哈希入库。
// 已存在则忽略 env;无口令且无 admin 时明确日志提示(不 panic,不阻断 /healthz)。
func (s *Service) Bootstrap(username, password string) error {
	var count int
	if err := s.db.QueryRow(`SELECT COUNT(1) FROM admin_user`).Scan(&count); err != nil {
		return fmt.Errorf("auth: bootstrap check: %w", err)
	}
	if count > 0 {
		// 已有管理员,忽略 env(口令变更在 Story 1.7)。
		return nil
	}
	if password == "" {
		// 无口令且无 admin:提示如何设置,但不阻断服务。
		log.Printf("[auth] 警告:未设置管理员账号。请通过 DEVOPSTOOL_ADMIN_PASSWORD 环境变量设置口令后重启。")
		return nil
	}
	if username == "" {
		username = "admin"
	}
	hash, err := HashPassword(password)
	if err != nil {
		return fmt.Errorf("auth: bootstrap hash: %w", err)
	}
	now := time.Now().UTC().Format(time.RFC3339)
	_, err = s.db.Exec(
		`INSERT INTO admin_user (id, username, password_hash, created_at, updated_at)
		 VALUES (1, ?, ?, ?, ?)`,
		username, hash, now, now,
	)
	if err != nil {
		return fmt.Errorf("auth: bootstrap insert: %w", err)
	}
	log.Printf("[auth] 管理员账号已初始化:username=%s", username)
	return nil
}

// Login 认证用户名/口令;通过则创建新会话,失败则记录失败计数。
func (s *Service) Login(username, password string) (*Session, error) {
	// 查询管理员记录。即便不匹配也要继续走口令校验路径以消除时序枚举。
	var storedUsername, storedHash string
	err := s.db.QueryRow(
		`SELECT username, password_hash FROM admin_user WHERE id = 1`,
	).Scan(&storedUsername, &storedHash)

	usernameMatches := false
	verifyHash := dummyHash
	switch {
	case err == nil:
		// 恒定时间比较用户名,避免按字节短路泄露。
		usernameMatches = subtle.ConstantTimeCompare([]byte(storedUsername), []byte(username)) == 1
		if usernameMatches {
			verifyHash = storedHash
		}
	case errors.Is(err, sql.ErrNoRows):
		// 无 admin 账号:用 dummy hash 校验,使耗时与正常路径一致。
		usernameMatches = false
	default:
		return nil, fmt.Errorf("auth: query admin: %w", err)
	}

	// 在单次持锁内完成「检查锁定 → 口令校验 → 记失败/重置」,消除 TOCTOU。
	// 无论用户名是否匹配都执行一次 VerifyPassword(真实 hash 或 dummy),
	// 使「用户名错」与「口令错」两条失败路径耗时一致。
	ok, locked, remaining, gErr := s.lockout.Guard(func() (bool, error) {
		pwOK, vErr := VerifyPassword(password, verifyHash)
		if vErr != nil {
			return false, vErr
		}
		// 用户名不匹配时即使口令校验偶然通过(dummy 永不通过)也判失败。
		return usernameMatches && pwOK, nil
	})
	if locked {
		return nil, &ErrLockedOut{RetryAfter: remaining}
	}
	if gErr != nil {
		return nil, fmt.Errorf("auth: verify password: %w", gErr)
	}
	if !ok {
		return nil, ErrInvalidCredentials
	}

	// 认证成功:创建会话(Guard 已重置计数)。
	sess, err := s.sessions.Create()
	if err != nil {
		return nil, fmt.Errorf("auth: create session: %w", err)
	}
	return sess, nil
}

// PurgeExpiredSessions 删除 DB 中所有已过期会话,返回删除行数。
// 由入口在启动后调用一次,清理惰性删除遗漏的过期会话。
func (s *Service) PurgeExpiredSessions() (int64, error) {
	return s.sessions.PurgeExpired()
}

// AdminUsername 返回当前存储的管理员用户名;无 admin 时返回 ErrSessionNotFound 语义之外的空。
func (s *Service) AdminUsername() (string, error) {
	var u string
	err := s.db.QueryRow(`SELECT username FROM admin_user WHERE id = 1`).Scan(&u)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", ErrSessionNotFound
		}
		return "", fmt.Errorf("auth: query admin username: %w", err)
	}
	return u, nil
}

// Verify 校验会话 token;有效返回 Session。
func (s *Service) Verify(token string) (*Session, error) {
	sess, err := s.sessions.Get(token)
	if err != nil {
		if errors.Is(err, ErrSessionNotFound) {
			return nil, ErrSessionNotFound
		}
		return nil, fmt.Errorf("auth: verify session: %w", err)
	}
	return sess, nil
}

// Logout 删除会话。
func (s *Service) Logout(token string) error {
	return s.sessions.Delete(token)
}

// ChangePassword 校验当前口令 → 更新为新 argon2id 哈希 → 删除除当前会话外的所有会话。
//   - current 错 → ErrInvalidCurrentPassword(401)。
//   - new 不满足强度(< minPasswordLen 或空)→ ErrWeakPassword(422)。
//   - 成功:更新 admin_user.password_hash;DELETE WHERE token != currentToken(当前不掉线)。
//
// 与当前口令相同的新口令也允许(不强制不同)。
func (s *Service) ChangePassword(current, newPassword, currentToken string) error {
	// 强度校验先行(无需触 argon2,避免无谓内存分配)。
	if len(newPassword) < minPasswordLen {
		return ErrWeakPassword
	}

	var storedHash string
	err := s.db.QueryRow(`SELECT password_hash FROM admin_user WHERE id = 1`).Scan(&storedHash)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrInvalidCurrentPassword
		}
		return fmt.Errorf("auth: query admin hash: %w", err)
	}

	ok, err := VerifyPassword(current, storedHash)
	if err != nil {
		return fmt.Errorf("auth: verify current password: %w", err)
	}
	if !ok {
		return ErrInvalidCurrentPassword
	}

	newHash, err := HashPassword(newPassword)
	if err != nil {
		return fmt.Errorf("auth: hash new password: %w", err)
	}
	now := time.Now().UTC().Format(time.RFC3339)
	if _, err := s.db.Exec(
		`UPDATE admin_user SET password_hash = ?, updated_at = ? WHERE id = 1`,
		newHash, now,
	); err != nil {
		return fmt.Errorf("auth: update password hash: %w", err)
	}

	// 改口令后使其它会话失效(当前 token 保留,用户不掉线)。
	if _, err := s.sessions.DeleteOthers(currentToken); err != nil {
		return fmt.Errorf("auth: revoke other sessions: %w", err)
	}
	return nil
}

// ListSessions 返回所有未过期会话元数据(不含原 token);currentToken 标识 current。
func (s *Service) ListSessions(currentToken string) ([]SessionMeta, error) {
	return s.sessions.List(currentToken)
}

// RevokeSession 按 id(sha256 前缀)删除会话;无匹配 → ErrSessionNotFound。
func (s *Service) RevokeSession(id string) error {
	n, err := s.sessions.DeleteByIDPrefix(id)
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrSessionNotFound
	}
	return nil
}
