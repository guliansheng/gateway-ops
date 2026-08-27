package notify

import (
	"fmt"
	"strings"

	"github.com/guliansheng/gateway-ops/internal/storage"
)

// notificationTitle gives every human-facing notifier the same visual title.
// The legacy [GatewayOps] prefix is replaced by the event icon to avoid a noisy header.
func notificationTitle(msg Message) string {
	subject := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(msg.Subject), "[GatewayOps]"))
	if subject == "" {
		subject = "GatewayOps 通知"
	}
	return notificationIcon(msg) + " " + subject
}

func notificationIcon(msg Message) string {
	if strings.Contains(msg.Subject, "测试") {
		return "🧪"
	}
	switch msg.Event {
	case storage.EventBalanceLow:
		return "💸"
	case storage.EventRateChanged:
		return "📊"
	case storage.EventLoginFailed:
		return "🔐"
	case storage.EventCaptchaFailed:
		return "🧩"
	case storage.EventMonitorFailed:
		return "⚠️"
	default:
		return "🔔"
	}
}

func notificationBody(msg Message) string {
	switch msg.Event {
	case storage.EventLoginFailed:
		return "渠道登录失败，请检查账号凭证、授权状态或网络连接。"
	case storage.EventCaptchaFailed:
		return "验证码处理失败，请检查验证码服务配置或上游服务。"
	case storage.EventMonitorFailed:
		return "渠道数据采集失败，请检查登录状态、渠道配置或上游服务。"
	}
	return strings.TrimSpace(msg.Body)
}

// sanitizeMessage prevents implementation or upstream details from being sent
// in failure notifications. The original error remains available in server
// logs and monitor logs for diagnosis.
func sanitizeMessage(msg Message) Message {
	if msg.Event == storage.EventLoginFailed || msg.Event == storage.EventCaptchaFailed || msg.Event == storage.EventMonitorFailed {
		msg.Body = notificationBody(msg)
	}
	return msg
}

func notificationText(msg Message) string {
	title := notificationTitle(msg)
	body := notificationBody(msg)
	if body == "" {
		return title
	}
	return fmt.Sprintf("%s\n\n%s", title, body)
}

func notificationDirection(oldValue, newValue float64) string {
	switch {
	case newValue > oldValue:
		return "⬆️"
	case newValue < oldValue:
		return "⬇️"
	default:
		return "➡️"
	}
}
