package notify

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"regexp"
	"sort"
	"strings"
)

var notificationURL = regexp.MustCompile(`(?i)https?://[^\s<>"']+`)

type safeNotifier struct {
	Notifier
	config string
}

// Every dispatcher path sees only sanitized errors, before retries, logging,
// database persistence or returning the test-send result to the API.
func (n *safeNotifier) Send(ctx context.Context, msg Message) error {
	return sanitizeNotificationError(n.Notifier.Send(ctx, msg), n.config)
}

func sanitizeNotificationError(err error, config string) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) {
		return context.Canceled
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return context.DeadlineExceeded
	}
	var requestError *url.Error
	if errors.As(err, &requestError) {
		if requestError.Timeout() {
			return errors.New("notification request timed out")
		}
		// URL errors contain secret-bearing webhook paths, queries and signatures.
		// Underlying proxy errors can also contain credentials; don't serialize them.
		return errors.New("notification request failed")
	}
	message := err.Error()
	var values any
	if json.Unmarshal([]byte(config), &values) == nil {
		var secrets []string
		var collect func(any)
		collect = func(v any) {
			switch v := v.(type) {
			case string:
				if len(v) >= 4 {
					secrets = append(secrets, v, url.QueryEscape(v), url.PathEscape(v))
				}
			case map[string]any:
				for _, value := range v {
					collect(value)
				}
			case []any:
				for _, value := range v {
					collect(value)
				}
			}
		}
		collect(values)
		sort.Slice(secrets, func(i, j int) bool { return len(secrets[i]) > len(secrets[j]) })
		for _, secret := range secrets {
			message = strings.ReplaceAll(message, secret, "[redacted]")
		}
	}
	message = notificationURL.ReplaceAllString(message, "[redacted-url]")
	// Remote service messages must not grow notification logs without bound.
	if len(message) > 4096 {
		message = string([]rune(message)[:min(len([]rune(message)), 1024)]) + "…"
	}
	return errors.New(message)
}
