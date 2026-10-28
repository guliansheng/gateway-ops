package relay

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/guliansheng/gateway-ops/internal/storage"
)

func degradationChatResponse(w http.ResponseWriter, content string) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]string{"content": content}, "finish_reason": "stop"}}})
}

func TestGroupDegradationPelicanTwoPassAndRawHTML(t *testing.T) {
	const key = "local-test-secret"
	html := "<!DOCTYPE html>\n<html><body><svg><circle r=\"4\"/></svg></body></html>\n"
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path != "/v1/chat/completions" || r.Method != http.MethodPost || r.Header.Get("Authorization") != "Bearer "+key {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		var body struct {
			Model    string `json:"model"`
			Effort   string `json:"reasoning_effort"`
			Messages []struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if body.Effort != "high" {
			t.Errorf("effort = %q", body.Effort)
		}
		if calls == 1 {
			if body.Model != "candidate" || !strings.Contains(body.Messages[0].Content, "不要依赖我本地的AGENTS.md") || !strings.Contains(body.Messages[0].Content, "prefers-reduced-motion") {
				t.Errorf("generation prompt/model mismatch: %+v", body)
			}
			degradationChatResponse(w, html)
		} else {
			if body.Model != "judge" || len(body.Messages) != 2 || body.Messages[0].Role != "system" || !strings.Contains(body.Messages[1].Content, `<circle r="4"/>`) {
				t.Errorf("judge prompt/model mismatch: %+v", body)
			}
			degradationChatResponse(w, `{"status":"inconclusive","reason":"需要实际渲染才能确定踏频效果"}`)
		}
	}))
	defer server.Close()
	s := &Service{client: &http.Client{Timeout: time.Nanosecond}}
	config := storage.GroupDegradationConfig{Method: "pelican", Model: "candidate", JudgeModel: "judge", Effort: "high"}
	result := s.evaluateGroupDegradation(context.Background(), server.URL, key, config, key)
	if calls != 2 || result.HTML != html || result.Status != "inconclusive" || result.JudgeModel != "judge" || !strings.Contains(result.Reason, "未进行视觉验证") {
		t.Fatalf("result = %+v, calls = %d", result, calls)
	}
	if s.client.Timeout != time.Nanosecond {
		t.Fatal("shared client was mutated")
	}
}

type degradationTestTransport func(*http.Request) (*http.Response, error)

func (f degradationTestTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func TestGroupDegradationCandyVerdictUsesCodeCheck(t *testing.T) {
	calls := 0
	answer := "最少需要 21 颗糖果。"
	transport := degradationTestTransport(func(r *http.Request) (*http.Response, error) {
		w := httptest.NewRecorder()
		calls++
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if _, ok := body["reasoning_effort"]; ok {
			t.Error("none reasoning_effort must be omitted")
		}
		if body["model"] != "candidate" {
			t.Errorf("generation model = %v", body["model"])
		}
		prompt := fmt.Sprint(body["messages"])
		for _, fragment := range []string{"黑色的袋子", "苹果味", "桃子味", "西瓜味", "圆形 | 7 | 9 | 8", "五角星形 | 7 | 6 | 4", "禁止联网", "禁止写代码算出答案", "禁止使用外部工具"} {
			if !strings.Contains(prompt, fragment) {
				t.Errorf("candy prompt missing %q", fragment)
			}
		}
		if strings.Contains(prompt, candyDegradationExpectedAnswer) {
			t.Error("generation prompt leaked reference answer")
		}
		degradationChatResponse(w, answer)
		return w.Result(), nil
	})
	s := &Service{client: &http.Client{Transport: transport}}
	result := s.evaluateGroupDegradation(context.Background(), "https://model.invalid", "key", storage.GroupDegradationConfig{Method: "candy", Model: "candidate", JudgeModel: "judge", Effort: "none"})
	if result.Status != "normal" || result.Answer != answer || result.HTML != "" || calls != 1 || result.JudgeModel != "" || !strings.Contains(result.Reason, "代码判定") {
		t.Fatalf("result = %+v calls = %d", result, calls)
	}
}

func TestCandyDegradationVerdictMatchesWholeReferenceAnswer(t *testing.T) {
	for _, tc := range []struct{ name, answer, expected, wantStatus string }{
		{"exact", "答案是 21。", "21", "normal"},
		{"with unit", "最少需要21颗糖果", "21", "normal"},
		{"full width digits", "答案是 ２１ 颗", "21", "normal"},
		{"longer number keeps fragment", "答案是 210 颗", "21", "degraded"},
		{"embedded fragment", "答案是 121 颗", "21", "degraded"},
		{"missing reference", "答案为 20 颗", "21", "degraded"},
		{"custom reference", "结论 forty-two", "forty-two", "normal"},
		{"custom reference differs", "结论 forty three", "forty-two", "degraded"},
		{"empty reference", "答案 21", "", "inconclusive"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			status, reason := candyDegradationVerdict(tc.answer, tc.expected)
			if status != tc.wantStatus || !strings.Contains(reason, "代码判定") {
				t.Fatalf("status = %q reason = %q", status, reason)
			}
			if tc.expected != "" && strings.Contains(reason, tc.expected) {
				t.Errorf("reason leaked the reference answer: %q", reason)
			}
		})
	}
}

