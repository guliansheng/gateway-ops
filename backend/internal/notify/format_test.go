package notify

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/guliansheng/gateway-ops/internal/storage"
)

func TestNotificationTitleUsesEventIconAndRemovesLegacyPrefix(t *testing.T) {
	got := notificationTitle(Message{
		Event:   storage.EventBalanceLow,
		Subject: "[GatewayOps] 渠道余额低于阈值",
	})
	if got != "💸 渠道余额低于阈值" {
		t.Fatalf("notificationTitle = %q", got)
	}
}

func TestNotificationTextKeepsCommonLayout(t *testing.T) {
	got := notificationText(Message{
		Event:   storage.EventRateChanged,
		Subject: "倍率变化提醒",
		Body:    "渠道：demo\n变更：1.0 ⬆️ 1.2",
	})
	want := "📊 倍率变化提醒\n\n渠道：demo\n变更：1.0 ⬆️ 1.2"
	if got != want {
		t.Fatalf("notificationText = %q, want %q", got, want)
	}
}

func TestNotificationBodySummarizesFailureEvents(t *testing.T) {
	raw := "dial tcp 10.0.0.8:443: connection refused"
	for _, tc := range []struct {
		event storage.NotificationEvent
		want  string
	}{
		{event: storage.EventLoginFailed, want: "渠道登录失败，请检查账号凭证、授权状态或网络连接。"},
		{event: storage.EventCaptchaFailed, want: "验证码处理失败，请检查验证码服务配置或上游服务。"},
		{event: storage.EventMonitorFailed, want: "渠道数据采集失败，请检查登录状态、渠道配置或上游服务。"},
	} {
		msg := sanitizeMessage(Message{Event: tc.event, Body: raw})
		if msg.Body != tc.want {
			t.Errorf("sanitizeMessage(%s).Body = %q, want %q", tc.event, msg.Body, tc.want)
		}
		if strings.Contains(msg.Body, raw) {
			t.Errorf("sanitizeMessage(%s).Body leaked raw error: %q", tc.event, msg.Body)
		}
	}
}

func TestNotificationBodyKeepsNormalEvents(t *testing.T) {
	msg := Message{Event: storage.EventRateChanged, Body: "倍率已从 1.0 调整为 1.2"}
	if got := notificationBody(msg); got != msg.Body {
		t.Fatalf("notificationBody() = %q, want %q", got, msg.Body)
	}
}

func TestTelegramRequestErrorNeverIncludesBotToken(t *testing.T) {
	token := "123456789:test-token-for-redaction"
	raw := fmt.Errorf("Post %s: context deadline exceeded", "https://api.telegram.org/bot"+token+"/sendMessage")
	got := telegramRequestError(raw)
	if strings.Contains(got.Error(), token) || strings.Contains(got.Error(), "api.telegram.org") {
		t.Fatalf("telegramRequestError leaked endpoint or token: %q", got)
	}
	if got.Error() != "telegram request failed" {
		t.Fatalf("telegramRequestError() = %q", got)
	}
}

func TestTelegramRequestErrorClassifiesTimeoutAndCancellation(t *testing.T) {
	if got := telegramRequestError(context.DeadlineExceeded); got.Error() != "telegram request timed out" {
		t.Fatalf("timeout error = %q", got)
	}
	if got := telegramRequestError(context.Canceled); got.Error() != "telegram request canceled" {
		t.Fatalf("canceled error = %q", got)
	}
	wrapped := fmt.Errorf("wrapped: %w", context.DeadlineExceeded)
	if got := telegramRequestError(wrapped); got.Error() != "telegram request timed out" {
		t.Fatalf("wrapped timeout error = %q", got)
	}
}

func TestRedactTelegramToken(t *testing.T) {
	token := "123456:secret"
	got := redactTelegramToken("request /bot"+token+"/sendMessage failed", token)
	if strings.Contains(got, token) || !strings.Contains(got, "[REDACTED]") {
		t.Fatalf("redactTelegramToken() = %q", got)
	}
}

func TestNotificationDirection(t *testing.T) {
	for _, tc := range []struct {
		oldValue float64
		newValue float64
		want     string
	}{
		{oldValue: 1, newValue: 2, want: "⬆️"},
		{oldValue: 2, newValue: 1, want: "⬇️"},
		{oldValue: 1, newValue: 1, want: "➡️"},
	} {
		if got := notificationDirection(tc.oldValue, tc.newValue); got != tc.want {
			t.Errorf("notificationDirection(%v, %v) = %q, want %q", tc.oldValue, tc.newValue, got, tc.want)
		}
	}
}
