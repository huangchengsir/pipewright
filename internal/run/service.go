package run

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

// defaultPageSize 是列表默认页大小;maxPageSize 防滥用。
const (
	defaultPageSize = 20
	maxPageSize     = 100
	// maxListPage 钳制 page 上界,防 (page-1)*size OFFSET 整数溢出。
	maxListPage = 100000
)

// Service 定义运行领域对外接口。
type Service interface {
	// Create 创建一次流水线运行(状态 queued)并入库,随后入队由 worker pool 调度。
	// 供 Story 3-2(触发)调用;本期亦内部 + 测试用。项目不存在 → ErrProjectNotFound。
	Create(ctx context.Context, projectID string, trigger Trigger) (*Run, error)
	// Get 返回单次运行(含步骤,按 ordinal 升序;join 项目名)。不存在 → ErrNotFound。
	Get(ctx context.Context, id string) (*Run, error)
	// List 按筛选/分页返回运行(列表行精简,不含步骤)。
	List(ctx context.Context, f ListFilter) (*ListResult, error)
	// Cancel 取消进行中(queued/running)运行:经 context 传播到 Runner;终态 → ErrNotCancelable。
	Cancel(ctx context.Context, id string) (*Run, error)
}

// service 是 store 支撑的 Service 实现,并持有内存事件总线与正在执行运行的取消句柄。
type service struct {
	db  *sql.DB
	bus *bus

	mu      sync.Mutex
	cancels map[string]context.CancelFunc // runID → 取消执行(仅运行中存在)

	// enqueue 由 worker pool 在构造时注入:Create 入库后据此把 run 推入调度队列。
	// 为 nil(无 pool;如纯单测 Service)时,Create 仅入库不调度。
	// 返回非 nil 错误(队列满/已停机)时 Create 据此回滚并失败返回(不留挂死)。
	enqueueMu sync.RWMutex
	enqueue   func(runID string) error
}

// setEnqueue 由 WorkerPool 注入调度回调(Create 入库后调用)。
func (s *service) setEnqueue(fn func(runID string) error) {
	s.enqueueMu.Lock()
	s.enqueue = fn
	s.enqueueMu.Unlock()
}

// New 构造运行 Service。db 经参数化 SQL 触库;事件总线用于 SSE 推送(不轮询 DB)。
// 不做任何重活(无 init() 副作用,避免抬高空载内存)。
func New(db *sql.DB) Service {
	return newService(db)
}

func newService(db *sql.DB) *service {
	return &service{
		db:      db,
		bus:     newBus(),
		cancels: make(map[string]context.CancelFunc),
	}
}

func (s *service) Create(ctx context.Context, projectID string, trigger Trigger) (*Run, error) {
	projectID = strings.TrimSpace(projectID)
	if projectID == "" {
		return nil, ErrProjectNotFound
	}
	if err := s.ensureProjectExists(ctx, projectID); err != nil {
		return nil, err
	}

	tt := trigger.Type
	if tt != TriggerWebhook && tt != TriggerManual {
		tt = TriggerManual
	}

	// 解析出的目标元数据(webhook 分支映射)持久化为运行内部列;手动触发为空/[]。
	// 这些列**不**进入冻结 run-detail DTO 输出,仅供 Epic 4 的 targets 消费。
	targetIDs := trigger.ResolvedTargetServerIDs
	if targetIDs == nil {
		targetIDs = []string{}
	}
	targetIDsJSON, err := json.Marshal(targetIDs)
	if err != nil {
		return nil, fmt.Errorf("run: marshal resolved target server ids: %w", err)
	}

	now := time.Now().UTC()
	id := uuid.NewString()
	_, err = s.db.ExecContext(ctx,
		`INSERT INTO pipeline_runs
		   (id, project_id, status, trigger_type, trigger_branch, trigger_commit, trigger_actor,
		    resolved_environment, resolved_target_server_ids, created_at, started_at, finished_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, NULL, NULL)`,
		id, projectID, StatusQueued, tt, trigger.Branch, trigger.Commit, trigger.Actor,
		trigger.ResolvedEnvironment, string(targetIDsJSON),
		now.Format(time.RFC3339),
	)
	if err != nil {
		if isForeignKeyErr(err) {
			return nil, ErrProjectNotFound
		}
		return nil, fmt.Errorf("run: insert run: %w", err)
	}

	created, err := s.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	// 入队由 worker pool 调度(若已装配 pool)。无 pool 时仅入库(测试/无调度场景)。
	s.enqueueMu.RLock()
	fn := s.enqueue
	s.enqueueMu.RUnlock()
	if fn != nil {
		if err := fn(id); err != nil {
			// 入队失败(队列满/已停机):删除孤儿 queued 记录,失败返回避免留下永不调度的运行。
			_, _ = s.db.ExecContext(ctx, `DELETE FROM pipeline_runs WHERE id = ? AND status = ?`, id, StatusQueued)
			return nil, err
		}
	}
	return created, nil
}

