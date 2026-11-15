// Package library 是「流水线模板 + 变量组」复用基座的领域层(FR-8-13 · 对标
// Jenkins Shared Library / 云效模板与变量组)。
//
// 两类可复用资产,跨项目共享、与具体项目解耦:
//   - 流水线模板(Template):命名、可复用的流水线定义(与项目流水线同一套 stages 模型)。
//     创建/列出/获取/删除模板 + 「应用模板到项目」(把模板 stages 拷进项目流水线,
//     经 pipeline.Service.Save 同一套规范化/校验/渲染落库)。校验复用 pipeline.NormalizeSpec。
//   - 变量组(VarGroup,见 vargroup.go):命名、可复用的变量集合,镜像每流水线变量模型;
//     secret 只存 vault 凭据引用,明文绝不入库。
//
// AC-SEC:模板 spec 不含任何明文 secret(凭据走保险库引用,2-4 settings 才落引用字段);
// 变量组的 secret 同 settings.normalizeVars 语义——只存 credentialId 引用、经保险库校验存在性。
// 领域包无 init() 副作用、无包级重对象,避免抬高空载内存。
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
	"github.com/huangchengsir/pipewright/internal/store"
)

// 领域错误。错误体不含任何明文 secret。
var (
	// ErrTemplateNotFound 表示引用的模板不存在。
	ErrTemplateNotFound = errors.New("library: template not found")
	// ErrInvalidTemplate 表示模板校验失败(名称空 / spec 非法)。
	ErrInvalidTemplate = errors.New("library: invalid template")
	// ErrTemplateNameTaken 表示模板名已被占用(唯一约束)。
	ErrTemplateNameTaken = errors.New("library: template name already in use")
	// ErrProjectNotFound 表示应用模板的目标项目不存在。
	ErrProjectNotFound = errors.New("library: project not found")
)

