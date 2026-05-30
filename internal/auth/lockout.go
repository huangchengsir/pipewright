package auth

import (
	"sync"
	"time"
)

// 锁定参数(AC3 / NFR-5):连续失败 5 次 → 锁定 15 分钟。
const (
	LockoutThreshold = 5
	LockoutDuration  = 15 * time.Minute
)

// Clock 允许测试注入可控时钟(避免真实等待 15 分钟)。
type Clock interface {
	Now() time.Time
}

// RealClock 使用系统时间。
type RealClock struct{}

func (RealClock) Now() time.Time { return time.Now().UTC() }

// LockoutManager 管理单管理员的登录失败计数与锁定状态。
// 单管理员实例:全局单一计数器 + mutex,无需按 IP/用户分桶。
//
// 注:当前实现为内存计数器(进程重启后清零)。
// 若需跨重启持久化,可将 failCount/lockedUntil 存入 DB,此为后续可选扩展。
type LockoutManager struct {
	mu          sync.Mutex
	failCount   int
	lockedUntil time.Time
	clock       Clock
}

// NewLockoutManager 构造 LockoutManager;clock 为 nil 时使用 RealClock。
func NewLockoutManager(clock Clock) *LockoutManager {
	if clock == nil {
		clock = RealClock{}
	}
	return &LockoutManager{clock: clock}
}

// Check 检查当前是否处于锁定状态。
// 返回 (locked bool, remainingDuration)。
func (lm *LockoutManager) Check() (bool, time.Duration) {
	lm.mu.Lock()
	defer lm.mu.Unlock()
	return lm.checkLocked()
}

// checkLocked 是 Check 的内部实现,要求调用方已持锁。
// 锁定到期时就地重置计数(到期后第一次检查清除)。
func (lm *LockoutManager) checkLocked() (bool, time.Duration) {
	now := lm.clock.Now()
	if !lm.lockedUntil.IsZero() && now.Before(lm.lockedUntil) {
		return true, lm.lockedUntil.Sub(now)
	}
	// 锁定到期:自动重置(到期后第一次检查清除)。
	if !lm.lockedUntil.IsZero() && !now.Before(lm.lockedUntil) {
		lm.failCount = 0
		lm.lockedUntil = time.Time{}
	}
	return false, 0
}

// recordFailureLocked 是 RecordFailure 的内部实现,要求调用方已持锁。
func (lm *LockoutManager) recordFailureLocked() {
	now := lm.clock.Now()
	// 若已过锁定期则先重置(防止上次锁定遗留的 failCount 叠加)。
	if !lm.lockedUntil.IsZero() && !now.Before(lm.lockedUntil) {
		lm.failCount = 0
		lm.lockedUntil = time.Time{}
	}
	lm.failCount++
	if lm.failCount >= LockoutThreshold {
		lm.lockedUntil = now.Add(LockoutDuration)
	}
}

// RecordFailure 记录一次失败。若累计失败 >= LockoutThreshold 则触发锁定。
func (lm *LockoutManager) RecordFailure() {
	lm.mu.Lock()
	defer lm.mu.Unlock()
	lm.recordFailureLocked()
}

// Guard 在单次持锁内完成「检查锁定 → 认证 → 据结果记一次失败/重置」的复合操作,
// 消除 Check 与 RecordFailure 之间的 TOCTOU 窗口,保证并发下阈值语义可靠。
//
// authenticate 在持锁状态下被调用:返回 (ok, err)。
//   - 若当前已锁定:不调用 authenticate,返回 locked=true。
//   - err != nil:不计为失败(视为系统故障,原样返回)。
//   - ok==true:重置计数。
//   - ok==false:记一次失败(可能触发锁定)。
//
// 注:authenticate 在持锁期间执行(含 argon2 重算),单管理员场景下登录本就串行,
// 这样换取的是阈值在并发下不可被绕过。
func (lm *LockoutManager) Guard(authenticate func() (bool, error)) (ok bool, locked bool, remaining time.Duration, err error) {
	lm.mu.Lock()
	defer lm.mu.Unlock()

	if l, rem := lm.checkLocked(); l {
		return false, true, rem, nil
	}

	ok, err = authenticate()
	if err != nil {
		return false, false, 0, err
	}
	if ok {
		lm.failCount = 0
		lm.lockedUntil = time.Time{}
		return true, false, 0, nil
	}
	lm.recordFailureLocked()
	return false, false, 0, nil
}

// Reset 成功登录后清零计数。
func (lm *LockoutManager) Reset() {
	lm.mu.Lock()
	defer lm.mu.Unlock()
	lm.failCount = 0
	lm.lockedUntil = time.Time{}
}
