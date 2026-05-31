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

	// MarkWaitingApproval 把运行从 running 置为 waiting_approval(Story 8-4 审批门阻塞)。
	// 非 running → ErrInvalidTransition。
	MarkWaitingApproval(ctx context.Context, id string) error
	// ResumeFromApproval 把运行从 waiting_approval 置回 running(批准/拒绝后让 worker 收尾)。
	// 非 waiting_approval → ErrInvalidTransition。
	ResumeFromApproval(ctx context.Context, id string) error

	// FailOrphanedRuns 把启动时残留的非终态运行(queued/running/waiting_approval)清为 failed。
	// 进程重启会丢失内存队列与审批等待者,这些运行无法恢复,应一次性清理(返回清理条数)。
	FailOrphanedRuns(ctx context.Context) (int, error)

	// LastSuccessfulRun 查 baseline:同 project + 同 trigger.Branch、created_at 早于 before、
	// 最近一条 status=success 的运行(Story 7.3 / FR-25 成功/失败差异对比)。
	// 用于「本次失败运行 → 上一次成功运行」对比的基线选取。无匹配 → ErrNotFound。
	LastSuccessfulRun(ctx context.Context, projectID, branch string, before time.Time) (*Run, error)

	// SetFailureLog 持久化某次运行的失败日志原文(脱敏前)。供 runner 失败路径 / 重试用。
	// 不存在 → ErrNotFound。
	SetFailureLog(ctx context.Context, id, log string) error
	// GetFailureLog 取某次运行的失败日志原文(脱敏前;空串 = 无)。不存在 → ErrNotFound。
	GetFailureLog(ctx context.Context, id string) (string, error)
	// SaveDiagnosis 持久化某次运行的 AI 诊断(覆盖既有;d 为 nil 视为清空诊断)。
	// 诊断须由调用方在出网前脱敏(evidence 取自脱敏后日志);本层只搬运形状,绝无明文 secret 责任下放。
	// 不存在 → ErrNotFound。
	SaveDiagnosis(ctx context.Context, id string, d *Diagnosis) error
	// GetDiagnosis 取某次运行已持久化的诊断(未诊断 → nil, nil)。不存在 → ErrNotFound。
	GetDiagnosis(ctx context.Context, id string) (*Diagnosis, error)

	// AppendLog 追加一行运行日志(Story 3.6):分配该 run 内单调 seq → 落 run_logs → 返回 seq。
	// **text 须由调用方在落库前脱敏**(本层只搬运形状落库;dbStepSink.Log 负责脱敏)。
	// seq 分配并发安全(service 内对 run_logs 的 MAX(seq) 读 + 插入以 logMu 串行化)。
	AppendLog(ctx context.Context, runID, stream string, stepOrdinal int, text string) (seq int, err error)
	// GetLogs 取某次运行 sinceSeq 之后的日志行(升序;sinceSeq<=0 取全部)。
	// 不全量驻留:调用方按需分页(NFR-4)。run 不存在不报错(返回空切片);由 HTTP 层据 run 存在性决定 404。
	GetLogs(ctx context.Context, runID string, sinceSeq int) ([]LogLine, error)

	// AddArtifact 持久化一条运行产物到 run_artifacts(Story 3.4 / FR-6)。
	// type 须为冻结枚举(image|jar|dist|archive),否则 ErrInvalidArtifactType;
	// run 不存在 → ErrNotFound。runner 报告产物(经 StepSink.EmitArtifact)/ 真实构建(3-3)同此接口接入。
	AddArtifact(ctx context.Context, a Artifact) (*Artifact, error)
	// ListArtifacts 取某次运行的全部产物(按 created_at 升序;无产物 → 空切片)。
	// run 不存在不报错(返回空切片);由 HTTP 层据 run 存在性决定 404(仿 GetLogs 语义)。
	ListArtifacts(ctx context.Context, runID string) ([]Artifact, error)

	// SaveDeployTargets 持久化一次部署的多机结果(Story 4.2 / FR-10;参数化 SQL,单事务)。
	// status 须为冻结枚举(pending|deploying|success|failed|rolled_back),否则 ErrInvalidTargetStatus;
	// run 不存在 → ErrNotFound。供 internal/deploy 写;httpapi run-detail 读填 targets slot。
	SaveDeployTargets(ctx context.Context, runID string, targets []DeployTarget) error
	// UpsertDeployTargets 逐目标 upsert 一次重试的部分机结果(Story 4.5「仅重试失败目标」)。
	// 与 SaveDeployTargets(整批删旧重写)不同:按 (run_id, server_id) 只更新/插入给定目标行,
	// **绝不删整批**,保留本次未重试的成功目标。非法 status → ErrInvalidTargetStatus;run 不存在 → ErrNotFound。
	UpsertDeployTargets(ctx context.Context, runID string, targets []DeployTarget) error
	// ListDeployTargets 取某次运行的全部部署目标结果(按 started_at 升序;无部署 → 空切片)。
	// run 不存在不报错(返回空切片);由 HTTP 层据 run 存在性决定 404。
	ListDeployTargets(ctx context.Context, runID string) ([]DeployTarget, error)
	// SetDeployTerminal 在部署后据每机结果置 run 终态:全成功 → success(保持);有失败 → partial_failed;
	// 全失败 → failed。部署在 run 已是成功终态后发生,故此处直接覆盖终态(不走常规转移图)。
	// 仅接受这三个终态;run 不存在 → ErrNotFound。
	SetDeployTerminal(ctx context.Context, runID, status string) error
}

