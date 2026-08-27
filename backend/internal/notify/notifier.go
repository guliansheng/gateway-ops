// Package notify 通知发送统一接口与 Dispatcher。
//
// 第一版至少支持 Telegram Bot；同时实现 Webhook / Email / 企业微信 / 钉钉 / 飞书。
// 每种类型走 ConfigCipher 里的 JSON 字段。
package notify

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/guliansheng/gateway-ops/internal/storage"
)

// Message 待发送的通知消息。
//
// ChannelID / ModelName 用于 Dispatcher 做订阅过滤：
//   - ChannelID = 来源上游 ID，0 表示系统消息或测试发送，跳过订阅过滤
//   - ModelName = 仅 rate_changed 事件填写当前分组名
type Message struct {
	Event     storage.NotificationEvent
	ChannelID uint
	ModelName string
	Subject   string
	Body      string
	Extra     map[string]any
}

// Notifier 通知渠道抽象。
type Notifier interface {
	Send(ctx context.Context, msg Message) error
	Type() storage.NotificationChannelType
}

// Factory 用解密后的 JSON 配置构造一个 Notifier。
type Factory func(rawConfig string) (Notifier, error)

var (
	mu       sync.RWMutex
	registry = map[storage.NotificationChannelType]Factory{}
)

// Register 由各 notifier 实现在 init() 中注册。
func Register(t storage.NotificationChannelType, f Factory) {
	mu.Lock()
	defer mu.Unlock()
	registry[t] = f
}

// Build 根据存储模型 + 已解密配置构造 Notifier。
func Build(c *storage.NotificationChannel, decryptedConfig string) (Notifier, error) {
	if c == nil {
		return nil, errors.New("notification channel is nil")
	}
	mu.RLock()
	f, ok := registry[c.Type]
	mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("unknown notifier type: %s", c.Type)
	}
	return f(decryptedConfig)
}