// Template 是一份命名、可复用的流水线定义(spec = 阶段集合,与项目流水线同形状)。
type Template struct {
	ID          string
	Name        string
	Description string
	Spec        pipeline.Spec
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// TemplateInput 是创建模板的入参。
type TemplateInput struct {
	Name        string
	Description string
	Spec        pipeline.Spec
}

// TemplateService 定义流水线模板领域对外接口。
type TemplateService interface {
	// List 返回所有模板(按名称升序),供挑选画廊。
	List(ctx context.Context) ([]Template, error)
	// Get 返回单个模板;不存在 → ErrTemplateNotFound。
	Get(ctx context.Context, id string) (*Template, error)
	// Create 校验(名称非空 + spec 经 pipeline.NormalizeSpec 规范化/校验)→ 持久化 → 回读。
	// 名称重复 → ErrTemplateNameTaken;spec 非法 → ErrInvalidTemplate(包裹底层 pipeline 错误)。
	Create(ctx context.Context, in TemplateInput) (*Template, error)
	// Delete 删除模板;不存在 → ErrTemplateNotFound。
	Delete(ctx context.Context, id string) error
	// Apply 把模板的 stages 应用到目标项目流水线(经注入的 pipeline.Service.Save 同一套规范化/
	// 校验/渲染落库,产出合法流水线)。模板不存在 → ErrTemplateNotFound;项目不存在 → ErrProjectNotFound;
	// 模板 spec 经再校验仍非法 → 透传 pipeline 校验错误。返回应用后的项目流水线配置。
	Apply(ctx context.Context, templateID, projectID string) (*pipeline.Config, error)
}

type templateService struct {
	db    *sql.DB
	pipes pipeline.Service
}

// NewTemplateService 构造 TemplateService。
//   - db:经参数化 SQL 触库。
//   - pipes:应用模板时经其 Save 把 stages 落到项目流水线(复用同一套校验/渲染)。
//
// 不在此做任何重活(无 init() 副作用,避免抬高空载内存)。
func NewTemplateService(db *sql.DB, pipes pipeline.Service) TemplateService {
	return &templateService{db: db, pipes: pipes}
}

func (s *templateService) List(ctx context.Context) ([]Template, error) {
	predicate, err := tenancy.QueryPredicate(ctx, s.db, "template.read", "pipeline_templates.id")
	if err != nil {
		return nil, err
	}
	rows, err := configstate.Queryer(ctx, s.db).QueryContext(ctx,
		`SELECT id, name, description, stages_json, created_at, updated_at
		 FROM pipeline_templates WHERE `+predicate.SQL+` ORDER BY name ASC`, predicate.Args...)
	if err != nil {
		return nil, fmt.Errorf("library: list templates: %w", err)
	}
	defer func() { _ = rows.Close() }()

	out := make([]Template, 0)
	for rows.Next() {
		t, err := scanTemplate(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *t)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("library: iterate templates: %w", err)
	}
	return out, nil
}

func (s *templateService) Get(ctx context.Context, id string) (*Template, error) {
	predicate, err := tenancy.QueryPredicate(ctx, s.db, "template.read", "pipeline_templates.id")
	if err != nil {
		return nil, err
	}
	args := append([]any{strings.TrimSpace(id)}, predicate.Args...)
	row := configstate.Queryer(ctx, s.db).QueryRowContext(ctx,
		`SELECT id, name, description, stages_json, created_at, updated_at
		 FROM pipeline_templates WHERE id = ? AND `+predicate.SQL, args...)
	t, err := scanTemplate(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrTemplateNotFound
	}
	return t, err
}

func (s *templateService) create(ctx context.Context, in TemplateInput) (*Template, error) {
	id := uuid.NewString()
	if err := tenancy.CheckNew(ctx, s.db, "template.create", "template", id); err != nil {
		return nil, err
	}
	if err := pipeline.AuthorizeReusableReferences(ctx, s.db, in.Spec); err != nil {
		return nil, err
	}
	name := strings.TrimSpace(in.Name)
	if name == "" {
		return nil, fmt.Errorf("%w: name must not be empty", ErrInvalidTemplate)
	}

	// spec 经 pipeline 同一套规范化/校验(补 id、trim、kind 枚举、恰一个 source、needs DAG)。
	normalized, err := pipeline.NormalizeSpec(in.Spec)
	if err != nil {
		return nil, fmt.Errorf("%w: %s", ErrInvalidTemplate, err.Error())
	}
	stagesJSON, err := json.Marshal(normalized.Stages)
	if err != nil {
		return nil, fmt.Errorf("library: marshal template stages: %w", err)
	}

	namespace, err := availableName(ctx, s.db, "pipeline_templates", id, name, ErrTemplateNameTaken)
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	nowStr := now.Format(time.RFC3339)
	_, err = configstate.Queryer(ctx, s.db).ExecContext(ctx,
		`INSERT INTO pipeline_templates (id, name_scope, name, description, stages_json, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		id, namespace, name, strings.TrimSpace(in.Description), string(stagesJSON), nowStr, nowStr,
	)
	if err != nil {
		if isUniqueErr(err) {
			return nil, ErrTemplateNameTaken
		}
		return nil, fmt.Errorf("library: insert template: %w", err)
	}
	if err = tenancy.RecordOwnership(ctx, s.db, "template", id, ""); err != nil {
		return nil, err
	}
	return s.created(ctx, id)
}

func (s *templateService) delete(ctx context.Context, id string) error {
	if err := tenancy.Check(ctx, s.db, "template.delete", "template", strings.TrimSpace(id)); err != nil {
		return err
	}
	res, err := configstate.Queryer(ctx, s.db).ExecContext(ctx,
		`DELETE FROM pipeline_templates WHERE id = ?`, strings.TrimSpace(id))
	if err != nil {
		return fmt.Errorf("library: delete template: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("library: delete template rows: %w", err)
	}
	if n == 0 {
		return ErrTemplateNotFound
	}
	return removeOwnership(ctx, s.db, "template", strings.TrimSpace(id))
}

func (s *templateService) apply(ctx context.Context, templateID, projectID string) (*pipeline.Config, error) {
	t, err := s.Get(ctx, templateID)
	if err != nil {
		return nil, err
	}
	if s.pipes == nil {
		return nil, fmt.Errorf("library: pipeline service unavailable")
	}
	// 把模板 stages 经项目流水线 Save 落库(再走一遍规范化/校验/渲染;项目存在性由 Save 校验)。
	cfg, err := s.pipes.Save(ctx, strings.TrimSpace(projectID), t.Spec)
	if err != nil {
		if errors.Is(err, pipeline.ErrProjectNotFound) {
			return nil, ErrProjectNotFound
		}
		return nil, err
	}
	return cfg, nil
}

// rowScanner 抽象 *sql.Row / *sql.Rows 的 Scan,供 Get/List 共用扫描逻辑。
type rowScanner interface {
	Scan(dest ...any) error
}

func scanTemplate(row rowScanner) (*Template, error) {
	var (
		id, name, desc, stagesJSON string
		createdStr, updatedStr     string
	)
	if err := row.Scan(&id, &name, &desc, &stagesJSON, &createdStr, &updatedStr); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, sql.ErrNoRows
		}
		return nil, fmt.Errorf("library: scan template: %w", err)
	}

	var stages []pipeline.Stage
	if strings.TrimSpace(stagesJSON) != "" {
		if err := json.Unmarshal([]byte(stagesJSON), &stages); err != nil {
			return nil, fmt.Errorf("library: parse template stages: %w", err)
		}
	}
	if stages == nil {
		stages = []pipeline.Stage{}
	}

	created, _ := time.Parse(time.RFC3339, createdStr)
	updated, _ := time.Parse(time.RFC3339, updatedStr)
	return &Template{
		ID:          id,
		Name:        name,
		Description: desc,
		Spec:        pipeline.Spec{Stages: stages},
		CreatedAt:   created,
		UpdatedAt:   updated,
	}, nil
}

// isUniqueErr 判定是否为唯一约束冲突(modernc sqlite 文本含 UNIQUE)。
func isUniqueErr(err error) bool { return store.IsUniqueErr(err) }

func (s *templateService) Create(ctx context.Context, in TemplateInput) (out *Template, err error) {
	err = mutate(ctx, s.db, func(c context.Context) error { var e error; out, e = s.create(c, in); return e })
	return
}

func (s *templateService) Delete(ctx context.Context, id string) error {
	return mutate(ctx, s.db, func(c context.Context) error { return s.delete(c, id) })
}

func (s *templateService) created(ctx context.Context, id string) (*Template, error) {
	row := configstate.Queryer(ctx, s.db).QueryRowContext(ctx,
		`SELECT id, name, description, stages_json, created_at, updated_at
		 FROM pipeline_templates WHERE id = ?`, id)
	t, err := scanTemplate(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrTemplateNotFound
	}
	return t, err
}

func (s *templateService) Apply(ctx context.Context, templateID, projectID string) (out *pipeline.Config, err error) {
	err = mutate(ctx, s.db, func(c context.Context) error {
		if e := tenancy.Check(c, s.db, "pipeline.edit", "project", strings.TrimSpace(projectID)); e != nil {
			return e
		}
		var e error
		out, e = s.apply(c, templateID, projectID)
		return e
	})
	return
}
