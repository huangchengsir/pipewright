// Package vault 是凭据加密保险库的领域层。
//
// 凭据明文(令牌/SSH 私钥/镜像账号)经 NaCl secretbox 加密后,仅以**密文 BLOB**
// 入库;DB 中绝无明文与 PEM 字段(AC-SEC-01)。master key 来自环境变量/文件
// (见 internal/config),绝不入库。对外(REST)只暴露掩码 + 元数据;明文仅经
// Vault.Get 在进程内供领域调用方(克隆/SSH/推镜像)取用。
//
// master key 缺失时 Vault 进入「未配置」态:所有操作返回 ErrVaultUnconfigured,
// 平台仍正常启动(不 panic)。
package vault

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

// 凭据类型枚举(DB 存 snake_case 字串;JSON camelCase 字段)。
const (
	TypeGitToken    = "git_token"
	TypeSSHKey      = "ssh_key"
	TypeRegistry    = "registry"
	TypeSSHPassword = "ssh_password" // SSH 登录密码(非 PEM);SSH 层据 looksLikePEM 自动按密码认证
)

// 领域错误。错误体永不含明文/密文/master key。
var (
	// ErrVaultUnconfigured 表示未配置 master key,保险库不可用。
	ErrVaultUnconfigured = errors.New("vault: master key not configured")
	// ErrNotFound 表示凭据不存在。
	ErrNotFound = errors.New("vault: credential not found")
	// ErrInvalidType 表示类型枚举非法。
	ErrInvalidType = errors.New("vault: invalid credential type")
	// ErrEmptySecret 表示创建/轮换时 secret 为空。
	ErrEmptySecret = errors.New("vault: secret must not be empty")
	// ErrEmptyName 表示名称为空。
	ErrEmptyName = errors.New("vault: name must not be empty")
	// ErrCredentialInUse 表示凭据正被项目/流水线配置引用,不可删除(防悬挂引用)。
	ErrCredentialInUse = errors.New("vault: credential in use")
)

// Credential 是对外可见的凭据视图:只含掩码 + 元数据,绝无明文/密文。
type Credential struct {
	ID          string
	Name        string
	Type        string
	Scope       string
	MaskedValue string
	LastUsedAt  *time.Time
	CreatedAt   time.Time
}

// CreateInput 是创建凭据的入参(含明文 secret,不被持久化为明文)。
type CreateInput struct {
	Name   string
	Type   string
	Scope  string
	Secret string
}

// UpdateInput 是更新凭据的入参;指针字段为 nil 表示不修改。
// 给 Secret 即轮换密钥(重新加密)。
type UpdateInput struct {
	Name   *string
	Scope  *string
	Secret *string
}

// Vault 定义保险库领域对外接口。(-er 约定的同类:领域聚合用 Vault)
type Vault interface {
	// Create 加密并持久化新凭据,返回掩码视图(不含明文)。
	Create(in CreateInput) (*Credential, error)
	// List 返回所有凭据的掩码视图(无明文/密文)。
	List() ([]Credential, error)
	// Get 解密并返回指定凭据的明文,**仅供进程内领域调用**;同时更新 last_used_at。
	Get(id string) (string, error)
	// Reveal 解密并返回指定凭据的明文,**不更新 last_used_at**(供脱敏登记等「只读取值、非真正使用」
	// 的场景:把凭据明文登记进 Masker 以便日志/诊断/通知出网前替换为 [MASKED],不应算「最近使用」)。
	// 未配置 master key → ErrVaultUnconfigured;不存在 → ErrNotFound;解密失败 → ErrDecrypt(不泄漏)。
	Reveal(id string) (string, error)
	// Exists 仅校验凭据是否存在,**不解密、不刷新 last_used_at**(供保存配置时校验引用,
	// 避免「仅编辑也算用过」的语义失真与无谓解密)。未配置 master key → ErrVaultUnconfigured。
	Exists(id string) (bool, error)
	// Update 改名/改作用域/轮换密钥;返回更新后的掩码视图。
	Update(id string, in UpdateInput) (*Credential, error)
	// Delete 删除凭据。
	Delete(id string) error
	// SealSecret 用 master key 加密任意明文,返回密文 BLOB(nonce||box),供其它
	// 领域(如 webhook 签名密钥)复用同一 secretbox 加密原语。未配置 master key
	// 时返回 ErrVaultUnconfigured。绝不持久化/日志明文。
	SealSecret(plaintext []byte) ([]byte, error)
	// OpenSecret 解密 SealSecret 产出的密文 BLOB;认证失败返回 ErrDecrypt(不泄漏细节)。
	// 未配置 master key 时返回 ErrVaultUnconfigured。
	OpenSecret(sealed []byte) ([]byte, error)
}