func (s *service) Get(ctx context.Context, id string) (*Run, error) {
	var (
		r          Run
		createdStr string
		startedStr sql.NullString
		finishStr  sql.NullString
	)
	r.ID = id
	err := s.db.QueryRowContext(ctx,
		`SELECT pr.project_id, COALESCE(p.name, ''), pr.status,
		        pr.trigger_type, pr.trigger_branch, pr.trigger_commit, pr.trigger_actor,
		        pr.created_at, pr.started_at, pr.finished_at
		 FROM pipeline_runs pr
		 LEFT JOIN projects p ON p.id = pr.project_id
		 WHERE pr.id = ?`, id,
	).Scan(&r.ProjectID, &r.ProjectName, &r.Status,
		&r.Trigger.Type, &r.Trigger.Branch, &r.Trigger.Commit, &r.Trigger.Actor,
		&createdStr, &startedStr, &finishStr)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("run: load run: %w", err)
	}

	if r.CreatedAt, err = time.Parse(time.RFC3339, createdStr); err != nil {
		return nil, fmt.Errorf("run: parse created_at: %w", err)
	}
	if r.StartedAt, err = parseNullTime(startedStr); err != nil {
		return nil, err
	}
	if r.FinishedAt, err = parseNullTime(finishStr); err != nil {
		return nil, err
	}

	steps, err := s.loadSteps(ctx, id)
	if err != nil {
		return nil, err
	}
	r.Steps = steps
	return &r, nil
}

