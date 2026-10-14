package auth

import (
	"strings"
	"sync"
	"time"
)

// GuardConfig 后台登录防爆破配置。
//
// 语义：同一个密钥（来源 IP 或登录账号）在 Window 内累计失败达到阈值就锁定该密钥；
// 首次锁定 BaseLock，之后每再触发一次翻倍，直到 MaxLock 封顶。登录成功即清零。
//
// 计数只存在进程内存里，进程重启即清零。当前是单实例部署，这样做够用且不引入
// 外部依赖；将来若横向扩容，需要把 Guard 换成 Redis / 数据库实现。
type GuardConfig struct {
	// Window 失败计数的滑动窗口。
	Window time.Duration
	// MaxFailuresPerIP 窗口内单个来源 IP 允许的失败次数；<=0 取默认值 5。
	MaxFailuresPerIP int
	// MaxFailuresPerAccount 窗口内单个账号允许的失败次数；<=0 取默认值 20，
	// 设为负数表示不做账号维度锁定（只锁 IP，避免攻击者故意输错密码把管理员锁在门外）。
	MaxFailuresPerAccount int
	// BaseLock 首次触发锁定时锁多久；<=0 取默认值 10 分钟。
	BaseLock time.Duration
	// MaxLock 指数退避后的封顶锁定时长；<=0 取默认值 24 小时。
	MaxLock time.Duration
	// MaxKeys 内存里最多保留多少个密钥的失败状态，超过后清理已过期条目。
	MaxKeys int
	// OnLockout 在"某个密钥刚被锁定"时回调，用于写审计日志 / 发告警。
	// 回调在 Fail 返回前同步调用（不在 Guard 锁内），实现里不要做阻塞 IO，
	// 需要发网络请求就自己开 goroutine。
	OnLockout func(LockoutEvent)
}

// LockoutEvent 描述一次刚发生的锁定。
type LockoutEvent struct {
	// Scope 为 "ip" 或 "account"。
	Scope string
	// Key 是触发锁定的来源 IP 或账号。
	Key string
	// Failures 是窗口内累计到触发的失败次数。
	Failures int
	// LockedUntil 是本次锁定的截止时间。
	LockedUntil time.Time
	// LockDuration 是本次锁定的时长。
	LockDuration time.Duration
}

// DefaultGuardConfig 返回默认参数：10 分钟窗口内单 IP 允许 5 次失败、
// 单账号允许 20 次，锁 10 分钟起、最长 24 小时。
func DefaultGuardConfig() GuardConfig {
	return GuardConfig{
		Window:                10 * time.Minute,
		MaxFailuresPerIP:      5,
		MaxFailuresPerAccount: 20,
		BaseLock:              10 * time.Minute,
		MaxLock:               24 * time.Hour,
		MaxKeys:               8192,
	}
}

func (c GuardConfig) withDefaults() GuardConfig {
	def := DefaultGuardConfig()
	if c.Window <= 0 {
		c.Window = def.Window
	}
	if c.MaxFailuresPerIP <= 0 {
		c.MaxFailuresPerIP = def.MaxFailuresPerIP
	}
	if c.MaxFailuresPerAccount == 0 {
		c.MaxFailuresPerAccount = def.MaxFailuresPerAccount
	}
	if c.BaseLock <= 0 {
		c.BaseLock = def.BaseLock
	}
	if c.MaxLock <= 0 {
		c.MaxLock = def.MaxLock
	}
	if c.MaxLock < c.BaseLock {
		c.MaxLock = c.BaseLock
	}
	if c.MaxKeys <= 0 {
		c.MaxKeys = def.MaxKeys
	}
	return c
}

// ErrLoginLocked 表示该 IP 或账号已被登录失败限流锁定。
type ErrLoginLocked struct {
	Scope      string
	RetryAfter time.Duration
}

func (e *ErrLoginLocked) Error() string {
	return "too many failed login attempts, retry later"
}

type guardBucket struct {
	failures    []time.Time
	lockedUntil time.Time
	level       int
}

type guardKey struct {
	id    string
	scope string
	label string
	limit int
}

// Guard 是进程内的登录失败限流器，同时跟踪来源 IP 与账号两个维度。
// 允许把 nil *Guard 当作"不限流"使用。
type Guard struct {
	cfg     GuardConfig
	now     func() time.Time
	mu      sync.Mutex
	buckets map[string]*guardBucket
}

// NewGuard 构造限流器；零值字段回退到 DefaultGuardConfig。
func NewGuard(cfg GuardConfig) *Guard {
	return &Guard{
		cfg:     cfg.withDefaults(),
		now:     time.Now,
		buckets: make(map[string]*guardBucket),
	}
}

// NewDefaultGuard 用默认参数构造限流器。
func NewDefaultGuard() *Guard { return NewGuard(GuardConfig{}) }

