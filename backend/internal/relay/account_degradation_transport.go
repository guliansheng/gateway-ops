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
	"strings"
	"time"
)

// These Sub2API test routes actually pass the supplied prompt to the model.
// Responses and Anthropic test routes in older versions still send a fixed hi.
func accountTestAcceptsPrompt(account degradationAccountRemote, protocol string) bool {
	switch strings.ToLower(account.Platform) {
	case "openai":
		return strings.EqualFold(account.Type, "apikey") && protocol == "chat"
	case "gemini", "antigravity":
		return true
	default:
		return false
	}
}

func normalizeAccountDegradationProtocol(protocol string) (string, error) {
	switch protocol = strings.ToLower(strings.TrimSpace(protocol)); protocol {
	case "", "auto":
		return "", nil
	case "chat", "responses", "anthropic", "gemini":
		return protocol, nil
	default:
		return "", errors.New("账号测试协议无效")
	}
}

// Use the same admin endpoint and account routing as the connection test. The
// station, not GatewayOps, resolves hidden credentials, mappings and proxies.
func (s *Service) accountDegradationAdminChat(ctx context.Context, baseURL, adminKey string, accountID int64, model, effort, system, prompt string, timeout time.Duration, limit int) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	if system != "" {
		prompt = system + "\n\n" + prompt
	}
	payload, err := json.Marshal(map[string]any{"model_id": model, "mode": "regular", "prompt": prompt, "reasoning_effort": effort})
	if err != nil {
		return "", errors.New("构造账号测试请求失败")
	}
	endpoint := fmt.Sprintf("%s/api/v1/admin/accounts/%d/test", strings.TrimRight(baseURL, "/"), accountID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return "", errors.New("账号测试地址无效")
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream, application/json")
	req.Header.Set("x-api-key", adminKey)
	client := http.Client{}
	if s.client != nil {
		client = *s.client
	}
	client.Timeout = 0
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return "", degradationContextError(ctx)
		}
		return "", errors.New("账号测试接口连接失败")
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", degradationHTTPError(resp.StatusCode)
	}
	wireLimit := int64(limit)*6 + 65536
	wire := &io.LimitedReader{R: resp.Body, N: wireLimit + 1}
	output, err := readAccountDegradationEvents(wire, limit, int(wireLimit))
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
		return "", errors.New("读取账号测试响应失败（上游错误详情已隐藏）")
	}
	return output, err
}

func readAccountDegradationEvents(reader io.Reader, limit, wireLimit int) (string, error) {
	var output strings.Builder
	finished := false
	consume := func(event, data string) (bool, error) {
		if event == "error" {
			return false, errDegradationUpstream
		}
		var message struct {
			Type    string          `json:"type"`
			Text    string          `json:"text"`
			Success bool            `json:"success"`
			Error   json.RawMessage `json:"error"`
		}
		if json.Unmarshal([]byte(data), &message) != nil {
			return false, errDegradationFormat
		}
		if message.Type == "" {
			message.Type = event
		} else if event != "" && event != "message" && event != message.Type {
			return false, errDegradationFormat
		}
		if degradationHasError(message.Error) && string(message.Error) != `""` {
			return false, errDegradationUpstream
		}
		switch message.Type {
		case "content":
			if finished {
				return false, errDegradationUnfinished
			}
			return false, degradationAppend(&output, message.Text, limit)
		case "test_complete":
			if !message.Success || finished {
				return false, errDegradationUnfinished
			}
			finished = true
		case "error":
			return false, errDegradationUpstream
		}
		return false, nil
	}
	// Consume through EOF so a late error after test_complete cannot pass.
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 4096), wireLimit+1)
	var event string
	var data strings.Builder
	dispatch := func() error {
		if data.Len() == 0 && event != "error" {
			return nil
		}
		_, err := consume(event, strings.TrimSuffix(data.String(), "\n"))
		return err
	}
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			if err := dispatch(); err != nil {
				return "", err
			}
			event = ""
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
		}
	}
	if err := scanner.Err(); err != nil {
		return "", err
	}
	if err := dispatch(); err != nil {
		return "", err
	}
	if !finished {
		return "", errDegradationUnfinished
	}
	return degradationNonempty(output.String())
}
