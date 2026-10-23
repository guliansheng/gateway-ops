package relay

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func degradationTestChatChunk(content string, finish any) string {
	data, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"index": 0, "delta": map[string]string{"content": content}, "finish_reason": finish}}})
	return "data: " + string(data) + "\n\n"
}

func degradationTestResponsesJSON(text, status string) string {
	data, _ := json.Marshal(map[string]any{"status": status, "output": []any{map[string]any{"type": "message", "status": "completed", "content": []any{map[string]string{"type": "output_text", "text": text}}}}})
	return string(data)
}

func degradationTestResponseEvent(kind, body string) string {
	return "event: " + kind + "\ndata: " + body + "\n\n"
}

func degradationTestResponseDelta(text string) string {
	data, _ := json.Marshal(map[string]string{"type": "response.output_text.delta", "delta": text})
	return degradationTestResponseEvent("response.output_text.delta", string(data))
}

func degradationTestResponseCompleted(text string) string {
	return degradationTestResponseEvent("response.completed", `{"type":"response.completed","response":`+degradationTestResponsesJSON(text, "completed")+`}`)
}

func degradationTestCall(t *testing.T, protocol, body string, limit int) (string, error) {
	t.Helper()
	s := &Service{client: &http.Client{Transport: degradationTestTransport(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(body))}, nil
	})}}
	return s.degradationChat(context.Background(), "https://offline.invalid", "local-secret", "candidate", "none", "", "prompt", time.Second, limit, protocol)
}

func TestDegradationTransportProtocolAndStreamRequest(t *testing.T) {
	for _, tc := range []struct{ model, protocol, want string }{
		{"gpt-6-astra", "", "responses"},
		{"gpt-6.1-sol", "", "responses"},
		{"openai/GPT-6.1-sol", "", "responses"},
		{"chatgpt-latest", "", "responses"},
		{"o1", "", "responses"},
		{"o3-mini", "", "responses"},
		{"o4-mini", "auto", "responses"},
		{"custom", "responses", "responses"},
		{"claude-test", "", "chat"},
		{"gemini-test", "", "chat"},
		{"o123-custom", "", "chat"},
		{"gpt-6-astra", "chat", "chat"},
		{"gpt-6-astra", "chat_completions", "chat"},
	} {
		t.Run(tc.model+"/"+tc.protocol, func(t *testing.T) {
			for _, effort := range []string{"high", "none", ""} {
				s := &Service{client: &http.Client{Timeout: time.Nanosecond, Transport: degradationTestTransport(func(r *http.Request) (*http.Response, error) {
					endpoint := "/proxy/v1/chat/completions"
					if tc.want == "responses" {
						endpoint = "/proxy/v1/responses"
					}
					if r.Method != http.MethodPost || r.URL.Path != endpoint || r.Header.Get("Authorization") != "Bearer local-secret" || !strings.Contains(r.Header.Get("Accept"), "text/event-stream") {
						t.Fatalf("wrong request: %s %s %+v", r.Method, r.URL.Path, r.Header)
					}
					if deadline, ok := r.Context().Deadline(); !ok || time.Until(deadline) > time.Second {
						t.Fatal("missing bounded request context")
					}
					var body map[string]json.RawMessage
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
						t.Fatal(err)
					}
					if string(body["stream"]) != "true" || string(body["model"]) != fmt.Sprintf("%q", tc.model) {
						t.Fatalf("non-streaming or wrong model: %s", body)
					}
					messagesKey, effortKey, otherMessages, otherEffort := "messages", "reasoning_effort", "input", "reasoning"
					if tc.want == "responses" {
						messagesKey, effortKey, otherMessages, otherEffort = "input", "reasoning", "messages", "reasoning_effort"
					}
					var messages []map[string]string
					if json.Unmarshal(body[messagesKey], &messages) != nil || len(messages) != 2 || messages[0]["role"] != "system" || messages[0]["content"] != "guard" || messages[1]["role"] != "user" || messages[1]["content"] != "prompt" || body[otherMessages] != nil || body[otherEffort] != nil {
						t.Fatalf("wrong protocol payload: %s", body)
					}
					if effort == "high" {
						want := `"high"`
						if tc.want == "responses" {
							want = `{"effort":"high"}`
						}
						if string(body[effortKey]) != want {
							t.Fatalf("wrong effort: %s", body[effortKey])
						}
					} else if body[effortKey] != nil {
						t.Fatal("none/empty effort must be omitted")
					}
					w := httptest.NewRecorder()
					if tc.want == "responses" {
						_, _ = io.WriteString(w, degradationTestResponsesJSON("answer", "completed"))
					} else {
						degradationChatResponse(w, "answer")
					}
					return w.Result(), nil
				})}}
				output, err := s.degradationChat(context.Background(), "https://offline.invalid/proxy/v1/", "local-secret", tc.model, effort, "guard", "prompt", time.Second, 128, tc.protocol)
				if err != nil || output != "answer" || s.client.Timeout != time.Nanosecond {
					t.Fatalf("output=%q error=%v", output, err)
				}
			}
		})
	}
}

