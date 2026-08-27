package notify

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"net/smtp"
	"strings"
	"time"

	"github.com/guliansheng/gateway-ops/internal/storage"
)

func init() {
	Register(storage.NotifyEmail, func(raw string) (Notifier, error) { return newEmail(raw) })
}

type emailConfig struct {
	Host     string   `json:"host"`     // smtp.example.com
	Port     int      `json:"port"`     // 465 / 587
	Username string   `json:"username"` // SMTP 用户名
	Password string   `json:"password"` // SMTP 密码 / 授权码
	From     string   `json:"from"`     // 发件人（可与 Username 不同）
	To       []string `json:"to"`       // 收件人列表
	UseTLS   bool     `json:"use_tls"`  // 是否使用隐式 TLS（一般 465 端口）
}

type email struct{ cfg emailConfig }

func newEmail(raw string) (*email, error) {
	var cfg emailConfig
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		return nil, err
	}
	if cfg.Host == "" || cfg.Port == 0 || cfg.From == "" || len(cfg.To) == 0 {
		return nil, errors.New("email config requires host/port/from/to")
	}
	return &email{cfg: cfg}, nil
}

func (e *email) Type() storage.NotificationChannelType { return storage.NotifyEmail }

func (e *email) Send(ctx context.Context, msg Message) error {
	addr := fmt.Sprintf("%s:%d", e.cfg.Host, e.cfg.Port)
	auth := smtp.PlainAuth("", e.cfg.Username, e.cfg.Password, e.cfg.Host)

	body := buildEmailBody(e.cfg.From, e.cfg.To, notificationTitle(msg), notificationText(msg))

	// 简单 deadline，避免完全阻塞调度。
	done := make(chan error, 1)
	go func() {
		if e.cfg.UseTLS {
			done <- sendTLS(addr, e.cfg.Host, auth, e.cfg.From, e.cfg.To, []byte(body))
			return
		}
		done <- smtp.SendMail(addr, auth, e.cfg.From, e.cfg.To, []byte(body))
	}()

	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(45 * time.Second):
		return errors.New("smtp send timeout")
	}
}

func buildEmailBody(from string, to []string, subject, body string) string {
	headers := []string{
		"From: " + from,
		"To: " + strings.Join(to, ", "),
		"Subject: " + subject,
		"MIME-Version: 1.0",
		"Content-Type: text/plain; charset=UTF-8",
	}
	return strings.Join(headers, "\r\n") + "\r\n\r\n" + body
}

// sendTLS 通过 SMTPS（隐式 TLS，常见于 465）发送邮件。
func sendTLS(addr, host string, auth smtp.Auth, from string, to []string, body []byte) error {
	tlsConfig := &tls.Config{ServerName: host}
	conn, err := tls.Dial("tcp", addr, tlsConfig)
	if err != nil {
		return fmt.Errorf("smtp tls dial: %w", err)
	}
	defer conn.Close()

	client, err := smtp.NewClient(conn, host)
	if err != nil {
		return fmt.Errorf("smtp new client: %w", err)
	}
	defer client.Quit()

	if auth != nil {
		if err := client.Auth(auth); err != nil {
			return fmt.Errorf("smtp auth: %w", err)
		}
	}
	if err := client.Mail(from); err != nil {
		return fmt.Errorf("smtp mail: %w", err)
	}
	for _, rcpt := range to {
		if err := client.Rcpt(rcpt); err != nil {
			return fmt.Errorf("smtp rcpt %s: %w", rcpt, err)
		}
	}
	w, err := client.Data()
	if err != nil {
		return fmt.Errorf("smtp data: %w", err)
	}
	if _, err := w.Write(body); err != nil {
		return fmt.Errorf("smtp write: %w", err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("smtp close: %w", err)
	}
	return nil
}
