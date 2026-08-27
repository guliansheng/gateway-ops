// Package progress 提供把"扫描子步骤进度"通过 context 透传到业务层、再由顶层 handler / scheduler 决定如何消费的能力。
//
// 调度任务（cron）走 context.Background() 时拿到的是 NopObserver，所有 Emit 都是 no-op；
// 手动 sync 的 HTTP handler 把 SSEObserver 塞进 ctx，业务每一步的 Emit 都会被 stream 出去。
package progress

import (
	"context"
	"time"
)

// Stage 标记当前事件属于哪个阶段，便于前端按 stage 给图标 / 颜色。
type Stage string

const (
	StageCaptcha Stage = "captcha"
	StageLogin   Stage = "login"
	StageSession Stage = "session"
	StageBalance Stage = "balance"
	StageRates   Stage = "rates"
	StageDone    Stage = "done"
	StageError   Stage = "error"
)

// Event 一条进度事件。OK 三态：nil = 进行中，true = 成功，false = 失败。
type Event struct {
	Stage   Stage     `json:"stage"`
	Message string    `json:"message"`
	OK      *bool     `json:"ok,omitempty"`
	Data    any       `json:"data,omitempty"`
	Time    time.Time `json:"time"`
}

// Observer 消费进度事件。
type Observer interface {
	Emit(Event)
}

// NopObserver 什么都不做，scheduler 默认走这条路径。
type NopObserver struct{}

func (NopObserver) Emit(Event) {}

type ctxKey struct{}

// WithObserver 把 observer 绑到 context。
func WithObserver(ctx context.Context, obs Observer) context.Context {
	if obs == nil {
		return ctx
	}
	return context.WithValue(ctx, ctxKey{}, obs)
}

// FromContext 从 context 取 observer；没有则返回 NopObserver。
func FromContext(ctx context.Context) Observer {
	if obs, ok := ctx.Value(ctxKey{}).(Observer); ok {
		return obs
	}
	return NopObserver{}
}

// Start 发送一条"进行中"事件。
func Start(ctx context.Context, s Stage, msg string) {
	FromContext(ctx).Emit(Event{Stage: s, Message: msg, Time: time.Now()})
}

// OK 发送一条"成功"事件，可附带 data。
func OK(ctx context.Context, s Stage, msg string, data ...any) {
	ok := true
	ev := Event{Stage: s, Message: msg, OK: &ok, Time: time.Now()}
	if len(data) > 0 {
		ev.Data = data[0]
	}
	FromContext(ctx).Emit(ev)
}

// Fail 发送一条"失败"事件。
func Fail(ctx context.Context, s Stage, msg string) {
	no := false
	FromContext(ctx).Emit(Event{Stage: s, Message: msg, OK: &no, Time: time.Now()})
}
