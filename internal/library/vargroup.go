package library

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/huangchengsir/pipewright/internal/configstate"
	"github.com/huangchengsir/pipewright/internal/tenancy"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/huangchengsir/pipewright/internal/pipeline"
	"github.com/huangchengsir/pipewright/internal/vault"
)

// 变量组领域错误。错误体不含任何明文 secret。
var (
	// ErrVarGroupNotFound 表示引用的变量组不存在。
	ErrVarGroupNotFound = errors.New("library: variable group not found")
	// ErrInvalidVarGroup 表示变量组校验失败(名称空)。
	ErrInvalidVarGroup = errors.New("library: invalid variable group")
	// ErrVarGroupNameTaken 表示变量组名已被占用(唯一约束)。
	ErrVarGroupNameTaken = errors.New("library: variable group name already in use")
)

// 透传 pipeline 的变量校验错误(供 handler 统一映射;变量组复用 settings.normalizeVars 语义)。
var (
	// ErrInvalidVar 表示变量校验失败(key 空 / 组内重复 / secret 缺 credentialId)。
	ErrInvalidVar = pipeline.ErrInvalidVar
	// ErrCredentialNotFound 表示 secret 引用的 credentialId 不存在于保险库。
	ErrCredentialNotFound = pipeline.ErrCredentialNotFound
	// ErrVaultUnconfigured 表示保险库未配置但存在 secret 引用,无法校验/掩码。
	ErrVaultUnconfigured = pipeline.ErrSettingsVaultUnconfigured
)

