// Package audit 是审计地基(NFR-7 / AC-SEC-03)。
//
// 敏感操作(凭据增删改、trigger secret reset、项目增删改、手动触发运行)成功后,
// 同步写入本地 append-only 审计表(0009_audit.sql,SQLite trigger 硬拦 UPDATE/DELETE),
// 并可选地同步到远端 sink(env DEVOPSTOOL_AUDIT_SINK)。本地日志被删后,远端 sink
// 仍持完整记录(AC-SEC-03)。
//
// 脱敏铁律:Entry.Detail 写库前一律过 mask.Masker.ScrubMap,detail_json 绝不含
// 明文 secret/密文/master key。
//
// 降级铁律:远端 sink 不可达只降级记录(返回 nil,不阻断主操作),绝不让审计失败
// 回滚或阻断核心业务。本地写入失败同样不阻断业务(由调用方决定是否记日志)。
//
// 无 init 副作用、无包级重对象:Recorder 为轻量结构(避免抬高空载内存)。
package audit

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/huangjiawei/devopstool/internal/mask"
)

// Action 操作枚举(snake_case;DB 存字串)。只增不改语义。
const (
	ActionCredentialCreate   = "credential_create"
	ActionCredentialUpdate   = "credential_update"
	ActionCredentialDelete   = "credential_delete"
	ActionTriggerSecretReset = "trigger_secret_reset"
	ActionProjectCreate      = "project_create"
	ActionProjectUpdate      = "project_update"
	ActionProjectDelete      = "project_delete"
	ActionRunTriggerManual   = "run_trigger_manual"
	ActionPasswordChange     = "password_change"
	ActionSessionRevoke      = "session_revoke"
)

// 目标类型枚举(供 TargetType 填值;非强制白名单,便于后续 story 扩展)。
const (
	TargetCredential = "credential"
	TargetProject    = "project"
	TargetTrigger    = "trigger"
	TargetRun        = "run"
	TargetAccount    = "account"
	TargetSession    = "session"
)

// Entry 是一条审计写入入参(冻结契约)。Detail 写库前过 Masker,绝不含明文 secret。
type Entry struct {
	Actor      string
	Action     string
	TargetType string
	TargetID   string
	Detail     map[string]any
	IP         string
}

// Record 是审计记录的对外只读视图(供查询端点返回)。
type Record struct {
	ID         string
	Timestamp  time.Time
	Actor      string
	Action     string
	TargetType string
	TargetID   string
	Detail     map[string]any
	IP         string
}

// ListFilter 是审计列表查询的过滤/分页参数。
type ListFilter struct {
	Limit      int    // 单页条数;<=0 或过大时归一到默认/上限
	Before     string // 游标:上一页最后一条的 id;空表示从头
	Action     string // 按 action 过滤;空表示不过滤
	TargetType string // 按 target_type 过滤;空表示不过滤
}

// ListResult 是审计列表查询结果。
type ListResult struct {
	Entries    []Record
	NextBefore string // 下一页游标(当前页最后一条 id);到底时为空
}

const (
	defaultLimit = 50
	maxLimit     = 200
)

// Recorder 定义审计写入与查询对外接口。
type Recorder interface {
	// Record 把 Entry 脱敏后追加到本地 append-only 审计表,并(若配置)同步到远端 sink。
	// 业务调用方应仅在业务成功后调用;返回 error 仅表示本地写入失败(供调用方记日志,
	// 不应据此回滚业务)。远端 sink 失败不影响返回值(降级记录)。
	Record(ctx context.Context, e Entry) error
	// List 按过滤 + 游标分页返回审计记录(timestamp DESC, id DESC)。只读。
	List(ctx context.Context, f ListFilter) (*ListResult, error)
}

// Sink 是可选远端审计 sink:本地写入成功后,审计条目同步推送一份到远端,
// 使「本地被删后远端仍完整」(AC-SEC-03)。实现须自行容错;Send 失败由 Recorder
// 降级吞掉(不阻断主操作)。
type Sink interface {
	// Send 推送一条已脱敏的审计记录到远端。
	Send(ctx context.Context, r Record) error
}

// recorder 是 store 支撑的 Recorder 实现。
type recorder struct {
	db     *sql.DB
	masker *mask.Masker
	sink   Sink // 可为 nil(无远端 sink)
	// onSinkError 仅供观测远端 sink 降级(默认 nil = 静默);测试可注入。
	onSinkError func(error)
}

// New 构造 Recorder。
//   - db:经参数化 SQL 触 audit_log。
//   - masker:Detail 落库前脱敏;为 nil 时退化为不脱敏(调用方应始终传入)。
//   - sink:可选远端 sink;为 nil 表示仅本地。
//
// 不做任何重活(无 init 副作用,避免抬高空载内存)。
func New(db *sql.DB, masker *mask.Masker, sink Sink) Recorder {
	return &recorder{db: db, masker: masker, sink: sink}
}

