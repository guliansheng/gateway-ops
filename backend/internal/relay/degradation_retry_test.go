package relay

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func withFastDegradationRetry(t *testing.T) {
	t.Helper()
	previous := degradationRetryBackoff
	degradationRetryBackoff = nil
	t.Cleanup(func() { degradationRetryBackoff = previous })
}

func degradationRetryTestService(t *testing.T, handler func(attempt int) (*http.Response, error)) (*Service, *int) {
	t.Helper()
	attempts := 0
	return &Service{client: &http.Client{Transport: degradationTestTransport(func(*http.Request) (*http.Response, error) {
		attempts++
		return handler(attempts)
	})}}, &attempts
}

func degradationRetryTestResponse(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(body))}
}

func degradationRetryTestChat(t *testing.T, ctx context.Context, s *Service) (string, error) {
	t.Helper()
	return s.degradationChat(ctx, "https://gateway.invalid", "local-secret", "candidate", "none", "", "prompt", 5*time.Second, 4096, "chat")
}

// A single upstream hiccup must not burn a scheduled run or a paid manual test.
func TestDegradationChatRetriesTransientGatewayFailures(t *testing.T) {
	withFastDegradationRetry(t)
	stream := degradationTestChatChunk("ok", "stop") + "data: [DONE]\n\n"
	s, attempts := degradationRetryTestService(t, func(attempt int) (*http.Response, error) {
		if attempt < degradationRetryAttempts {
			return degradationRetryTestResponse(http.StatusBadGateway, `{"error":"upstream"}`), nil
		}
		return degradationRetryTestResponse(http.StatusOK, stream), nil
	})
	output, err := degradationRetryTestChat(t, context.Background(), s)
	if err != nil || output != "ok" || *attempts != degradationRetryAttempts {
		t.Fatalf("output=%q err=%v attempts=%d", output, err, *attempts)
	}
}

func TestDegradationChatRetriesConnectionFailures(t *testing.T) {
	withFastDegradationRetry(t)
	stream := degradationTestChatChunk("ok", "stop") + "data: [DONE]\n\n"
	s, attempts := degradationRetryTestService(t, func(attempt int) (*http.Response, error) {
		if attempt == 1 {
			return nil, errors.New("dial tcp 10.0.0.1:443: connection reset by peer")
		}
		return degradationRetryTestResponse(http.StatusOK, stream), nil
	})
	output, err := degradationRetryTestChat(t, context.Background(), s)
	if err != nil || output != "ok" || *attempts != 2 {
		t.Fatalf("output=%q err=%v attempts=%d", output, err, *attempts)
	}
}

func TestDegradationChatReportsRetriesAfterExhaustingAttempts(t *testing.T) {
	withFastDegradationRetry(t)
	s, attempts := degradationRetryTestService(t, func(int) (*http.Response, error) {
		return degradationRetryTestResponse(http.StatusBadGateway, ""), nil
	})
	_, err := degradationRetryTestChat(t, context.Background(), s)
	if err == nil || *attempts != degradationRetryAttempts {
		t.Fatalf("err=%v attempts=%d", err, *attempts)
	}
	if !strings.Contains(err.Error(), "HTTP 502") || !strings.Contains(err.Error(), "上游服务异常") || !strings.Contains(err.Error(), "已重试 2 次") {
		t.Fatalf("err=%q", err)
	}
	var status degradationStatusError
	if !errors.As(err, &status) || status.Status != http.StatusBadGateway {
		t.Fatalf("status error lost: %v", err)
	}
}

// 401/404/malformed answers will not change on a second paid call.
func TestDegradationChatDoesNotRetryDeterministicFailures(t *testing.T) {
	withFastDegradationRetry(t)
	for _, tc := range []struct {
		name   string
		status int
		body   string
		want   string
	}{
		{name: "unauthorized", status: http.StatusUnauthorized, want: "认证失败"},
		{name: "not found", status: http.StatusNotFound, want: "模型或协议端点不存在"},
		{name: "malformed body", status: http.StatusOK, body: `{"choices":`, want: "模型响应格式无效"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, attempts := degradationRetryTestService(t, func(int) (*http.Response, error) {
				return degradationRetryTestResponse(tc.status, tc.body), nil
			})
			_, err := degradationRetryTestChat(t, context.Background(), s)
			if err == nil || *attempts != 1 || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err=%v attempts=%d", err, *attempts)
			}
			if strings.Contains(err.Error(), "已重试") {
				t.Fatalf("deterministic failure was retried: %v", err)
			}
		})
	}
}

// A retry that cannot fit in the remaining run budget is skipped, so the run
// still records an error result instead of being killed by the lease.
func TestDegradationChatSkipsRetryWhenRunBudgetIsShort(t *testing.T) {
	withFastDegradationRetry(t)
	s, attempts := degradationRetryTestService(t, func(int) (*http.Response, error) {
		return degradationRetryTestResponse(http.StatusBadGateway, ""), nil
	})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, err := degradationRetryTestChat(t, ctx, s)
	if err == nil || *attempts != 1 || strings.Contains(err.Error(), "已重试") {
		t.Fatalf("err=%v attempts=%d", err, *attempts)
	}
}
