package notify

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/go-resty/resty/v2"
	"github.com/guliansheng/gateway-ops/internal/httpclient"
	"github.com/guliansheng/gateway-ops/internal/storage"
)

func TestHTTPNotifiersUseHardenedClients(t *testing.T) {
	for _, typ := range []storage.NotificationChannelType{
		storage.NotifyWebhook, storage.NotifyBark, storage.NotifyTelegram,
		storage.NotifyDingTalk, storage.NotifyFeishu, storage.NotifyWecom,
	} {
		t.Run(string(typ), func(t *testing.T) {
			var received atomic.Int32
			sink := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { received.Add(1) }))
			defer sink.Close()
			origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				http.Redirect(w, r, strings.Replace(sink.URL, "127.0.0.1", "localhost", 1), http.StatusTemporaryRedirect)
			}))
			defer origin.Close()
			raw, err := json.Marshal(map[string]string{
				"url": origin.URL + "/local-fake-key", "webhook_url": origin.URL,
				"bot_token": "local-fake-bot-token", "chat_id": "local-fake-chat",
			})
			if err != nil {
				t.Fatal(err)
			}
			// Inspect each concrete constructor, without calling Telegram's fixed
			// public endpoint or any real service.
			n, err := registry[typ](string(raw))
			if err != nil {
				t.Fatal(err)
			}
			var client *resty.Client
			switch n := n.(type) {
			case *webhook:
				client = n.http
			case *bark:
				client = n.http
			case *telegram:
				client = n.http
			case *dingTalk:
				client = n.http
			case *feishu:
				client = n.http
			case *wecom:
				client = n.http
			default:
				t.Fatalf("unexpected notifier type %T", n)
			}
			if client.ResponseBodyLimit != httpclient.MaxResponseBodyBytes {
				t.Fatal("notifier is missing the shared response body limit")
			}
			_, err = client.R().SetContext(context.Background()).
				SetHeader("x-api-key", "local-fake-webhook-key").
				SetBody(map[string]string{"body": "local-fake-message"}).Post(origin.URL)
			if !errors.Is(err, resty.ErrAutoRedirectDisabled) {
				t.Fatalf("send error = %v, want redirect rejection", err)
			}
			if received.Load() != 0 {
				t.Fatal("redirect destination received notification credentials or body")
			}
		})
	}
}