func TestGroupDegradationInvalidHTMLRetainedWithoutJudge(t *testing.T) {
	for _, output := range []string{"", "not HTML <svg></svg>", "<html><body><svg>", "<html><!-- <svg></svg> --></html>"} {
		t.Run(output, func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++; degradationChatResponse(w, output) }))
			defer server.Close()
			s := &Service{client: server.Client()}
			result := s.evaluateGroupDegradation(context.Background(), server.URL, "key", storage.GroupDegradationConfig{Method: "pelican", Model: "candidate", Effort: "medium"})
			wantStatus := "degraded"
			if output == "" {
				wantStatus = "error"
			}
			if calls != 1 || result.Status != wantStatus || result.HTML != output {
				t.Fatalf("result = %+v, calls = %d", result, calls)
			}
		})
	}
}

func TestGroupDegradationJudgeFailurePreservesOutputAndRedacts(t *testing.T) {
	withFastDegradationRetry(t)
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			degradationChatResponse(w, "<html><body><svg></svg></body></html> local-secret sk-unsafe123 Bearer leaked123")
			return
		}
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte("admin-secret local-secret raw upstream details"))
	}))
	defer server.Close()
	s := &Service{client: server.Client()}
	result := s.evaluateGroupDegradation(context.Background(), server.URL, "local-secret", storage.GroupDegradationConfig{Method: "pelican", Model: "candidate", Effort: "low"}, "local-secret", "admin-secret")
	serialized, _ := json.Marshal(result)
	if result.Status != "inconclusive" || result.HTML == "" || !strings.Contains(result.Reason, "HTTP 502") || !strings.Contains(result.Reason, "人工确认") {
		t.Fatalf("result = %+v", result)
	}
	for _, secret := range []string{"local-secret", "admin-secret", "sk-unsafe123", "leaked123", "raw upstream details"} {
		if strings.Contains(string(serialized), secret) {
			t.Errorf("leaked %q", secret)
		}
	}
}

func TestGroupDegradationChatBoundsAndTimeout(t *testing.T) {
	for _, tc := range []struct{ name, body, want string }{
		{"oversized decoded", strings.Repeat("a", 65), "超过大小上限"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { degradationChatResponse(w, tc.body) }))
			defer server.Close()
			s := &Service{client: server.Client()}
			_, err := s.groupDegradationChat(context.Background(), server.URL, "secret", "model", "medium", "", "prompt", time.Second, 64)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v", err)
			}
		})
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(100 * time.Millisecond):
		}
	}))
	defer server.Close()
	s := &Service{client: server.Client()}
	_, err := s.groupDegradationChat(context.Background(), server.URL, "secret", "model", "medium", "", "prompt", 20*time.Millisecond, 64)
	if err == nil || !strings.Contains(err.Error(), "超时") || strings.Contains(err.Error(), server.URL) {
		t.Fatalf("timeout error = %v", err)
	}
}

func TestGroupDegradationRedirectDoesNotForwardCredential(t *testing.T) {
	forwarded := false
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { forwarded = true }))
	defer destination.Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, destination.URL, http.StatusTemporaryRedirect)
	}))
	defer server.Close()
	s := &Service{client: server.Client()}
	_, err := s.groupDegradationChat(context.Background(), server.URL, "secret", "model", "none", "", "prompt", time.Second, 64)
	if err == nil || forwarded || !strings.Contains(err.Error(), "307") {
		t.Fatalf("redirect forwarded=%v err=%v", forwarded, err)
	}
}