// service 是 store 支撑的 Vault 实现。master key 为 nil 时为「未配置」态。
type service struct {
	db  *sql.DB
	key *[keySize]byte // nil ⇒ 未配置
}

// New 构造 Vault。key 为 nil 时进入未配置态(操作返回 ErrVaultUnconfigured)。
// 不在此做任何加密/重计算(避免抬高空载内存)。
func New(db *sql.DB, key *[keySize]byte) Vault {
	return &service{db: db, key: key}
}

// Configured 报告保险库是否已配置 master key(供 HTTP 层快速短路/友好提示)。
func (s *service) configured() bool { return s.key != nil }

// validateType 校验类型枚举。
func validateType(t string) error {
	switch t {
	case TypeGitToken, TypeSSHKey, TypeRegistry, TypeSSHPassword:
		return nil
	default:
		return ErrInvalidType
	}
}

func (s *service) Create(in CreateInput) (*Credential, error) {
	if !s.configured() {
		return nil, ErrVaultUnconfigured
	}
	if err := validateType(in.Type); err != nil {
		return nil, err
	}
	if in.Name == "" {
		return nil, ErrEmptyName
	}
	if in.Secret == "" {
		return nil, ErrEmptySecret
	}

	sealed, err := seal(s.key, []byte(in.Secret))
	if err != nil {
		return nil, err
	}
	masked := mask(in.Type, in.Secret)

	id := uuid.NewString()
	now := time.Now().UTC()
	nowStr := now.Format(time.RFC3339)

	_, err = s.db.Exec(
		`INSERT INTO credentials (id, name, type, scope, ciphertext, masked_value, last_used_at, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, NULL, ?, ?)`,
		id, in.Name, in.Type, in.Scope, sealed, masked, nowStr, nowStr,
	)
	if err != nil {
		return nil, fmt.Errorf("vault: insert credential: %w", err)
	}

	return &Credential{
		ID:          id,
		Name:        in.Name,
		Type:        in.Type,
		Scope:       in.Scope,
		MaskedValue: masked,
		LastUsedAt:  nil,
		CreatedAt:   now,
	}, nil
}

func (s *service) List() ([]Credential, error) {
	if !s.configured() {
		return nil, ErrVaultUnconfigured
	}
	rows, err := s.db.Query(
		`SELECT id, name, type, scope, masked_value, last_used_at, created_at
		 FROM credentials ORDER BY created_at DESC, id`,
	)
	if err != nil {
		return nil, fmt.Errorf("vault: list credentials: %w", err)
	}
	defer func() { _ = rows.Close() }()

	creds := make([]Credential, 0)
	for rows.Next() {
		c, err := scanCredential(rows)
		if err != nil {
			return nil, err
		}
		creds = append(creds, *c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("vault: iterate credentials: %w", err)
	}
	return creds, nil
}

func (s *service) Get(id string) (string, error) {
	if !s.configured() {
		return "", ErrVaultUnconfigured
	}
	var sealed []byte
	err := s.db.QueryRow(`SELECT ciphertext FROM credentials WHERE id = ?`, id).Scan(&sealed)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", ErrNotFound
		}
		return "", fmt.Errorf("vault: get credential: %w", err)
	}
	plaintext, err := open(s.key, sealed)
	if err != nil {
		return "", err // ErrDecrypt:不泄漏明文/密钥
	}
	// 更新最近使用时间(尽力而为:失败不影响取用)。
	now := time.Now().UTC().Format(time.RFC3339)
	_, _ = s.db.Exec(`UPDATE credentials SET last_used_at = ? WHERE id = ?`, now, id)
	return string(plaintext), nil
}

// Reveal 同 Get 但**不更新 last_used_at**(供脱敏登记等只读取值场景)。
func (s *service) Reveal(id string) (string, error) {
	if !s.configured() {
		return "", ErrVaultUnconfigured
	}
	var sealed []byte
	err := s.db.QueryRow(`SELECT ciphertext FROM credentials WHERE id = ?`, id).Scan(&sealed)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", ErrNotFound
		}
		return "", fmt.Errorf("vault: reveal credential: %w", err)
	}
	plaintext, err := open(s.key, sealed)
	if err != nil {
		return "", err // ErrDecrypt:不泄漏明文/密钥
	}
	return string(plaintext), nil
}

func (s *service) Exists(id string) (bool, error) {
	if !s.configured() {
		return false, ErrVaultUnconfigured
	}
	var one int
	err := s.db.QueryRow(`SELECT 1 FROM credentials WHERE id = ?`, id).Scan(&one)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return false, fmt.Errorf("vault: check credential exists: %w", err)
	}
	return true, nil
}

