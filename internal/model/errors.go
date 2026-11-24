package model

import (
	"errors"
	"fmt"
	"net/http"
	"time"
)

// APIError is a typed endpoint failure. P1-2's retry policy and the P3 error
// cards both branch on Kind (SPEC §13.3, §23).
type APIError struct {
	Status     int
	Kind       string        // auth | not_found | rate_limit | server | request | stream
	Message    string        // server-provided detail (truncated)
	RetryAfter time.Duration // from Retry-After when present (rate_limit)
}

// Kind values.
const (
	KindAuth      = "auth"
	KindNotFound  = "not_found"
	KindRateLimit = "rate_limit"
	KindServer    = "server"
	KindRequest   = "request"
	KindStream    = "stream"
)

func (e *APIError) Error() string {
	return fmt.Sprintf("endpoint %s (HTTP %d): %s", e.Kind, e.Status, e.Message)
}

// NewAPIError classifies an HTTP response status + body snippet.
func NewAPIError(status int, body string, retryAfter time.Duration) *APIError {
	kind := KindRequest
	switch {
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		kind = KindAuth
	case status == http.StatusNotFound:
		kind = KindNotFound
	case status == http.StatusTooManyRequests:
		kind = KindRateLimit
	case status >= 500:
		kind = KindServer
	}
	if len(body) > 512 {
		body = body[:512] + "…"
	}
	return &APIError{Status: status, Kind: kind, Message: body, RetryAfter: retryAfter}
}

// AsAPIError unwraps err to an *APIError.
func AsAPIError(err error) (*APIError, bool) {
	var ae *APIError
	if errors.As(err, &ae) {
		return ae, true
	}
	return nil, false
}

// IsTransient reports whether retrying the same request may succeed (P1-2).
func IsTransient(err error) bool {
	ae, ok := AsAPIError(err)
	if !ok {
		return err != nil // transport errors: retryable with backoff
	}
	return ae.Kind == KindRateLimit || ae.Kind == KindServer
}
