package httpclient

import (
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-resty/resty/v2"
)

func TestNewRejectsRedirectsWithSensitiveData(t *testing.T) {
	for _, status := range []int{301, 302, 303, 307, 308} {
		for _, crossHost := range []bool{false, true} {
			t.Run(fmt.Sprintf("status_%d_cross_host_%t", status, crossHost), func(t *testing.T) {
				var received atomic.Int32
				sink := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					received.Add(1)
					w.WriteHeader(http.StatusOK)
				}))
				defer sink.Close()
				target := sink.URL
				if crossHost {
					target = strings.Replace(target, "127.0.0.1", "localhost", 1)
				}
				origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					http.Redirect(w, r, target, status)
				}))
				defer origin.Close()

				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				_, err := New().R().SetContext(ctx).
					SetHeader("Authorization", "Bearer local-fake-token").
					SetHeader("Cookie", "session=local-fake-cookie").
					SetHeader("x-api-key", "local-fake-key").
					SetBody(map[string]string{"password": "local-fake-password", "refresh_token": "local-fake-refresh"}).
					Post(origin.URL)
				if !errors.Is(err, resty.ErrAutoRedirectDisabled) {
					t.Fatalf("error = %v, want redirect rejection", err)
				}
				if got := received.Load(); got != 0 {
					t.Fatalf("redirect destination received %d requests with sensitive data", got)
				}
			})
		}
	}
}

func TestNewLimitsDecompressedResponses(t *testing.T) {
	var compressed bytes.Buffer
	zw := gzip.NewWriter(&compressed)
	block := bytes.Repeat([]byte("x"), 64<<10)
	for i := 0; i < MaxResponseBodyBytes/len(block); i++ {
		if _, err := zw.Write(block); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := zw.Write([]byte("x")); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if compressed.Len() >= MaxResponseBodyBytes {
		t.Fatal("fixture must be below the compressed-byte limit")
	}

	for _, status := range []int{http.StatusOK, http.StatusBadGateway} {
		t.Run(fmt.Sprintf("status_%d", status), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Encoding", "gzip")
				w.WriteHeader(status)
				_, _ = w.Write(compressed.Bytes())
			}))
			defer server.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_, err := New().R().SetContext(ctx).Get(server.URL)
			if !errors.Is(err, resty.ErrResponseBodyTooLarge) {
				t.Fatalf("error = %v, want decompressed-response limit rejection", err)
			}
		})
	}
}

func TestNewAcceptsBoundedResponses(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"ok":true}`)
	}))
	defer server.Close()
	response, err := New().R().Get(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	if string(response.Body()) != `{"ok":true}` {
		t.Fatalf("unexpected bounded response: %q", response.Body())
	}
}
