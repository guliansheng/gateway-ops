package api

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

const (
	maxAPIRequestBytes    = 8 << 20
	maxPublicCommentBytes = 16 << 10
)

// securityHeaders also protects direct deployments without a reverse proxy.
// The CSP deliberately preserves the sandboxed srcdoc help renderer and theme
// bootstrap; it is not a substitute for the help renderer's HTML allowlist.
func securityHeaders(policies ...*framePolicy) gin.HandlerFunc {
	var policy *framePolicy
	if len(policies) > 0 {
		policy = policies[0]
	}
	return func(c *gin.Context) {
		ancestors := policy.ancestors(c.Request.URL.Path)
		c.Header("X-Content-Type-Options", "nosniff")
		if ancestors == "'none'" {
			c.Header("X-Frame-Options", "DENY")
		} else {
			// XFO has no portable multi-origin allowlist. CSP is authoritative.
			c.Writer.Header().Del("X-Frame-Options")
		}
		c.Header("Referrer-Policy", "no-referrer")
		c.Header("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		c.Header("Content-Security-Policy", "base-uri 'self'; object-src 'none'; frame-ancestors "+ancestors)
		// A policy change must not leave cached public documents with a stale
		// frame-ancestors allowlist (or retain an old XFO header after a 304).
		if strings.HasPrefix(c.Request.URL.Path, "/api/") || publicFramePath.MatchString(c.Request.URL.Path) {
			c.Header("Cache-Control", "no-store")
		}
		c.Next()
	}
}

// Bound bodies before JSON binding allocates memory. MaxBytesReader also covers
// chunked requests (Content-Length alone is not a reliable limit).
func limitRequestBody(limit int64) gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.Request.ContentLength > limit {
			c.AbortWithStatusJSON(http.StatusRequestEntityTooLarge, gin.H{"error": "请求内容过大"})
			return
		}
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, limit)
		c.Next()
	}
}

type commentWindow struct {
	start time.Time
	count int
}

type publicCommentLimiter struct {
	mu      sync.Mutex
	clients map[string]commentWindow
	global  commentWindow
}

// Bounded fixed-window limiter: at most five attempts/minute/IP and sixty
// attempts/minute/process. New keys fail closed at capacity, not by evicting an
// existing attacker's counter. Invalid attempts count too, before JSON parsing.
func newPublicCommentGuard() gin.HandlerFunc {
	limiter := &publicCommentLimiter{clients: make(map[string]commentWindow)}
	return func(c *gin.Context) {
		if retry := limiter.allow(c.ClientIP(), time.Now()); retry > 0 {
			c.Header("Retry-After", strconv.Itoa(retry))
			c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{"error": "留言过于频繁，请稍后再试"})
			return
		}
		// Buffer only this tiny public write body so chunked oversize requests
		// receive 413 rather than reaching binding or touching the database.
		body, err := io.ReadAll(http.MaxBytesReader(c.Writer, c.Request.Body, maxPublicCommentBytes))
		if err != nil {
			var tooLarge *http.MaxBytesError
			if errors.As(err, &tooLarge) {
				c.AbortWithStatusJSON(http.StatusRequestEntityTooLarge, gin.H{"error": "留言内容过大"})
			} else {
				c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "无法读取留言内容"})
			}
			return
		}
		c.Request.Body = io.NopCloser(bytes.NewReader(body))
		c.Next()
	}
}

func (l *publicCommentLimiter) allow(ip string, now time.Time) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	const window = time.Minute
	if l.global.start.IsZero() || !now.Before(l.global.start.Add(window)) {
		l.global = commentWindow{start: now}
		for key, entry := range l.clients {
			if !now.Before(entry.start.Add(window)) {
				delete(l.clients, key)
			}
		}
	}
	client := l.clients[ip]
	if client.start.IsZero() || !now.Before(client.start.Add(window)) {
		client = commentWindow{start: now}
	}
	blockedUntil := time.Time{}
	if l.global.count >= 60 {
		blockedUntil = l.global.start.Add(window)
	}
	if client.count >= 5 && client.start.Add(window).After(blockedUntil) {
		blockedUntil = client.start.Add(window)
	}
	if blockedUntil.After(now) {
		return int(blockedUntil.Sub(now).Seconds()) + 1
	}
	if _, exists := l.clients[ip]; !exists && len(l.clients) >= 4096 {
		return 60
	}
	client.count++
	l.clients[ip] = client
	l.global.count++
	return 0
}