func (s *service) Update(id string, in UpdateInput) (*Credential, error) {
	if !s.configured() {
		return nil, ErrVaultUnconfigured
	}

	// 先取出当前行(需 type 以便在轮换 secret 时重算掩码)。
	var name, credType, scope, masked string
	err := s.db.QueryRow(
		`SELECT name, type, scope, masked_value FROM credentials WHERE id = ?`, id,
	).Scan(&name, &credType, &scope, &masked)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("vault: load credential: %w", err)
	}

	var newSealed []byte
	rotate := false
	if in.Name != nil {
		if *in.Name == "" {
			return nil, ErrEmptyName
		}
		name = *in.Name
	}
	if in.Scope != nil {
		scope = *in.Scope
	}
	if in.Secret != nil {
		if *in.Secret == "" {
			return nil, ErrEmptySecret
		}
		sealed, err := seal(s.key, []byte(*in.Secret))
		if err != nil {
			return nil, err
		}
		newSealed = sealed
		masked = mask(credType, *in.Secret)
		rotate = true
	}

	nowStr := time.Now().UTC().Format(time.RFC3339)
	if rotate {
		_, err = s.db.Exec(
			`UPDATE credentials SET name = ?, scope = ?, ciphertext = ?, masked_value = ?, updated_at = ? WHERE id = ?`,
			name, scope, newSealed, masked, nowStr, id,
		)
	} else {
		_, err = s.db.Exec(
			`UPDATE credentials SET name = ?, scope = ?, updated_at = ? WHERE id = ?`,
			name, scope, nowStr, id,
		)
	}
	if err != nil {
		return nil, fmt.Errorf("vault: update credential: %w", err)
	}

	// 回读完整视图(含 last_used_at/created_at)。
	return s.getView(id)
}

func (s *service) Delete(id string) error {
	if !s.configured() {
		return ErrVaultUnconfigured
	}
	// 在用守卫:被流水线配置 secret 引用的凭据不可删,否则悬挂引用使该项目配置再不可保存。
	// pipeline_settings 的引用以 JSON 存(无外键),此处显式 LIKE 检查;credentialId 为 UUID,
	// 无子串歧义。表不存在(迁移未应用)时 QueryRow 报错,跳过此检查、退回普通删除。
	var refCount int
	if err := s.db.QueryRow(
		`SELECT COUNT(*) FROM pipeline_settings WHERE build_json LIKE ? OR environments_json LIKE ? OR steps_json LIKE ?`,
		"%"+id+"%", "%"+id+"%", "%"+id+"%",
	).Scan(&refCount); err == nil && refCount > 0 {
		return ErrCredentialInUse
	}
	res, err := s.db.Exec(`DELETE FROM credentials WHERE id = ?`, id)
	if err != nil {
		// projects.credential_id 等外键(ON DELETE RESTRICT)引用 → 约束错误,映射为在用。
		if isConstraintErr(err) {
			return ErrCredentialInUse
		}
		return fmt.Errorf("vault: delete credential: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// isConstraintErr 判断错误是否为 SQLite 约束失败(外键/RESTRICT 等;modernc 文本匹配)。
func isConstraintErr(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToUpper(err.Error())
	return strings.Contains(msg, "FOREIGN KEY") || strings.Contains(msg, "CONSTRAINT")
}

func (s *service) SealSecret(plaintext []byte) ([]byte, error) {
	if !s.configured() {
		return nil, ErrVaultUnconfigured
	}
	return seal(s.key, plaintext)
}

func (s *service) OpenSecret(sealed []byte) ([]byte, error) {
	if !s.configured() {
		return nil, ErrVaultUnconfigured
	}
	return open(s.key, sealed)
}

// getView 回读单条凭据的掩码视图。
func (s *service) getView(id string) (*Credential, error) {
	row := s.db.QueryRow(
		`SELECT id, name, type, scope, masked_value, last_used_at, created_at
		 FROM credentials WHERE id = ?`, id,
	)
	c, err := scanCredential(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return c, nil
}

// scanner 抽象 *sql.Row 与 *sql.Rows 的 Scan。
type scanner interface {
	Scan(dest ...any) error
}

// scanCredential 把一行扫描为 Credential 掩码视图(永不读 ciphertext)。
func scanCredential(sc scanner) (*Credential, error) {
	var c Credential
	var lastUsedStr sql.NullString
	var createdStr string
	if err := sc.Scan(&c.ID, &c.Name, &c.Type, &c.Scope, &c.MaskedValue, &lastUsedStr, &createdStr); err != nil {
		return nil, err
	}
	created, err := time.Parse(time.RFC3339, createdStr)
	if err != nil {
		return nil, fmt.Errorf("vault: parse created_at: %w", err)
	}
	c.CreatedAt = created
	if lastUsedStr.Valid && lastUsedStr.String != "" {
		t, err := time.Parse(time.RFC3339, lastUsedStr.String)
		if err != nil {
			return nil, fmt.Errorf("vault: parse last_used_at: %w", err)
		}
		c.LastUsedAt = &t
	}
	return &c, nil
}
