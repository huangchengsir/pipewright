package library

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

// customnode.go 是「自定义节点」复用基座的领域层(复用库 Tier 2 · 对标 Jenkins 自定义步骤 /
// 云效自建任务模板)。
//
// 自定义节点(CustomNode):一份命名、可复用的「单节点」定义 —— 底层 Job 类型 + config 快照。
// 用户在画布把某节点(常为 templated/script 自定义节点,也可任意内建类型)配好后「存为自定义节点」,
// 之后从选择器挑选即插入一个预填好 config 的 Job。与流水线模板(整段 stages)互补。
//
// 参数自由:Config 是 Job.Config 的原样快照(自由 KV),领域层不强加 schema(单管理员场景,
// 留足自定义自由度);只校验 name 非空 + nodeType 非空。绝不存任何明文 secret(config 与流水线
// spec 同形状,secret 走保险库引用)。领域包无 init() 副作用、无包级重对象,避免抬高空载内存。

// 自定义节点领域错误。错误体不含任何明文 secret。
var (
	// ErrCustomNodeNotFound 表示引用的自定义节点不存在。
	ErrCustomNodeNotFound = errors.New("library: custom node not found")
	// ErrInvalidCustomNode 表示自定义节点校验失败(名称空 / 类型空)。
	ErrInvalidCustomNode = errors.New("library: invalid custom node")
	// ErrCustomNodeNameTaken 表示自定义节点名已被占用(唯一约束)。
	ErrCustomNodeNameTaken = errors.New("library: custom node name already in use")
)

// CustomNode 是一份命名、可复用的单节点定义(NodeType + Config 快照)。
type CustomNode struct {
	ID          string
	Name        string
	Description string
	NodeType    string
	Summary     string
	Config      map[string]any
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// CustomNodeInput 是创建/更新自定义节点的入参。Config 为自由 KV(不含明文 secret)。
type CustomNodeInput struct {
	Name        string
	Description string
	NodeType    string
	Summary     string
	Config      map[string]any
}

// CustomNodeService 定义自定义节点领域对外接口。
type CustomNodeService interface {
	// List 返回所有自定义节点(按名称升序),供选择器/复用库画廊。
	List(ctx context.Context) ([]CustomNode, error)
	// Get 返回单个自定义节点;不存在 → ErrCustomNodeNotFound。
	Get(ctx context.Context, id string) (*CustomNode, error)
	// Create 校验(名称 + 类型非空)→ 持久化 config 快照 → 回读。名称重复 → ErrCustomNodeNameTaken。
	Create(ctx context.Context, in CustomNodeInput) (*CustomNode, error)
	// Update 同 Create 的校验/持久化,按 id 覆盖;不存在 → ErrCustomNodeNotFound。
	Update(ctx context.Context, id string, in CustomNodeInput) (*CustomNode, error)
	// Delete 删除自定义节点;不存在 → ErrCustomNodeNotFound。
	Delete(ctx context.Context, id string) error
}

type customNodeService struct {
	db *sql.DB
}

// NewCustomNodeService 构造 CustomNodeService(经参数化 SQL 触库)。
// 不在此做任何重活(无 init() 副作用,避免抬高空载内存)。
func NewCustomNodeService(db *sql.DB) CustomNodeService {
	return &customNodeService{db: db}
}

func (s *customNodeService) List(ctx context.Context) ([]CustomNode, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, name, description, node_type, summary, config_json, created_at, updated_at
		 FROM custom_nodes ORDER BY name ASC`)
	if err != nil {
		return nil, fmt.Errorf("library: list custom nodes: %w", err)
	}
	defer func() { _ = rows.Close() }()

	out := make([]CustomNode, 0)
	for rows.Next() {
		n, err := scanCustomNode(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *n)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("library: iterate custom nodes: %w", err)
	}
	return out, nil
}

func (s *customNodeService) Get(ctx context.Context, id string) (*CustomNode, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, name, description, node_type, summary, config_json, created_at, updated_at
		 FROM custom_nodes WHERE id = ?`, strings.TrimSpace(id))
	n, err := scanCustomNode(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrCustomNodeNotFound
	}
	return n, err
}

