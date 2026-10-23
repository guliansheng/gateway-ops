package relay

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

var (
	errDegradationWireLimit  = errors.New("模型响应超过大小上限")
	errDegradationTextLimit  = errors.New("模型输出超过大小上限")
	errDegradationFormat     = errors.New("模型响应格式无效")
	errDegradationUnfinished = errors.New("模型响应未完整结束，不能用于能力判定")
	errDegradationTruncated  = errors.New("模型输出因长度限制被截断，不能用于能力判定")
	errDegradationEmpty      = errors.New("模型返回空内容，不能用于能力判定")
	errDegradationRefused    = errors.New("模型拒绝回答或内容被过滤，不能用于能力判定")
	errDegradationUpstream   = errors.New("模型上游返回错误事件（上游错误详情已隐藏）")
	errDegradationConnection = errors.New("模型网关连接失败（上游错误详情已隐藏）")
	errDegradationRead       = errors.New("读取模型响应失败（上游错误详情已隐藏）")
)

const (
	degradationRetryAttempts     = 3
	degradationRetryMinRemaining = 30 * time.Second
)

// degradationRetryBackoff is the wait before each retry. Tests shorten it so
// transport assertions stay fast.
var degradationRetryBackoff = []time.Duration{2 * time.Second, 6 * time.Second}

// degradationStatusError keeps the numeric status so retries can tell a transient
// gateway failure (502/503/504) from a deterministic client error (401/404).
type degradationStatusError struct {
	Status  int
	Message string
}

func (e degradationStatusError) Error() string { return e.Message }

func degradationProtocol(model, protocol string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(protocol)) {
	case "responses":
		return "responses", nil
	case "chat", "chat_completions", "chat/completions":
		return "chat", nil
	case "", "auto":
		model = strings.ToLower(strings.TrimSpace(model))
		// Also recognize provider-qualified OpenAI model IDs.
		if index := strings.LastIndexByte(model, '/'); index >= 0 {
			model = model[index+1:]
		}
		if strings.HasPrefix(model, "gpt-") || strings.HasPrefix(model, "chatgpt-") {
			return "responses", nil
		}
		for _, prefix := range []string{"o1", "o3", "o4"} {
			if model == prefix || strings.HasPrefix(model, prefix+"-") || strings.HasPrefix(model, prefix+".") {
				return "responses", nil
			}
		}
		return "chat", nil
	default:
		return "", errors.New("降智检测模型协议无效")
	}
}

// degradationChat never exposes transport errors, URLs, or raw upstream bodies.
// Both protocols request streams, but JSON-only compatible gateways also work.
//
// Transient gateway failures (502/503/504, connection resets, mid-stream read
// errors) are retried: one upstream hiccup should not waste a scheduled run or a
// paid manual test. Deterministic failures (auth, format, refusal, timeout) are
// returned immediately.
func (s *Service) degradationChat(ctx context.Context, baseURL, key, model, effort, system, prompt string, timeout time.Duration, limit int, protocol string) (string, error) {
	protocol, err := degradationProtocol(model, protocol)
	if err != nil {
		return "", err
	}
	if limit <= 0 || limit > groupDegradationOutputLimit {
		return "", errors.New("模型输出大小上限无效")
	}
	for attempt := 1; ; attempt++ {
		output, err := s.degradationChatAttempt(ctx, baseURL, key, model, effort, system, prompt, timeout, limit, protocol)
		if err == nil {
			return output, nil
		}
		if attempt >= degradationRetryAttempts || !degradationRetryable(err) || !degradationRetryWait(ctx, attempt) {
			if attempt > 1 {
				return "", fmt.Errorf("%w；已重试 %d 次", err, attempt-1)
			}
			return "", err
		}
	}
}

