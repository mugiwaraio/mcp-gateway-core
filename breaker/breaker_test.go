package breaker

import (
	"sync"
	"testing"
	"time"
)

// newTestBreaker 构造一个 cooldown=50ms 的 breaker，加速测试。
// failThreshold 走默认值（5），与生产一致。
func newTestBreaker() *Breaker {
	return New(Options{
		FailThreshold: 5,
		Cooldown:      50 * time.Millisecond,
	})
}

// --- 状态机用例（移植自 monitor-mcp 既有覆盖）---

func TestBreaker_StaysClosedBelowThreshold(t *testing.T) {
	b := newTestBreaker()
	for i := 0; i < 4; i++ {
		if !b.Allow() {
			t.Fatalf("expected Allow() = true at fail #%d", i)
		}
		b.OnFailure()
	}
	if !b.Allow() {
		t.Errorf("expected breaker still CLOSED after 4 fails")
	}
	if b.State() != StateClosed {
		t.Errorf("expected StateClosed, got %v", b.State())
	}
}

func TestBreaker_OpensAtThreshold(t *testing.T) {
	b := newTestBreaker()
	for i := 0; i < 5; i++ {
		if !b.Allow() {
			t.Fatalf("unexpected reject at fail #%d", i)
		}
		b.OnFailure()
	}
	if b.State() != StateOpen {
		t.Errorf("expected StateOpen after 5 fails, got %v", b.State())
	}
	if b.Allow() {
		t.Errorf("expected Allow() = false in OPEN state")
	}
}

func TestBreaker_RecoversAfterCooldown(t *testing.T) {
	b := newTestBreaker()
	for i := 0; i < 5; i++ {
		b.Allow()
		b.OnFailure()
	}
	if b.State() != StateOpen {
		t.Fatalf("precondition: expected StateOpen")
	}
	if b.Allow() {
		t.Errorf("expected reject before cooldown elapses")
	}
	time.Sleep(60 * time.Millisecond)
	if !b.Allow() {
		t.Errorf("expected Allow() = true after cooldown")
	}
	if b.State() != StateHalfOpen {
		t.Errorf("expected StateHalfOpen after probe admit, got %v", b.State())
	}
	if b.Allow() {
		t.Errorf("expected HalfOpen to only admit a single probe")
	}
	b.OnSuccess()
	if b.State() != StateClosed {
		t.Errorf("expected StateClosed after HalfOpen success, got %v", b.State())
	}
	if !b.Allow() {
		t.Errorf("expected Allow() = true after recovery")
	}
}

func TestBreaker_HalfOpenFailureReopens(t *testing.T) {
	b := newTestBreaker()
	for i := 0; i < 5; i++ {
		b.Allow()
		b.OnFailure()
	}
	time.Sleep(60 * time.Millisecond)
	if !b.Allow() {
		t.Fatalf("precondition: expected Allow() = true after cooldown")
	}
	if b.State() != StateHalfOpen {
		t.Fatalf("precondition: expected StateHalfOpen")
	}
	b.OnFailure()
	if b.State() != StateOpen {
		t.Errorf("expected StateOpen after HalfOpen failure, got %v", b.State())
	}
	if b.Allow() {
		t.Errorf("expected Allow() = false (re-entered OPEN)")
	}
}

func TestBreaker_SuccessResetsFailCount(t *testing.T) {
	b := newTestBreaker()
	for i := 0; i < 4; i++ {
		b.Allow()
		b.OnFailure()
	}
	b.OnSuccess()
	// 再来 4 次失败仍不 OPEN（计数被清零）
	for i := 0; i < 4; i++ {
		b.Allow()
		b.OnFailure()
	}
	if b.State() != StateClosed {
		t.Errorf("expected StateClosed (counter was reset), got %v", b.State())
	}
}

// --- 回调用例 ---

func TestBreaker_OnTripCalledOnceOnEachOpen(t *testing.T) {
	trips := 0
	b := New(Options{
		FailThreshold: 5,
		Cooldown:      50 * time.Millisecond,
		OnTrip:        func() { trips++ },
	})
	// CLOSED → OPEN
	for i := 0; i < 5; i++ {
		b.Allow()
		b.OnFailure()
	}
	if trips != 1 {
		t.Errorf("after CLOSED→OPEN: trips=%d, want 1", trips)
	}
	// cooldown 到 → HalfOpen → fail → OPEN：再触发一次
	time.Sleep(60 * time.Millisecond)
	b.Allow() // → HalfOpen
	b.OnFailure()
	if trips != 2 {
		t.Errorf("after HalfOpen→OPEN: trips=%d, want 2", trips)
	}
}

