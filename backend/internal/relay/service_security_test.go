package relay

import (
	"context"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/guliansheng/gateway-ops/internal/crypto"
)

const serviceAdminHTTPTestKey = "fake-relay-admin-key"
const serviceAdminHTTPTestResponse = `{"code":0,"message":"ok","data":{"id":99,"name":"fake-account"}}`

type serviceAdminHTTPTestResult struct {
	id         int64
	statusCode int
	output     string
}

type serviceAdminHTTPTestCase struct {
	name        string
	method      string
	path        string
	body        map[string]string
	statusCode  int
	decodesData bool
	accountTest bool
	call        func(*Service, string) (serviceAdminHTTPTestResult, error)
}

func serviceAdminHTTPTestCases() []serviceAdminHTTPTestCase {
	mutationBody := map[string]string{"name": "fake-name", "api_key": "fake-request-body-key"}
	return []serviceAdminHTTPTestCase{
		{
			name: "get", method: http.MethodGet, path: "/admin-test", statusCode: http.StatusOK, decodesData: true,
			call: func(s *Service, baseURL string) (serviceAdminHTTPTestResult, error) {
				var out struct {
					ID int64 `json:"id"`
				}
				err := s.get(context.Background(), baseURL+"/admin-test", serviceAdminHTTPTestKey, &out)
				return serviceAdminHTTPTestResult{id: out.ID}, err
			},
		},
		{
			name: "put", method: http.MethodPut, path: "/admin-test", body: mutationBody, statusCode: http.StatusOK,
			call: func(s *Service, baseURL string) (serviceAdminHTTPTestResult, error) {
				return serviceAdminHTTPTestResult{}, s.put(context.Background(), baseURL+"/admin-test", serviceAdminHTTPTestKey, mutationBody)
			},
		},
		{
			name: "post", method: http.MethodPost, path: "/admin-test", body: mutationBody, statusCode: http.StatusCreated, decodesData: true,
			call: func(s *Service, baseURL string) (serviceAdminHTTPTestResult, error) {
				var out struct {
					ID int64 `json:"id"`
				}
				err := s.post(context.Background(), baseURL+"/admin-test", serviceAdminHTTPTestKey, mutationBody, &out)
				return serviceAdminHTTPTestResult{id: out.ID}, err
			},
		},
		{
			name: "deleteRemote", method: http.MethodDelete, path: "/admin-test", statusCode: http.StatusNoContent,
			call: func(s *Service, baseURL string) (serviceAdminHTTPTestResult, error) {
				return serviceAdminHTTPTestResult{}, s.deleteRemote(context.Background(), baseURL+"/admin-test", serviceAdminHTTPTestKey)
			},
		},
		{
			name: "duplicateRemote", method: http.MethodPost, path: "/api/v1/admin/accounts/42/duplicate", statusCode: http.StatusCreated, decodesData: true,
			call: func(s *Service, baseURL string) (serviceAdminHTTPTestResult, error) {
				account, err := s.duplicateRemote(context.Background(), baseURL, serviceAdminHTTPTestKey, 42, "fake-idempotency-key")
				var result serviceAdminHTTPTestResult
				if account != nil {
					result.id = account.ID
				}
				return result, err
			},
		},
		{
			name: "TestAccount", method: http.MethodPost, path: "/api/v1/admin/accounts/42/test", statusCode: http.StatusOK, accountTest: true,
			body: map[string]string{"model_id": "fake-model", "mode": "fake-mode"},
			call: func(s *Service, _ string) (serviceAdminHTTPTestResult, error) {
				result, err := s.TestAccount(context.Background(), 1, 42, "fake-model", "fake-mode")
				return serviceAdminHTTPTestResult{statusCode: result.StatusCode, output: result.Output}, err
			},
		},
		{
			name: "TestAccountWithoutBody", method: http.MethodPost, path: "/api/v1/admin/accounts/42/test", statusCode: http.StatusOK, accountTest: true,
			call: func(s *Service, _ string) (serviceAdminHTTPTestResult, error) {
				result, err := s.TestAccount(context.Background(), 1, 42, "", "")
				return serviceAdminHTTPTestResult{statusCode: result.StatusCode, output: result.Output}, err
			},
		},
	}
}

func newServiceAdminHTTPTestService(t *testing.T, baseURL string, needsStation bool) *Service {
	t.Helper()
	if !needsStation {
		return NewService(nil, nil)
	}
	cipher, err := crypto.NewCipher("fake-relay-security-test-cipher")
	if err != nil {
		t.Fatal(err)
	}
	ciphertext, err := cipher.Encrypt(serviceAdminHTTPTestKey)
	if err != nil {
		t.Fatal(err)
	}
	// Reuse the package's scripted SQL connector: no real database or secrets.
	repo, _ := newDegradationRunSQL(t, degradationRunSQLStep{
		contains: `FROM "relay_stations"`,
		columns:  []string{"id", "base_url", "api_key_cipher"},
		rows:     [][]driver.Value{{int64(1), baseURL, ciphertext}},
	})
	return NewService(repo, cipher)
}

