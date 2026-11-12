package captcha

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/go-resty/resty/v2"
	"github.com/guliansheng/gateway-ops/internal/storage"
)

func TestProvidersRejectClientKeyRedirects(t *testing.T) {
	for _, typ := range []storage.CaptchaProviderType{
		storage.CaptchaCapSolver, storage.CaptchaTwoCaptcha, storage.CaptchaAntiCaptcha, storage.CaptchaYesCaptcha,
	} {
		t.Run(string(typ), func(t *testing.T) {
			var received atomic.Int32
			sink := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { received.Add(1) }))
			defer sink.Close()
			origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				http.Redirect(w, r, strings.Replace(sink.URL, "127.0.0.1", "localhost", 1), http.StatusTemporaryRedirect)
			}))
			defer origin.Close()
			provider, err := Build(&storage.CaptchaConfig{Type: typ, Endpoint: origin.URL}, "local-fake-captcha-key")
			if err != nil {
				t.Fatal(err)
			}
			_, err = provider.SolveTurnstile(context.Background(), "local-fake-site-key", "https://local-fake.invalid")
			if !errors.Is(err, resty.ErrAutoRedirectDisabled) {
				t.Fatalf("solve error = %v, want redirect rejection", err)
			}
			if received.Load() != 0 {
				t.Fatal("redirect destination received the captcha API key")
			}
		})
	}
}
