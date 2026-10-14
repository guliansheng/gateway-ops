package auth

import (
	"sync"
	"testing"
	"time"
)

// newTestGuard 返回一个可手动推进时钟的 Guard。
func newTestGuard(cfg GuardConfig) (*Guard, func(time.Duration)) {
	g := NewGuard(cfg)
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	var mu sync.Mutex
	g.now = func() time.Time {
		mu.Lock()
		defer mu.Unlock()
		return now
	}
	advance := func(d time.Duration) {
		mu.Lock()
		now = now.Add(d)
		mu.Unlock()
	}
	return g, advance
}

func TestGuardLocksAfterThreshold(t *testing.T) {
	g, _ := newTestGuard(GuardConfig{Window: time.Minute, MaxFailuresPerIP: 3, BaseLock: time.Minute, MaxLock: time.Hour})

	for i := 0; i < 2; i++ {
		if _, hit := g.Fail("1.2.3.4", "admin"); hit {
			t.Fatalf("第 %d 次失败不应触发锁定", i+1)
		}
	}
	if ok, _, _ := g.Allow("1.2.3.4", "admin"); !ok {
		t.Fatal("阈值前不应锁定")
	}
	ev, hit := g.Fail("1.2.3.4", "admin")
	if !hit {
		t.Fatal("第 3 次失败应触发锁定")
	}
	if ev.Scope != "ip" || ev.Key != "1.2.3.4" {
		t.Fatalf("锁定事件维度不对: %+v", ev)
	}
	if ok, retry, scope := g.Allow("1.2.3.4", "admin"); ok || scope != "ip" || retry <= 0 {
		t.Fatalf("锁定后应拒绝: ok=%v scope=%s retry=%v", ok, scope, retry)
	}
	// 其它 IP 不受影响。
	if ok, _, _ := g.Allow("5.6.7.8", "admin"); !ok {
		t.Fatal("其它 IP 不应被锁定")
	}
}

func TestGuardLockEscalatesAndExpires(t *testing.T) {
	g, advance := newTestGuard(GuardConfig{Window: time.Minute, MaxFailuresPerIP: 1, BaseLock: time.Minute, MaxLock: 8 * time.Minute})

	_, hit := g.Fail("1.2.3.4", "admin")
	if !hit {
		t.Fatal("阈值 1 时首次失败即锁定")
	}
	// 第一次锁 1 分钟。
	if ok, retry, _ := g.Allow("1.2.3.4", "admin"); ok || retry != time.Minute {
		t.Fatalf("首次锁定时长应为 1m: ok=%v retry=%v", ok, retry)
	}
	advance(time.Minute + time.Second)
	if ok, _, _ := g.Allow("1.2.3.4", "admin"); !ok {
		t.Fatal("锁过期后应放行")
	}

	// 第二次触发：锁 2 分钟。
	if _, hit := g.Fail("1.2.3.4", "admin"); !hit {
		t.Fatal("再次触发应锁定")
	}
	if ok, retry, _ := g.Allow("1.2.3.4", "admin"); ok || retry != 2*time.Minute {
		t.Fatalf("第二次锁定时长应为 2m: ok=%v retry=%v", ok, retry)
	}

	// 连续触发直到封顶 8 分钟。
	for i := 0; i < 5; i++ {
		advance(9 * time.Minute)
		if _, hit := g.Fail("1.2.3.4", "admin"); !hit {
			t.Fatal("再次触发应锁定")
		}
	}
	if ok, retry, _ := g.Allow("1.2.3.4", "admin"); ok || retry != 8*time.Minute {
		t.Fatalf("封顶应为 8m: ok=%v retry=%v", ok, retry)
	}
}

func TestGuardSuccessClearsState(t *testing.T) {
	g, _ := newTestGuard(GuardConfig{Window: time.Minute, MaxFailuresPerIP: 2, BaseLock: time.Minute, MaxLock: time.Hour})

	g.Fail("1.2.3.4", "admin")
	g.Success("1.2.3.4", "admin")
	if _, hit := g.Fail("1.2.3.4", "admin"); hit {
		t.Fatal("成功登录后计数应清零")
	}
}