func checkServiceAdminHTTPRequest(t *testing.T, r *http.Request, tc serviceAdminHTTPTestCase) {
	t.Helper()
	if r.Method != tc.method || r.URL.Path != tc.path {
		t.Errorf("request = %s %s, want %s %s", r.Method, r.URL.Path, tc.method, tc.path)
	}
	if got := r.Header.Get("x-api-key"); got != serviceAdminHTTPTestKey {
		t.Errorf("original request x-api-key = %q, want fake admin key", got)
	}
	if tc.name == "duplicateRemote" && r.Header.Get("Idempotency-Key") != "fake-idempotency-key" {
		t.Error("original duplicate request lost its idempotency key")
	}
	if tc.body != nil && r.Header.Get("Content-Type") != "application/json" {
		t.Error("original JSON request lost its content type")
	}
	payload, err := io.ReadAll(r.Body)
	if err != nil {
		t.Errorf("read original request body: %v", err)
		return
	}
	if tc.body == nil {
		if len(payload) != 0 {
			t.Errorf("unexpected original request body: %q", payload)
		}
		return
	}
	var body map[string]string
	if err := json.Unmarshal(payload, &body); err != nil {
		t.Errorf("decode original request body: %v", err)
		return
	}
	if !reflect.DeepEqual(body, tc.body) {
		t.Errorf("original request body = %#v, want %#v", body, tc.body)
	}
}

func TestServiceAdminRequestsDoNotFollowRedirects(t *testing.T) {
	for _, tc := range serviceAdminHTTPTestCases() {
		for _, status := range []int{http.StatusFound, http.StatusTemporaryRedirect, http.StatusPermanentRedirect} {
			for _, sameOrigin := range []bool{false, true} {
				origin := map[bool]string{false: "cross-origin", true: "same-origin"}[sameOrigin]
				t.Run(fmt.Sprintf("%s/%d/%s", tc.name, status, origin), func(t *testing.T) {
					var targetRequests, originalRequests atomic.Int64
					target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						targetRequests.Add(1)
						_, _ = io.WriteString(w, serviceAdminHTTPTestResponse)
					}))
					defer target.Close()
					const redirectBody = `{"code":0,"message":"fake redirect","data":{"id":99}}`
					source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						if r.URL.Path == "/redirect-target" {
							targetRequests.Add(1)
							_, _ = io.WriteString(w, serviceAdminHTTPTestResponse)
							return
						}
						originalRequests.Add(1)
						checkServiceAdminHTTPRequest(t, r, tc)
						location := target.URL + "/redirect-target"
						if sameOrigin {
							location = "/redirect-target"
						}
						w.Header().Set("Location", location)
						w.Header().Set("Content-Type", "application/json")
						w.WriteHeader(status)
						_, _ = io.WriteString(w, redirectBody)
					}))
					defer source.Close()

					svc := newServiceAdminHTTPTestService(t, source.URL, tc.accountTest)
					result, err := tc.call(svc, source.URL)
					if err == nil || !strings.Contains(err.Error(), fmt.Sprintf("HTTP %d", status)) {
						t.Errorf("redirect error = %v, want original HTTP %d", err, status)
					}
					if tc.accountTest && (result.statusCode != status || result.output != redirectBody) {
						t.Errorf("account test result = %+v, want original redirect response", result)
					}
					if got := originalRequests.Load(); got != 1 {
						t.Errorf("original endpoint received %d requests, want 1", got)
					}
					if got := targetRequests.Load(); got != 0 {
						t.Errorf("redirect target received %d requests; credentials or mutations could escape", got)
					}
				})
			}
		}
	}
}

func TestServiceAdminRequestsPreserveSuccess(t *testing.T) {
	for _, tc := range serviceAdminHTTPTestCases() {
		t.Run(tc.name, func(t *testing.T) {
			responseBody := serviceAdminHTTPTestResponse
			if tc.accountTest {
				responseBody = "data: fake-success\n\n"
			}
			var requests atomic.Int64
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				checkServiceAdminHTTPRequest(t, r, tc)
				w.WriteHeader(tc.statusCode)
				if tc.statusCode != http.StatusNoContent {
					_, _ = io.WriteString(w, responseBody)
				}
			}))
			defer server.Close()
			svc := newServiceAdminHTTPTestService(t, server.URL, tc.accountTest)
			result, err := tc.call(svc, server.URL)
			if err != nil {
				t.Fatalf("normal HTTP %d response failed: %v", tc.statusCode, err)
			}
			if tc.decodesData && result.id != 99 {
				t.Errorf("decoded account ID = %d, want 99", result.id)
			}
			if tc.accountTest && (result.statusCode != tc.statusCode || result.output != responseBody) {
				t.Errorf("account test result = %+v, want unmodified successful SSE output", result)
			}
			if got := requests.Load(); got != 1 {
				t.Errorf("normal endpoint received %d requests, want 1", got)
			}
		})
	}
}

