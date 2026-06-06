package run

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/huangchengsir/pipewright/internal/store"
)

// parameters.go 实现项目级「类型化运行参数」定义(P0 · 对标 Jenkins parameters / 云效 variables type)。
//
// 每项目一份参数定义数组(key/label/type/default/options/required)。手动触发弹窗据此渲染
// 类型化控件(枚举→下拉、布尔→开关、数字→数字框)并在提交时校验;执行期仍以 key=value
// (map[string]string)注入容器,故这仅是「触发期定义 + 校验层」,不改执行语义(Story 8-11)。
//
// 无定义(空数组)→ Resolve 透传调用方所给参数,与现有自由 KV 行为字节级一致(向后兼容)。

// 参数类型枚举(JSON/DB 存小写串)。
const (
	ParamTypeString  = "string"
	ParamTypeChoice  = "choice"
	ParamTypeBoolean = "boolean"
	ParamTypeNumber  = "number"
)

// maxParamDefs 是单项目参数定义条数上界(防误填巨量)。
const maxParamDefs = 64

// 参数键命名:env-var 安全(参数以 PW_<KEY> 注入容器环境),字母/下划线开头。
var paramKeyRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// 领域错误(错误体不含敏感数据)。
var (
	// ErrParamProjectNotFound 表示引用的项目不存在。
	ErrParamProjectNotFound = errors.New("run: parameter project not found")
	// ErrInvalidParamDef 表示参数定义非法(键/类型/枚举/默认值校验失败)。
	ErrInvalidParamDef = errors.New("run: invalid parameter definition")
	// ErrInvalidParamValue 表示触发时提供的参数值不满足定义(必填缺失 / 枚举越界 / 类型不符)。
	ErrInvalidParamValue = errors.New("run: invalid parameter value")
)

// ParamDef 是一条参数定义。Default/Options 均为字符串(执行期注入字符串环境变量);
// boolean 的合法值为 "true"/"false",number 为可解析数值。
type ParamDef struct {
	Key      string   `json:"key"`
	Label    string   `json:"label"`
	Type     string   `json:"type"`
	Default  string   `json:"default"`
	Options  []string `json:"options,omitempty"`
	Required bool     `json:"required"`
}

// ParametersConfig 是项目参数定义领域模型。
type ParametersConfig struct {
	Defs      []ParamDef
	UpdatedAt time.Time
}

// ParameterService 定义项目参数定义领域接口(HTTP 读写 + 触发期解析校验)。
type ParameterService interface {
	// Get 返回项目参数定义;无配置 → 空 Defs(非错误)。
	Get(ctx context.Context, projectID string) (*ParametersConfig, error)
	// Save 校验参数定义(键合法/唯一、类型枚举、choice 有 options、默认值符合类型)→ upsert → 回读。
	// 项目不存在 → ErrParamProjectNotFound;定义非法 → ErrInvalidParamDef(附人读原因)。
	Save(ctx context.Context, projectID string, defs []ParamDef) (*ParametersConfig, error)
	// Resolve 据项目定义把「触发时提供的参数」校验 + 强转 + 填默认,返回执行期注入用的 key=value。
	// 无定义 → 原样透传 provided(向后兼容)。非法值 → ErrInvalidParamValue(附人读原因)。
	// 定义非空时:仅保留已定义的键(丢弃未定义键),缺省值由 default 填充。
	Resolve(ctx context.Context, projectID string, provided map[string]string) (map[string]string, error)
}

type parameterService struct {
	db *sql.DB
}

// NewParameterService 构造项目参数定义 Service(参数化 SQL 触库;无 init 副作用、不驻留)。
func NewParameterService(db *sql.DB) ParameterService { return &parameterService{db: db} }

func (s *parameterService) Get(ctx context.Context, projectID string) (*ParametersConfig, error) {
	var (
		defsJSON string
		updated  string
	)
	err := s.db.QueryRowContext(ctx,
		`SELECT defs_json, updated_at FROM project_parameters WHERE project_id = ?`,
		strings.TrimSpace(projectID),
	).Scan(&defsJSON, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return &ParametersConfig{Defs: []ParamDef{}}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("run: load parameters: %w", err)
	}
	defs := []ParamDef{}
	if strings.TrimSpace(defsJSON) != "" {
		if uerr := json.Unmarshal([]byte(defsJSON), &defs); uerr != nil {
			return nil, fmt.Errorf("run: parse parameter defs: %w", uerr)
		}
	}
	if defs == nil {
		defs = []ParamDef{}
	}
	t, _ := time.Parse(time.RFC3339, updated)
	return &ParametersConfig{Defs: defs, UpdatedAt: t}, nil
}

func (s *parameterService) Save(ctx context.Context, projectID string, defs []ParamDef) (*ParametersConfig, error) {
	projectID = strings.TrimSpace(projectID)
	normalized, err := normalizeParamDefs(defs)
	if err != nil {
		return nil, err
	}
	blob, err := json.Marshal(normalized)
	if err != nil {
		return nil, fmt.Errorf("run: marshal parameter defs: %w", err)
	}
	now := time.Now().UTC().Format(time.RFC3339)
	_, err = s.db.ExecContext(ctx,
		`INSERT INTO project_parameters (project_id, defs_json, created_at, updated_at)
		 VALUES (?, ?, ?, ?) `+
			store.UpsertSuffix(store.DialectOf(s.db), []string{"project_id"}, []string{"defs_json", "updated_at"}),
		projectID, string(blob), now, now,
	)
	if err != nil {
		if isForeignKeyErr(err) {
			return nil, ErrParamProjectNotFound
		}
		return nil, fmt.Errorf("run: upsert parameters: %w", err)
	}
	return s.Get(ctx, projectID)
}