func (r *recorder) Record(ctx context.Context, e Entry) error {
	detail := e.Detail
	if r.masker != nil {
		detail = r.masker.ScrubMap(detail)
	}
	detailJSON := "{}"
	if detail != nil {
		b, err := json.Marshal(detail)
		if err != nil {
			return fmt.Errorf("audit: marshal detail: %w", err)
		}
		detailJSON = string(b)
	}

	id := uuid.NewString()
	now := time.Now().UTC()
	nowStr := now.Format(time.RFC3339Nano)

	var targetID, ip any
	if e.TargetID != "" {
		targetID = e.TargetID
	}
	if e.IP != "" {
		ip = e.IP
	}

	_, err := r.db.ExecContext(ctx,
		`INSERT INTO audit_log (id, timestamp, actor, action, target_type, target_id, detail_json, ip, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		id, nowStr, e.Actor, e.Action, e.TargetType, targetID, detailJSON, ip, nowStr,
	)
	if err != nil {
		return fmt.Errorf("audit: insert: %w", err)
	}

	// 远端 sink:本地成功后同步一份;失败仅降级记录,不阻断主操作。
	if r.sink != nil {
		rec := Record{
			ID:         id,
			Timestamp:  now,
			Actor:      e.Actor,
			Action:     e.Action,
			TargetType: e.TargetType,
			TargetID:   e.TargetID,
			Detail:     detail,
			IP:         e.IP,
		}
		if serr := r.sink.Send(ctx, rec); serr != nil && r.onSinkError != nil {
			r.onSinkError(serr)
		}
	}
	return nil
}

func (r *recorder) List(ctx context.Context, f ListFilter) (*ListResult, error) {
	limit := f.Limit
	if limit <= 0 {
		limit = defaultLimit
	}
	if limit > maxLimit {
		limit = maxLimit
	}

	var (
		conds []string
		args  []any
	)
	if f.Action != "" {
		conds = append(conds, "action = ?")
		args = append(args, f.Action)
	}
	if f.TargetType != "" {
		conds = append(conds, "target_type = ?")
		args = append(args, f.TargetType)
	}
	// 游标:before=上一页最后一条 id。以 (timestamp,id) 复合次序定位,稳定分页
	// (同毫秒多条时仍按 id 二级排序确定边界)。
	if f.Before != "" {
		var ts string
		err := r.db.QueryRowContext(ctx,
			`SELECT timestamp FROM audit_log WHERE id = ?`, f.Before,
		).Scan(&ts)
		switch {
		case errors.Is(err, sql.ErrNoRows):
			// 游标失效:按从头处理(不报错)。
		case err != nil:
			return nil, fmt.Errorf("audit: resolve cursor: %w", err)
		default:
			conds = append(conds, "(timestamp < ? OR (timestamp = ? AND id < ?))")
			args = append(args, ts, ts, f.Before)
		}
	}

	where := ""
	if len(conds) > 0 {
		where = "WHERE " + strings.Join(conds, " AND ")
	}
	// 多取一条以判断是否还有下一页。
	args = append(args, limit+1)
	query := `SELECT id, timestamp, actor, action, target_type, target_id, detail_json, ip
	          FROM audit_log ` + where + `
	          ORDER BY timestamp DESC, id DESC
	          LIMIT ?`

	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("audit: list: %w", err)
	}
	defer func() { _ = rows.Close() }()

	entries := make([]Record, 0, limit)
	for rows.Next() {
		rec, err := scanRecord(rows)
		if err != nil {
			return nil, err
		}
		entries = append(entries, *rec)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("audit: iterate: %w", err)
	}

	res := &ListResult{Entries: entries}
	if len(entries) > limit {
		// 截断到 limit,游标取本页最后一条 id。
		res.Entries = entries[:limit]
		res.NextBefore = res.Entries[len(res.Entries)-1].ID
	}
	return res, nil
}

// scanRecord 把一行扫描为 Record;detail_json 反序列化为 map(空/坏数据 → 空 map)。
func scanRecord(rows *sql.Rows) (*Record, error) {
	var (
		rec        Record
		tsStr      string
		targetID   sql.NullString
		detailJSON string
		ip         sql.NullString
	)
	if err := rows.Scan(&rec.ID, &tsStr, &rec.Actor, &rec.Action, &rec.TargetType, &targetID, &detailJSON, &ip); err != nil {
		return nil, fmt.Errorf("audit: scan: %w", err)
	}
	ts, err := time.Parse(time.RFC3339Nano, tsStr)
	if err != nil {
		// 兼容历史无纳秒格式。
		ts, err = time.Parse(time.RFC3339, tsStr)
		if err != nil {
			return nil, fmt.Errorf("audit: parse timestamp: %w", err)
		}
	}
	rec.Timestamp = ts
	if targetID.Valid {
		rec.TargetID = targetID.String
	}
	if ip.Valid {
		rec.IP = ip.String
	}
	rec.Detail = map[string]any{}
	if strings.TrimSpace(detailJSON) != "" {
		_ = json.Unmarshal([]byte(detailJSON), &rec.Detail)
		if rec.Detail == nil {
			rec.Detail = map[string]any{}
		}
	}
	return &rec, nil
}
