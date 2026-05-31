// Package dag 是流水线 DAG 调度引擎核心(Epic 8 · Story 8-3)。
//
// 它是一个**纯粹、确定、可并发**的调度内核:给定一组带依赖(needs)的节点(= 流水线阶段)
// 与一个「执行单节点」的回调,按 DAG 拓扑序调度执行 —— 无依赖关系的节点并行跑(受
// 最大并发上限约束),节点在其全部上游成功后才就绪,任一上游失败则该节点(及其下游)
// 被跳过(skipped),除非该上游声明了 AllowFailure。构造期做环检测(防环)。
//
// 本内核刻意**不触库、不发事件、不感知容器/SSH** —— 执行副作用全在调用方传入的 RunFunc 里。
// 这样它能被纯单测彻底覆盖,后续由 run 引擎(8-2 脚本执行器 + StepSink 流式日志)把每个
// 节点的真实执行(stage 内并行 job → job 内有序 step)接进 RunFunc,复用现有持久化/SSE 管道。
//
// 失败语义(对标 Jenkins/云效):
//   - 节点成功 ⇒ 下游解锁。
//   - 节点失败且未声明 AllowFailure ⇒ 下游(直接与间接)全部 skipped;**互不依赖的并行分支照常继续**。
//   - 节点失败但声明 AllowFailure ⇒ 视为「有效成功」解锁下游,但自身仍记为 failed(整体 run 计失败可由调用方据 Result 判定)。
//   - context 取消 ⇒ 已在跑的节点收到取消(由 RunFunc 自行响应);未跑的节点记为 canceled 并向下游传播为跳过。
package dag

import (
	"context"
	"errors"
	"fmt"
	"sort"
)

// Status 是一个节点调度后的终态。
type Status string

const (
	// StatusSuccess 表示节点执行成功。
	StatusSuccess Status = "success"
	// StatusFailed 表示节点执行失败(RunFunc 返回非 nil)。
	StatusFailed Status = "failed"
	// StatusSkipped 表示节点因上游失败/跳过而未执行。
	StatusSkipped Status = "skipped"
	// StatusCanceled 表示节点因 context 取消而未(完整)执行。
	StatusCanceled Status = "canceled"
)

// Node 是一个可调度单元(对应流水线一个 stage)。
type Node struct {
	// ID 是节点唯一标识(同图内不可重复、非空)。
	ID string
	// Needs 是该节点依赖的上游节点 ID 列表(必须都在图内;不可自指;整体不可成环)。
	Needs []string
	// AllowFailure 为 true 时,该节点失败仍视为「有效成功」放行下游(自身仍记 failed)。
	AllowFailure bool
}

// NodeResult 是单个节点的调度结果。
type NodeResult struct {
	Status Status
	// Err 是 RunFunc 返回的错误(仅 failed 时非 nil)。
	Err error
}

// Result 是整图调度结果(节点 ID → 终态)。
type Result map[string]NodeResult

// Succeeded 报告是否所有节点都成功(无 failed/skipped/canceled)。
// AllowFailure 节点失败时本方法仍返回 false(它确实失败了);若调用方希望宽容,
// 可自行遍历 Result 按业务判定。
func (r Result) Succeeded() bool {
	for _, nr := range r {
		if nr.Status != StatusSuccess {
			return false
		}
	}
	return true
}

// Counts 汇总各终态数量(便于摘要/断言)。
func (r Result) Counts() map[Status]int {
	out := map[Status]int{}
	for _, nr := range r {
		out[nr.Status]++
	}
	return out
}

// RunFunc 执行单个节点;返回非 nil 表示该节点失败。必须响应 ctx 取消。
type RunFunc func(ctx context.Context, id string) error

// Options 调度选项。
type Options struct {
	// MaxConcurrency 是同时执行的节点数上限;<=0 表示不限(取节点总数)。
	MaxConcurrency int
}

