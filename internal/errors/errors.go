package errors

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
)

// Kind classifies application errors.
type Kind string

const (
	KindUnknown        Kind = "unknown"
	KindInvalid        Kind = "invalid"
	KindUnauthorized   Kind = "unauthorized"
	KindNotFound       Kind = "not_found"
	KindRateLimited    Kind = "rate_limited"
	KindUnavailable    Kind = "unavailable"
	KindTimeout        Kind = "timeout"
	KindCanceled       Kind = "canceled"
	KindNetwork        Kind = "network"
	KindContextTooLong Kind = "context_too_long"
	KindMaxSteps       Kind = "max_steps"
)

var ErrToolNotFound = errors.New("tool not found")

// Error is a structured application error.
type Error struct {
	Kind       Kind
	Op         string
	StatusCode int
	Public     string // user-friendly message
	Err        error
}

func (e *Error) Error() string {
	if e == nil {
		return "<nil>"
	}
	var parts []string
	if e.Op != "" {
		parts = append(parts, e.Op)
	}
	if e.Kind != "" {
		parts = append(parts, string(e.Kind))
	}
	if e.StatusCode != 0 {
		parts = append(parts, fmt.Sprintf("HTTP %d", e.StatusCode))
	}
	prefix := strings.Join(parts, ": ")
	if e.Err == nil {
		if prefix == "" {
			return string(KindUnknown)
		}
		return prefix
	}
	if prefix == "" {
		return e.Err.Error()
	}
	return prefix + ": " + e.Err.Error()
}

func (e *Error) Unwrap() error { return e.Err }

func Wrap(kind Kind, op string, err error) error {
	if err == nil {
		return nil
	}
	return &Error{Kind: kind, Op: op, Err: err}
}

func WithStatus(kind Kind, op string, statusCode int, err error) error {
	if err == nil {
		return nil
	}
	return &Error{Kind: kind, Op: op, StatusCode: statusCode, Err: err}
}

func KindOf(err error) Kind {
	var appErr *Error
	if errors.As(err, &appErr) {
		return appErr.Kind
	}
	return KindUnknown
}

func Is(err error, kind Kind) bool { return KindOf(err) == kind }

func Retryable(err error) bool {
	switch KindOf(err) {
	case KindUnavailable, KindTimeout, KindNetwork:
		return true
	}
	return false
}

// PublicMessage returns a user-friendly error message, or "" if err is nil.
func PublicMessage(err error) string {
	if err == nil {
		return ""
	}
	var appErr *Error
	if errors.As(err, &appErr) && appErr.Public != "" {
		return appErr.Public
	}
	return ""
}

// Normalize converts raw errors to structured Error values with public messages.
func Normalize(op string, err error) error {
	if err == nil {
		return nil
	}
	var appErr *Error
	if errors.As(err, &appErr) {
		return err
	}

	if errors.Is(err, context.Canceled) {
		return &Error{Kind: KindCanceled, Op: op, Public: "请求已取消。", Err: err}
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return &Error{Kind: KindTimeout, Op: op, Public: "请求超时，请稍后再试。", Err: err}
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return &Error{Kind: KindTimeout, Op: op, Public: "网络超时，请稍后再试。", Err: err}
	}

	msg := err.Error()
	lower := strings.ToLower(msg)
	switch {
	case strings.Contains(msg, "HTTP 503"), strings.Contains(lower, "service unavailable"):
		return &Error{Kind: KindUnavailable, Op: op, StatusCode: 503, Public: "服务暂时不可用，请稍后再试。", Err: err}
	case strings.Contains(msg, "HTTP 429"), strings.Contains(lower, "rate limit"), strings.Contains(lower, "too many requests"):
		return &Error{Kind: KindRateLimited, Op: op, StatusCode: 429, Public: "请求过于频繁，请稍后再试。", Err: err}
	case strings.Contains(msg, "HTTP 401"), strings.Contains(msg, "HTTP 403"), strings.Contains(lower, "unauthorized"), strings.Contains(lower, "invalid api key"):
		return &Error{Kind: KindUnauthorized, Op: op, Public: "认证失败，请检查 API Key 配置。", Err: err}
	case strings.Contains(lower, "model_not_found"), strings.Contains(msg, "HTTP 404"):
		return &Error{Kind: KindNotFound, Op: op, StatusCode: 404, Public: "模型未找到，请检查模型名称。", Err: err}
	case strings.Contains(msg, "HTTP 400"):
		return &Error{Kind: KindInvalid, Op: op, StatusCode: 400, Public: "请求参数有误。", Err: err}
	case strings.Contains(msg, "HTTP 500"), strings.Contains(msg, "HTTP 502"), strings.Contains(msg, "HTTP 504"):
		return &Error{Kind: KindUnavailable, Op: op, Public: "服务异常，请稍后再试。", Err: err}
	case strings.Contains(lower, "context deadline exceeded"), strings.Contains(lower, "timeout"):
		return &Error{Kind: KindTimeout, Op: op, Public: "请求超时，请稍后再试。", Err: err}
	case strings.Contains(lower, "connection reset"), strings.Contains(lower, "connection refused"):
		return &Error{Kind: KindNetwork, Op: op, Public: "网络连接失败，请稍后再试。", Err: err}
	}
	return &Error{Kind: KindUnknown, Op: op, Public: "发生未知错误，请稍后再试。", Err: err}
}
