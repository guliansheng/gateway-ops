package captcha

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/go-resty/resty/v2"
	"github.com/guliansheng/gateway-ops/internal/storage"
)

func init() {
	Register(storage.CaptchaAntiCaptcha, func(c Config) Provider { return newAntiCaptcha(c) })
}

// antiCaptcha 对接 https://anti-captcha.com 的 TurnstileTaskProxyless。
//
// 流程与 2Captcha / CapSolver 完全一致（这套 createTask + getTaskResult 协议本来就是 AntiCaptcha 最早定的，
// 两家后续都按这个形状抄）：
//
//	POST /createTask     -> { errorId, taskId }
//	POST /getTaskResult  -> { status: "ready", solution: { token } } 或 status: "processing"
//
// 在拿到 ready 之前每 2 秒轮询一次，最多 ~120 秒。
type antiCaptcha struct {
	cfg  Config
	http *resty.Client
	base string
}

func newAntiCaptcha(c Config) *antiCaptcha {
	base := c.Endpoint
	if base == "" {
		base = "https://api.anti-captcha.com"
	}
	return &antiCaptcha{
		cfg:  c,
		http: resty.New().SetTimeout(30 * time.Second),
		base: base,
	}
}

type antiCaptchaCreateResp struct {
	ErrorID          int    `json:"errorId"`
	ErrorCode        string `json:"errorCode"`
	ErrorDescription string `json:"errorDescription"`
	TaskID           any    `json:"taskId"` // AntiCaptcha 文档里给的是数字，但稳妥起见用 any 兼容
}

type antiCaptchaResultResp struct {
	ErrorID          int    `json:"errorId"`
	ErrorCode        string `json:"errorCode"`
	ErrorDescription string `json:"errorDescription"`
	Status           string `json:"status"` // "ready" | "processing"
	Solution         struct {
		Token string `json:"token"`
	} `json:"solution"`
}

func (p *antiCaptcha) SolveTurnstile(ctx context.Context, siteKey, pageURL string) (string, error) {
	if p.cfg.APIKey == "" {
		return "", errors.New("anticaptcha: api key is empty")
	}
	if siteKey == "" {
		return "", errors.New("anticaptcha: siteKey is empty")
	}

	taskID, err := p.createTask(ctx, siteKey, pageURL)
	if err != nil {
		return "", err
	}

	deadline := time.Now().Add(120 * time.Second)
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-ticker.C:
			if time.Now().After(deadline) {
				return "", errors.New("anticaptcha: timed out waiting for solution")
			}
			token, ready, err := p.fetchResult(ctx, taskID)
			if err != nil {
				return "", err
			}
			if ready {
				return token, nil
			}
		}
	}
}

func (p *antiCaptcha) createTask(ctx context.Context, siteKey, pageURL string) (string, error) {
	body := map[string]any{
		"clientKey": p.cfg.APIKey,
		"task": map[string]any{
			"type":       "TurnstileTaskProxyless",
			"websiteURL": pageURL,
			"websiteKey": siteKey,
		},
	}
	resp, err := p.http.R().
		SetContext(ctx).
		SetHeader("Content-Type", "application/json").
		SetBody(body).
		Post(p.base + "/createTask")
	if err != nil {
		return "", fmt.Errorf("anticaptcha createTask http: %w", err)
	}
	var r antiCaptchaCreateResp
	if err := json.Unmarshal(resp.Body(), &r); err != nil {
		return "", fmt.Errorf("anticaptcha createTask decode: %w", err)
	}
	if r.ErrorID != 0 || r.TaskID == nil {
		return "", fmt.Errorf("anticaptcha createTask: %s %s", r.ErrorCode, r.ErrorDescription)
	}
	switch v := r.TaskID.(type) {
	case string:
		if v == "" {
			return "", errors.New("anticaptcha createTask: empty taskId")
		}
		return v, nil
	case float64:
		return fmt.Sprintf("%.0f", v), nil
	default:
		return fmt.Sprint(v), nil
	}
}

func (p *antiCaptcha) fetchResult(ctx context.Context, taskID string) (string, bool, error) {
	resp, err := p.http.R().
		SetContext(ctx).
		SetHeader("Content-Type", "application/json").
		SetBody(map[string]any{
			"clientKey": p.cfg.APIKey,
			"taskId":    taskID,
		}).
		Post(p.base + "/getTaskResult")
	if err != nil {
		return "", false, fmt.Errorf("anticaptcha getTaskResult http: %w", err)
	}
	var r antiCaptchaResultResp
	if err := json.Unmarshal(resp.Body(), &r); err != nil {
		return "", false, fmt.Errorf("anticaptcha getTaskResult decode: %w", err)
	}
	if r.ErrorID != 0 {
		return "", false, fmt.Errorf("anticaptcha getTaskResult: %s %s", r.ErrorCode, r.ErrorDescription)
	}
	if r.Status == "ready" {
		if r.Solution.Token == "" {
			return "", false, errors.New("anticaptcha: ready but empty token")
		}
		return r.Solution.Token, true, nil
	}
	return "", false, nil
}