func TestDegradationTransportJSONAndSSE(t *testing.T) {
	chat := ": keepalive\r\n\r\ndata: {\"choices\":[\n" + "data: {\"index\":0,\"delta\":{\"role\":\"assistant\"}}]}\n\n" + degradationTestChatChunk("完整", nil) + degradationTestChatChunk(" answer\n", nil) + degradationTestChatChunk("", "stop") + "data: {\"choices\":[],\"usage\":{\"total_tokens\":9}}\n\ndata: [DONE]\n\n"
	responses := degradationTestResponseEvent("response.created", `{"type":"response.created","response":{"status":"in_progress"}}`) + degradationTestResponseEvent("response.reasoning_summary_text.delta", `{"type":"response.reasoning_summary_text.delta","delta":"not candidate text"}`) + degradationTestResponseDelta("完整") + degradationTestResponseDelta(" answer\n") + degradationTestResponseCompleted("完整 answer\n")
	for _, tc := range []struct{ name, protocol, body, want string }{
		{"chat SSE", "chat", chat, "完整 answer\n"},
		{"chat SSE final event without newline", "chat", strings.TrimRight(chat, "\n"), "完整 answer\n"},
		{"responses SSE", "responses", responses, "完整 answer\n"},
		{"responses completed only", "responses", degradationTestResponseCompleted("complete"), "complete"},
		{"responses empty completed envelope after deltas", "responses", degradationTestResponseDelta("complete") + degradationTestResponseEvent("response.completed", `{"response":{"status":"completed"}}`), "complete"},
		{"responses type only", "responses", "data: {\"type\":\"response.output_text.delta\",\"delta\":\"complete\"}\n\ndata: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\"}}\n\n", "complete"},
		{"chat JSON", "chat", `{"choices":[{"message":{"content":"complete"},"finish_reason":"stop"}]}`, "complete"},
		{"responses JSON", "responses", degradationTestResponsesJSON("complete", "completed"), "complete"},
		{"responses flattened JSON", "responses", `{"status":"completed","output_text":"complete"}`, "complete"},
		{"BOM and whitespace", "responses", "\xef\xbb\xbf  \n\t" + degradationTestResponseCompleted("complete"), "complete"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			output, err := degradationTestCall(t, tc.protocol, tc.body, 128)
			if err != nil || output != tc.want {
				t.Fatalf("output=%q error=%v want=%q", output, err, tc.want)
			}
		})
	}
}

