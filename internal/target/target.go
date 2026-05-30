// Package target 是「目标服务器」的领域层 + 通用 SSH 执行/会话基座。
//
// 一台服务器按引用绑定一条 SSH 凭据:DB 中只存 credential_id,绝无任何明文私钥/口令
// (AC-SEC-01)。Exec/Test 时,平台经 vault.OpenSecret 取凭据明文(私钥或口令)在进程内
// 装配 ssh.ClientConfig,用完即弃,绝不入库/日志/响应/错误体。
//
// 本包**通用化**:对外只暴露 Exec(serverID, cmd []string) / Test(serverID) /
// CRUD,不含任何部署语义。Epic 4 部署(4-2)与 Epic 6 运维都在其上构建,只消费这些签名、
// 不改它们(冻结契约)。
//
// AC-SEC-02:命令以 []string(程序 + 参数)传入,经各参数 shell 转义后再交 SSH session,
// 绝不让调用方拼接原始 shell 字符串;杜绝命令注入。
package target

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/huangjiawei/devopstool/internal/vault"
)

// 领域错误。错误体永不含明文/私钥/口令/master key/内部栈。
var (
	// ErrNotFound 表示服务器不存在。
	ErrNotFound = errors.New("target: server not found")
	// ErrEmptyName 表示服务器名称为空。
	ErrEmptyName = errors.New("target: name must not be empty")
	// ErrEmptyHost 表示主机地址为空。
	ErrEmptyHost = errors.New("target: host must not be empty")
	// ErrEmptyUser 表示登录用户为空。
	ErrEmptyUser = errors.New("target: user must not be empty")
	// ErrEmptyCredentialID 表示未选择 SSH 凭据。
	ErrEmptyCredentialID = errors.New("target: credential id must not be empty")
	// ErrInvalidPort 表示端口非法(非 1..65535)。
	ErrInvalidPort = errors.New("target: port must be in 1..65535")
	// ErrCredentialNotFound 表示引用的凭据不存在(下拉项已被删除等)。
	ErrCredentialNotFound = errors.New("target: referenced credential not found")
	// ErrVaultUnconfigured 表示保险库未配置 master key,无法取 SSH 凭据。
	ErrVaultUnconfigured = errors.New("target: vault unconfigured")
	// ErrAuth 表示 SSH 认证失败(密钥/口令无效或无权限)。错误体不含凭据明文。
	ErrAuth = errors.New("target: ssh authentication failed")
	// ErrUnreachable 表示无法连接(端口关闭/主机不可达/超时)。
	ErrUnreachable = errors.New("target: server unreachable")
	// ErrInvalidCredential 表示凭据明文不是可用的 SSH 私钥/口令(解析失败)。
	ErrInvalidCredential = errors.New("target: credential is not a usable ssh key or password")
)

// DefaultPort 是未显式给定端口时的 SSH 默认端口。
const DefaultPort = 22

// probeCommand 是 Test 连通性所跑的只读探测命令(AC-SEC-02:array 形式)。
var probeCommand = []string{"uname", "-a"}

