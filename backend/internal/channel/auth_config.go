package channel

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/guliansheng/gateway-ops/internal/connector"
	"github.com/guliansheng/gateway-ops/internal/storage"
)

type RequestKV struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

func DefaultLoginConfig(channelType storage.ChannelType) (headers, params []RequestKV) {
	headers = []RequestKV{{Key: "Content-Type", Value: "application/json"}}
	switch channelType {
	case storage.ChannelTypeNewAPI:
		params = []RequestKV{{Key: "username", Value: "{{username}}"}, {Key: "password", Value: "{{password}}"}}
	case storage.ChannelTypeSub2API:
		params = []RequestKV{
			{Key: "email", Value: "{{username}}"},
			{Key: "password", Value: "{{password}}"},
			{Key: "turnstile_token", Value: "{{turnstile_token}}"},
		}
	default:
		params = []RequestKV{}
	}
	return
}

func ParseLoginConfig(channelType storage.ChannelType, headersJSON, paramsJSON string) ([]RequestKV, []RequestKV, error) {
	defaultHeaders, defaultParams := DefaultLoginConfig(channelType)
	headers, err := parseRequestKVJSON(headersJSON, defaultHeaders)
	if err != nil {
		return nil, nil, fmt.Errorf("parse login headers: %w", err)
	}
	params, err := parseRequestKVJSON(paramsJSON, defaultParams)
	if err != nil {
		return nil, nil, fmt.Errorf("parse login params: %w", err)
	}
	return headers, params, nil
}

func parseRequestKVJSON(raw string, fallback []RequestKV) ([]RequestKV, error) {
	if strings.TrimSpace(raw) == "" {
		return append([]RequestKV(nil), fallback...), nil
	}
	var items []RequestKV
	if err := json.Unmarshal([]byte(raw), &items); err != nil {
		return nil, err
	}
	return normalizeRequestKV(items), nil
}

func normalizeRequestKV(items []RequestKV) []RequestKV {
	out := make([]RequestKV, 0, len(items))
	for _, item := range items {
		item.Key = strings.TrimSpace(item.Key)
		if item.Key != "" {
			out = append(out, item)
		}
	}
	return out
}

func EncodeRequestKV(items []RequestKV) (string, error) {
	encoded, err := json.Marshal(normalizeRequestKV(items))
	if err != nil {
		return "", err
	}
	return string(encoded), nil
}

func encodeOptionalRequestKV(items *[]RequestKV) (string, error) {
	if items == nil {
		return "", nil
	}
	return EncodeRequestKV(*items)
}

func toConnectorRequestKV(items []RequestKV) []connector.RequestKV {
	out := make([]connector.RequestKV, 0, len(items))
	for _, item := range items {
		out = append(out, connector.RequestKV{Key: item.Key, Value: item.Value})
	}
	return out
}

func ExpandRequestKV(items []RequestKV, vars map[string]string) map[string]string {
	out := make(map[string]string, len(items))
	for _, item := range normalizeRequestKV(items) {
		value := item.Value
		for key, replacement := range vars {
			value = strings.ReplaceAll(value, "{{"+key+"}}", replacement)
		}
		if strings.Contains(item.Value, "{{turnstile_token}}") && strings.TrimSpace(vars["turnstile_token"]) == "" {
			continue
		}
		out[item.Key] = value
	}
	return out
}
