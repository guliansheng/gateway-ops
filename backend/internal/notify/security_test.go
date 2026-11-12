package notify

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"testing"

	"github.com/guliansheng/gateway-ops/internal/storage"
)

type failingSecurityNotifier struct{ err error }

func (n failingSecurityNotifier) Send(context.Context, Message) error   { return n.err }
func (n failingSecurityNotifier) Type() storage.NotificationChannelType { return storage.NotifyWebhook }

func TestNotificationErrorsRedactedBeforeDispatcher(t *testing.T) {
	const secret = "synthetic-notification-secret"
	config := `{"url":"https://notify.test/` + secret + `?access_token=` + secret + `","headers":{"x-api-key":"` + secret + `"}}`
	for _, raw := range []error{
		&url.Error{Op: "Post", URL: "https://notify.test/" + secret + "?access_token=" + secret, Err: errors.New("connection failed")},
		fmt.Errorf("wrapped: %w", &url.Error{Op: "Post", URL: "https://notify.test/" + secret, Err: errors.New("proxy credential " + secret)}),
		errors.New("server echo " + secret + " https://notify.test/generated-signature?sign=dynamic-secret"),
	} {
		n := &safeNotifier{Notifier: failingSecurityNotifier{err: raw}, config: config}
		err := n.Send(context.Background(), Message{})
		if err == nil || strings.Contains(err.Error(), secret) || strings.Contains(err.Error(), "https://") || strings.Contains(err.Error(), "dynamic-secret") {
			t.Fatalf("unredacted error: %v", err)
		}
	}
	if !errors.Is(sanitizeNotificationError(fmt.Errorf("wrapped: %w", context.Canceled), config), context.Canceled) {
		t.Fatal("lost cancellation")
	}
	if !errors.Is(sanitizeNotificationError(context.DeadlineExceeded, config), context.DeadlineExceeded) {
		t.Fatal("lost timeout")
	}
	if err := sanitizeNotificationError(errors.New(strings.Repeat("x", 8192)), config); len(err.Error()) > 4096 {
		t.Fatal("unbounded error")
	}
	if sanitizeNotificationError(nil, config) != nil {
		t.Fatal("nil error changed")
	}
}

func TestBuildWrapsAllNotificationFactories(t *testing.T) {
	// Refused loopback port: no real service or secret is used.
	for _, test := range []struct {
		kind   storage.NotificationChannelType
		config string
	}{
		{storage.NotifyWebhook, `{"url":"http://127.0.0.1:1/synthetic-secret"}`},
	} {
		n, err := Build(&storage.NotificationChannel{Type: test.kind}, test.config)
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := n.(*safeNotifier); !ok {
			t.Fatal("factory bypassed error boundary")
		}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if err := n.Send(ctx, Message{}); err == nil || strings.Contains(err.Error(), "synthetic-secret") {
			t.Fatalf("error: %v", err)
		}
	}
}
