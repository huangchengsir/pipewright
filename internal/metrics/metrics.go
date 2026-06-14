// Package metrics 是服务器指标时序历史的领域层(异常检测「看趋势」折线图的数据源)。
//
// 后台采样器(main 定时,复用 6-1 SSH 采集的同一 collector)周期性调用 Record 把每台可达
// 服务器的 CPU / 内存 / 磁盘使用率(百分比)写一行;前端经 QueryRange 取某台某时间窗的序列画
// 折线。Sweep 按保留窗口删旧样本,避免无限增长。
//
// 设计纪律:
//   - 不含任何 SSH/采集逻辑(采集复用 6-1,样本由调用方喂入);本包只管「存 + 查 + 清」。
//   - 各指标可空(*float64 nil = 该时刻不可得);查询原样回传 nil,由前端断线处理。
//   - 参数化 SQL;指标无敏感信息。
package metrics

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// Sample 是一台服务器某时刻的指标采样(百分比;nil = 该指标不可得)。
type Sample struct {
	ServerID string
	CPU      *float64
	Memory   *float64
	Disk     *float64
	At       time.Time
}

// Point 是趋势序列上的一个时间点(同 Sample 但不含 ServerID;按时间升序返回)。
type Point struct {
	At     time.Time
	CPU    *float64
	Memory *float64
	Disk   *float64
}

// Service 定义指标时序历史对外接口(httpapi / main 采样器消费)。
type Service interface {
	// Record 批量写入一次采样(每台一行);samples 为空 → no-op 不报错。
	Record(ctx context.Context, samples []Sample) error
	// QueryRange 返回某服务器自 since 起的采样点(按时间升序;上限 maxPoints 防爆)。
	QueryRange(ctx context.Context, serverID string, since time.Time) ([]Point, error)
	// Sweep 删除 sampled_at 早于 before 的样本,返回删除行数(保留清理)。
	Sweep(ctx context.Context, before time.Time) (int64, error)
}

// maxRangePoints 单次趋势查询返回的最大点数(防止超长窗口拖垮前端;约 7 天 @ 1min)。
const maxRangePoints = 11000

type service struct{ db *sql.DB }

// New 构造 Service。无 init 副作用。
func New(db *sql.DB) Service { return &service{db: db} }

func (s *service) Record(ctx context.Context, samples []Sample) error {
	if len(samples) == 0 {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("metrics: begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	stmt, err := tx.PrepareContext(ctx,
		`INSERT INTO metric_samples (id, server_id, cpu_percent, memory_percent, disk_percent, sampled_at)
		 VALUES (?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return fmt.Errorf("metrics: prepare: %w", err)
	}
	defer func() { _ = stmt.Close() }()

	for _, sm := range samples {
		at := sm.At.UTC().Format(time.RFC3339)
		if _, err := stmt.ExecContext(ctx, uuid.NewString(), sm.ServerID,
			nullFloat(sm.CPU), nullFloat(sm.Memory), nullFloat(sm.Disk), at); err != nil {
			return fmt.Errorf("metrics: insert sample: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("metrics: commit: %w", err)
	}
	return nil
}

func (s *service) QueryRange(ctx context.Context, serverID string, since time.Time) ([]Point, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT cpu_percent, memory_percent, disk_percent, sampled_at
		   FROM metric_samples
		  WHERE server_id = ? AND sampled_at >= ?
		  ORDER BY sampled_at ASC
		  LIMIT ?`,
		serverID, since.UTC().Format(time.RFC3339), maxRangePoints,
	)
	if err != nil {
		return nil, fmt.Errorf("metrics: query range: %w", err)
	}
	defer func() { _ = rows.Close() }()

	out := make([]Point, 0)
	for rows.Next() {
		var (
			cpu, mem, disk sql.NullFloat64
			atStr          string
		)
		if err := rows.Scan(&cpu, &mem, &disk, &atStr); err != nil {
			return nil, fmt.Errorf("metrics: scan sample: %w", err)
		}
		at, err := time.Parse(time.RFC3339, atStr)
		if err != nil {
			return nil, fmt.Errorf("metrics: parse sampled_at: %w", err)
		}
		out = append(out, Point{At: at, CPU: fromNull(cpu), Memory: fromNull(mem), Disk: fromNull(disk)})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("metrics: iterate samples: %w", err)
	}
	return out, nil
}

func (s *service) Sweep(ctx context.Context, before time.Time) (int64, error) {
	res, err := s.db.ExecContext(ctx,
		`DELETE FROM metric_samples WHERE sampled_at < ?`,
		before.UTC().Format(time.RFC3339),
	)
	if err != nil {
		return 0, fmt.Errorf("metrics: sweep: %w", err)
	}
	n, _ := res.RowsAffected()
	return n, nil
}

// nullFloat 把 *float64 转为可空 SQL 值(nil → NULL)。
func nullFloat(f *float64) any {
	if f == nil {
		return nil
	}
	return *f
}

// fromNull 把可空 SQL 浮点转回 *float64(NULL → nil)。
func fromNull(n sql.NullFloat64) *float64 {
	if !n.Valid {
		return nil
	}
	v := n.Float64
	return &v
}