func (s *service) loadSteps(ctx context.Context, runID string) ([]Step, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, name, status, ordinal, started_at, finished_at
		 FROM run_steps WHERE run_id = ? ORDER BY ordinal ASC`, runID)
	if err != nil {
		return nil, fmt.Errorf("run: load steps: %w", err)
	}
	defer func() { _ = rows.Close() }()

	steps := []Step{}
	for rows.Next() {
		var (
			st         Step
			startedStr sql.NullString
			finishStr  sql.NullString
		)
		if err := rows.Scan(&st.ID, &st.Name, &st.Status, &st.Ordinal, &startedStr, &finishStr); err != nil {
			return nil, fmt.Errorf("run: scan step: %w", err)
		}
		if st.StartedAt, err = parseNullTime(startedStr); err != nil {
			return nil, err
		}
		if st.FinishedAt, err = parseNullTime(finishStr); err != nil {
			return nil, err
		}
		steps = append(steps, st)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("run: iterate steps: %w", err)
	}
	return steps, nil
}

func (s *service) List(ctx context.Context, f ListFilter) (*ListResult, error) {
	if f.Status != "" && !isValidStatus(f.Status) {
		return nil, ErrInvalidStatus
	}
	page := f.Page
	if page < 1 {
		page = 1
	}
	if page > maxListPage {
		// 钳制上界:防 (page-1)*size 整数溢出为负 OFFSET(防御直接调用方;HTTP 层已先做 400)。
		page = maxListPage
	}
	size := f.PageSize
	if size < 1 {
		size = defaultPageSize
	}
	if size > maxPageSize {
		size = maxPageSize
	}

	where := []string{}
	args := []any{}
	if f.ProjectID != "" {
		where = append(where, "pr.project_id = ?")
		args = append(args, f.ProjectID)
	}
	if f.Status != "" {
		where = append(where, "pr.status = ?")
		args = append(args, f.Status)
	}
	clause := ""
	if len(where) > 0 {
		clause = " WHERE " + strings.Join(where, " AND ")
	}

	var total int
	if err := s.db.QueryRowContext(ctx,
		"SELECT COUNT(1) FROM pipeline_runs pr"+clause, args...,
	).Scan(&total); err != nil {
		return nil, fmt.Errorf("run: count runs: %w", err)
	}

	offset := (page - 1) * size
	listArgs := append(append([]any{}, args...), size, offset)
	rows, err := s.db.QueryContext(ctx,
		`SELECT pr.id, pr.project_id, COALESCE(p.name, ''), pr.status,
		        pr.trigger_type, pr.trigger_branch, pr.trigger_commit, pr.trigger_actor,
		        pr.created_at, pr.started_at, pr.finished_at
		 FROM pipeline_runs pr
		 LEFT JOIN projects p ON p.id = pr.project_id`+clause+`
		 ORDER BY pr.created_at DESC, pr.id DESC
		 LIMIT ? OFFSET ?`, listArgs...)
	if err != nil {
		return nil, fmt.Errorf("run: list runs: %w", err)
	}
	defer func() { _ = rows.Close() }()

	items := []Run{}
	for rows.Next() {
		var (
			r          Run
			createdStr string
			startedStr sql.NullString
			finishStr  sql.NullString
		)
		if err := rows.Scan(&r.ID, &r.ProjectID, &r.ProjectName, &r.Status,
			&r.Trigger.Type, &r.Trigger.Branch, &r.Trigger.Commit, &r.Trigger.Actor,
			&createdStr, &startedStr, &finishStr); err != nil {
			return nil, fmt.Errorf("run: scan run: %w", err)
		}
		if r.CreatedAt, err = time.Parse(time.RFC3339, createdStr); err != nil {
			return nil, fmt.Errorf("run: parse created_at: %w", err)
		}
		if r.StartedAt, err = parseNullTime(startedStr); err != nil {
			return nil, err
		}
		if r.FinishedAt, err = parseNullTime(finishStr); err != nil {
			return nil, err
		}
		items = append(items, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("run: iterate runs: %w", err)
	}
	return &ListResult{Items: items, Page: page, Total: total}, nil
}

func (s *service) Cancel(ctx context.Context, id string) (*Run, error) {
	r, err := s.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if IsTerminal(r.Status) {
		return nil, ErrNotCancelable
	}

	// 若该 run 正在 worker 中执行:经 context 取消传播到 Runner,由 worker 落终态。
	s.mu.Lock()
	cancel, running := s.cancels[id]
	s.mu.Unlock()
	if running {
		cancel()
		// worker 会异步落 failed;此处返回当前快照(可能仍 running),客户端经 SSE/轮询见终态。
		return s.Get(ctx, id)
	}

	// 尚未被 worker 取走(queued):直接置 failed(queued→failed 合法转移)。
	if err := s.transition(ctx, id, StatusQueued, StatusFailed, true); err != nil {
		// 竞态:worker 恰好取走/已落终态 → 重查当前状态,绝不把 ErrInvalidTransition 透传到 HTTP(避免 500)。
		if errors.Is(err, ErrInvalidTransition) {
			// 仍在执行 → 退化为「运行中取消」语义。
			s.mu.Lock()
			cancel, running = s.cancels[id]
			s.mu.Unlock()
			if running {
				cancel()
				return s.Get(ctx, id)
			}
			// 已被 worker 落终态(竞态末尾):重查,终态 → ErrNotCancelable(handler 映射 409)。
			cur, gerr := s.Get(ctx, id)
			if gerr != nil {
				return nil, gerr
			}
			if IsTerminal(cur.Status) {
				return nil, ErrNotCancelable
			}
			// 既非运行中又非终态(罕见中间窗口):返回当前快照,客户端经 SSE/轮询收敛。
			return cur, nil
		}
		return nil, err
	}
	return s.Get(ctx, id)
}

// transition 校验并持久化运行状态转移:CAS 式更新(WHERE status=from),
// 顺带按 to 维护 started_at/finished_at;成功后经事件总线发布 status 事件。
// requireFrom=true 时,当前状态非 from(竞态/非法)→ ErrInvalidTransition。
func (s *service) transition(ctx context.Context, id, from, to string, requireFrom bool) error {
	if !canTransition(from, to) {
		return ErrInvalidTransition
	}
	now := time.Now().UTC().Format(time.RFC3339)

	set := "status = ?"
	args := []any{to}
	if to == StatusRunning {
		set += ", started_at = ?"
		args = append(args, now)
	}
	if IsTerminal(to) {
		set += ", finished_at = ?"
		args = append(args, now)
	}
	q := "UPDATE pipeline_runs SET " + set + " WHERE id = ?"
	args = append(args, id)
	if requireFrom {
		q += " AND status = ?"
		args = append(args, from)
	}

	res, err := s.db.ExecContext(ctx, q, args...)
	if err != nil {
		return fmt.Errorf("run: transition: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrInvalidTransition
	}
	s.bus.publish(Event{Kind: EventStatus, RunID: id, Status: to})
	return nil
}

// failToTerminal 幂等地把运行置为 failed:先读当前状态,已是终态则不动,
// 否则按当前状态合法转移到 failed(queued→failed 或 running→failed)。
// 用于 worker panic/加载失败兜底:不假定来源是 running(避免破坏 started_at 语义)。
func (s *service) failToTerminal(runID string) {
	var status string
	err := s.db.QueryRowContext(context.Background(),
		`SELECT status FROM pipeline_runs WHERE id = ?`, runID).Scan(&status)
	if err != nil {
		// 不存在/读失败:无可兜底(记录已删或库异常)。
		return
	}
	if IsTerminal(status) {
		return // 已终态:幂等返回。
	}
	if err := s.transition(context.Background(), runID, status, StatusFailed, true); err != nil {
		// 竞态(状态在读后被改):非致命,SSE/轮询仍可见最终态。
		_ = err
	}
}

// ensureProjectExists 校验项目存在(运行依附项目)。
func (s *service) ensureProjectExists(ctx context.Context, projectID string) error {
	var one int
	err := s.db.QueryRowContext(ctx, `SELECT 1 FROM projects WHERE id = ?`, projectID).Scan(&one)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrProjectNotFound
		}
		return fmt.Errorf("run: check project: %w", err)
	}
	return nil
}

// parseNullTime 把可空 RFC3339 文本解析为 *time.Time(NULL → nil)。
func parseNullTime(ns sql.NullString) (*time.Time, error) {
	if !ns.Valid || strings.TrimSpace(ns.String) == "" {
		return nil, nil
	}
	t, err := time.Parse(time.RFC3339, ns.String)
	if err != nil {
		return nil, fmt.Errorf("run: parse time: %w", err)
	}
	tt := t.UTC()
	return &tt, nil
}

// isForeignKeyErr 判断错误是否为外键约束失败(modernc sqlite 文本含 FOREIGN KEY)。
func isForeignKeyErr(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(strings.ToUpper(err.Error()), "FOREIGN KEY")
}