func (s *parameterService) Resolve(ctx context.Context, projectID string, provided map[string]string) (map[string]string, error) {
	cfg, err := s.Get(ctx, projectID)
	if err != nil {
		return nil, err
	}
	return ResolveParams(cfg.Defs, provided)
}

// normalizeParamDefs 校验 + 规范化定义列表:键合法/唯一、类型枚举、choice 有 options、默认值符合类型。
// label 留空 → 回退 key。
func normalizeParamDefs(defs []ParamDef) ([]ParamDef, error) {
	if len(defs) > maxParamDefs {
		return nil, fmt.Errorf("%w: 参数定义不能超过 %d 条", ErrInvalidParamDef, maxParamDefs)
	}
	out := make([]ParamDef, 0, len(defs))
	seen := make(map[string]struct{}, len(defs))
	for _, d := range defs {
		key := strings.TrimSpace(d.Key)
		if !paramKeyRe.MatchString(key) {
			return nil, fmt.Errorf("%w: 参数键「%s」非法(须字母/下划线开头,仅含字母数字下划线)", ErrInvalidParamDef, d.Key)
		}
		if _, dup := seen[key]; dup {
			return nil, fmt.Errorf("%w: 参数键「%s」重复", ErrInvalidParamDef, key)
		}
		seen[key] = struct{}{}

		typ := strings.TrimSpace(d.Type)
		if typ == "" {
			typ = ParamTypeString
		}
		opts := trimOptions(d.Options)
		switch typ {
		case ParamTypeString, ParamTypeNumber, ParamTypeBoolean:
			opts = nil // 非枚举类型不带 options
		case ParamTypeChoice:
			if len(opts) == 0 {
				return nil, fmt.Errorf("%w: 枚举参数「%s」必须至少有一个选项", ErrInvalidParamDef, key)
			}
		default:
			return nil, fmt.Errorf("%w: 参数「%s」类型「%s」未知", ErrInvalidParamDef, key, typ)
		}

		def := strings.TrimSpace(d.Default)
		if err := validateParamValue(key, typ, opts, def); err != nil {
			return nil, fmt.Errorf("%w: 参数「%s」默认值不合法:%v", ErrInvalidParamDef, key, err)
		}

		label := strings.TrimSpace(d.Label)
		if label == "" {
			label = key
		}
		out = append(out, ParamDef{Key: key, Label: label, Type: typ, Default: d.Default, Options: opts, Required: d.Required})
	}
	return out, nil
}

func trimOptions(in []string) []string {
	out := make([]string, 0, len(in))
	for _, o := range in {
		if t := strings.TrimSpace(o); t != "" {
			out = append(out, t)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// validateParamValue 校验单个值是否满足类型/枚举(空值由调用方据 required 处理,这里只查非空值的合法性)。
func validateParamValue(key, typ string, options []string, value string) error {
	v := strings.TrimSpace(value)
	if v == "" {
		return nil // 空值合法性(必填)由 ResolveParams 单独判
	}
	switch typ {
	case ParamTypeNumber:
		if _, err := strconv.ParseFloat(v, 64); err != nil {
			return fmt.Errorf("「%s」不是数字", value)
		}
	case ParamTypeBoolean:
		if v != "true" && v != "false" {
			return fmt.Errorf("布尔值须为 true/false,得「%s」", value)
		}
	case ParamTypeChoice:
		for _, o := range options {
			if o == v {
				return nil
			}
		}
		return fmt.Errorf("「%s」不在可选项 %v 中", value, options)
	}
	return nil
}

// ResolveParams 是 Resolve 的纯函数核心(供 Service 与测试复用)。
// 无定义 → 透传 provided(向后兼容,nil 安全)。有定义:逐项取「提供值(非空)否则默认值」,
// 校验必填/枚举/类型,仅保留已定义键。
func ResolveParams(defs []ParamDef, provided map[string]string) (map[string]string, error) {
	if len(defs) == 0 {
		if provided == nil {
			return nil, nil
		}
		out := make(map[string]string, len(provided))
		for k, v := range provided {
			out[k] = v
		}
		return out, nil
	}
	out := make(map[string]string, len(defs))
	for _, d := range defs {
		v, ok := provided[d.Key]
		if !ok || strings.TrimSpace(v) == "" {
			v = d.Default
		}
		if strings.TrimSpace(v) == "" {
			if d.Required {
				return nil, fmt.Errorf("%w: 参数「%s」为必填", ErrInvalidParamValue, d.Label)
			}
			out[d.Key] = ""
			continue
		}
		if err := validateParamValue(d.Key, d.Type, d.Options, v); err != nil {
			return nil, fmt.Errorf("%w: 参数「%s」%v", ErrInvalidParamValue, d.Label, err)
		}
		out[d.Key] = v
	}
	return out, nil
}