// degradationChatAttempt performs exactly one paid model call.
func (s *Service) degradationChatAttempt(ctx context.Context, baseURL, key, model, effort, system, prompt string, timeout time.Duration, limit int, protocol string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	messages := []map[string]string{}
	if system != "" {
		messages = append(messages, map[string]string{"role": "system", "content": system})
	}
	messages = append(messages, map[string]string{"role": "user", "content": prompt})
	body := map[string]any{"model": model, "stream": true}
	endpoint := "/v1/chat/completions"
	if protocol == "responses" {
		endpoint = "/v1/responses"
		body["input"] = messages
		if effort != "none" && effort != "" {
			body["reasoning"] = map[string]string{"effort": effort}
		}
	} else {
		body["messages"] = messages
		if effort != "none" && effort != "" {
			body["reasoning_effort"] = effort
		}
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return "", errors.New("无法构造模型请求")
	}
	gateway, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil || gateway.Host == "" || (gateway.Scheme != "http" && gateway.Scheme != "https") || gateway.User != nil || gateway.RawQuery != "" || gateway.Fragment != "" {
		return "", errors.New("模型网关地址无效")
	}
	gateway.Path = strings.TrimSuffix(strings.TrimRight(gateway.Path, "/"), "/v1") + endpoint
	gateway.RawPath = ""
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, gateway.String(), bytes.NewReader(payload))
	if err != nil {
		return "", errors.New("模型网关地址无效")
	}
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream, application/json")
	client := http.Client{}
	if s.client != nil {
		client = *s.client
	}
	client.Timeout = 0 // Each generation/review has its own bounded context.
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return "", degradationContextError(ctx)
		}
		return "", errDegradationConnection
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", degradationHTTPError(resp.StatusCode)
	}
	// JSON escaping and SSE envelopes can exceed the decoded output by several
	// times. Limit all wire bytes, including ignored reasoning/keepalive events;
	// reasoning models emit long streams before the short answer, so the slack is
	// generous enough that a verbose review is not mistaken for a failed call.
	wireLimit := int64(limit)*12 + 64*1024
	wire := &io.LimitedReader{R: resp.Body, N: wireLimit + 1}
	reader := bufio.NewReader(wire)
	output, err := readDegradationOutput(reader, protocol, limit, int(wireLimit))
	if ctx.Err() != nil {
		return "", degradationContextError(ctx)
	}
	if wire.N == 0 {
		return "", errDegradationWireLimit
	}
	if err != nil {
		for _, safe := range []error{errDegradationWireLimit, errDegradationTextLimit, errDegradationFormat, errDegradationUnfinished, errDegradationTruncated, errDegradationEmpty, errDegradationRefused, errDegradationUpstream} {
			if errors.Is(err, safe) {
				return "", safe
			}
		}
		return "", errDegradationRead
	}
	return output, nil
}

func degradationContextError(ctx context.Context) error {
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return errors.New("模型请求超时")
	}
	return errors.New("模型请求已取消")
}

func degradationHTTPError(status int) error {
	category := "上游请求失败"
	switch {
	case status >= 300 && status < 400:
		category = "禁止重定向"
	case status == http.StatusUnauthorized:
		category = "认证失败，请检查密钥"
	case status == http.StatusForbidden:
		category = "无访问权限，请检查模型权限或请求协议"
	case status == http.StatusNotFound:
		category = "模型或协议端点不存在"
	case status == http.StatusRequestTimeout || status == http.StatusGatewayTimeout:
		category = "上游请求超时"
	case status == http.StatusTooManyRequests:
		category = "请求受限或额度不足"
	case status >= 500:
		category = "上游服务异常"
	}
	return degradationStatusError{Status: status, Message: fmt.Sprintf("模型网关返回 HTTP %d（%s；上游错误详情已隐藏）", status, category)}
}

// degradationRetryable reports whether a failed call may succeed when repeated.
// Only transport-level hiccups are retried: a 401/404, a malformed response or a
// refusal will answer the same way on a second paid call.
func degradationRetryable(err error) bool {
	var status degradationStatusError
	if errors.As(err, &status) {
		switch {
		case status.Status == http.StatusRequestTimeout, status.Status == http.StatusTooManyRequests, status.Status == http.StatusTooEarly:
			return true
		case status.Status >= 500 && status.Status <= 599:
			return true
		}
		return false
	}
	return errors.Is(err, errDegradationConnection) || errors.Is(err, errDegradationRead)
}

