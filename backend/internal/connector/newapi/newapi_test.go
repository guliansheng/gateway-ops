package newapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/guliansheng/gateway-ops/internal/connector"
)

func TestCheckAuthUsesCustomAccessTokenHeaders(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer permanent-token" {
			t.Fatalf("Authorization = %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"success":true,"message":"","data":{"id":1}}`))
	}))
	defer server.Close()

	err := New().CheckAuth(context.Background(), &connector.Channel{SiteURL: server.URL}, &connector.AuthSession{
		AccessToken: "permanent-token",
		Headers:     map[string]string{"Authorization": "Bearer permanent-token"},
	})
	if err != nil {
		t.Fatal(err)
	}
}
