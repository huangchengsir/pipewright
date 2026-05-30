package run

import "sync"

// EventKind 区分事件总线推送的事件类型(对应 SSE event 名)。
type EventKind string

const (
	// EventStatus 表示 run 状态变更事件。
	EventStatus EventKind = "status"
	// EventStep 表示步骤状态变更事件。
	EventStep EventKind = "step"
)

// Event 是事件总线发布/订阅的载荷。仅状态/步骤元数据,绝不含日志正文(=3-6)。
type Event struct {
	Kind  EventKind
	RunID string
	// Status 在 Kind==EventStatus 时填运行状态;Kind==EventStep 时填该步骤状态。
	Status string
	// Step 在 Kind==EventStep 时携带步骤快照;EventStatus 时为零值。
	Step Step
}

// bus 是进程内内存事件总线:worker 发布运行/步骤变更,SSE handler 订阅。
// SSE 据此推送,从而无需轮询 DB(避免 SetMaxOpenConns(1) 下长连接占用唯一连接)。
type bus struct {
	mu   sync.RWMutex
	next int
	// subs 按订阅者 id 索引;每个订阅者持有一个有缓冲 channel。
	subs map[int]subscriber
}

type subscriber struct {
	runID string // 仅接收该 run 的事件
	ch    chan Event
}

func newBus() *bus {
	return &bus{subs: make(map[int]subscriber)}
}

// subscribe 注册一个针对 runID 的订阅,返回事件通道与取消函数。
// 通道有缓冲;满时丢弃最旧策略由 publish 端「非阻塞发送」实现,避免慢订阅者拖垮 worker。
func (b *bus) subscribe(runID string) (<-chan Event, func()) {
	b.mu.Lock()
	id := b.next
	b.next++
	ch := make(chan Event, 32)
	b.subs[id] = subscriber{runID: runID, ch: ch}
	b.mu.Unlock()

	cancel := func() {
		b.mu.Lock()
		if s, ok := b.subs[id]; ok {
			delete(b.subs, id)
			close(s.ch)
		}
		b.mu.Unlock()
	}
	return ch, cancel
}

// publish 向匹配 runID 的订阅者非阻塞投递事件;慢订阅者(缓冲满)被跳过,
// 保证 worker 永不因 SSE 客户端阻塞(SSE 可降级轮询 GET /api/runs/{id})。
func (b *bus) publish(ev Event) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	for _, s := range b.subs {
		if s.runID != ev.RunID {
			continue
		}
		select {
		case s.ch <- ev:
		default:
			// 订阅者缓冲已满:丢弃本事件,不阻塞 worker。
		}
	}
}