func (s *customNodeService) Create(ctx context.Context, in CustomNodeInput) (*CustomNode, error) {
	name, nodeType, configJSON, err := validateCustomNode(in)
	if err != nil {
		return nil, err
	}

	id := uuid.NewString()
	nowStr := time.Now().UTC().Format(time.RFC3339)
	_, err = s.db.ExecContext(ctx,
		`INSERT INTO custom_nodes (id, name, description, node_type, summary, config_json, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		id, name, strings.TrimSpace(in.Description), nodeType, strings.TrimSpace(in.Summary), configJSON, nowStr, nowStr,
	)
	if err != nil {
		if isUniqueErr(err) {
			return nil, ErrCustomNodeNameTaken
		}
		return nil, fmt.Errorf("library: insert custom node: %w", err)
	}
	return s.Get(ctx, id)
}

func (s *customNodeService) Update(ctx context.Context, id string, in CustomNodeInput) (*CustomNode, error) {
	id = strings.TrimSpace(id)
	// 先确认存在(给明确 404,而非静默 0 行)。
	if _, err := s.Get(ctx, id); err != nil {
		return nil, err
	}
	name, nodeType, configJSON, err := validateCustomNode(in)
	if err != nil {
		return nil, err
	}

	nowStr := time.Now().UTC().Format(time.RFC3339)
	_, err = s.db.ExecContext(ctx,
		`UPDATE custom_nodes SET name = ?, description = ?, node_type = ?, summary = ?, config_json = ?, updated_at = ?
		 WHERE id = ?`,
		name, strings.TrimSpace(in.Description), nodeType, strings.TrimSpace(in.Summary), configJSON, nowStr, id,
	)
	if err != nil {
		if isUniqueErr(err) {
			return nil, ErrCustomNodeNameTaken
		}
		return nil, fmt.Errorf("library: update custom node: %w", err)
	}
	return s.Get(ctx, id)
}

func (s *customNodeService) Delete(ctx context.Context, id string) error {
	res, err := s.db.ExecContext(ctx,
		`DELETE FROM custom_nodes WHERE id = ?`, strings.TrimSpace(id))
	if err != nil {
		return fmt.Errorf("library: delete custom node: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("library: delete custom node rows: %w", err)
	}
	if n == 0 {
		return ErrCustomNodeNotFound
	}
	return nil
}

// validateCustomNode 校验名称 + 类型非空,返回 trim 后名称/类型 + config 快照 JSON(nil 补 {})。
// 不对 config 强加 schema(留足自定义自由度);config 与流水线 spec 同形状,不含明文 secret。
func validateCustomNode(in CustomNodeInput) (name, nodeType, configJSON string, err error) {
	name = strings.TrimSpace(in.Name)
	if name == "" {
		return "", "", "", fmt.Errorf("%w: name must not be empty", ErrInvalidCustomNode)
	}
	nodeType = strings.TrimSpace(in.NodeType)
	if nodeType == "" {
		return "", "", "", fmt.Errorf("%w: node type must not be empty", ErrInvalidCustomNode)
	}
	cfg := in.Config
	if cfg == nil {
		cfg = map[string]any{}
	}
	raw, err := json.Marshal(cfg)
	if err != nil {
		return "", "", "", fmt.Errorf("library: marshal custom node config: %w", err)
	}
	return name, nodeType, string(raw), nil
}

func scanCustomNode(row rowScanner) (*CustomNode, error) {
	var (
		id, name, desc, nodeType, summary, configJSON string
		createdStr, updatedStr                        string
	)
	if err := row.Scan(&id, &name, &desc, &nodeType, &summary, &configJSON, &createdStr, &updatedStr); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, sql.ErrNoRows
		}
		return nil, fmt.Errorf("library: scan custom node: %w", err)
	}

	var config map[string]any
	if strings.TrimSpace(configJSON) != "" {
		if err := json.Unmarshal([]byte(configJSON), &config); err != nil {
			return nil, fmt.Errorf("library: parse custom node config: %w", err)
		}
	}
	if config == nil {
		config = map[string]any{}
	}

	created, _ := time.Parse(time.RFC3339, createdStr)
	updated, _ := time.Parse(time.RFC3339, updatedStr)
	return &CustomNode{
		ID:          id,
		Name:        name,
		Description: desc,
		NodeType:    nodeType,
		Summary:     summary,
		Config:      config,
		CreatedAt:   created,
		UpdatedAt:   updated,
	}, nil
}
