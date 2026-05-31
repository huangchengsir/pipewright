// Package run 是「流水线运行」的领域层(FR-13 / Story 3.1)。
//
// 它定义运行/步骤数据模型、状态机(合法转移校验)、进程内 worker pool(goroutine 池
// 调度)、可插拔 Runner 接口与本期桩实现。运行/步骤状态变更经 store 持久化到 SQLite,
// 并经内存事件总线发布,供 SSE 订阅(SSE 长连接不轮询 DB、不占用唯一 DB 连接)。
//
// 边界(本期不做,只留地基):
//   - 真实构建/部署执行 = Story 3-3/4-x(本期桩 runner 跑占位步骤→成功/可注入失败)。
//   - 真实触发创建 = Story 3-2(Create 供其调用;本期内部 + 测试用)。
//   - 日志内容流 = Story 3-6(本期 SSE 只推状态/步骤,不含日志正文)。
//   - targets 多机(Epic 4)/diagnosis 诊断(Epic 7)= run-detail DTO 前向声明的 null 块,
//     形状冻结于 httpapi 层;领域层本期不建模其内容。
//
// 多 run 并行、非事务、结果独立:单 run 失败/panic 不连累其它 run。
package run

import (
	"context"
	"errors"
	"time"
)

// 运行状态枚举(DB 存小写串;JSON 同值)。状态机:
//
//	queued → running → success | failed | partial_failed | rolled_back
//
// 取消复用 StatusFailed(进行中取消 → failed;经事件/步骤可观察)。
const (
	// StatusQueued 表示已入队、等待 worker 调度。
	StatusQueued = "queued"
	// StatusRunning 表示 worker 正在执行。
	StatusRunning = "running"
	// StatusWaitingApproval 表示运行阻塞在人工审批门(Story 8-4),等待批准/拒绝(非终态)。
	// worker 仍持有该 run(在审批门内阻塞);批准 → 回 running 继续,拒绝/超时 → failed。
	StatusWaitingApproval = "waiting_approval"
	// StatusSuccess 表示全部步骤成功(终态)。
	StatusSuccess = "success"
	// StatusFailed 表示执行失败或被取消(终态)。
	StatusFailed = "failed"
	// StatusPartialFailed 表示多机部分失败(终态;Epic 4 填充语义,本期仅为合法终态)。
	StatusPartialFailed = "partial_failed"
	// StatusRolledBack 表示已回滚(终态;Epic 4 填充语义,本期仅为合法终态)。
	StatusRolledBack = "rolled_back"
)

// 步骤状态枚举。
const (
	// StepPending 表示步骤待执行。
	StepPending = "pending"
	// StepRunning 表示步骤执行中。
	StepRunning = "running"
	// StepSuccess 表示步骤成功。
	StepSuccess = "success"
	// StepFailed 表示步骤失败。
	StepFailed = "failed"
	// StepSkipped 表示步骤被跳过。
	StepSkipped = "skipped"
	// StepWaitingApproval 表示步骤(审批门阶段)正等待人工批准(Story 8-4;非终态步骤状态)。
	StepWaitingApproval = "waiting_approval"
)

// 触发类型枚举。
const (
	// TriggerWebhook 表示 webhook 触发(真实接收=3-2)。
	TriggerWebhook = "webhook"
	// TriggerManual 表示手动触发。
	TriggerManual = "manual"
	// TriggerSchedule 表示定时(cron)触发(Story 8-6)。
	TriggerSchedule = "schedule"
)

// 领域错误。错误体不含敏感数据。
var (
	// ErrNotFound 表示运行不存在。
	ErrNotFound = errors.New("run: not found")
	// ErrProjectNotFound 表示引用的项目不存在。
	ErrProjectNotFound = errors.New("run: project not found")
	// ErrInvalidTransition 表示非法状态转移(状态机拒绝)。
	ErrInvalidTransition = errors.New("run: invalid status transition")
	// ErrNotCancelable 表示运行处于终态、不可取消。
	ErrNotCancelable = errors.New("run: not cancelable")
	// ErrInvalidStatus 表示筛选/写入的状态枚举非法。
	ErrInvalidStatus = errors.New("run: invalid status")
	// ErrQueueFull 表示调度队列已满(突发创建过多),入队失败但 run 已入库。
	ErrQueueFull = errors.New("run: schedule queue full")
	// ErrPoolStopped 表示 worker pool 已停机,无法入队。
	ErrPoolStopped = errors.New("run: worker pool stopped")
	// ErrInvalidArtifactType 表示产物 type 非冻结枚举(image|jar|dist|archive)。
	ErrInvalidArtifactType = errors.New("run: invalid artifact type")
	// ErrInvalidTargetStatus 表示部署目标 status 非冻结枚举(pending|deploying|success|failed|rolled_back)。
	ErrInvalidTargetStatus = errors.New("run: invalid deploy target status")
)

// Trigger 是运行触发上下文(冻结 DTO 的 trigger 块来源)。
//
// ResolvedEnvironment / ResolvedTargetServerIDs 是 Story 3-2 由 webhook 分支映射
// 解析出的**运行内部元数据**(供 Epic 4 的 targets 扇出消费);它们**不进入**冻结的
// run-detail DTO 输出(骨架所有权),仅持久化到 pipeline_runs 的内部列。
type Trigger struct {
	Type   string // webhook | manual
	Branch string
	Commit string
	Actor  string

	// ResolvedEnvironment 是解析出的目标环境名(手动触发为空)。
	ResolvedEnvironment string
	// ResolvedTargetServerIDs 是解析出的目标服务器引用 id 列表(手动触发为空)。
	ResolvedTargetServerIDs []string

	// Params 是参数化手动运行的 key=value 参数(Story 8-11);执行时注入 script 步骤容器作环境变量。
	// 非敏感明文(含密钥应走保险库引用);为空表示无参数。
	Params map[string]string
}

