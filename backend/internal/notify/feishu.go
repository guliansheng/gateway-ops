package notify

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/go-resty/resty/v2"
	"github.com/guliansheng/gateway-ops/internal/storage"
)

func init() {
	Register(storage.NotifyFeishu, func(raw string) (Notifier, error) { return newFeishu(raw) })
}

type feishuConfig struct {
	WebhookURL string `json:"webhook_url"`
	Secret     string `json:"secret,omitempty"`
}

type feishu struct {
	cfg  feishuConfig
	http *resty.Client
}

func newFeishu(raw string) (*feishu, error) {
	var cfg feishuConfig
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		return nil, err
	}
	if cfg.WebhookURL == "" {
		return nil, errors.New("feishu webhook_url is required")
	}
	return &feishu{cfg: cfg, http: resty.New()}, nil
}

func (f *feishu) Type() storage.NotificationChannelType { return storage.NotifyFeishu }

func (f *feishu) Send(ctx context.Context, msg Message) error {
	content := make([][]map[string]string, 0)
	for _, line := range strings.Split(notificationBody(msg), "\n") {
		content = append(content, []map[string]string{{"tag": "text", "text": line}})
	}
	if len(content) == 0 {
		content = append(content, []map[string]string{{"tag": "text", "text": " "}})
	}
	body := map[string]any{
		"msg_type": "post",
		"content": map[string]any{
			"post": map[string]any{
				"zh_cn": map[string]any{
					"title":   notificationTitle(msg),
					"content": content,
				},
			},
		},
	}
	if f.cfg.Secret != "" {
		ts := time.Now().Unix()
		stringToSign := strconv.FormatInt(ts, 10) + "\n" + f.cfg.Secret
		mac := hmac.New(sha256.New, []byte(stringToSign))
		sign := base64.StdEncoding.EncodeToString(mac.Sum(nil))
		body["timestamp"] = strconv.FormatInt(ts, 10)
		body["sign"] = sign
	}
	resp, err := f.http.R().
		SetContext(ctx).
		SetBody(body).
		Post(f.cfg.WebhookURL)
	if err != nil {
		return err
	}
	if resp.IsError() {
		return errors.New("feishu returned " + resp.Status())
	}
	return nil
}