// Allow 判断这次登录尝试是否放行。被锁定时返回剩余锁定时间与触发锁定的维度。
func (g *Guard) Allow(ip, username string) (bool, time.Duration, string) {
	if g == nil {
		return true, 0, ""
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	now := g.now()
	var (
		retry time.Duration
		scope string
	)
	for _, k := range g.keys(ip, username) {
		b := g.buckets[k.id]
		if b == nil || !now.Before(b.lockedUntil) {
			continue
		}
		if d := b.lockedUntil.Sub(now); d > retry {
			retry, scope = d, k.scope
		}
	}
	if retry > 0 {
		return false, retry, scope
	}
	return true, 0, ""
}

// Fail 记录一次登录失败，返回本次触发的锁定事件（未触发时 ok=false）。
//
// 两个维度都累计；账号维度可通过 MaxFailuresPerAccount < 0 关闭。
func (g *Guard) Fail(ip, username string) (LockoutEvent, bool) {
	if g == nil {
		return LockoutEvent{}, false
	}
	now := g.now()
	g.mu.Lock()
	var (
		ev     LockoutEvent
		locked bool
	)
	for _, k := range g.keys(ip, username) {
		if k.limit <= 0 {
			continue
		}
		e, hit := g.recordLocked(k, now)
		if hit {
			ev, locked = e, true
		}
	}
	g.sweepLocked(now)
	g.mu.Unlock()

	// 回调放在锁外执行，避免实现方回调 Guard 造成死锁。
	if locked && g.cfg.OnLockout != nil {
		g.cfg.OnLockout(ev)
	}
	return ev, locked
}

// Success 把这次登录涉及的 IP 与账号失败计数清零。
func (g *Guard) Success(ip, username string) {
	if g == nil {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	for _, k := range g.keys(ip, username) {
		delete(g.buckets, k.id)
	}
}

// keys 返回本次尝试涉及的限流维度；IP 或账号为空时跳过该维度。
func (g *Guard) keys(ip, username string) []guardKey {
	out := make([]guardKey, 0, 2)
	if ip = strings.TrimSpace(ip); ip != "" {
		out = append(out, guardKey{id: "ip:" + ip, scope: "ip", label: ip, limit: g.cfg.MaxFailuresPerIP})
	}
	if u := normalizeUsername(username); u != "" {
		out = append(out, guardKey{id: "account:" + u, scope: "account", label: u, limit: g.cfg.MaxFailuresPerAccount})
	}
	return out
}

// recordLocked 在持有 g.mu 的前提下累计一次失败，必要时锁定。
func (g *Guard) recordLocked(k guardKey, now time.Time) (LockoutEvent, bool) {
	b := g.buckets[k.id]
	if b == nil {
		b = &guardBucket{}
		g.buckets[k.id] = b
	}
	b.failures = pruneFailures(b.failures, now.Add(-g.cfg.Window))
	b.failures = append(b.failures, now)
	if len(b.failures) < k.limit {
		return LockoutEvent{}, false
	}
	// 达到阈值：清空窗口计数，按 level 指数退避锁定。
	failures := len(b.failures)
	b.failures = b.failures[:0]
	b.level++
	lock := g.cfg.BaseLock
	for i := 1; i < b.level && lock < g.cfg.MaxLock; i++ {
		lock *= 2
	}
	if lock > g.cfg.MaxLock {
		lock = g.cfg.MaxLock
	}
	b.lockedUntil = now.Add(lock)
	return LockoutEvent{
		Scope:        k.scope,
		Key:          k.label,
		Failures:     failures,
		LockedUntil:  b.lockedUntil,
		LockDuration: lock,
	}, true
}

// pruneFailures 丢弃 cutoff 之前的失败记录，原地复用底层数组。
func pruneFailures(in []time.Time, cutoff time.Time) []time.Time {
	out := in[:0]
	for _, t := range in {
		if t.After(cutoff) {
			out = append(out, t)
		}
	}
	return out
}

// sweepLocked 清理已过期且无失败记录的条目，避免 map 无限增长。调用方需持有 g.mu。
func (g *Guard) sweepLocked(now time.Time) {
	if len(g.buckets) <= g.cfg.MaxKeys {
		return
	}
	cutoff := now.Add(-g.cfg.Window)
	for id, b := range g.buckets {
		if now.Before(b.lockedUntil) {
			continue
		}
		if len(pruneFailures(b.failures, cutoff)) == 0 {
			delete(g.buckets, id)
		}
	}
}

// normalizeUsername 统一大小写与空白，避免用 Admin/admin 绕过账号维度计数。
func normalizeUsername(u string) string {
	return strings.ToLower(strings.TrimSpace(u))
}