func TestGroupDegradationJudgmentAndHTML(t *testing.T) {
	for _, raw := range []string{`{"status":"error","reason":"x"}`, `{"status":"normal","reason":""}`, `normal`, `{"status":"normal","reason":"x"} trailing`} {
		if status, _ := parseDegradationJudgment(raw); status != "inconclusive" {
			t.Fatalf("accepted invalid judgment %q", raw)
		}
	}
	if got := stripDegradationFence("```html\n<html><svg/></html>\n```"); got != "<html><svg/></html>" || !validDegradationHTML(got) {
		t.Fatalf("HTML = %q", got)
	}
	if validDegradationHTML(`<html><script>"<svg></svg>"</script></html>`) {
		t.Fatal("SVG in script is not markup")
	}
}

func TestGroupDegradationConcurrencyBound(t *testing.T) {
	s := &Service{}
	releases := []func(){}
	for i := range s.manualDegradationSlots {
		release, ok := s.acquireGroupDegradationSlot()
		if !ok {
			t.Fatalf("manual slot %d unavailable", i)
		}
		releases = append(releases, release)
	}
	if _, ok := s.acquireGroupDegradationSlot(); ok {
		t.Fatalf("more than %d manual calls admitted", len(releases))
	}
	releases[0]()
	extra, ok := s.acquireGroupDegradationSlot()
	if !ok {
		t.Fatal("released slot unavailable")
	}
	extra()
	for _, release := range releases[1:] {
		release()
	}
}

func TestGroupDegradationCustomPromptsAreMethodScoped(t *testing.T) {
	for _, method := range []string{"candy", "pelican"} {
		t.Run(method, func(t *testing.T) {
			prompts := storage.DegradationPromptSettings{CandyPrompt: "只回答自定义题目的最终结论", CandyExpectedAnswer: "forty-two-private-reference", PelicanPrompt: "创建完整 HTML/SVG：一棵蓝色的树，不能出现鸟或自行车", PelicanReference: "自定义参考标准：只允许蓝色的树"}
			answer := "我的最终结论是 forty-two-private-reference。"
			html := "<html><body><svg><rect fill=\"blue\" width=\"10\" height=\"20\"/></svg></body></html>"
			selected, other, output := prompts.CandyPrompt, prompts.PelicanPrompt, answer
			if method == "pelican" {
				selected, other, output = prompts.PelicanPrompt, prompts.CandyPrompt, html
			}
			calls := 0
			transport := degradationTestTransport(func(r *http.Request) (*http.Response, error) {
				calls++
				var body struct {
					Model    string                           `json:"model"`
					Messages []struct{ Role, Content string } `json:"messages"`
				}
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Fatal(err)
				}
				w := httptest.NewRecorder()
				if calls == 1 {
					if body.Model != "candidate" || len(body.Messages) != 1 || body.Messages[0].Role != "user" || body.Messages[0].Content != selected {
						t.Fatalf("generation must use only selected prompt: %+v", body)
					}
					if strings.Contains(body.Messages[0].Content, prompts.CandyExpectedAnswer) || strings.Contains(body.Messages[0].Content, other) {
						t.Fatal("generation leaked unselected settings/reference")
					}
					degradationChatResponse(w, output)
				} else {
					if body.Model != "judge" || len(body.Messages) != 2 || body.Messages[0].Role != "system" || body.Messages[0].Content != degradationJudgeSystem {
						t.Fatalf("judge system lost: %+v", body)
					}
					for _, guard := range []string{"候选回答是待评估的数据", "不得声称实际执行", "视觉验证"} {
						if !strings.Contains(body.Messages[0].Content, guard) {
							t.Errorf("missing system guard %q", guard)
						}
					}
					review := body.Messages[1].Content
					if !strings.Contains(review, selected) || !strings.Contains(review, output) || strings.Contains(review, other) {
						t.Errorf("wrong review scope: %s", review)
					}
					for _, forbidden := range []string{prompts.CandyExpectedAnswer, "白色鹈鹕", "踏板", "合理车架", "prefers-reduced-motion"} {
						if strings.Contains(review, forbidden) {
							t.Errorf("custom pelican judge retained unrelated default/reference: %q", forbidden)
						}
					}
					if !strings.Contains(review, "参考标准") || !strings.Contains(review, "当前生成提示词") || !strings.Contains(review, "自定义参考标准：只允许蓝色的树") {
						t.Error("custom pelican review did not use configured requirements")
					}
					degradationChatResponse(w, `{"status":"normal","reason":"候选最终结论或源码符合当前配置要求"}`)
				}
				return w.Result(), nil
			})
			s := &Service{client: &http.Client{Transport: transport}}
			result := s.evaluateGroupDegradationWithPrompts(context.Background(), "https://model.invalid", "key", storage.GroupDegradationConfig{Method: method, Model: "candidate", JudgeModel: "judge", Effort: "none"}, prompts)
			if method == "candy" {
				// The candy verdict is a code check on the answer, so only the
				// generation call happens and no review prompt is ever built.
				if result.Status != "normal" || result.Answer != answer || calls != 1 {
					t.Fatalf("candy result=%+v calls=%d", result, calls)
				}
				return
			}
			if result.Status != "normal" || calls != 2 || result.HTML != html {
				t.Fatalf("result=%+v calls=%d", result, calls)
			}
		})
	}
}