// 构造期错误。
var (
	// ErrEmptyID 表示存在空 ID 节点。
	ErrEmptyID = errors.New("dag: node id must not be empty")
	// ErrDuplicateID 表示节点 ID 重复。
	ErrDuplicateID = errors.New("dag: duplicate node id")
	// ErrUnknownDep 表示某节点依赖了图中不存在的节点。
	ErrUnknownDep = errors.New("dag: unknown dependency")
	// ErrSelfDep 表示某节点依赖了自身。
	ErrSelfDep = errors.New("dag: node depends on itself")
	// ErrCycle 表示图中存在环(非 DAG)。
	ErrCycle = errors.New("dag: cycle detected")
)

// Graph 是一张已校验的 DAG。
type Graph struct {
	nodes      map[string]Node
	order      []string            // 稳定声明序(用于确定性遍历/种子)
	dependents map[string][]string // id → 依赖它的下游(按声明序)
}

// New 校验节点集合并构造 DAG。
// 校验:ID 非空且唯一;Needs 去重后必须都在图内、不可自指;整体不可成环(Kahn 拓扑校验)。
func New(nodes []Node) (*Graph, error) {
	g := &Graph{
		nodes:      make(map[string]Node, len(nodes)),
		order:      make([]string, 0, len(nodes)),
		dependents: make(map[string][]string),
	}
	// 收集 + 唯一性
	for _, n := range nodes {
		if n.ID == "" {
			return nil, ErrEmptyID
		}
		if _, dup := g.nodes[n.ID]; dup {
			return nil, fmt.Errorf("%w: %s", ErrDuplicateID, n.ID)
		}
		// 去重 needs(保留首次出现序)
		seen := make(map[string]struct{}, len(n.Needs))
		deduped := make([]string, 0, len(n.Needs))
		for _, d := range n.Needs {
			if d == n.ID {
				return nil, fmt.Errorf("%w: %s", ErrSelfDep, n.ID)
			}
			if _, ok := seen[d]; ok {
				continue
			}
			seen[d] = struct{}{}
			deduped = append(deduped, d)
		}
		nn := Node{ID: n.ID, Needs: deduped, AllowFailure: n.AllowFailure}
		g.nodes[nn.ID] = nn
		g.order = append(g.order, nn.ID)
	}
	// 依赖存在性 + 反向边
	for _, id := range g.order {
		for _, d := range g.nodes[id].Needs {
			if _, ok := g.nodes[d]; !ok {
				return nil, fmt.Errorf("%w: %s -> %s", ErrUnknownDep, id, d)
			}
			g.dependents[d] = append(g.dependents[d], id)
		}
	}
	if err := g.checkAcyclic(); err != nil {
		return nil, err
	}
	return g, nil
}

// checkAcyclic 用 Kahn 算法做拓扑校验:若无法把所有节点排完,则存在环。
func (g *Graph) checkAcyclic() error {
	indeg := make(map[string]int, len(g.nodes))
	for _, id := range g.order {
		indeg[id] = len(g.nodes[id].Needs)
	}
	queue := make([]string, 0, len(g.order))
	for _, id := range g.order {
		if indeg[id] == 0 {
			queue = append(queue, id)
		}
	}
	visited := 0
	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		visited++
		for _, dep := range g.dependents[id] {
			indeg[dep]--
			if indeg[dep] == 0 {
				queue = append(queue, dep)
			}
		}
	}
	if visited != len(g.nodes) {
		return ErrCycle
	}
	return nil
}

// TopoOrder 返回一个确定性的拓扑序(同层按声明序),便于串行执行或测试断言。
func (g *Graph) TopoOrder() []string {
	indeg := make(map[string]int, len(g.nodes))
	for _, id := range g.order {
		indeg[id] = len(g.nodes[id].Needs)
	}
	out := make([]string, 0, len(g.order))
	// 反复取出当前 indeg==0 的节点(按声明序),保证确定性。
	resolved := make(map[string]bool, len(g.order))
	for len(out) < len(g.order) {
		progressed := false
		for _, id := range g.order {
			if resolved[id] || indeg[id] != 0 {
				continue
			}
			resolved[id] = true
			out = append(out, id)
			for _, dep := range g.dependents[id] {
				indeg[dep]--
			}
			progressed = true
		}
		if !progressed {
			break // 不应发生(已 checkAcyclic);防御性退出
		}
	}
	return out
}