// Step 是穿珠时间线节点(运行步骤)。
type Step struct {
	ID         string
	Name       string
	Status     string // pending|running|success|failed|skipped
	Ordinal    int
	StartedAt  *time.Time
	FinishedAt *time.Time
}

// Run 是一次流水线运行的领域模型。Steps 按 Ordinal 升序。
type Run struct {
	ID          string
	ProjectID   string
	ProjectName string // 冗余只读展示名(join projects),非持久列
	Status      string
	Trigger     Trigger
	Steps       []Step
	CreatedAt   time.Time
	StartedAt   *time.Time
	FinishedAt  *time.Time

	// FailureLog 是失败日志原文(脱敏前;桩 runner 合成,3-3/3-6 落地换真实日志)。
	// 空串 = 无失败日志。**绝不**原样出网:出网前必过 mask.Masker(诊断在 ai 层脱敏)。
	FailureLog string
	// Diagnosis 是已持久化的 AI 诊断(失败且已诊断时非 nil;否则 nil → run-detail diagnosis=null)。
	// 领域层只搬运形状(由 ai 层生成、httpapi 层落库),run 包不 import ai(经 hook 解耦)。
	Diagnosis *Diagnosis
}

// 诊断状态枚举(对齐冻结 run-detail diagnosis 子 DTO 的 status)。
const (
	// DiagnosisReady 表示诊断有效(有 hypothesis 等)。
	DiagnosisReady = "ready"
	// DiagnosisUnavailable 表示诊断不可用(AI 未配/超时/不可解析/低质);带 reason。
	DiagnosisUnavailable = "unavailable"
	// DiagnosisPending 表示诊断进行中(本期不主动用;前端据此显 loading)。
	DiagnosisPending = "pending"
)

// DiagnosisEvidence 是一条日志证据(取自**脱敏后**日志;绝无明文 secret)。
type DiagnosisEvidence struct {
	Line      int    // 失败日志行号(1-based)
	Text      string // 该行脱敏后文本
	Highlight bool   // 是否命中行(高亮)
}

// Diagnosis 是 AI 失败诊断的领域模型(对齐冻结 diagnosis 子 DTO,与 ai 层解耦)。
// run 包仅持有此搬运形状,不依赖 ai 包;ai 层产出结构经 httpapi 层映射后落库。
type Diagnosis struct {
	Status          string              // ready | unavailable | pending
	Reason          string              // status≠ready 时人读原因(绝无密钥)
	Hypothesis      string              // 根因假说(措辞「假说,非结论」)
	Confidence      string              // high | medium | low
	AlternateCauses []string            // 低置信时非空
	FixSuggestions  []string            // 修复建议
	Evidence        []DiagnosisEvidence // 脱敏后日志证据
	GeneratedAt     time.Time           // 生成时刻
}

// ListFilter 是运行列表筛选/分页入参(零值合理:不筛选、首页)。
type ListFilter struct {
	ProjectID string // 空 = 不按项目筛选
	Status    string // 空 = 不按状态筛选
	Page      int    // 1-based;<1 视为 1
	PageSize  int    // <1 时用默认页大小
}

// ListResult 是分页列表结果。
type ListResult struct {
	Items []Run
	Page  int
	Total int
}

// terminalStatuses 是不可再转移的终态集合。
var terminalStatuses = map[string]bool{
	StatusSuccess:       true,
	StatusFailed:        true,
	StatusPartialFailed: true,
	StatusRolledBack:    true,
}

// allowedTransitions 定义状态机合法转移图(from → 允许的 to 集合)。
var allowedTransitions = map[string]map[string]bool{
	StatusQueued: {
		StatusRunning: true,
		StatusFailed:  true, // 入队即取消 / 调度前失败
	},
	StatusRunning: {
		StatusSuccess:         true,
		StatusFailed:          true,
		StatusPartialFailed:   true,
		StatusRolledBack:      true,
		StatusWaitingApproval: true, // 进入审批门:running → waiting_approval(Story 8-4)
	},
	StatusWaitingApproval: {
		StatusRunning: true, // 批准:waiting_approval → running 继续
		StatusFailed:  true, // 拒绝/超时/取消:waiting_approval → failed
	},
}

// IsTerminal 报告状态是否为终态。
func IsTerminal(status string) bool { return terminalStatuses[status] }

// IsStepTerminal 报告步骤状态是否为终态(success|failed|skipped)。
func IsStepTerminal(status string) bool {
	switch status {
	case StepSuccess, StepFailed, StepSkipped:
		return true
	default:
		return false
	}
}

// isValidStatus 报告状态是否为已知运行状态枚举。
func isValidStatus(status string) bool {
	switch status {
	case StatusQueued, StatusRunning, StatusWaitingApproval, StatusSuccess, StatusFailed, StatusPartialFailed, StatusRolledBack:
		return true
	default:
		return false
	}
}

// canTransition 报告从 from 到 to 是否为合法状态转移。
func canTransition(from, to string) bool {
	tos, ok := allowedTransitions[from]
	if !ok {
		return false
	}
	return tos[to]
}

// ctxCanceled 报告 context 是否已被取消(用于桩 runner 与 worker 协作)。
func ctxCanceled(ctx context.Context) bool {
	select {
	case <-ctx.Done():
		return true
	default:
		return false
	}
}