// The pelican review must keep judging linked motion and the other key items:
// a rider whose legs only swing from the hip while the pedals turn separately is
// exactly what a degraded model produces. Cosmetic gaps stay out of the verdict.
func TestPelicanReviewKeepsKeyCriteriaAndIgnoresCosmetics(t *testing.T) {
	var review string
	chat := func(_ context.Context, model, _ string, _ string, prompt string, _ time.Duration, _ int) (string, error) {
		if model == "judge" {
			review = prompt
			return `{"status":"degraded","reason":"双腿未与踏板同步"}`, nil
		}
		return "<html><svg viewBox='0 0 10 10'></svg></html>", nil
	}
	config := storage.GroupDegradationConfig{Method: "pelican", Model: "candidate", JudgeModel: "judge", Effort: "none"}
	prompts := storage.DegradationPromptSettings{PelicanPrompt: "画一只鸟骑车", PelicanReference: storage.DefaultPelicanDegradationReference}
	if result := (&Service{}).evaluateDegradation(context.Background(), config, prompts, chat); result.Status != "degraded" {
		t.Fatalf("result = %+v", result)
	}
	for _, fragment := range []string{"关键功能项", "任一项不满足即判 degraded", "联动动作真实成立", "不作为降智依据", "当前生成提示词"} {
		if !strings.Contains(review, fragment) {
			t.Fatalf("pelican review lost %q: %s", fragment, review)
		}
	}
}

func TestGroupDegradationPrivateReviewEchoRedaction(t *testing.T) {
	prompts := storage.DegradationPromptSettings{CandyPrompt: "private question\nsecond line", CandyExpectedAnswer: "private-answer", PelicanPrompt: "private drawing", PelicanReference: "private reference\nsecond line"}
	calls := 0
	s := &Service{client: &http.Client{Transport: degradationTestTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		w := httptest.NewRecorder()
		if calls == 1 {
			degradationChatResponse(w, "<html><body><svg></svg></body></html>")
		} else {
			judgment, _ := json.Marshal(map[string]string{"status": "normal", "reason": prompts.PelicanReference + " / " + prompts.PelicanPrompt})
			degradationChatResponse(w, string(judgment))
		}
		return w.Result(), nil
	})}}
	result := s.evaluateGroupDegradationWithPrompts(context.Background(), "https://model.invalid", "key", storage.GroupDegradationConfig{Method: "pelican", Model: "candidate"}, prompts)
	if result.Status != "normal" || !strings.Contains(result.Reason, "[REDACTED]") || strings.Contains(result.Reason, prompts.PelicanPrompt) || strings.Contains(result.Reason, "private reference") {
		t.Fatalf("private review echo leaked: %+v", result)
	}
}