// Schedule 按 DAG 调度执行整图,返回每个节点的终态。
//
// 并发:互不依赖且就绪的节点并行执行(受 opts.MaxConcurrency 约束)。
// 失败传播:见包注释。本方法阻塞至所有节点终态。
func (g *Graph) Schedule(ctx context.Context, run RunFunc, opts Options) Result {
	total := len(g.nodes)
	results := make(Result, total)
	if total == 0 {
		return results
	}

	max := opts.MaxConcurrency
	if max <= 0 || max > total {
		max = total
	}
	sem := make(chan struct{}, max)

	indeg := make(map[string]int, total)
	for _, id := range g.order {
		indeg[id] = len(g.nodes[id].Needs)
	}
	// upstreamFailed[id]:其某个上游「有效失败」(失败且未 AllowFailure,或被跳过/取消)。
	upstreamFailed := make(map[string]bool, total)

	type fin struct {
		id     string
		status Status
		err    error
	}
	finCh := make(chan fin)
	done := 0

	launch := func(id string) {
		go func() {
			// 取并发令牌前先看取消,避免无谓占位。
			select {
			case sem <- struct{}{}:
			case <-ctx.Done():
				finCh <- fin{id, StatusCanceled, ctx.Err()}
				return
			}
			defer func() { <-sem }()
			if ctx.Err() != nil {
				finCh <- fin{id, StatusCanceled, ctx.Err()}
				return
			}
			err := run(ctx, id)
			st := StatusSuccess
			if err != nil {
				st = StatusFailed
			}
			finCh <- fin{id, st, err}
		}()
	}

	// effectiveFail 报告某终态是否应让下游「跳过」。
	// 失败但 AllowFailure 的节点不阻断下游;skipped/canceled 阻断;真失败阻断。
	effectiveFail := func(id string, st Status) bool {
		switch st {
		case StatusSuccess:
			return false
		case StatusFailed:
			return !g.nodes[id].AllowFailure
		default: // skipped / canceled
			return true
		}
	}

	// resolve 处理一个已终态节点对其下游的影响:递减下游 indeg,标记上游失败传播;
	// 下游 indeg 归零时,要么立即跳过(并递归传播为跳过),要么 launch 执行。
	// 用显式栈处理跳过链(避免递归)。
	var resolve func(id string, st Status)
	resolve = func(id string, st Status) {
		type pending struct {
			id   string
			fail bool
		}
		stack := make([]pending, 0)
		for _, dep := range g.dependents[id] {
			stack = append(stack, pending{dep, effectiveFail(id, st)})
		}
		for len(stack) > 0 {
			p := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			indeg[p.id]--
			if p.fail {
				upstreamFailed[p.id] = true
			}
			if indeg[p.id] != 0 {
				continue
			}
			if upstreamFailed[p.id] {
				// 上游有效失败 → 跳过;跳过本身向其下游传播为失败。
				results[p.id] = NodeResult{Status: StatusSkipped}
				done++
				for _, dd := range g.dependents[p.id] {
					stack = append(stack, pending{dd, true})
				}
			} else {
				launch(p.id)
			}
		}
	}

	// 种子:所有 indeg==0 节点按声明序启动。
	for _, id := range g.order {
		if indeg[id] == 0 {
			launch(id)
		}
	}

	for done < total {
		f := <-finCh
		results[f.id] = NodeResult{Status: f.status, Err: f.err}
		done++
		resolve(f.id, f.status)
	}
	return results
}

// SortedIDs 返回结果里所有节点 ID 的稳定排序(测试便利)。
func (r Result) SortedIDs() []string {
	ids := make([]string, 0, len(r))
	for id := range r {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}
