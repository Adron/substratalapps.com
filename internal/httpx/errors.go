// Package httpx implements the cross-cutting request/response rules from
// API Reference → Conventions: the error envelope, JSON bodies, PATCH
// presence, pagination cursors, ETag/If-Match, and request ids. Every
// handler gets these from here rather than reimplementing them.
package httpx

import (
	"fmt"
	"net/http"
	"strconv"
)

// Error is an API error: a stable code, an HTTP status, a human message,
// and optional details. It renders as {"error": {code, message, details}}.
type Error struct {
	Status  int
	Code    string
	Message string
	Details map[string]any
	// Headers are extra response headers (Retry-After, …).
	Headers map[string]string
}

func (e *Error) Error() string { return fmt.Sprintf("%d %s: %s", e.Status, e.Code, e.Message) }

// E builds an Error.
func E(status int, code, message string) *Error {
	return &Error{Status: status, Code: code, Message: message}
}

// With adds a details key.
func (e *Error) With(key string, value any) *Error {
	if e.Details == nil {
		e.Details = map[string]any{}
	}
	e.Details[key] = value
	return e
}

// RetryAfter sets the Retry-After header, in seconds.
func (e *Error) RetryAfter(seconds int) *Error {
	if e.Headers == nil {
		e.Headers = map[string]string{}
	}
	e.Headers["Retry-After"] = strconv.Itoa(seconds)
	return e
}

// Cross-cutting errors (Conventions → Errors).
var (
	ErrInvalidCursor = func() *Error {
		return E(400, "invalid_cursor", "The pagination cursor is expired, invalid, or was issued for different filters.")
	}
	ErrUserTokenRequired = func() *Error {
		return E(400, "user_token_required", "This endpoint acts as a User and can't be called with an API Key.")
	}
	ErrIdempotencyRequired = func() *Error {
		return E(400, "idempotency_key_required", "This request requires an Idempotency-Key header.")
	}
	ErrUnauthenticated = func() *Error { return E(401, "unauthenticated", "Missing, malformed, or expired credentials.") }
	ErrSessionRevoked  = func() *Error { return E(401, "session_revoked", "This session has been logged out or revoked.") }
	ErrNotFound        = func() *Error { return E(404, "not_found", "No route matches this path.") }
	ErrPayloadTooLarge = func() *Error { return E(413, "payload_too_large", "Request body exceeds 1 MB.") }
	ErrInternal        = func() *Error {
		return E(500, "internal_error", "Unexpected server error. Quote the X-Request-Id to support.")
	}
)

// Invalid is 400 invalid_request.
func Invalid(format string, a ...any) *Error {
	return E(400, "invalid_request", fmt.Sprintf(format, a...))
}

// Forbidden is 403 forbidden naming the missing permission.
func Forbidden(permission string) *Error {
	e := E(403, "forbidden", "The caller lacks the permission this request requires.")
	if permission != "" {
		e = e.With("required_permission", permission)
	}
	return e
}

// NotFoundResource is 404 <resource>_not_found.
func NotFoundResource(resource string) *Error {
	return E(404, resource+"_not_found", "No "+humanize(resource)+" with this id exists, or it isn't visible to the caller.")
}

// Conflict is a 409 with a resource-specific code.
func Conflict(code, message string) *Error { return E(409, code, message) }

// VersionConflict is 409 version_conflict with the current ETag.
func VersionConflict(currentVersion int) *Error {
	return E(409, "version_conflict", "The resource changed since the If-Match ETag was read.").
		With("current_etag", ETag(currentVersion))
}

// FieldError is one entry of details.fields on a 422 validation_failed.
type FieldError struct {
	Field   string   `json:"field"`
	Code    string   `json:"code"`
	Min     *int     `json:"min,omitempty"`
	Max     *int     `json:"max,omitempty"`
	Allowed []string `json:"allowed,omitempty"`
	Pattern string   `json:"pattern,omitempty"`
	Message string   `json:"message,omitempty"`
}

// Fields accumulates field errors for one request.
type Fields []FieldError

// Add records a field error.
func (f *Fields) Add(field, code string) *FieldError {
	*f = append(*f, FieldError{Field: field, Code: code})
	return &(*f)[len(*f)-1]
}

// Max records a too_long / out_of_range error with its bound.
func (f *Fields) Max(field, code string, max int) {
	f.Add(field, code).Max = &max
}

// Enum records an enum_mismatch with the allowed values.
func (f *Fields) Enum(field string, allowed ...string) {
	f.Add(field, "enum_mismatch").Allowed = allowed
}

// Err returns the 422, or nil when there were no field errors. One field
// error and many both use details.fields here; the array form is what
// clients parse, so it's always present on validation_failed.
func (f Fields) Err() error {
	if len(f) == 0 {
		return nil
	}
	msg := "1 field failed validation."
	if len(f) > 1 {
		msg = fmt.Sprintf("%d fields failed validation.", len(f))
	}
	return E(http.StatusUnprocessableEntity, "validation_failed", msg).With("fields", []FieldError(f))
}

// ReadOnly is 422 read_only_field.
func ReadOnly(field string) *Error {
	return E(422, "read_only_field", "Read-only fields can't be sent in a PATCH body.").With("field", field)
}

func humanize(s string) string {
	b := []byte(s)
	for i, c := range b {
		if c == '_' {
			b[i] = ' '
		}
	}
	return string(b)
}