// degradationRetryWait sleeps before the next attempt. A retry that cannot
// finish inside the remaining run budget is skipped so the run still records a
// result instead of being killed by the lease.
func degradationRetryWait(ctx context.Context, attempt int) bool {
	wait := time.Duration(0)
	if len(degradationRetryBackoff) > 0 {
		wait = degradationRetryBackoff[min(attempt-1, len(degradationRetryBackoff)-1)]
	}
	if deadline, ok := ctx.Deadline(); ok && time.Until(deadline) < wait+degradationRetryMinRemaining {
		return false
	}
	if wait <= 0 {
		return ctx.Err() == nil
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

type degradationChatReply struct {
	Error   json.RawMessage `json:"error"`
	Choices []struct {
		Index   int `json:"index"`
		Message struct {
			Content string `json:"content"`
			Refusal string `json:"refusal"`
		} `json:"message"`
		Delta struct {
			Content string `json:"content"`
			Refusal string `json:"refusal"`
		} `json:"delta"`
		FinishReason *string `json:"finish_reason"`
	} `json:"choices"`
}

type degradationResponsesReply struct {
	Status            string          `json:"status"`
	Error             json.RawMessage `json:"error"`
	IncompleteDetails json.RawMessage `json:"incomplete_details"`
	OutputText        string          `json:"output_text"`
	Output            []struct {
		Type    string `json:"type"`
		Status  string `json:"status"`
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	} `json:"output"`
}

func degradationHasError(raw json.RawMessage) bool {
	return len(raw) != 0 && string(raw) != "null"
}

func degradationFinish(reason *string) error {
	if reason == nil {
		return errDegradationUnfinished
	}
	switch *reason {
	case "stop":
		return nil
	case "length", "max_tokens", "max_output_tokens":
		return errDegradationTruncated
	case "content_filter":
		return errDegradationRefused
	default:
		return errDegradationUnfinished
	}
}

func degradationAppend(output *strings.Builder, text string, limit int) error {
	if len(text) > limit-output.Len() {
		return errDegradationTextLimit
	}
	output.WriteString(text)
	return nil
}

func degradationNonempty(output string) (string, error) {
	if strings.TrimSpace(output) == "" {
		return "", errDegradationEmpty
	}
	return output, nil
}

func degradationResponseText(response degradationResponsesReply, limit int) (string, error) {
	if degradationHasError(response.Error) || response.Status == "failed" {
		return "", errDegradationUpstream
	}
	if response.Status != "completed" || degradationHasError(response.IncompleteDetails) {
		return "", errDegradationUnfinished
	}
	var output strings.Builder
	for _, item := range response.Output {
		if item.Status != "" && item.Status != "completed" {
			return "", errDegradationUnfinished
		}
		for _, part := range item.Content {
			if part.Type == "refusal" {
				return "", errDegradationRefused
			}
			if item.Type == "message" && part.Type == "output_text" {
				if err := degradationAppend(&output, part.Text, limit); err != nil {
					return "", err
				}
			}
		}
	}
	if output.Len() == 0 {
		if err := degradationAppend(&output, response.OutputText, limit); err != nil {
			return "", err
		}
	}
	return output.String(), nil
}

func readDegradationOutput(reader *bufio.Reader, protocol string, limit, wireLimit int) (string, error) {
	// Sniff the payload rather than trusting gateways' sometimes incorrect
	// Content-Type. Whitespace (and a possible UTF-8 BOM) is still wire-bounded.
	if prefix, _ := reader.Peek(3); bytes.Equal(prefix, []byte{0xef, 0xbb, 0xbf}) {
		_, _ = reader.Discard(3)
	}
	for {
		char, err := reader.ReadByte()
		if err != nil {
			if errors.Is(err, io.EOF) {
				return "", errDegradationEmpty
			}
			return "", err
		}
		if char == ' ' || char == '\n' || char == '\r' || char == '\t' {
			continue
		}
		_ = reader.UnreadByte()
		if char != '{' {
			return readDegradationSSE(reader, protocol, limit, wireLimit)
		}
		data, err := io.ReadAll(reader)
		if err != nil {
			return "", err
		}
		if len(data) > wireLimit {
			return "", errDegradationWireLimit
		}
		if protocol == "responses" {
			var response degradationResponsesReply
			if json.Unmarshal(data, &response) != nil {
				return "", errDegradationFormat
			}
			output, err := degradationResponseText(response, limit)
			if err != nil {
				return "", err
			}
			return degradationNonempty(output)
		}
		var response degradationChatReply
		if json.Unmarshal(data, &response) != nil {
			return "", errDegradationFormat
		}
		if degradationHasError(response.Error) {
			return "", errDegradationUpstream
		}
		if len(response.Choices) == 0 {
			return "", errDegradationFormat
		}
		choice := response.Choices[0]
		if err := degradationFinish(choice.FinishReason); err != nil {
			return "", err
		}
		if choice.Message.Refusal != "" {
			return "", errDegradationRefused
		}
		if len(choice.Message.Content) > limit {
			return "", errDegradationTextLimit
		}
		return degradationNonempty(choice.Message.Content)
	}
}

func readDegradationSSE(reader io.Reader, protocol string, limit, wireLimit int) (string, error) {
	var output strings.Builder
	finished := false
	consume := func(event, data string) (bool, error) {
		if event == "error" {
			return false, errDegradationUpstream
		}
		if data == "[DONE]" {
			if protocol == "responses" || !finished {
				return false, errDegradationUnfinished
			}
			return true, nil
		}
		if protocol == "responses" {
			var message struct {
				Type     string                     `json:"type"`
				Delta    string                     `json:"delta"`
				Error    json.RawMessage            `json:"error"`
				Response *degradationResponsesReply `json:"response"`
			}
			if json.Unmarshal([]byte(data), &message) != nil {
				return false, errDegradationFormat
			}
			if degradationHasError(message.Error) {
				return false, errDegradationUpstream
			}
			if message.Response != nil {
				if degradationHasError(message.Response.Error) || message.Response.Status == "failed" {
					return false, errDegradationUpstream
				}
				if message.Response.Status == "incomplete" || degradationHasError(message.Response.IncompleteDetails) {
					return false, errDegradationUnfinished
				}
			}
			if message.Type == "" {
				message.Type = event
			} else if event != "" && event != "message" && event != message.Type {
				return false, errDegradationFormat
			}
			switch message.Type {
			case "response.output_text.delta":
				return false, degradationAppend(&output, message.Delta, limit)
			case "response.completed":
				if message.Response == nil {
					return false, errDegradationUnfinished
				}
				text, err := degradationResponseText(*message.Response, limit)
				if err != nil {
					return false, err
				}
				if output.Len() == 0 {
					if err := degradationAppend(&output, text, limit); err != nil {
						return false, err
					}
				} else if text != "" && text != output.String() {
					return false, errDegradationUnfinished
				}
				return true, nil
			case "error", "response.failed":
				return false, errDegradationUpstream
			case "response.incomplete", "response.cancelled":
				return false, errDegradationUnfinished
			case "response.refusal.delta", "response.refusal.done":
				return false, errDegradationRefused
			}
			return false, nil // Lifecycle, reasoning and usage are not candidate text.
		}
		var response degradationChatReply
		if json.Unmarshal([]byte(data), &response) != nil {
			return false, errDegradationFormat
		}
		if degradationHasError(response.Error) {
			return false, errDegradationUpstream
		}
		for _, choice := range response.Choices {
			if choice.Index != 0 {
				continue
			}
			if choice.Delta.Refusal != "" {
				return false, errDegradationRefused
			}
			if finished && choice.Delta.Content != "" {
				return false, errDegradationUnfinished
			}
			if err := degradationAppend(&output, choice.Delta.Content, limit); err != nil {
				return false, err
			}
			if choice.FinishReason != nil {
				if err := degradationFinish(choice.FinishReason); err != nil {
					return false, err
				}
				finished = true
			}
		}
		return false, nil
	}

	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 4096), wireLimit+1)
	var event string
	var data strings.Builder
	hasData := false
	dispatch := func() (bool, error) {
		if !hasData && event != "error" {
			return false, nil
		}
		return consume(event, strings.TrimSuffix(data.String(), "\n"))
	}
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			done, err := dispatch()
			if err != nil {
				return "", err
			}
			if done {
				return degradationNonempty(output.String())
			}
			event, hasData = "", false
			data.Reset()
			continue
		}
		if strings.HasPrefix(line, ":") {
			continue
		}
		field, value, _ := strings.Cut(line, ":")
		value = strings.TrimPrefix(value, " ")
		switch field {
		case "event":
			event = value
		case "data":
			if data.Len()+len(value)+1 > wireLimit {
				return "", errDegradationWireLimit
			}
			data.WriteString(value)
			data.WriteByte('\n')
			hasData = true
		}
	}
	if err := scanner.Err(); err != nil {
		return "", err
	}
	if done, err := dispatch(); err != nil {
		return "", err
	} else if done {
		return degradationNonempty(output.String())
	}
	return "", errDegradationUnfinished
}