func TestServiceAdminJSONResponseLimit(t *testing.T) {
	const limit = 16 << 20
	prefix, suffix := `{"code":0,"data":{"id":99},"padding":"`, `"}`
	responses := []struct {
		name     string
		body     string
		tooLarge bool
	}{
		{name: "exactly-16-MiB", body: prefix + strings.Repeat("a", limit-len(prefix)-len(suffix)) + suffix},
		{name: "over-16-MiB", body: prefix + strings.Repeat("a", limit+1-len(prefix)-len(suffix)) + suffix, tooLarge: true},
		{name: "valid-prefix-over-16-MiB", body: serviceAdminHTTPTestResponse + strings.Repeat(" ", limit+1-len(serviceAdminHTTPTestResponse)), tooLarge: true},
	}
	for _, response := range responses {
		for _, tc := range serviceAdminHTTPTestCases() {
			if tc.accountTest || tc.name == "deleteRemote" {
				continue // These endpoints already have separate, smaller non-JSON limits.
			}
			t.Run(response.name+"/"+tc.name, func(t *testing.T) {
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					checkServiceAdminHTTPRequest(t, r, tc)
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusOK)
					w.(http.Flusher).Flush() // Chunked response: no trusted Content-Length.
					_, _ = io.WriteString(w, response.body)
				}))
				defer server.Close()
				svc := NewService(nil, nil)
				result, err := tc.call(svc, server.URL)
				if response.tooLarge {
					if !errors.Is(err, errRemoteJSONResponseTooLarge) {
						t.Errorf("oversized response error = %v, want size-limit error", err)
					}
				} else {
					if err != nil {
						t.Fatalf("valid 16 MiB response failed: %v", err)
					}
					if tc.decodesData && result.id != 99 {
						t.Errorf("decoded account ID = %d, want 99", result.id)
					}
				}
			})
		}
	}
}

type serviceSecurityCountingReader struct {
	io.Reader
	bytesRead int
}

func (r *serviceSecurityCountingReader) Read(p []byte) (int, error) {
	n, err := r.Reader.Read(p)
	r.bytesRead += n
	return n, err
}

type serviceSecurityFillReader byte

func (value serviceSecurityFillReader) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = byte(value)
	}
	return len(p), nil
}

func TestDecodeRemoteEnvelopeBoundsRead(t *testing.T) {
	const limit = 16 << 20
	reader := &serviceSecurityCountingReader{Reader: io.MultiReader(
		strings.NewReader(serviceAdminHTTPTestResponse),
		io.LimitReader(serviceSecurityFillReader(' '), 2*limit),
	)}
	var envelope remoteEnvelope
	if err := decodeRemoteEnvelope(reader, &envelope); !errors.Is(err, errRemoteJSONResponseTooLarge) {
		t.Errorf("decode error = %v, want size-limit error", err)
	}
	if reader.bytesRead != limit+1 {
		t.Errorf("read %d bytes, want at most 16 MiB plus one sentinel byte", reader.bytesRead)
	}
}

func TestServiceGetAcceptsLargeAccountPage(t *testing.T) {
	const accountCount = 2048
	name := strings.Repeat("a", 4096)
	item := `{"id":42,"name":"` + name + `"}`
	body := `{"code":0,"data":{"items":[` + strings.Repeat(item+",", accountCount-1) + item + `],"page":1,"pages":1,"page_size":2048}}`
	if len(body) <= 8<<20 || len(body) >= 16<<20 {
		t.Fatalf("large-page fixture size = %d bytes, want between 8 and 16 MiB", len(body))
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.Header.Get("x-api-key") != serviceAdminHTTPTestKey {
			t.Error("large account page request lost its method or fake credential")
		}
		_, _ = io.WriteString(w, body)
	}))
	defer server.Close()
	var page remoteAccountPage
	if err := NewService(nil, nil).get(context.Background(), server.URL, serviceAdminHTTPTestKey, &page); err != nil {
		t.Fatalf("large account page failed: %v", err)
	}
	if len(page.Items) != accountCount || page.Page != 1 || page.Pages != 1 || page.PageSize != accountCount {
		t.Fatalf("decoded large page has %d items, pagination %d/%d/%d", len(page.Items), page.Page, page.Pages, page.PageSize)
	}
	if page.Items[0].Name != name || page.Items[accountCount-1].Name != name {
		t.Error("large account page was truncated")
	}
}