func TestDegradationTransportRejectsIncompleteEmptyAndErrors(t *testing.T) {
	partialChat := degradationTestChatChunk("partial local-secret", nil)
	partialResponses := degradationTestResponseDelta("partial local-secret")
	for _, tc := range []struct {
		name, protocol, body string
		want                 error
	}{
		{"chat missing finish", "chat", `{"choices":[{"message":{"content":"partial"}}]}`, errDegradationUnfinished},
		{"chat empty finish", "chat", `{"choices":[{"message":{"content":"partial"},"finish_reason":""}]}`, errDegradationUnfinished},
		{"chat length", "chat", `{"choices":[{"message":{"content":"partial"},"finish_reason":"length"}]}`, errDegradationTruncated},
		{"chat filtered", "chat", `{"choices":[{"message":{"content":"partial"},"finish_reason":"content_filter"}]}`, errDegradationRefused},
		{"chat tool calls", "chat", `{"choices":[{"message":{"content":"partial"},"finish_reason":"tool_calls"}]}`, errDegradationUnfinished},
		{"chat refusal", "chat", `{"choices":[{"message":{"content":"partial","refusal":"local-secret"},"finish_reason":"stop"}]}`, errDegradationRefused},
		{"chat empty", "chat", `{"choices":[{"message":{"content":"  \n"},"finish_reason":"stop"}]}`, errDegradationEmpty},
		{"chat no done", "chat", partialChat + degradationTestChatChunk("", "stop"), errDegradationUnfinished},
		{"chat done without stop", "chat", partialChat + "data: [DONE]\n\n", errDegradationUnfinished},
		{"chat truncated stream", "chat", partialChat, errDegradationUnfinished},
		{"chat length stream", "chat", partialChat + degradationTestChatChunk("", "length") + "data: [DONE]\n\n", errDegradationTruncated},
		{"chat empty stream", "chat", degradationTestChatChunk("", "stop") + "data: [DONE]\n\n", errDegradationEmpty},
		{"chat error JSON", "chat", `{"error":{"message":"local-secret raw upstream body"}}`, errDegradationUpstream},
		{"chat error stream", "chat", partialChat + "data: {\"error\":{\"message\":\"local-secret raw upstream body\"}}\n\n", errDegradationUpstream},
		{"chat error event", "chat", partialChat + "event: error\ndata: local-secret raw upstream body\n\n", errDegradationUpstream},
		{"chat bare error event", "chat", "event: error\n\n", errDegradationUpstream},
		{"chat malformed JSON", "chat", `{"choices": [local-secret]}`, errDegradationFormat},
		{"chat trailing JSON", "chat", `{"choices":[{"message":{"content":"ok"},"finish_reason":"stop"}]} garbage`, errDegradationFormat},
		{"responses missing status", "responses", `{"output_text":"partial"}`, errDegradationUnfinished},
		{"responses incomplete JSON", "responses", degradationTestResponsesJSON("partial", "incomplete"), errDegradationUnfinished},
		{"responses incomplete details", "responses", `{"status":"completed","output_text":"partial","incomplete_details":{"reason":"max_output_tokens"}}`, errDegradationUnfinished},
		{"responses incomplete item", "responses", `{"status":"completed","output":[{"type":"message","status":"in_progress","content":[{"type":"output_text","text":"partial"}]}]}`, errDegradationUnfinished},
		{"responses failed JSON", "responses", `{"status":"failed","error":{"message":"local-secret"}}`, errDegradationUpstream},
		{"responses refusal", "responses", `{"status":"completed","output":[{"type":"message","content":[{"type":"refusal","refusal":"local-secret"}]}]}`, errDegradationRefused},
		{"responses empty JSON", "responses", degradationTestResponsesJSON("", "completed"), errDegradationEmpty},
		{"responses no completed", "responses", partialResponses, errDegradationUnfinished},
		{"responses done only", "responses", partialResponses + "data: [DONE]\n\n", errDegradationUnfinished},
		{"responses missing completed payload", "responses", partialResponses + degradationTestResponseEvent("response.completed", `{"type":"response.completed"}`), errDegradationUnfinished},
		{"responses missing completed status", "responses", partialResponses + degradationTestResponseEvent("response.completed", `{"response":{}}`), errDegradationUnfinished},
		{"responses completed mismatch", "responses", partialResponses + degradationTestResponseCompleted("different"), errDegradationUnfinished},
		{"responses incomplete event", "responses", partialResponses + degradationTestResponseEvent("response.incomplete", `{"type":"response.incomplete","response":{"status":"incomplete"}}`), errDegradationUnfinished},
		{"responses failed event", "responses", partialResponses + degradationTestResponseEvent("response.failed", `{"type":"response.failed","response":{"status":"failed","error":{"message":"local-secret"}}}`), errDegradationUpstream},
		{"responses error event", "responses", partialResponses + "event: error\ndata: local-secret raw upstream body\n\n", errDegradationUpstream},
		{"responses type error", "responses", partialResponses + "data: {\"type\":\"error\",\"message\":\"local-secret raw upstream body\"}\n\n", errDegradationUpstream},
		{"responses refusal event", "responses", partialResponses + degradationTestResponseEvent("response.refusal.delta", `{"type":"response.refusal.delta","delta":"local-secret"}`), errDegradationRefused},
		{"responses empty stream", "responses", degradationTestResponseCompleted(""), errDegradationEmpty},
		{"responses malformed event", "responses", "data: local-secret raw upstream body\n\n", errDegradationFormat},
	} {
		t.Run(tc.name, func(t *testing.T) {
			output, err := degradationTestCall(t, tc.protocol, tc.body, 128)
			if output != "" || !errors.Is(err, tc.want) {
				t.Fatalf("output=%q error=%v want=%v", output, err, tc.want)
			}
			for _, secret := range []string{"local-secret", "raw upstream body", "offline.invalid"} {
				if strings.Contains(err.Error(), secret) {
					t.Fatalf("error leaked %q: %v", secret, err)
				}
			}
		})
	}
}