func TestGroupDegradationHTMLMustBeCompleteAndSelfContained(t *testing.T) {
	for _, source := range []string{
		`<svg></svg><html></html>`,
		`<html><svg></svg><svg></html>`,
		`<html><svg/></html><svg/>`,
		`<html><svg><image href="https://external.invalid/a.png"/></svg></html>`,
		`<html><svg><use xlink:href="/external.svg#shape"/></svg></html>`,
		`<html><svg/><script src="//external.invalid/app.js"></script></html>`,
		`<html><svg/><link rel="stylesheet" href="/style.css"></html>`,
		`<html><svg/><style>body{background:url(https://external.invalid/bg.png)}</style></html>`,
		`<html><svg/><style>@import "https://external.invalid/style.css";</style></html>`,
		`<html><svg style="fill:url('/external.svg#shape')"/></html>`,
	} {
		if validDegradationHTML(source) {
			t.Errorf("accepted incomplete/external document: %s", source)
		}
	}
	for _, source := range []string{
		`<!DOCTYPE html><html><body><svg xmlns="http://www.w3.org/2000/svg"><defs><linearGradient id="g"/></defs><path fill="url(#g)"/><use href="#g"/></svg></body></html>`,
		`<html><svg/><style>body{background:url("data:image/svg+xml;base64,PHN2Zy8+")}</style><img src="data:image/png;base64,aA=="></html>`,
	} {
		if !validDegradationHTML(source) {
			t.Errorf("rejected self-contained document: %s", source)
		}
	}
	for _, method := range []string{"pelican"} {
		calls := 0
		output := `<html><svg><image href="https://external.invalid/a.png"/></svg></html>`
		s := &Service{client: &http.Client{Transport: degradationTestTransport(func(r *http.Request) (*http.Response, error) {
			calls++
			w := httptest.NewRecorder()
			degradationChatResponse(w, output)
			return w.Result(), nil
		})}}
		result := s.evaluateGroupDegradationWithPrompts(context.Background(), "https://model.invalid", "key", storage.GroupDegradationConfig{Method: method, Model: "candidate"}, storage.DefaultDegradationPromptSettings())
		if calls != 1 || result.Status != "degraded" || result.HTML != output {
			t.Fatalf("external document reached judge: %+v calls=%d", result, calls)
		}
	}
}

func TestGroupDegradationEvaluateInjectedChat(t *testing.T) {
	for _, method := range []string{"candy", "pelican"} {
		t.Run(method, func(t *testing.T) {
			prompts := storage.DegradationPromptSettings{CandyPrompt: "private question", CandyExpectedAnswer: "21", PelicanPrompt: "private drawing"}
			config := storage.GroupDegradationConfig{Method: method, Model: "candidate", JudgeModel: "judge", Effort: "high", Protocol: "responses"}
			calls := 0
			chat := func(ctx context.Context, model, effort, system, prompt string, timeout time.Duration, limit int) (string, error) {
				calls++
				if effort != config.Effort || ctx.Err() != nil {
					t.Fatalf("wrong effort/context: %q %v", effort, ctx.Err())
				}
				if calls == 1 {
					want := prompts.CandyPrompt
					if method == "pelican" {
						want = prompts.PelicanPrompt
					}
					if model != "candidate" || system != "" || prompt != want || timeout != 6*time.Minute || limit != groupDegradationOutputLimit {
						t.Fatalf("wrong generation arguments: %q %q %q %v %d", model, system, prompt, timeout, limit)
					}
					if method == "pelican" {
						return "<html>\n  <svg id=\"injected-secret\"/>\n</html>", nil
					}
					return "final answer 21 injected-secret", nil
				}
				if model != "judge" || system != degradationJudgeSystem || timeout != groupDegradationJudgeTimeout || limit != groupDegradationJudgeOutputLimit || strings.Contains(prompt, "injected-secret") || !strings.Contains(prompt, "[REDACTED]") {
					t.Fatalf("wrong judge arguments: %q %q %v %d", model, prompt, timeout, limit)
				}
				return `{"status":"normal","reason":"符合配置要求"}`, nil
			}
			result := (&Service{}).evaluateDegradation(context.Background(), config, prompts, chat, "injected-secret")
			wantCalls := 2
			if method == "candy" {
				wantCalls = 1
			}
			if calls != wantCalls || result.Status != "normal" || strings.Contains(result.Answer+result.HTML, "injected-secret") {
				t.Fatalf("result=%+v calls=%d", result, calls)
			}
		})
	}
	if groupDegradationRunTimeout < groupDegradationGenerationTimeout+groupDegradationJudgeTimeout+20*time.Second || groupDegradationLease < groupDegradationRunTimeout+30*time.Second {
		t.Fatal("run timeout and lease do not leave time for discovery and persistence")
	}
}

