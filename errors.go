package webhookadmin

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Kind classifies an [Error].
type Kind string

// Error kinds. Match them with errors.Is and the Err… sentinels, or read [Error.Kind].
const (
	// KindAuthentication is 401: the API key is missing, wrong, revoked or expired.
	KindAuthentication Kind = "authentication"
	// KindPermission is 403: the API key does not have the scope for this operation.
	KindPermission Kind = "permission"
	// KindNotFound is 404: the resource does not exist in the key's environment.
	KindNotFound Kind = "not_found"
	// KindValidation is 400, 413 or 422: the request body or parameters are invalid. See Fields.
	KindValidation Kind = "validation"
	// KindPlanLimit is 402: the plan's limit was reached.
	KindPlanLimit Kind = "plan_limit"
	// KindConflict is 409: conflicts with the current state (duplicate external_id, delivery still in progress).
	KindConflict Kind = "conflict"
	// KindRateLimit is 429: too many requests, returned after retries are used up.
	KindRateLimit Kind = "rate_limit"
	// KindAPI is any other error response, including 5xx.
	KindAPI Kind = "api"
	// KindConnection means the request could not reach the API (DNS, TCP, TLS) or was cancelled.
	KindConnection Kind = "connection"
	// KindTimeout means the request did not complete within the timeout. It also matches ErrConnection.
	KindTimeout Kind = "timeout"
	// KindWebhookVerification means signature verification of an incoming webhook failed.
	KindWebhookVerification Kind = "webhook_verification"
	// KindInvalidArgument is a usage error, such as a missing API key or an invalid webhook secret.
	KindInvalidArgument Kind = "invalid_argument"
)

// Sentinels for errors.Is.
var (
	ErrAuthentication      = errors.New("webhookadmin: authentication error")
	ErrPermission          = errors.New("webhookadmin: permission error")
	ErrNotFound            = errors.New("webhookadmin: not found")
	ErrValidation          = errors.New("webhookadmin: validation error")
	ErrPlanLimit           = errors.New("webhookadmin: plan limit")
	ErrConflict            = errors.New("webhookadmin: conflict")
	ErrRateLimit           = errors.New("webhookadmin: rate limited")
	ErrAPI                 = errors.New("webhookadmin: api error")
	ErrConnection          = errors.New("webhookadmin: connection error")
	ErrTimeout             = errors.New("webhookadmin: timeout")
	ErrWebhookVerification = errors.New("webhookadmin: webhook verification failed")
	ErrInvalidArgument     = errors.New("webhookadmin: invalid argument")
)

var sentinels = map[Kind]error{
	KindAuthentication:      ErrAuthentication,
	KindPermission:          ErrPermission,
	KindNotFound:            ErrNotFound,
	KindValidation:          ErrValidation,
	KindPlanLimit:           ErrPlanLimit,
	KindConflict:            ErrConflict,
	KindRateLimit:           ErrRateLimit,
	KindAPI:                 ErrAPI,
	KindConnection:          ErrConnection,
	KindTimeout:             ErrTimeout,
	KindWebhookVerification: ErrWebhookVerification,
	KindInvalidArgument:     ErrInvalidArgument,
}

// Error is every error returned by the SDK. Use errors.As to read it, or errors.Is with a sentinel:
//
//	var e *webhookadmin.Error
//	if errors.As(err, &e) { fmt.Println(e.Status, e.Code, e.Fields, e.RequestID) }
//	if errors.Is(err, webhookadmin.ErrNotFound) { … }
type Error struct {
	Kind Kind
	// Message from the API, or a description of the failure.
	Message string
	// Status is the HTTP status. 0 when no response was received.
	Status int
	// Code is the API error code, such as "invalid" or "not_found".
	Code string
	// Fields has per-field messages for validation errors.
	Fields map[string]string
	// RequestID to quote when contacting support.
	RequestID string
	// RetryAfter is the wait from retry-after on a 429, or nil.
	RetryAfter *time.Duration
	// Err is the underlying cause, if any.
	Err error
}

func (e *Error) Error() string { return e.Message }

func (e *Error) Unwrap() error { return e.Err }

// Is reports whether target is the sentinel for e's kind. A timeout also matches ErrConnection.
func (e *Error) Is(target error) bool {
	if target == sentinels[e.Kind] {
		return true
	}
	return e.Kind == KindTimeout && target == ErrConnection
}

func newError(kind Kind, message string, cause error) *Error {
	return &Error{Kind: kind, Message: message, Err: cause}
}

// parseRetryAfter returns the wait from retry-after (seconds or HTTP date).
func parseRetryAfter(value string, now time.Time) (time.Duration, bool) {
	if strings.TrimSpace(value) == "" {
		return 0, false
	}
	if n, err := strconv.ParseFloat(value, 64); err == nil {
		if math.IsNaN(n) || math.IsInf(n, 0) {
			return 0, false
		}
		return time.Duration(math.Max(0, n) * float64(time.Second)), true
	}
	at, err := http.ParseTime(value)
	if err != nil {
		return 0, false
	}
	d := at.Sub(now)
	if d < 0 {
		d = 0
	}
	return d, true
}

func requestIDOf(h http.Header) string {
	if v := h.Get("x-request-id"); v != "" {
		return v
	}
	return h.Get("cf-ray")
}

// errorFromResponse builds the error for a non-2xx response.
func errorFromResponse(status int, body []byte, h http.Header, now time.Time) *Error {
	var parsed map[string]any
	_ = json.Unmarshal(body, &parsed) // HTML from a proxy, empty body, etc. leave it nil
	e := &Error{Status: status, Message: fmt.Sprintf("HTTP %d", status), RequestID: requestIDOf(h)}
	if s, ok := parsed["error"].(string); ok {
		e.Code = s
	}
	if s, ok := parsed["message"].(string); ok {
		e.Message = s
	}
	if f, ok := parsed["fields"].(map[string]any); ok {
		e.Fields = make(map[string]string, len(f))
		for k, v := range f {
			if s, ok := v.(string); ok {
				e.Fields[k] = s
			} else {
				e.Fields[k] = fmt.Sprint(v)
			}
		}
	}
	switch status {
	case 401:
		e.Kind = KindAuthentication
	case 403:
		e.Kind = KindPermission
	case 404:
		e.Kind = KindNotFound
	case 400, 413, 422:
		e.Kind = KindValidation
	case 402:
		e.Kind = KindPlanLimit
	case 409:
		e.Kind = KindConflict
	case 429:
		e.Kind = KindRateLimit
		if d, ok := parseRetryAfter(h.Get("retry-after"), now); ok {
			e.RetryAfter = &d
		}
	default:
		e.Kind = KindAPI
	}
	return e
}