// Server 是服务器领域模型。绝不含凭据明文,只持 credentialId 引用。
type Server struct {
	ID           string
	Name         string
	Host         string
	Port         int
	User         string
	CredentialID string
	// CredentialName 是冗余只读展示名(join credentials),便于列表展示;非持久列。
	CredentialName string
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// CreateInput 是登记服务器的入参。
type CreateInput struct {
	Name         string
	Host         string
	Port         int // <=0 时归一为 DefaultPort
	User         string
	CredentialID string
}

// UpdateInput 是更新服务器的入参;指针字段为 nil 表示不修改。
type UpdateInput struct {
	Name         *string
	Host         *string
	Port         *int
	User         *string
	CredentialID *string
}

// ExecResult 是通用 Exec 的结果(冻结契约;Epic 4/6 消费)。
type ExecResult struct {
	Stdout   string
	Stderr   string
	ExitCode int
}

// TestResult 是测试连接的结果(冻结契约)。Output 为探测命令的截断输出。
type TestResult struct {
	OK        bool
	LatencyMs int64
	Output    string
	// Err 为人读错误(失败时非空);绝不含凭据明文。成功时为空。
	Err string
}

// Service 定义目标服务器领域 + 通用 SSH 执行层对外接口(冻结契约,Epic 4/6 import)。
type Service interface {
	// Get 返回单个服务器视图(无明文)。
	Get(ctx context.Context, id string) (*Server, error)
	// List 返回所有服务器(join credentials 取展示名;无明文)。
	List(ctx context.Context) ([]*Server, error)
	// Create 校验入参后入库;返回服务器视图(无明文)。不在此触网(纯登记)。
	Create(ctx context.Context, in CreateInput) (*Server, error)
	// Update 改名/改 host/port/user/改绑凭据;返回更新后视图。
	Update(ctx context.Context, id string, in UpdateInput) (*Server, error)
	// Delete 删除服务器。
	Delete(ctx context.Context, id string) error
	// Test 经 SSH 实连并跑只读探测命令(uname -a),返回成功/延迟/输出或人读错误。
	Test(ctx context.Context, id string) (*TestResult, error)
	// Exec 通用执行:对指定服务器经 SSH 跑 cmd(程序 + 参数,**array 不拼 shell**,AC-SEC-02),
	// 返回 stdout/stderr/exitCode。超时经 ctx。供 Epic 4 部署 / Epic 6 运维复用。
	Exec(ctx context.Context, serverID string, cmd []string) (*ExecResult, error)
}

// SSHDialer 抽象「用装配好的 SSH 配置连服务器并跑一条 array 命令」的能力。
// 注入便于测试(可用 stub 替换真实网络拨号),也便于 4-2/Epic 6 在不触网下做单测。
type SSHDialer interface {
	// Run 连 addr(host:port)、以 cfg 认证、跑 cmd(已是程序+参数 array),返回结果。
	// 连接/认证失败映射为 ErrUnreachable / ErrAuth(绝不含凭据明文)。
	Run(ctx context.Context, addr string, cfg SSHConfig, cmd []string) (*ExecResult, error)
}

// SSHConfig 是拨号所需的最小认证材料(进程内,用完即弃)。
// 二选一:PrivateKey(PEM 私钥明文)或 Password;两者皆空则无可用认证法。
type SSHConfig struct {
	User       string
	PrivateKey string // PEM 私钥明文(经 vault 取出,用完即弃)
	Password   string // 口令明文(经 vault 取出,用完即弃)
}

// service 是 store + vault + dialer 支撑的 Service 实现。
type service struct {
	db     *sql.DB
	vault  vault.Vault
	dialer SSHDialer
}

// New 构造 Service。
//   - db:仅供本包通过参数化 SQL 触库。
//   - v:凭据保险库;v 为 nil 或未配置 master key 时,Exec/Test 返回 ErrVaultUnconfigured。
//   - dialer:SSH 拨号器;为 nil 时使用默认 x/crypto/ssh 实现(sshDialer)。
//
// 不在此做任何重活(无 init() 副作用,避免抬高空载内存)。
func New(db *sql.DB, v vault.Vault, dialer SSHDialer) Service {
	if dialer == nil {
		dialer = sshDialer{}
	}
	return &service{db: db, vault: v, dialer: dialer}
}

// normalizePort 归一端口:<=0 → DefaultPort。
func normalizePort(p int) int {
	if p <= 0 {
		return DefaultPort
	}
	return p
}

// validateCreate 校验登记入参的必填项与端口范围。
func validateCreate(in CreateInput) error {
	if in.Name == "" {
		return ErrEmptyName
	}
	if in.Host == "" {
		return ErrEmptyHost
	}
	if in.User == "" {
		return ErrEmptyUser
	}
	if in.CredentialID == "" {
		return ErrEmptyCredentialID
	}
	if p := normalizePort(in.Port); p < 1 || p > 65535 {
		return ErrInvalidPort
	}
	return nil
}

func (s *service) Create(ctx context.Context, in CreateInput) (*Server, error) {
	if err := validateCreate(in); err != nil {
		return nil, err
	}
	port := normalizePort(in.Port)

	id := uuid.NewString()
	now := time.Now().UTC()
	nowStr := now.Format(time.RFC3339)

	_, err := s.db.ExecContext(ctx,
		`INSERT INTO servers (id, name, host, port, user, credential_id, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		id, in.Name, in.Host, port, in.User, in.CredentialID, nowStr, nowStr,
	)
	if err != nil {
		if isForeignKeyErr(err) {
			return nil, ErrCredentialNotFound
		}
		return nil, fmt.Errorf("target: insert: %w", err)
	}
	return s.Get(ctx, id)
}

func (s *service) List(ctx context.Context) ([]*Server, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT s.id, s.name, s.host, s.port, s.user, s.credential_id,
		        COALESCE(c.name, ''), s.created_at, s.updated_at
		 FROM servers s
		 LEFT JOIN credentials c ON c.id = s.credential_id
		 ORDER BY s.created_at DESC, s.id`,
	)
	if err != nil {
		return nil, fmt.Errorf("target: list: %w", err)
	}
	defer func() { _ = rows.Close() }()

	out := make([]*Server, 0)
	for rows.Next() {
		srv, err := scanServer(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, srv)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("target: iterate: %w", err)
	}
	return out, nil
}

func (s *service) Get(ctx context.Context, id string) (*Server, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT s.id, s.name, s.host, s.port, s.user, s.credential_id,
		        COALESCE(c.name, ''), s.created_at, s.updated_at
		 FROM servers s
		 LEFT JOIN credentials c ON c.id = s.credential_id
		 WHERE s.id = ?`, id,
	)
	srv, err := scanServer(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return srv, nil
}

func (s *service) Update(ctx context.Context, id string, in UpdateInput) (*Server, error) {
	// 先取当前行。
	var name, host, user, credentialID string
	var port int
	err := s.db.QueryRowContext(ctx,
		`SELECT name, host, port, user, credential_id FROM servers WHERE id = ?`, id,
	).Scan(&name, &host, &port, &user, &credentialID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("target: load: %w", err)
	}

	if in.Name != nil {
		if *in.Name == "" {
			return nil, ErrEmptyName
		}
		name = *in.Name
	}
	if in.Host != nil {
		if *in.Host == "" {
			return nil, ErrEmptyHost
		}
		host = *in.Host
	}
	if in.User != nil {
		if *in.User == "" {
			return nil, ErrEmptyUser
		}
		user = *in.User
	}
	if in.Port != nil {
		p := normalizePort(*in.Port)
		if p < 1 || p > 65535 {
			return nil, ErrInvalidPort
		}
		port = p
	}
	if in.CredentialID != nil {
		if *in.CredentialID == "" {
			return nil, ErrEmptyCredentialID
		}
		credentialID = *in.CredentialID
	}

	nowStr := time.Now().UTC().Format(time.RFC3339)
	_, err = s.db.ExecContext(ctx,
		`UPDATE servers SET name = ?, host = ?, port = ?, user = ?, credential_id = ?, updated_at = ? WHERE id = ?`,
		name, host, port, user, credentialID, nowStr, id,
	)
	if err != nil {
		if isForeignKeyErr(err) {
			return nil, ErrCredentialNotFound
		}
		return nil, fmt.Errorf("target: update: %w", err)
	}
	return s.Get(ctx, id)
}

func (s *service) Delete(ctx context.Context, id string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM servers WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("target: delete: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *service) Test(ctx context.Context, id string) (*TestResult, error) {
	start := time.Now()
	res, err := s.Exec(ctx, id, probeCommand)
	latency := time.Since(start).Milliseconds()
	if err != nil {
		// 服务器/凭据不存在等定位类错误:上抛供 HTTP 层映射 404/422/503。
		if errors.Is(err, ErrNotFound) ||
			errors.Is(err, ErrCredentialNotFound) ||
			errors.Is(err, ErrVaultUnconfigured) {
			return nil, err
		}
		// 连接/认证/执行类错误:映射为 TestResult.ok=false + 人读错误(绝不含凭据明文)。
		return &TestResult{OK: false, LatencyMs: latency, Err: humanError(err)}, nil
	}
	return &TestResult{
		OK:        true,
		LatencyMs: latency,
		Output:    truncate(res.Stdout, 4096),
	}, nil
}

// Exec 取凭据明文装配 SSH 配置并跑 array 命令;凭据明文仅进程内存在,用完即弃。
func (s *service) Exec(ctx context.Context, serverID string, cmd []string) (*ExecResult, error) {
	if len(cmd) == 0 {
		return nil, fmt.Errorf("target: empty command")
	}
	srv, err := s.Get(ctx, serverID)
	if err != nil {
		return nil, err
	}
	if s.vault == nil {
		return nil, ErrVaultUnconfigured
	}

	// 取凭据明文(私钥或口令)。明文仅进程内,Exec 返回前清引用。
	secret, err := s.vault.Get(srv.CredentialID)
	if err != nil {
		switch {
		case errors.Is(err, vault.ErrVaultUnconfigured):
			return nil, ErrVaultUnconfigured
		case errors.Is(err, vault.ErrNotFound):
			return nil, ErrCredentialNotFound
		default:
			// 解密等内部错误:不泄漏细节,统一按认证错误对待。
			return nil, ErrAuth
		}
	}

	cfg := SSHConfig{User: srv.User}
	if looksLikePEM(secret) {
		cfg.PrivateKey = secret
	} else {
		cfg.Password = secret
	}

	addr := fmt.Sprintf("%s:%d", srv.Host, srv.Port)
	res, runErr := s.dialer.Run(ctx, addr, cfg, cmd)

	// 显式清明文引用,尽早不可达(明文不留)。
	secret = ""
	cfg.PrivateKey = ""
	cfg.Password = ""
	_ = secret

	if runErr != nil {
		return nil, runErr
	}
	return res, nil
}

// looksLikePEM 判定凭据明文是否为 PEM 私钥(决定走私钥还是口令认证)。
func looksLikePEM(secret string) bool {
	return strings.Contains(secret, "-----BEGIN") && strings.Contains(secret, "PRIVATE KEY")
}

// humanError 把领域错误映射为人读文案(绝不含凭据明文/内部栈)。
func humanError(err error) string {
	switch {
	case errors.Is(err, ErrAuth):
		return "SSH 认证失败:密钥或口令无效,或无登录权限"
	case errors.Is(err, ErrUnreachable):
		return "无法连接服务器:端口未开放、主机不可达或超时"
	case errors.Is(err, ErrInvalidCredential):
		return "凭据不是可用的 SSH 私钥或口令"
	case errors.Is(err, context.DeadlineExceeded):
		return "连接超时"
	default:
		return "连接失败"
	}
}

// truncate 截断字符串到 max 字节(防超大输出撑爆响应/内存)。
func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "…(已截断)"
}

// scanner 抽象 *sql.Row 与 *sql.Rows 的 Scan。
type scanner interface {
	Scan(dest ...any) error
}

// scanServer 把一行扫描为 Server(永不读任何密文/明文列)。
func scanServer(sc scanner) (*Server, error) {
	var srv Server
	var createdStr, updatedStr string
	if err := sc.Scan(
		&srv.ID, &srv.Name, &srv.Host, &srv.Port, &srv.User, &srv.CredentialID,
		&srv.CredentialName, &createdStr, &updatedStr,
	); err != nil {
		return nil, err
	}
	created, err := time.Parse(time.RFC3339, createdStr)
	if err != nil {
		return nil, fmt.Errorf("target: parse created_at: %w", err)
	}
	updated, err := time.Parse(time.RFC3339, updatedStr)
	if err != nil {
		return nil, fmt.Errorf("target: parse updated_at: %w", err)
	}
	srv.CreatedAt = created
	srv.UpdatedAt = updated
	return &srv, nil
}

// isForeignKeyErr 判断错误是否为外键约束失败(modernc sqlite 文本含 FOREIGN KEY)。
func isForeignKeyErr(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToUpper(err.Error())
	return strings.Contains(msg, "FOREIGN KEY")
}