// service 是 store 支撑的 Service 实现,并持有内存事件总线与正在执行运行的取消句柄。
type service struct {
	db  *sql.DB
	bus *bus

	mu      sync.Mutex
	cancels map[string]context.CancelFunc // runID → 取消执行(仅运行中存在)

	// logMu 串行化 run_logs 的 seq 分配(SELECT MAX(seq)+1 → INSERT 原子化),
	// 使同一/不同 run 的并发 AppendLog 不致 seq 撞号(SetMaxOpenConns(1) 下亦正确)。
	logMu sync.Mutex

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

	// 分支留空 → 取项目默认分支(手动触发可不填:项目已知默认分支,系统自动解析)。
	if strings.TrimSpace(trigger.Branch) == "" {
		var defaultBranch string
		if qerr := s.db.QueryRowContext(ctx,
			`SELECT default_branch FROM projects WHERE id = ?`, projectID).Scan(&defaultBranch); qerr == nil {
			trigger.Branch = strings.TrimSpace(defaultBranch)
		}
	}

	tt := trigger.Type
	if tt != TriggerWebhook && tt != TriggerManual && tt != TriggerSchedule {
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
		r             Run
		createdStr    string
		startedStr    sql.NullString
		finishStr     sql.NullString
		failureLog    string
		diagnosisJSON string
	)
	r.ID = id
	err := s.db.QueryRowContext(ctx,
		`SELECT pr.project_id, COALESCE(p.name, ''), pr.status,
		        pr.trigger_type, pr.trigger_branch, pr.trigger_commit, pr.trigger_actor,
		        pr.created_at, pr.started_at, pr.finished_at,
		        pr.failure_log, pr.diagnosis_json
		 FROM pipeline_runs pr
		 LEFT JOIN projects p ON p.id = pr.project_id
		 WHERE pr.id = ?`, id,
	).Scan(&r.ProjectID, &r.ProjectName, &r.Status,
		&r.Trigger.Type, &r.Trigger.Branch, &r.Trigger.Commit, &r.Trigger.Actor,
		&createdStr, &startedStr, &finishStr,
		&failureLog, &diagnosisJSON)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("run: load run: %w", err)
	}

	r.FailureLog = failureLog
	if d, derr := decodeDiagnosis(diagnosisJSON); derr == nil {
		r.Diagnosis = d
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

// LastSuccessfulRun 查 baseline:同 project + 同 trigger.Branch、created_at 严格早于 before、
// 最近一条 status=success 的运行。参数化 SQL;无匹配 → ErrNotFound。
//
// 选取语义(冻结):同项目 + 同分支 + 更早 + 最近成功。created_at 以 RFC3339 文本存储,
// 字典序与时间序一致(同一 UTC 格式),故可直接以文本比较取「早于 before」。
func (s *service) LastSuccessfulRun(ctx context.Context, projectID, branch string, before time.Time) (*Run, error) {
	projectID = strings.TrimSpace(projectID)
	if projectID == "" {
		return nil, ErrNotFound
	}
	beforeStr := before.UTC().Format(time.RFC3339)

	var id string
	err := s.db.QueryRowContext(ctx,
		`SELECT id FROM pipeline_runs
		 WHERE project_id = ? AND trigger_branch = ? AND status = ? AND created_at < ?
		 ORDER BY created_at DESC, id DESC
		 LIMIT 1`,
		projectID, branch, StatusSuccess, beforeStr,
	).Scan(&id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("run: query last successful run: %w", err)
	}
	return s.Get(ctx, id)
}

// SetFailureLog 持久化某次运行的失败日志原文(脱敏前)。参数化 SQL;不存在 → ErrNotFound。
func (s *service) SetFailureLog(ctx context.Context, id, logText string) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE pipeline_runs SET failure_log = ? WHERE id = ?`, logText, id)
	if err != nil {
		return fmt.Errorf("run: set failure log: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// GetFailureLog 取某次运行的失败日志原文(脱敏前;空串 = 无)。不存在 → ErrNotFound。
func (s *service) GetFailureLog(ctx context.Context, id string) (string, error) {
	var logText string
	err := s.db.QueryRowContext(ctx,
		`SELECT failure_log FROM pipeline_runs WHERE id = ?`, id).Scan(&logText)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", ErrNotFound
		}
		return "", fmt.Errorf("run: get failure log: %w", err)
	}
	return logText, nil
}

// SaveDiagnosis 持久化某次运行的 AI 诊断(覆盖既有;d 为 nil 视为清空)。
// **诊断须由调用方在出网前脱敏**(evidence 取自脱敏后日志);本层只搬运形状落库。
// 参数化 SQL;不存在 → ErrNotFound。
func (s *service) SaveDiagnosis(ctx context.Context, id string, d *Diagnosis) error {
	encoded, err := encodeDiagnosis(d)
	if err != nil {
		return fmt.Errorf("run: encode diagnosis: %w", err)
	}
	res, err := s.db.ExecContext(ctx,
		`UPDATE pipeline_runs SET diagnosis_json = ? WHERE id = ?`, encoded, id)
	if err != nil {
		return fmt.Errorf("run: save diagnosis: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// GetDiagnosis 取某次运行已持久化的诊断(未诊断 → nil, nil)。不存在 → ErrNotFound。
func (s *service) GetDiagnosis(ctx context.Context, id string) (*Diagnosis, error) {
	var diagnosisJSON string
	err := s.db.QueryRowContext(ctx,
		`SELECT diagnosis_json FROM pipeline_runs WHERE id = ?`, id).Scan(&diagnosisJSON)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("run: get diagnosis: %w", err)
	}
	return decodeDiagnosis(diagnosisJSON)
}

// AppendLog 分配该 run 内单调 seq 并落 run_logs(text 须已脱敏)。参数化 SQL。
// seq = COALESCE(MAX(seq),0)+1,经 logMu 串行化以保证并发下 seq 单调不撞号。
func (s *service) AppendLog(ctx context.Context, runID, stream string, stepOrdinal int, text string) (int, error) {
	switch stream {
	case streamStdout, streamStderr:
	default:
		stream = streamStdout
	}
	now := time.Now().UTC().Format(time.RFC3339)

	s.logMu.Lock()
	defer s.logMu.Unlock()

	var maxSeq int
	if err := s.db.QueryRowContext(ctx,
		`SELECT COALESCE(MAX(seq), 0) FROM run_logs WHERE run_id = ?`, runID).Scan(&maxSeq); err != nil {
		return 0, fmt.Errorf("run: max log seq: %w", err)
	}
	seq := maxSeq + 1

	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO run_logs (id, run_id, seq, ts, stream, step_ordinal, text)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		uuid.NewString(), runID, seq, now, stream, stepOrdinal, text,
	); err != nil {
		return 0, fmt.Errorf("run: insert log: %w", err)
	}
	return seq, nil
}

// GetLogs 取 sinceSeq 之后的日志行(升序)。sinceSeq<=0 取全部。参数化 SQL;不全量驻留。
func (s *service) GetLogs(ctx context.Context, runID string, sinceSeq int) ([]LogLine, error) {
	if sinceSeq < 0 {
		sinceSeq = 0
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT seq, ts, stream, step_ordinal, text
		 FROM run_logs WHERE run_id = ? AND seq > ? ORDER BY seq ASC`, runID, sinceSeq)
	if err != nil {
		return nil, fmt.Errorf("run: load logs: %w", err)
	}
	defer func() { _ = rows.Close() }()

	out := []LogLine{}
	for rows.Next() {
		var (
			l     LogLine
			tsStr string
		)
		if err := rows.Scan(&l.Seq, &tsStr, &l.Stream, &l.StepOrdinal, &l.Text); err != nil {
			return nil, fmt.Errorf("run: scan log: %w", err)
		}
		if t, perr := time.Parse(time.RFC3339, tsStr); perr == nil {
			l.Ts = t.UTC()
		}
		out = append(out, l)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("run: iterate logs: %w", err)
	}
	return out, nil
}

