// Package breaker 提供 mcp-gateway 系列网关共用的简单熔断器：
// 5 次连续失败 → OPEN 30s → HALF_OPEN 单次探测 → 成功回 CLOSED / 失败回 OPEN。
//
// 设计取舍（CLAUDE.md 第 2 / 8.2 / 8.4 章）：
//   - 核心状态机不依赖任何 IO / 配置 / metrics 包；可观测性通过 OnStateChange /
//     OnTrip 回调注入。consumer 在构造时传入闭包指向自己的 metrics（logs-mcp 用
//     resourceID + 包级 metrics 直调；monitor-mcp 用 wiring 时绑定的闭包），
//     core 不感知差异。
//   - 状态机所有可变字段由 mu 保护；回调一律在锁释放之后触发，避免回调反向
//     调用 Breaker 方法导致死锁。回调闭包内允许再次调用 Breaker（不会自死锁），
//     但语义上不建议依赖。
//   - 默认值：FailThreshold=5（10% 失败率下 5 连失概率 ~0.001%），Cooldown=30s
//     （与多数后端 restart / GC pause / failover 窗口同量级）。Options{} 零值即
//     这一对默认值。
//
// 使用示例（consumer 侧）：
//
//	b := breaker.New(breaker.Options{
//	    OnStateChange: func(s breaker.State) {
//	        metrics.SetBreakerState(resourceID, s.String())
//	    },
//	    OnTrip: func() { metrics.IncBreakerTrip(resourceID) },
//	})
//	if !b.Allow() {
//	    return ErrBreakerOpen
//	}
//	resp, err := doRequest(...)
//	if err != nil || resp.StatusCode >= 500 {
//	    b.OnFailure()
//	} else {
//	    b.OnSuccess()
//	}
//
// 调用方注意：4xx 与 ctx canceled 是业务问题或客户端主动取消，不应作为 OnFailure
// 输入；否则正常的鉴权失败会误触发熔断。
package breaker

import (
	"sync"
	"time"
)

// State 是熔断器状态。
// String() 返回稳定的 CamelCase 字面值（"Closed" / "Open" / "HalfOpen"），
// consumer metrics 标签绑定这些字符串，任何修改都是 break change。
type State int

const (
	// StateClosed 正常放行。
	StateClosed State = iota
	// StateOpen 短路拒绝；cooldown 到期后下一次 Allow 会迁到 HalfOpen。
	StateOpen
	// StateHalfOpen 单次探测：放行一个请求；其余请求拒绝，等待该探测的 OnSuccess / OnFailure。
	StateHalfOpen
)

// String 返回稳定的 metrics 标签字面值。修改此处会破坏 consumer 的 metrics 维度。
func (s State) String() string {
	switch s {
	case StateClosed:
		return "Closed"
	case StateOpen:
		return "Open"
	case StateHalfOpen:
		return "HalfOpen"
	default:
		return "Unknown"
	}
}

// 默认参数。Options 零值字段以这两个常量兜底。
const (
	defaultFailThreshold = 5
	defaultCooldown      = 30 * time.Second
)

// Options 是 New 的构造参数。零值字段使用合理默认（FailThreshold=5, Cooldown=30s）；
// OnStateChange / OnTrip 可空（nil-safe）。
type Options struct {
	// FailThreshold 触发 OPEN 的连续失败阈值。零值 → 5。
	FailThreshold int
	// Cooldown OPEN → HalfOpen 等待时长。零值 → 30s。
	Cooldown time.Duration
	// OnStateChange 在每次状态变化时触发（CLOSED↔OPEN↔HalfOpen，包括
	// OPEN→HalfOpen 自动转换）。回调在锁释放之后调用；nil 时跳过。
	OnStateChange func(State)
	// OnTrip 仅在进入 OPEN 时触发（CLOSED→OPEN 与 HalfOpen→OPEN）。用于
	// trips_total 计数。回调在锁释放之后调用；nil 时跳过。
	OnTrip func()
}