func TestDegradationTransportWireAndDecodedLimits(t *testing.T) {
	const limit = 64
	for _, tc := range []struct{ name, protocol, body string }{
		{"chat JSON decoded", "chat", `{"choices":[{"message":{"content":"` + strings.Repeat("a", limit+1) + `"},"finish_reason":"stop"}]}`},
		{"chat SSE decoded", "chat", degradationTestChatChunk(strings.Repeat("a", 33), nil) + degradationTestChatChunk(strings.Repeat("b", 32), nil) + degradationTestChatChunk("", "stop") + "data: [DONE]\n\n"},
		{"responses JSON decoded", "responses", degradationTestResponsesJSON(strings.Repeat("a", limit+1), "completed")},
		{"responses SSE decoded", "responses", degradationTestResponseDelta(strings.Repeat("a", 33)) + degradationTestResponseDelta(strings.Repeat("b", 32)) + degradationTestResponseCompleted("")},
		{"responses completed decoded", "responses", degradationTestResponseCompleted(strings.Repeat("a", limit+1))},
		{"JSON wire", "chat", `{"padding":"` + strings.Repeat("a", 70*1024) + `"}`},
		{"SSE wire line", "chat", ":" + strings.Repeat("a", 70*1024) + "\n\n"},
		{"SSE wire keepalives", "responses", strings.Repeat(": keepalive\n\n", 7000)},
		{"wire whitespace", "chat", strings.Repeat(" ", 70*1024)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			output, err := degradationTestCall(t, tc.protocol, tc.body, limit)
			if output != "" || err == nil || !strings.Contains(err.Error(), "超过大小上限") {
				t.Fatalf("output=%q error=%v", output, err)
			}
		})
	}
	for _, protocol := range []string{"chat", "responses"} {
		body := degradationTestResponseCompleted(strings.Repeat("a", limit))
		if protocol == "chat" {
			body = degradationTestChatChunk(strings.Repeat("a", limit), "stop") + "data: [DONE]\n\n"
		}
		output, err := degradationTestCall(t, protocol, body, limit)
		if err != nil || len(output) != limit {
			t.Fatalf("boundary output length=%d error=%v", len(output), err)
		}
	}
}

// A reasoning model streams long reasoning_content before its short answer.
// Those bytes are ignored for the result but still count toward the wire budget,
// so a verbose review must not be reported as a failed call.
func TestDegradationTransportReasoningStreamFitsJudgeBudget(t *testing.T) {
	var body strings.Builder
	reasoning := strings.Repeat("r", 8*1024)
	for i := 0; i < 64; i++ {
		data, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"index": 0, "delta": map[string]string{"reasoning_content": reasoning}}}})
		body.WriteString("data: " + string(data) + "\n\n")
	}
	body.WriteString(degradationTestChatChunk(`{"status":"normal","reason":"整体成型"}`, "stop"))
	body.WriteString("data: [DONE]\n\n")
	output, err := degradationTestCall(t, "chat", body.String(), groupDegradationJudgeOutputLimit)
	if err != nil || !strings.Contains(output, `"normal"`) {
		t.Fatalf("ignored reasoning exceeded the judge budget: output=%q error=%v", output, err)
	}
}

