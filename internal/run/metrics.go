package run

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"
)

// metrics.go 暴露 DORA 四指标(FR-8-15)所需的**只读**运行投影查询。
//
// 不新增表、不改既有 service.go:只新增一个轻量查询服务,从 pipeline_runs 拉取窗口内
// 的运行(精简列:status / created_at / finished_at),供 internal/dora 做纯聚合。
// commit 时间本平台运行模型当前不持久化,故投影只回 created_at(dora 层据此回退作提交代理)。

// MetricsRecord 是计算 DORA 所需的单条运行投影(精简,只取聚合必需列)。
// 与 dora.Record 字段对齐但解耦(run 包不 import dora;httpapi 层做映射)。
type MetricsRecord struct {
	// Status 是运行状态(success|failed|partial_failed|rolled_back|其它非终态)。
	Status string
	// CreatedAt 是入队时刻(必有;dora 层在无 commit 时间时作前置时长的提交代理)。
	CreatedAt time.Time
	// FinishedAt 是终态时刻(非终态为 nil;dora 层据此判定「是否一次部署」)。
	FinishedAt *time.Time
}

// MetricsService 暴露 DORA 聚合所需的只读查询。
type MetricsService interface {
	// MetricsRecords 返回窗口 [since, now] 内、按指定项目(projectID 为空 = 全部项目)的运行投影,
	// 按 created_at 升序。只取计算必需列;非终态运行 finished_at 为 nil(dora 层自然忽略)。
	// since 为零值时不设下界(取全部历史)。
	MetricsRecords(ctx context.Context, projectID string, since time.Time) ([]MetricsRecord, error)
}

// MetricsRecords 实现 MetricsService:参数化 SQL 拉窗口内运行投影。
//
// created_at 以 RFC3339 文本存储,字典序与时间序一致(同一 UTC 格式),故 since 下界可直接文本比较。
func (s *service) MetricsRecords(ctx context.Context, projectID string, since time.Time) ([]MetricsRecord, error) {
	where := []string{}
	args := []any{}
	if pid := strings.TrimSpace(projectID); pid != "" {
		where = append(where, "project_id = ?")
		args = append(args, pid)
	}
	if !since.IsZero() {
		where = append(where, "created_at >= ?")
		args = append(args, since.UTC().Format(time.RFC3339))
	}
	clause := ""
	if len(where) > 0 {
		clause = " WHERE " + strings.Join(where, " AND ")
	}

	rows, err := s.db.QueryContext(ctx,
		`SELECT status, created_at, finished_at
		 FROM pipeline_runs`+clause+`
		 ORDER BY created_at ASC, id ASC`, args...)
	if err != nil {
		return nil, fmt.Errorf("run: query metrics records: %w", err)
	}
	defer func() { _ = rows.Close() }()

	out := []MetricsRecord{}
	for rows.Next() {
		var (
			rec        MetricsRecord
			createdStr string
			finishStr  sql.NullString
		)
		if err := rows.Scan(&rec.Status, &createdStr, &finishStr); err != nil {
			return nil, fmt.Errorf("run: scan metrics record: %w", err)
		}
		t, perr := time.Parse(time.RFC3339, createdStr)
		if perr != nil {
			return nil, fmt.Errorf("run: parse metrics created_at: %w", perr)
		}
		rec.CreatedAt = t.UTC()
		if rec.FinishedAt, err = parseNullTime(finishStr); err != nil {
			return nil, err
		}
		out = append(out, rec)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("run: iterate metrics records: %w", err)
	}
	return out, nil
}

// NewMetricsService 把既有运行 Service 暴露为 MetricsService(同一 *service 实现)。
// 供 httpapi 层装配 DORA 端点用;不引入新依赖、不开新连接。
func NewMetricsService(s Service) MetricsService {
	if impl, ok := s.(*service); ok {
		return impl
	}
	return nil
}