// Breaker 是状态机本体（并发安全）。
type Breaker struct {
	mu            sync.Mutex
	state         State
	failCount     int
	openedAt      time.Time
	failThreshold int
	cooldown      time.Duration
	onStateChange func(State)
	onTrip        func()
}

// New 构造一个 CLOSED 状态的 Breaker。Options 零值合法。
func New(opts Options) *Breaker {
	threshold := opts.FailThreshold
	if threshold <= 0 {
		threshold = defaultFailThreshold
	}
	cooldown := opts.Cooldown
	if cooldown <= 0 {
		cooldown = defaultCooldown
	}
	return &Breaker{
		state:         StateClosed,
		failThreshold: threshold,
		cooldown:      cooldown,
		onStateChange: opts.OnStateChange,
		onTrip:        opts.OnTrip,
	}
}

// Allow 判断是否放行本次请求；返回 false 表示熔断中应直接拒。
//   - CLOSED：放行。
//   - OPEN 且 cooldown 未到：拒绝。
//   - OPEN 且 cooldown 到了：转 HalfOpen 并放行一个探测请求（触发 OnStateChange）。
//   - HalfOpen：拒绝（只允许一个 in-flight 探测）。
func (b *Breaker) Allow() bool {
	b.mu.Lock()
	var (
		allowed     bool
		stateChange *State
	)
	switch b.state {
	case StateClosed:
		allowed = true
	case StateOpen:
		if time.Since(b.openedAt) < b.cooldown {
			allowed = false
		} else {
			b.state = StateHalfOpen
			s := b.state
			stateChange = &s
			allowed = true
		}
	case StateHalfOpen:
		allowed = false
	default:
		allowed = false
	}
	hook := b.onStateChange
	b.mu.Unlock()

	if stateChange != nil && hook != nil {
		hook(*stateChange)
	}
	return allowed
}

// OnSuccess 调用方在请求成功时调用。
//   - CLOSED：清零连续失败计数。
//   - HalfOpen：探测成功 → CLOSED，重置计数（触发 OnStateChange）。
//   - OPEN：无视（不该发生，但容忍）。
func (b *Breaker) OnSuccess() {
	b.mu.Lock()
	var stateChange *State
	switch b.state {
	case StateClosed:
		b.failCount = 0
	case StateHalfOpen:
		b.state = StateClosed
		b.failCount = 0
		s := b.state
		stateChange = &s
	}
	hook := b.onStateChange
	b.mu.Unlock()

	if stateChange != nil && hook != nil {
		hook(*stateChange)
	}
}

// OnFailure 调用方在请求失败时调用（仅限 5xx / net.OpError / read header timeout
// 等基础设施级错误；禁止用于 4xx / ctx canceled）。
//   - CLOSED：计数 +1；达到 FailThreshold → OPEN（触发 OnStateChange 与 OnTrip）。
//   - HalfOpen：任一失败 → 立刻 OPEN（保持保守，不漂回 CLOSED；触发 OnStateChange 与 OnTrip）。
//   - OPEN：无视。
func (b *Breaker) OnFailure() {
	b.mu.Lock()
	var (
		stateChange *State
		tripped     bool
	)
	switch b.state {
	case StateClosed:
		b.failCount++
		if b.failCount >= b.failThreshold {
			b.state = StateOpen
			b.openedAt = time.Now()
			s := b.state
			stateChange = &s
			tripped = true
		}
	case StateHalfOpen:
		b.state = StateOpen
		b.openedAt = time.Now()
		s := b.state
		stateChange = &s
		tripped = true
	}
	stateHook := b.onStateChange
	tripHook := b.onTrip
	b.mu.Unlock()

	if stateChange != nil && stateHook != nil {
		stateHook(*stateChange)
	}
	if tripped && tripHook != nil {
		tripHook()
	}
}

// State 返回当前状态（用于测试 / metrics 兜底读）。
func (b *Breaker) State() State {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.state
}
