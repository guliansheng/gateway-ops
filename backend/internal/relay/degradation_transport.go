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
)

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
func (s *Service) degradationChat(ctx context.Context, baseURL, key, model, effort, system, prompt string, timeout time.Duration, limit int, protocol string) (string, error) {
	protocol, err := degradationProtocol(model, protocol)
	if err != nil {
		return "", err
	}
	if limit <= 0 || limit > groupDegradationOutputLimit {
		return "", errors.New("模型输出大小上限无效")
	}
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
		return "", errors.New("模型网关连接失败（上游错误详情已隐藏）")
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
		return "", errors.New("读取模型响应失败（上游错误详情已隐藏）")
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
	return fmt.Errorf("模型网关返回 HTTP %d（%s；上游错误详情已隐藏）", status, category)
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