func TestDegradationTransportHTTPClassificationAndRetryPolicy(t *testing.T) {
	withFastDegradationRetry(t)
	for _, tc := range []struct {
		status  int
		want    string
		retried bool
	}{
		{401, "认证失败", false}, {403, "无访问权限", false}, {404, "端点不存在", false},
		{429, "请求受限", true}, {408, "超时", true}, {504, "超时", true}, {502, "上游服务异常", true}, {307, "禁止重定向", false},
	} {
		t.Run(fmt.Sprint(tc.status), func(t *testing.T) {
			calls := 0
			s := &Service{client: &http.Client{Transport: degradationTestTransport(func(*http.Request) (*http.Response, error) {
				calls++
				return &http.Response{StatusCode: tc.status, Body: io.NopCloser(strings.NewReader("local-secret raw upstream body"))}, nil
			})}}
			_, err := s.degradationChat(context.Background(), "https://offline.invalid", "local-secret", "gpt-6-astra", "none", "", "prompt", time.Second, 64, "")
			wantCalls := 1
			if tc.retried {
				wantCalls = degradationRetryAttempts
			}
			if err == nil || calls != wantCalls || !strings.Contains(err.Error(), fmt.Sprintf("HTTP %d", tc.status)) || !strings.Contains(err.Error(), tc.want) || strings.Contains(err.Error(), "local-secret") || strings.Contains(err.Error(), "raw upstream body") {
				t.Fatalf("calls=%d error=%v", calls, err)
			}
			if retried := strings.Contains(err.Error(), "已重试"); retried != tc.retried {
				t.Fatalf("retry marker=%v error=%v", retried, err)
			}
		})
	}
}

type degradationTestReader func([]byte) (int, error)

func (f degradationTestReader) Read(p []byte) (int, error) { return f(p) }

func TestDegradationTransportStopsAtCompletionAndHidesReadErrors(t *testing.T) {
	withFastDegradationRetry(t)
	for _, protocol := range []string{"chat", "responses"} {
		body := degradationTestResponseCompleted("answer")
		if protocol == "chat" {
			body = degradationTestChatChunk("answer", "stop") + "data: [DONE]\n\n"
		}
		reads := 0
		s := &Service{client: &http.Client{Transport: degradationTestTransport(func(*http.Request) (*http.Response, error) {
			reader := io.MultiReader(strings.NewReader(body), degradationTestReader(func([]byte) (int, error) {
				reads++
				return 0, errors.New("local-secret read details")
			}))
			return &http.Response{StatusCode: 200, Body: io.NopCloser(reader)}, nil
		})}}
		output, err := s.degradationChat(context.Background(), "https://offline.invalid", "local-secret", "candidate", "none", "", "prompt", time.Second, 64, protocol)
		if err != nil || output != "answer" || reads != 0 {
			t.Fatalf("completion waited for EOF: output=%q reads=%d error=%v", output, reads, err)
		}
	}
	s := &Service{client: &http.Client{Transport: degradationTestTransport(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: io.NopCloser(degradationTestReader(func([]byte) (int, error) {
			return 0, errors.New("local-secret read details")
		}))}, nil
	})}}
	_, err := s.groupDegradationChat(context.Background(), "https://offline.invalid", "local-secret", "candidate", "none", "", "prompt", time.Second, 64)
	if err == nil || !strings.Contains(err.Error(), "读取模型响应失败") || strings.Contains(err.Error(), "local-secret") || strings.Contains(err.Error(), "read details") {
		t.Fatalf("unsafe read error=%v", err)
	}
}

func TestDegradationTransportCancellationAndBodyTimeout(t *testing.T) {
	for _, deadline := range []bool{false, true} {
		ctx, cancel := context.WithCancel(context.Background())
		s := &Service{client: &http.Client{Transport: degradationTestTransport(func(r *http.Request) (*http.Response, error) {
			body := io.MultiReader(strings.NewReader(degradationTestChatChunk("partial", nil)), degradationTestReader(func([]byte) (int, error) {
				if !deadline {
					cancel()
				}
				<-r.Context().Done()
				return 0, fmt.Errorf("local-secret: %w", r.Context().Err())
			}))
			return &http.Response{StatusCode: 200, Body: io.NopCloser(body)}, nil
		})}}
		output, err := s.groupDegradationChat(ctx, "https://offline.invalid", "local-secret", "candidate", "none", "", "prompt", 10*time.Millisecond, 64)
		cancel()
		want := "已取消"
		if deadline {
			want = "超时"
		}
		if output != "" || err == nil || !strings.Contains(err.Error(), want) || strings.Contains(err.Error(), "local-secret") {
			t.Fatalf("deadline=%v output=%q error=%v", deadline, output, err)
		}
	}
}
