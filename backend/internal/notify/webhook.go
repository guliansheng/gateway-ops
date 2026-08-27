package notify

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/go-resty/resty/v2"
	"github.com/guliansheng/gateway-ops/internal/storage"
)

func init() {
	Register(storage.NotifyWebhook, func(raw string) (Notifier, error) { return newWebhook(raw) })
}

type webhookConfig struct {
	URL     string            `json:"url"`
	Method  string            `json:"method,omitempty"`
	Headers map[string]string `json:"headers,omitempty"`
}

type webhook struct {
	cfg  webhookConfig
	http *resty.Client
}

func newWebhook(raw string) (*webhook, error) {
	var cfg webhookConfig
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		return nil, err
	}
	if cfg.URL == "" {
		return nil, errors.New("webhook url is required")
	}
	if cfg.Method == "" {
		cfg.Method = "POST"
	}
	return &webhook{cfg: cfg, http: resty.New()}, nil
}

func (w *webhook) Type() storage.NotificationChannelType { return storage.NotifyWebhook }

func (w *webhook) Send(ctx context.Context, msg Message) error {
	req := w.http.R().
		SetContext(ctx).
		SetHeader("Content-Type", "application/json").
		SetBody(map[string]any{
			"event":   msg.Event,
			"subject": msg.Subject,
			"body":    notificationBody(msg),
			"extra":   msg.Extra,
		})
	for k, v := range w.cfg.Headers {
		req.SetHeader(k, v)
	}
	resp, err := req.Execute(w.cfg.Method, w.cfg.URL)
	if err != nil {
		return err
	}
	if resp.IsError() {
		return errors.New("webhook returned " + resp.Status())
	}
	return nil
}