// transition 校验并持久化运行状态转移:CAS 式更新(WHERE status=from),
// 顺带按 to 维护 started_at/finished_at;成功后经事件总线发布 status 事件。
// requireFrom=true 时,当前状态非 from(竞态/非法)→ ErrInvalidTransition。
// MarkWaitingApproval 实现 Service:running → waiting_approval(审批门进入)。
func (s *service) MarkWaitingApproval(ctx context.Context, id string) error {
	return s.transition(ctx, id, StatusRunning, StatusWaitingApproval, true)
}

// ResumeFromApproval 实现 Service:waiting_approval → running(决定后让 worker 收尾)。
func (s *service) ResumeFromApproval(ctx context.Context, id string) error {
	return s.transition(ctx, id, StatusWaitingApproval, StatusRunning, true)
}

// FailOrphanedRuns 实现 Service:启动时把残留非终态运行清为 failed(孤儿,不可恢复)。
func (s *service) FailOrphanedRuns(ctx context.Context) (int, error) {
	now := time.Now().UTC().Format(time.RFC3339)
	res, err := s.db.ExecContext(ctx,
		`UPDATE pipeline_runs SET status = ?, finished_at = ?
		 WHERE status IN (?, ?, ?)`,
		StatusFailed, now, StatusQueued, StatusRunning, StatusWaitingApproval,
	)
	if err != nil {
		return 0, fmt.Errorf("run: fail orphaned runs: %w", err)
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

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