func TestBreaker_OnStateChangeCalledOnAllTransitions(t *testing.T) {
	var (
		mu     sync.Mutex
		states []State
	)
	push := func(s State) { mu.Lock(); states = append(states, s); mu.Unlock() }
	b := New(Options{
		FailThreshold: 5,
		Cooldown:      50 * time.Millisecond,
		OnStateChange: push,
	})
	// CLOSED → OPEN
	for i := 0; i < 5; i++ {
		b.Allow()
		b.OnFailure()
	}
	// OPEN → HalfOpen
	time.Sleep(60 * time.Millisecond)
	b.Allow()
	// HalfOpen → CLOSED
	b.OnSuccess()

	mu.Lock()
	defer mu.Unlock()
	if len(states) != 3 {
		t.Fatalf("transitions captured=%v, want 3 entries", states)
	}
	if states[0] != StateOpen || states[1] != StateHalfOpen || states[2] != StateClosed {
		t.Errorf("transition sequence=%v, want [Open HalfOpen Closed]", states)
	}
}

func TestBreaker_NilCallbacksSafe(t *testing.T) {
	// Options{} 默认零值（OnStateChange / OnTrip 均 nil），完整跑一遍状态机不应 panic。
	b := New(Options{Cooldown: 50 * time.Millisecond})
	for i := 0; i < 5; i++ {
		b.Allow()
		b.OnFailure()
	}
	time.Sleep(60 * time.Millisecond)
	b.Allow()
	b.OnSuccess()
	if b.State() != StateClosed {
		t.Errorf("expected StateClosed after recovery with nil callbacks, got %v", b.State())
	}
}

// --- 默认值 / 配置 ---

func TestBreaker_DefaultsAppliedWhenZero(t *testing.T) {
	b := New(Options{})
	if b.failThreshold != 5 {
		t.Errorf("FailThreshold default=%d, want 5", b.failThreshold)
	}
	if b.cooldown != 30*time.Second {
		t.Errorf("Cooldown default=%v, want 30s", b.cooldown)
	}
}

func TestBreaker_CustomFailThreshold(t *testing.T) {
	b := New(Options{FailThreshold: 3, Cooldown: 50 * time.Millisecond})
	for i := 0; i < 3; i++ {
		b.Allow()
		b.OnFailure()
	}
	if b.State() != StateOpen {
		t.Errorf("expected StateOpen after 3 fails with FailThreshold=3, got %v", b.State())
	}
}

// --- 死锁防御：回调在锁外触发 ---

func TestBreaker_CallbacksFireOutsideLock(t *testing.T) {
	// 关键测试：若回调在锁内触发，Allow() / State() 反向调用会死锁。
	// 在 OnStateChange 内反向调 b.Allow()、b.State()，若 1s 没回则视为死锁失败。
	done := make(chan struct{})
	var b *Breaker
	b = New(Options{
		FailThreshold: 5,
		Cooldown:      50 * time.Millisecond,
		OnStateChange: func(_ State) {
			_ = b.State()
			_ = b.Allow()
		},
		OnTrip: func() {
			_ = b.State()
		},
	})
	go func() {
		for i := 0; i < 5; i++ {
			b.Allow()
			b.OnFailure()
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(1 * time.Second):
		t.Fatalf("deadlock: callback appears to be holding the breaker mutex")
	}
}

// --- 并发安全 ---

func TestBreaker_ConcurrentSafe(t *testing.T) {
	b := newTestBreaker()
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(seed int) {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				if b.Allow() {
					if (seed+j)%3 == 0 {
						b.OnFailure()
					} else {
						b.OnSuccess()
					}
				}
			}
		}(i)
	}
	wg.Wait()
	// 无 race 即视为通过；状态可任意（取决于调度），仅断言不 panic。
	_ = b.State()
}

// --- State.String() 稳定性（绑定 metrics 标签）---

func TestState_StringStable(t *testing.T) {
	cases := []struct {
		s    State
		want string
	}{
		{StateClosed, "Closed"},
		{StateOpen, "Open"},
		{StateHalfOpen, "HalfOpen"},
	}
	for _, c := range cases {
		if got := c.s.String(); got != c.want {
			t.Errorf("State(%d).String()=%q, want %q (consumer metrics labels depend on this exact value)", c.s, got, c.want)
		}
	}
}