func TestGuardWindowExpiresFailures(t *testing.T) {
	g, advance := newTestGuard(GuardConfig{Window: time.Minute, MaxFailuresPerIP: 2, BaseLock: time.Minute, MaxLock: time.Hour})

	g.Fail("1.2.3.4", "admin")
	advance(2 * time.Minute)
	if _, hit := g.Fail("1.2.3.4", "admin"); hit {
		t.Fatal("窗口外的旧失败不应计入")
	}
}

func TestGuardAccountDimensionAndDisable(t *testing.T) {
	g, _ := newTestGuard(GuardConfig{Window: time.Minute, MaxFailuresPerIP: 100, MaxFailuresPerAccount: 2, BaseLock: time.Minute, MaxLock: time.Hour})

	g.Fail("1.1.1.1", "admin")
	ev, hit := g.Fail("2.2.2.2", "Admin ") // 大小写/空白归一化后是同一账号
	if !hit || ev.Scope != "account" {
		t.Fatalf("账号维度应触发锁定: hit=%v ev=%+v", hit, ev)
	}
	// 其它账号不受影响。
	if ok, _, _ := g.Allow("3.3.3.3", "someone"); !ok {
		t.Fatal("其它账号不应被锁定")
	}

	// 负数表示关闭账号维度，只锁 IP。
	off, _ := newTestGuard(GuardConfig{Window: time.Minute, MaxFailuresPerIP: 100, MaxFailuresPerAccount: -1, BaseLock: time.Minute, MaxLock: time.Hour})
	for i := 0; i < 10; i++ {
		if _, hit := off.Fail("1.1.1.1", "admin"); hit {
			t.Fatal("账号维度关闭时不应锁定")
		}
	}
}

func TestGuardNilIsNoop(t *testing.T) {
	var g *Guard
	if ok, _, _ := g.Allow("1.2.3.4", "admin"); !ok {
		t.Fatal("nil Guard 应放行")
	}
	if _, hit := g.Fail("1.2.3.4", "admin"); hit {
		t.Fatal("nil Guard 不应锁定")
	}
	g.Success("1.2.3.4", "admin")
}

func TestGuardOnLockoutCallback(t *testing.T) {
	var got []LockoutEvent
	g, _ := newTestGuard(GuardConfig{Window: time.Minute, MaxFailuresPerIP: 1, BaseLock: time.Minute, MaxLock: time.Hour})
	g.cfg.OnLockout = func(ev LockoutEvent) { got = append(got, ev) }

	g.Fail("1.2.3.4", "admin")
	if len(got) != 1 {
		t.Fatalf("锁定回调应触发一次，实际 %d", len(got))
	}
	if got[0].Scope != "ip" || got[0].Key != "1.2.3.4" || got[0].Failures != 1 {
		t.Fatalf("回调内容不对: %+v", got[0])
	}
}

func TestServiceLoginRateLimited(t *testing.T) {
	guard, _ := newTestGuard(GuardConfig{Window: time.Minute, MaxFailuresPerIP: 2, MaxFailuresPerAccount: 100, BaseLock: time.Minute, MaxLock: time.Hour})
	svc, err := New("admin", "correct-horse", "0123456789abcdef0123456789abcdef", time.Hour, guard)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if _, _, err := svc.Login("9.9.9.9", "admin", "wrong"); err != ErrInvalidCredentials {
		t.Fatalf("期望凭证错误，实际 %v", err)
	}
	if _, _, err := svc.Login("9.9.9.9", "admin", "wrong"); err != ErrInvalidCredentials {
		t.Fatalf("期望凭证错误，实际 %v", err)
	}
	_, _, err = svc.Login("9.9.9.9", "admin", "correct-horse")
	var locked *ErrLoginLocked
	if !asLocked(err, &locked) {
		t.Fatalf("达到阈值后应返回锁定错误，实际 %v", err)
	}
	if locked.Scope != "ip" || locked.RetryAfter <= 0 {
		t.Fatalf("锁定信息不对: %+v", locked)
	}
}

func asLocked(err error, target **ErrLoginLocked) bool {
	e, ok := err.(*ErrLoginLocked)
	if ok {
		*target = e
	}
	return ok
}