// VarGroup 是一份命名、可复用的变量集合(镜像每流水线变量模型)。Vars 复用 pipeline.BuildVar:
// 非 secret 存明文 Value;secret 只存 CredentialID 引用(明文绝不入库),MaskedValue 仅响应里回算。
type VarGroup struct {
	ID          string
	Name        string
	Description string
	Vars        []pipeline.BuildVar
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// VarGroupInput 是创建/更新变量组的入参(secret 项只传 credentialId,不传明文/掩码)。
type VarGroupInput struct {
	Name        string
	Description string
	Vars        []pipeline.BuildVar
}

// VarGroupService 定义变量组领域对外接口。
type VarGroupService interface {
	// List 返回所有变量组(按名称升序;secret 项回掩码)。
	List(ctx context.Context) ([]VarGroup, error)
	// Get 返回单个变量组(secret 项回掩码);不存在 → ErrVarGroupNotFound。
	Get(ctx context.Context, id string) (*VarGroup, error)
	// Create 校验(名称非空 + 变量经 pipeline.NormalizeVars:key 非空不重复、secret 引用存在性)→
	// 持久化(只存引用,绝无明文 secret)→ 回读(回掩码)。名称重复 → ErrVarGroupNameTaken。
	Create(ctx context.Context, in VarGroupInput) (*VarGroup, error)
	// Update 同 Create 的校验/持久化,按 id 覆盖;不存在 → ErrVarGroupNotFound。
	Update(ctx context.Context, id string, in VarGroupInput) (*VarGroup, error)
	// Delete 删除变量组;不存在 → ErrVarGroupNotFound。
	Delete(ctx context.Context, id string) error
}

type varGroupService struct {
	db    *sql.DB
	vault vault.Vault
}

// NewVarGroupService 构造 VarGroupService。
//   - db:经参数化 SQL 触库 + 回算掩码。
//   - v:凭据保险库,校验 secret 引用存在性;nil/未配置且存在 secret 引用 → ErrVaultUnconfigured。
//
// 不在此做任何重活(无 init() 副作用,避免抬高空载内存)。
func NewVarGroupService(db *sql.DB, v vault.Vault) VarGroupService {
	return &varGroupService{db: db, vault: v}
}

func (s *varGroupService) List(ctx context.Context) ([]VarGroup, error) {
	predicate, err := tenancy.QueryPredicate(ctx, s.db, "variable_group.read", "variable_groups.id")
	if err != nil {
		return nil, err
	}
	rows, err := configstate.Queryer(ctx, s.db).QueryContext(ctx,
		`SELECT id, name, description, vars_json, created_at, updated_at
		 FROM variable_groups WHERE `+predicate.SQL+` ORDER BY name ASC`, predicate.Args...)
	if err != nil {
		return nil, fmt.Errorf("library: list variable groups: %w", err)
	}
	defer func() { _ = rows.Close() }()

	// 先把所有行扫完(不在持有 rows 的同时再查库:单连接 SQLite 会自死锁)。
	out := make([]VarGroup, 0)
	for rows.Next() {
		g, err := scanGroupRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *g)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("library: iterate variable groups: %w", err)
	}
	_ = rows.Close()

	// rows 关闭后再回算掩码(每个 secret 引用单独查 credentials.masked_value)。
	for i := range out {
		if err := pipeline.ApplyVarMasks(ctx, s.db, out[i].Vars); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func (s *varGroupService) Get(ctx context.Context, id string) (*VarGroup, error) {
	predicate, err := tenancy.QueryPredicate(ctx, s.db, "variable_group.read", "variable_groups.id")
	if err != nil {
		return nil, err
	}
	args := append([]any{strings.TrimSpace(id)}, predicate.Args...)
	row := configstate.Queryer(ctx, s.db).QueryRowContext(ctx,
		`SELECT id, name, description, vars_json, created_at, updated_at
		 FROM variable_groups WHERE id = ? AND `+predicate.SQL, args...)
	g, err := scanGroupRow(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrVarGroupNotFound
	}
	if err != nil {
		return nil, err
	}
	if err := pipeline.ApplyVarMasks(ctx, s.db, g.Vars); err != nil {
		return nil, err
	}
	return g, nil
}

func (s *varGroupService) create(ctx context.Context, in VarGroupInput) (*VarGroup, error) {
	id := uuid.NewString()
	if err := tenancy.CheckNew(ctx, s.db, "variable_group.create", "variable_group", id); err != nil {
		return nil, err
	}
	name, varsJSON, err := s.validate(ctx, in)
	if err != nil {
		return nil, err
	}

	namespace, err := availableName(ctx, s.db, "variable_groups", id, name, ErrVarGroupNameTaken)
	if err != nil {
		return nil, err
	}
	nowStr := time.Now().UTC().Format(time.RFC3339)
	_, err = configstate.Queryer(ctx, s.db).ExecContext(ctx,
		`INSERT INTO variable_groups (id, name_scope, name, description, vars_json, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		id, namespace, name, strings.TrimSpace(in.Description), varsJSON, nowStr, nowStr,
	)
	if err != nil {
		if isUniqueErr(err) {
			return nil, ErrVarGroupNameTaken
		}
		return nil, fmt.Errorf("library: insert variable group: %w", err)
	}
	if err = tenancy.RecordOwnership(ctx, s.db, "variable_group", id, ""); err != nil {
		return nil, err
	}
	return s.created(ctx, id)
}

func (s *varGroupService) update(ctx context.Context, id string, in VarGroupInput) (*VarGroup, error) {
	if err := tenancy.Check(ctx, s.db, "variable_group.edit", "variable_group", strings.TrimSpace(id)); err != nil {
		return nil, err
	}
	id = strings.TrimSpace(id)
	// 先确认存在(给明确 404,而非静默 0 行)。
	if _, err := s.Get(ctx, id); err != nil {
		return nil, err
	}
	name, varsJSON, err := s.validate(ctx, in)
	if err != nil {
		return nil, err
	}

	if _, err = availableName(ctx, s.db, "variable_groups", id, name, ErrVarGroupNameTaken); err != nil {
		return nil, err
	}
	nowStr := time.Now().UTC().Format(time.RFC3339)
	_, err = configstate.Queryer(ctx, s.db).ExecContext(ctx,
		`UPDATE variable_groups SET name = ?, description = ?, vars_json = ?, updated_at = ?
		 WHERE id = ?`,
		name, strings.TrimSpace(in.Description), varsJSON, nowStr, id,
	)
	if err != nil {
		if isUniqueErr(err) {
			return nil, ErrVarGroupNameTaken
		}
		return nil, fmt.Errorf("library: update variable group: %w", err)
	}
	return s.Get(ctx, id)
}

func (s *varGroupService) delete(ctx context.Context, id string) error {
	if err := tenancy.Check(ctx, s.db, "variable_group.delete", "variable_group", strings.TrimSpace(id)); err != nil {
		return err
	}
	res, err := configstate.Queryer(ctx, s.db).ExecContext(ctx,
		`DELETE FROM variable_groups WHERE id = ?`, strings.TrimSpace(id))
	if err != nil {
		return fmt.Errorf("library: delete variable group: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("library: delete variable group rows: %w", err)
	}
	if n == 0 {
		return ErrVarGroupNotFound
	}
	return removeOwnership(ctx, s.db, "variable_group", strings.TrimSpace(id))
}

// validate 校验名称 + 经 pipeline.NormalizeVars 规范化变量(key 非空不重复;secret 引用存在性),
// 返回 trim 后名称 + 持久化形状 JSON(剥离掩码;明文 secret 从不入库)。
func (s *varGroupService) validate(ctx context.Context, in VarGroupInput) (string, string, error) {
	name := strings.TrimSpace(in.Name)
	if name == "" {
		return "", "", fmt.Errorf("%w: name must not be empty", ErrInvalidVarGroup)
	}
	if err := pipeline.AuthorizeReusableReferences(ctx, s.db, in.Vars); err != nil {
		return "", "", err
	}
	vars, err := pipeline.NormalizeVars(vault.WithContext(ctx, s.vault), in.Vars)
	if err != nil {
		return "", "", err
	}
	varsJSON, err := pipeline.ToStoredVarsJSON(vars)
	if err != nil {
		return "", "", fmt.Errorf("library: marshal variable group vars: %w", err)
	}
	return name, string(varsJSON), nil
}

// scanGroupRow 把一行扫成 VarGroup(纯扫描,不回算掩码——掩码须在 rows 关闭后做,
// 否则单连接 SQLite 会因「持有游标时再查库」自死锁)。
func scanGroupRow(row rowScanner) (*VarGroup, error) {
	var (
		id, name, desc, varsJSON string
		createdStr, updatedStr   string
	)
	if err := row.Scan(&id, &name, &desc, &varsJSON, &createdStr, &updatedStr); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, sql.ErrNoRows
		}
		return nil, fmt.Errorf("library: scan variable group: %w", err)
	}

	var vars []pipeline.BuildVar
	if strings.TrimSpace(varsJSON) != "" {
		if err := json.Unmarshal([]byte(varsJSON), &vars); err != nil {
			return nil, fmt.Errorf("library: parse variable group vars: %w", err)
		}
	}
	if vars == nil {
		vars = []pipeline.BuildVar{}
	}

	created, _ := time.Parse(time.RFC3339, createdStr)
	updated, _ := time.Parse(time.RFC3339, updatedStr)
	return &VarGroup{
		ID:          id,
		Name:        name,
		Description: desc,
		Vars:        vars,
		CreatedAt:   created,
		UpdatedAt:   updated,
	}, nil
}

func (s *varGroupService) Create(ctx context.Context, in VarGroupInput) (out *VarGroup, err error) {
	err = mutate(ctx, s.db, func(c context.Context) error { var e error; out, e = s.create(c, in); return e })
	return
}

func (s *varGroupService) Update(ctx context.Context, id string, in VarGroupInput) (out *VarGroup, err error) {
	err = mutate(ctx, s.db, func(c context.Context) error { var e error; out, e = s.update(c, id, in); return e })
	return
}

func (s *varGroupService) Delete(ctx context.Context, id string) error {
	return mutate(ctx, s.db, func(c context.Context) error { return s.delete(c, id) })
}

func (s *varGroupService) created(ctx context.Context, id string) (*VarGroup, error) {
	row := configstate.Queryer(ctx, s.db).QueryRowContext(ctx,
		`SELECT id, name, description, vars_json, created_at, updated_at
		 FROM variable_groups WHERE id = ?`, id)
	g, err := scanGroupRow(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrVarGroupNotFound
	}
	if err != nil {
		return nil, err
	}
	if err := pipeline.ApplyVarMasks(ctx, s.db, g.Vars); err != nil {
		return nil, err
	}
	return g, nil
}
