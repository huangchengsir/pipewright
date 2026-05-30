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
)

// 触发类型枚举。
const (
	// TriggerWebhook 表示 webhook 触发(真实接收=3-2)。
	TriggerWebhook = "webhook"
	// TriggerManual 表示手动触发。
	TriggerManual = "manual"
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
		StatusSuccess:       true,
		StatusFailed:        true,
		StatusPartialFailed: true,
		StatusRolledBack:    true,
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
	case StatusQueued, StatusRunning, StatusSuccess, StatusFailed, StatusPartialFailed, StatusRolledBack:
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
