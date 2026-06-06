package run

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/huangchengsir/pipewright/internal/store"
)

// feedback.go 是「诊断反馈闭环」领域层(FR-26 / Story 7.5)。
//
// 每条 AI 诊断 👍/👎(👎 可附正确根因);反馈持久化、关联运行与诊断快照,
// 汇入设置页统计(准确率/反馈数/趋势/最近 corrections),并为知识库种子。
//
// **脱敏铁律**:correctRootCause 须由调用方在落库前脱敏(过 mask.Masker;绝无明文 secret),
// 并截断到长度上限防滥用。本层只搬运形状落库,但 SaveFeedback 自带长度上限兜底
// (脱敏交由 httpapi 层做,因 run 包不 import mask 以保持领域纯净;长度截断是无依赖的防御)。

// 反馈裁决枚举(DB 存小写串)。
const (
	// VerdictUp 表示 👍(诊断正确/有用)。
	VerdictUp = "up"
	// VerdictDown 表示 👎(诊断错误/无用;可附正确根因)。
	VerdictDown = "down"
)

// MaxCorrectRootCauseLen 是 correctRootCause 落库长度上限(字符级,防滥用)。
// 超出由 SaveFeedback 截断(领域层无依赖兜底;脱敏在 httpapi 层完成)。
const MaxCorrectRootCauseLen = 2000

// recentCorrectionsLimit 是 GetStats 返回的最近 corrections 条数上限(知识库种子可视化)。
const recentCorrectionsLimit = 10

// recentTrendBuckets 是 GetStats 趋势的最近 N 条桶切分(本期取最近若干条等分,简单实现)。
const recentTrendBuckets = 3

// ErrInvalidVerdict 表示 verdict 非冻结枚举(up|down)。
var ErrInvalidVerdict = errors.New("run: invalid feedback verdict")

// Feedback 是一条诊断反馈的领域模型(对齐 0020 表 + 冻结请求形状)。
type Feedback struct {
	ID                string
	RunID             string
	Verdict           string // up | down
	CorrectRootCause  string // down 附的正确根因(脱敏 + 截断后)
	DiagnosisSnapshot string // 反馈时刻诊断快照 JSON(脱敏后;空串 = 无)
	CreatedAt         time.Time
	UpdatedAt         time.Time
}

// Correction 是一条 👎 附根因的最近修正(知识库种子可视化;对齐冻结 stats DTO recentCorrections)。
type Correction struct {
	RunID            string
	CorrectRootCause string // 脱敏后
	At               time.Time
}

// TrendBucket 是统计趋势的一个分桶(对齐冻结 stats DTO recentTrend)。
type TrendBucket struct {
	Period   string
	Accuracy float64
	Count    int
}

// FeedbackStats 是诊断反馈聚合统计(对齐冻结 stats DTO)。
// 无反馈时:Total=Up=Down=0、Accuracy=nil、Trend/Corrections 为空切片。
type FeedbackStats struct {
	TotalFeedback     int
	ThumbsUp          int
	ThumbsDown        int
	Accuracy          *float64 // 准确率 = up/total;无反馈 → nil(不报错)
	RecentTrend       []TrendBucket
	RecentCorrections []Correction
}

// FeedbackService 定义诊断反馈领域对外接口。
type FeedbackService interface {
	// SaveFeedback 持久化一条诊断反馈(upsert by runID:同 run 改判覆盖最新)。
	// verdict 须为冻结枚举(up|down),否则 ErrInvalidVerdict。
	// correctRootCause 须由调用方在传入前脱敏;本层兜底截断到 MaxCorrectRootCauseLen。
	// run 不存在 → ErrNotFound(FK 约束失败映射)。
	SaveFeedback(ctx context.Context, in SaveFeedbackInput) (*Feedback, error)
	// GetStats 返回诊断反馈聚合统计(准确率/计数/最近趋势/最近 corrections)。
	// 无反馈 → 全 0 / 空数组 / Accuracy nil(绝不报错)。
	GetStats(ctx context.Context) (*FeedbackStats, error)
}

// SaveFeedbackInput 是 SaveFeedback 入参(correctRootCause 须已脱敏)。
type SaveFeedbackInput struct {
	RunID             string
	Verdict           string
	CorrectRootCause  string // 须已脱敏(httpapi 层过 mask)
	DiagnosisSnapshot string // 反馈时刻诊断快照 JSON(须已脱敏;空串 = 无)
}

