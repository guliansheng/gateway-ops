package notify

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"time"

	"github.com/go-resty/resty/v2"
	"github.com/guliansheng/gateway-ops/internal/storage"
)

func init() {
	Register(storage.NotifyDingTalk, func(raw string) (Notifier, error) { return newDingTalk(raw) })
}

type dingTalkConfig struct {
	WebhookURL string `json:"webhook_url"`
	Secret     string `json:"secret,omitempty"`
}

type dingTalk struct {
	cfg  dingTalkConfig
	http *resty.Client
}

func newDingTalk(raw string) (*dingTalk, error) {
	var cfg dingTalkConfig
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		return nil, err
	}
	if cfg.WebhookURL == "" {
		return nil, errors.New("dingtalk webhook_url is required")
	}
	return &dingTalk{cfg: cfg, http: resty.New()}, nil
}

func (d *dingTalk) Type() storage.NotificationChannelType { return storage.NotifyDingTalk }

func (d *dingTalk) Send(ctx context.Context, msg Message) error {
	endpoint := d.cfg.WebhookURL
	if d.cfg.Secret != "" {
		ts := time.Now().UnixMilli()
		stringToSign := fmt.Sprintf("%d\n%s", ts, d.cfg.Secret)
		mac := hmac.New(sha256.New, []byte(d.cfg.Secret))
		mac.Write([]byte(stringToSign))
		sign := base64.StdEncoding.EncodeToString(mac.Sum(nil))
		endpoint = fmt.Sprintf("%s&timestamp=%d&sign=%s", endpoint, ts, url.QueryEscape(sign))
	}
	resp, err := d.http.R().
		SetContext(ctx).
		SetBody(map[string]any{
			"msgtype": "markdown",
			"markdown": map[string]string{
				"title": notificationTitle(msg),
				"text":  "### " + notificationTitle(msg) + "\n\n" + notificationBody(msg),
			},
		}).
		Post(endpoint)
	if err != nil {
		return err
	}
	if resp.IsError() {
		return errors.New("dingtalk returned " + resp.Status())
	}
	return nil
}
