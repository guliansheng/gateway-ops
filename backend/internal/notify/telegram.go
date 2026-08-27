package notify

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"net"
	"strings"
	"time"

	"github.com/go-resty/resty/v2"
	"github.com/guliansheng/gateway-ops/internal/storage"
)

func init() {
	Register(storage.NotifyTelegram, func(raw string) (Notifier, error) { return newTelegram(raw) })
}

type telegramConfig struct {
	BotToken string `json:"bot_token"`
	ChatID   string `json:"chat_id"`
}

type telegram struct {
	cfg  telegramConfig
	http *resty.Client
}

func newTelegram(raw string) (*telegram, error) {
	var cfg telegramConfig
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		return nil, err
	}
	if cfg.BotToken == "" || cfg.ChatID == "" {
		return nil, errors.New("telegram bot_token and chat_id are required")
	}
	return &telegram{cfg: cfg, http: resty.New().SetTimeout(15 * time.Second)}, nil
}

func (t *telegram) Type() storage.NotificationChannelType { return storage.NotifyTelegram }

func (t *telegram) Send(ctx context.Context, msg Message) error {
	url := fmt.Sprintf("https://api.telegram.org/bot%s/sendMessage", t.cfg.BotToken)
	text := fmt.Sprintf("<b>%s</b>\n\n%s", html.EscapeString(notificationTitle(msg)), html.EscapeString(notificationBody(msg)))
	resp, err := t.http.R().
		SetContext(ctx).
		SetBody(map[string]any{
			"chat_id":    t.cfg.ChatID,
			"text":       text,
			"parse_mode": "HTML",
		}).
		Post(url)
	if err != nil {
		return telegramRequestError(err)
	}
	if resp.IsError() {
		detail := redactTelegramToken(strings.TrimSpace(resp.String()), t.cfg.BotToken)
		if len(detail) > 512 {
			detail = detail[:512] + "…"
		}
		if detail == "" {
			return errors.New("telegram returned " + resp.Status())
		}
		return fmt.Errorf("telegram returned %s: %s", resp.Status(), detail)
	}
	return nil
}

func redactTelegramToken(value, token string) string {
	if token == "" {
		return value
	}
	return strings.ReplaceAll(value, token, "[REDACTED]")
}

// telegramRequestError deliberately drops the URL from transport errors. The
// Telegram endpoint embeds the bot token in that URL, and resty/net/http may
// include it when formatting timeout or connection errors.
func telegramRequestError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return errors.New("telegram request timed out")
	}
	if errors.Is(err, context.Canceled) {
		return errors.New("telegram request canceled")
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return errors.New("telegram request timed out")
	}
	return errors.New("telegram request failed")
}