// feedbackService 是 store 支撑的 FeedbackService 实现。
type feedbackService struct {
	db *sql.DB
}

// NewFeedbackService 构造诊断反馈 Service。db 经参数化 SQL 触库;无 init 副作用(不抬高空载内存)。
func NewFeedbackService(db *sql.DB) FeedbackService {
	return &feedbackService{db: db}
}

// SaveFeedback upsert 一条反馈(run_id 唯一冲突 → 覆盖 verdict/correct_root_cause/snapshot/updated_at,
// 保留首次 created_at)。参数化 SQL;verdict 校验;长度兜底截断;run 不存在 → ErrNotFound。
func (s *feedbackService) SaveFeedback(ctx context.Context, in SaveFeedbackInput) (*Feedback, error) {
	runID := strings.TrimSpace(in.RunID)
	if runID == "" {
		return nil, ErrNotFound
	}
	verdict := strings.TrimSpace(in.Verdict)
	if verdict != VerdictUp && verdict != VerdictDown {
		return nil, ErrInvalidVerdict
	}
	// 👍 不携带正确根因(语义上仅 👎 有意义);长度兜底截断防滥用。
	correct := in.CorrectRootCause
	if verdict == VerdictUp {
		correct = ""
	}
	correct = truncateRunes(correct, MaxCorrectRootCauseLen)

	now := time.Now().UTC()
	nowStr := now.Format(time.RFC3339)
	id := uuid.NewString()

	// upsert by run_id(唯一约束冲突 → 覆盖最新;created_at 不动,updated_at 刷新)。
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO diagnosis_feedback
		   (id, run_id, verdict, correct_root_cause, diagnosis_snapshot, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?) `+
			store.UpsertSuffix(store.DialectOf(s.db), []string{"run_id"},
				[]string{"verdict", "correct_root_cause", "diagnosis_snapshot", "updated_at"}),
		id, runID, verdict, correct, in.DiagnosisSnapshot, nowStr, nowStr,
	)
	if err != nil {
		if isForeignKeyErr(err) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("run: upsert feedback: %w", err)
	}

	return s.getFeedbackByRun(ctx, runID)
}

// getFeedbackByRun 取某 run 的反馈(供 SaveFeedback 回读最新态)。不存在 → ErrNotFound。
func (s *feedbackService) getFeedbackByRun(ctx context.Context, runID string) (*Feedback, error) {
	var (
		f          Feedback
		createdStr string
		updatedStr string
	)
	err := s.db.QueryRowContext(ctx,
		`SELECT id, run_id, verdict, correct_root_cause, diagnosis_snapshot, created_at, updated_at
		 FROM diagnosis_feedback WHERE run_id = ?`, runID,
	).Scan(&f.ID, &f.RunID, &f.Verdict, &f.CorrectRootCause, &f.DiagnosisSnapshot, &createdStr, &updatedStr)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("run: get feedback: %w", err)
	}
	if t, perr := time.Parse(time.RFC3339, createdStr); perr == nil {
		f.CreatedAt = t.UTC()
	}
	if t, perr := time.Parse(time.RFC3339, updatedStr); perr == nil {
		f.UpdatedAt = t.UTC()
	}
	return &f, nil
}

// GetStats 聚合统计:准确率(up/total)、计数、最近趋势(最近 N 条等分)、最近 corrections。
// 无反馈 → 全 0 / 空数组 / Accuracy nil。参数化 SQL;不全量驻留(corrections/trend 取最近若干)。
func (s *feedbackService) GetStats(ctx context.Context) (*FeedbackStats, error) {
	stats := &FeedbackStats{
		RecentTrend:       []TrendBucket{},
		RecentCorrections: []Correction{},
	}

	// 计数 + 准确率。
	if err := s.db.QueryRowContext(ctx,
		`SELECT
		   COUNT(1),
		   COALESCE(SUM(CASE WHEN verdict = ? THEN 1 ELSE 0 END), 0),
		   COALESCE(SUM(CASE WHEN verdict = ? THEN 1 ELSE 0 END), 0)
		 FROM diagnosis_feedback`, VerdictUp, VerdictDown,
	).Scan(&stats.TotalFeedback, &stats.ThumbsUp, &stats.ThumbsDown); err != nil {
		return nil, fmt.Errorf("run: feedback counts: %w", err)
	}
	if stats.TotalFeedback > 0 {
		acc := float64(stats.ThumbsUp) / float64(stats.TotalFeedback)
		stats.Accuracy = &acc
	}

	trend, err := s.recentTrend(ctx)
	if err != nil {
		return nil, err
	}
	stats.RecentTrend = trend

	corrections, err := s.recentCorrections(ctx)
	if err != nil {
		return nil, err
	}
	stats.RecentCorrections = corrections

	return stats, nil
}

// recentTrend 取最近 (recentTrendBuckets * 桶大小) 条反馈、按时间升序等分为若干桶,
// 各桶算 accuracy + count(简单实现:最近 N 条趋势,而非日历周)。无反馈 → 空切片。
func (s *feedbackService) recentTrend(ctx context.Context) ([]TrendBucket, error) {
	// 取最近 30 条(按 updated_at 降序),再升序等分 recentTrendBuckets 个桶。
	const window = 30
	rows, err := s.db.QueryContext(ctx,
		`SELECT verdict, updated_at FROM diagnosis_feedback
		 ORDER BY updated_at DESC, id DESC LIMIT ?`, window)
	if err != nil {
		return nil, fmt.Errorf("run: feedback trend: %w", err)
	}
	defer func() { _ = rows.Close() }()

	type fb struct {
		up bool
		at string
	}
	var recent []fb
	for rows.Next() {
		var (
			verdict string
			at      string
		)
		if err := rows.Scan(&verdict, &at); err != nil {
			return nil, fmt.Errorf("run: scan trend: %w", err)
		}
		recent = append(recent, fb{up: verdict == VerdictUp, at: at})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("run: iterate trend: %w", err)
	}
	if len(recent) == 0 {
		return []TrendBucket{}, nil
	}
	// 升序(最早 → 最新)以便桶序符合时间方向。
	for i, j := 0, len(recent)-1; i < j; i, j = i+1, j-1 {
		recent[i], recent[j] = recent[j], recent[i]
	}

	buckets := recentTrendBuckets
	if buckets > len(recent) {
		buckets = len(recent)
	}
	out := make([]TrendBucket, 0, buckets)
	per := len(recent) / buckets
	rem := len(recent) % buckets
	idx := 0
	for b := 0; b < buckets; b++ {
		size := per
		if b < rem {
			size++ // 余数摊到前面的桶
		}
		var up, cnt int
		var lastAt string
		for k := 0; k < size && idx < len(recent); k, idx = k+1, idx+1 {
			if recent[idx].up {
				up++
			}
			cnt++
			lastAt = recent[idx].at
		}
		var acc float64
		if cnt > 0 {
			acc = float64(up) / float64(cnt)
		}
		out = append(out, TrendBucket{Period: lastAt, Accuracy: acc, Count: cnt})
	}
	return out, nil
}

// recentCorrections 取最近若干条 👎 附根因(非空 correct_root_cause)的修正(知识库种子可视化)。
// 按 updated_at 降序;无 → 空切片。correct_root_cause 已脱敏(落库前由 httpapi 层过 mask)。
func (s *feedbackService) recentCorrections(ctx context.Context) ([]Correction, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT run_id, correct_root_cause, updated_at FROM diagnosis_feedback
		 WHERE verdict = ? AND correct_root_cause <> ''
		 ORDER BY updated_at DESC, id DESC LIMIT ?`, VerdictDown, recentCorrectionsLimit)
	if err != nil {
		return nil, fmt.Errorf("run: feedback corrections: %w", err)
	}
	defer func() { _ = rows.Close() }()

	out := []Correction{}
	for rows.Next() {
		var (
			c     Correction
			atStr string
		)
		if err := rows.Scan(&c.RunID, &c.CorrectRootCause, &atStr); err != nil {
			return nil, fmt.Errorf("run: scan correction: %w", err)
		}
		if t, perr := time.Parse(time.RFC3339, atStr); perr == nil {
			c.At = t.UTC()
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("run: iterate corrections: %w", err)
	}
	return out, nil
}

// truncateRunes 按 rune(字符)截断到 max(防 UTF-8 截半);max<=0 返回空串。
func truncateRunes(s string, max int) string {
	if max <= 0 {
		return ""
	}
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max])
}