func TestGroupDegradationGenerationAndJudgeProtocols(t *testing.T) {
	for _, tc := range []struct{ candidate, judge, protocol, generationPath, judgePath string }{
		{"custom", "", "responses", "/v1/responses", "/v1/responses"},
		{"custom", "claude-test", "responses", "/v1/responses", "/v1/chat/completions"},
		{"gpt-6-astra", "gpt-6.1-sol", "", "/v1/responses", "/v1/responses"},
		{"claude-test", "gpt-6.1-sol", "", "/v1/chat/completions", "/v1/responses"},
	} {
		t.Run(tc.candidate+"/"+tc.judge+"/"+tc.protocol, func(t *testing.T) {
			calls := 0
			s := &Service{client: &http.Client{Transport: degradationTestTransport(func(r *http.Request) (*http.Response, error) {
				calls++
				want, text := tc.generationPath, "<html><svg/></html>"
				if calls == 2 {
					want, text = tc.judgePath, `{"status":"normal","reason":"符合配置要求"}`
				}
				if r.URL.Path != want {
					t.Errorf("request %d path=%q want=%q", calls, r.URL.Path, want)
				}
				w := httptest.NewRecorder()
				if want == "/v1/responses" {
					_, _ = w.Write([]byte(degradationTestResponseDelta(text) + degradationTestResponseCompleted(text)))
				} else {
					_, _ = w.Write([]byte(degradationTestChatChunk(text, "stop") + "data: [DONE]\n\n"))
				}
				return w.Result(), nil
			})}}
			result := s.evaluateGroupDegradation(context.Background(), "https://offline.invalid", "key", storage.GroupDegradationConfig{Method: "pelican", Model: tc.candidate, JudgeModel: tc.judge, Protocol: tc.protocol})
			if result.Status != "normal" || result.HTML != "<html><svg/></html>" || calls != 2 {
				t.Fatalf("result=%+v calls=%d", result, calls)
			}
		})
	}
}

func TestGroupDegradationTruncatedGenerationNeverDegraded(t *testing.T) {
	for _, tc := range []struct{ model, body string }{
		{"claude-test", degradationTestChatChunk("<html><svg>", nil)},
		{"claude-test", degradationTestChatChunk("<html><svg>", "length") + "data: [DONE]\n\n"},
		{"gpt-6-astra", degradationTestResponseDelta("<html><svg>")},
		{"gpt-6-astra", degradationTestResponseDelta("<html><svg>") + degradationTestResponseEvent("response.incomplete", `{"response":{"status":"incomplete"}}`)},
	} {
		calls := 0
		s := &Service{client: &http.Client{Transport: degradationTestTransport(func(*http.Request) (*http.Response, error) {
			calls++
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(tc.body))}, nil
		})}}
		result := s.evaluateGroupDegradation(context.Background(), "https://offline.invalid", "key", storage.GroupDegradationConfig{Method: "pelican", Model: tc.model})
		if result.Status != "error" || result.HTML != "" || calls != 1 || !strings.Contains(result.Reason, "生成失败") {
			t.Fatalf("truncated response was judged: result=%+v calls=%d", result, calls)
		}
	}
}

// The review prompt used to carry the drawing verbatim, indentation included,
// which is what made a slow judge time out on 30KB documents.
func TestCompactDegradationEvidenceKeepsEveryElement(t *testing.T) {
	source := "<html>\n  <!-- hidden -->\n  <style>\n    .p { fill: #fff; }\n  </style>\n\n  <svg id=\"a\">\n    <circle r=\"4\"/>\n  </svg>\n</html>\n"
	compacted := compactDegradationEvidence(source)
	if strings.Contains(compacted, "hidden") || strings.Contains(compacted, "\n\n") || strings.Contains(compacted, "  <") {
		t.Fatalf("compaction left noise: %q", compacted)
	}
	for _, keep := range []string{"<style>", ".p { fill: #fff; }", `<svg id="a">`, `<circle r="4"/>`} {
		if !strings.Contains(compacted, keep) {
			t.Fatalf("compaction dropped %q: %q", keep, compacted)
		}
	}
	if len(compacted) >= len(source) {
		t.Fatalf("compaction did not shrink the evidence: %d -> %d", len(source), len(compacted))
	}
}
