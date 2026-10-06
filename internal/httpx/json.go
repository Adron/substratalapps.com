package httpx

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
)

// MaxBody is the request body limit (Conventions → Size and length limits).
const MaxBody = 1 << 20

// ReadBody reads the request body, enforcing the 1 MB limit.
func ReadBody(r *http.Request) ([]byte, error) {
	if r.Body == nil {
		return nil, nil
	}
	b, err := io.ReadAll(io.LimitReader(r.Body, MaxBody+1))
	if err != nil {
		return nil, Invalid("Couldn't read the request body.")
	}
	if len(b) > MaxBody {
		return nil, ErrPayloadTooLarge()
	}
	return b, nil
}

// Decode reads a JSON object body into dst. An empty body decodes as {}.
// Unknown fields are ignored (additive changes stay non-breaking).
func Decode(r *http.Request, dst any) error {
	b, err := ReadBody(r)
	if err != nil {
		return err
	}
	return DecodeBytes(b, dst)
}

// DecodeBytes is Decode over an already-read body.
func DecodeBytes(b []byte, dst any) error {
	if len(bytes.TrimSpace(b)) == 0 {
		b = []byte("{}")
	}
	if err := json.Unmarshal(b, dst); err != nil {
		var te *json.UnmarshalTypeError
		if errors.As(err, &te) && te.Field != "" {
			var f Fields
			f.Add(te.Field, "invalid_format").Message = "expected " + te.Type.String()
			return f.Err()
		}
		return Invalid("The request body isn't valid JSON.")
	}
	return nil
}

// JSON writes a 2xx JSON response.
func JSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(v)
}

// JSONWithETag writes a single-resource response with its ETag.
func JSONWithETag(w http.ResponseWriter, status int, version int, v any) {
	w.Header().Set("ETag", ETag(version))
	JSON(w, status, v)
}

// NoContent writes 204.
func NoContent(w http.ResponseWriter) { w.WriteHeader(http.StatusNoContent) }

// WriteError renders err. Non-*Error values become 500 internal_error;
// the caller is responsible for logging those.
func WriteError(w http.ResponseWriter, err error) {
	var e *Error
	if !errors.As(err, &e) {
		e = ErrInternal()
	}
	for k, v := range e.Headers {
		w.Header().Set(k, v)
	}
	body := map[string]any{"code": e.Code, "message": e.Message}
	if len(e.Details) > 0 {
		body["details"] = e.Details
	}
	JSON(w, e.Status, map[string]any{"error": body})
}

// ETag is the weak validator over a row's version counter.
func ETag(version int) string { return `W/"` + strconv.Itoa(version) + `"` }

// IfMatch parses If-Match into a version. ok is false when the header is
// absent (last-write-wins). A malformed header never matches any version.
func IfMatch(r *http.Request) (version int, ok bool) {
	h := strings.TrimSpace(r.Header.Get("If-Match"))
	if h == "" {
		return 0, false
	}
	h = strings.TrimPrefix(h, "W/")
	h = strings.Trim(h, `"`)
	v, err := strconv.Atoi(h)
	if err != nil {
		return -1, true
	}
	return v, true
}

// CheckIfMatch returns version_conflict when If-Match is present and stale.
func CheckIfMatch(r *http.Request, current int) error {
	if want, ok := IfMatch(r); ok && want != current {
		return VersionConflict(current)
	}
	return nil
}

// Patch is a PATCH body decoded with field presence preserved, so an
// omitted field (leave unchanged) and an explicit null (clear) differ.
type Patch map[string]json.RawMessage

// DecodePatch reads a PATCH body.
func DecodePatch(r *http.Request) (Patch, error) {
	b, err := ReadBody(r)
	if err != nil {
		return nil, err
	}
	if len(bytes.TrimSpace(b)) == 0 {
		return Patch{}, nil
	}
	var p Patch
	if err := json.Unmarshal(b, &p); err != nil {
		return nil, Invalid("The request body isn't a JSON object.")
	}
	return p, nil
}

// Has reports whether field was sent.
func (p Patch) Has(field string) bool { _, ok := p[field]; return ok }

// IsNull reports whether field was sent as an explicit null.
func (p Patch) IsNull(field string) bool {
	v, ok := p[field]
	return ok && bytes.Equal(bytes.TrimSpace(v), []byte("null"))
}

// Get decodes field into dst. It returns false when the field is absent.
// A type mismatch is recorded in f.
func (p Patch) Get(field string, dst any, f *Fields) bool {
	v, ok := p[field]
	if !ok {
		return false
	}
	if err := json.Unmarshal(v, dst); err != nil {
		f.Add(field, "invalid_format")
		return false
	}
	return true
}

// RejectReadOnly returns read_only_field if any of fields was sent.
func (p Patch) RejectReadOnly(fields ...string) error {
	for _, f := range fields {
		if p.Has(f) {
			return ReadOnly(f)
		}
	}
	return nil
}

// RequireNonNull records a validation error for each field sent as null
// that isn't nullable (Conventions → Partial updates).
func (p Patch) RequireNonNull(f *Fields, fields ...string) {
	for _, name := range fields {
		if p.IsNull(name) {
			f.Add(name, "required").Message = "this field isn't nullable"
		}
	}
}

// String is a decoded optional-nullable string for PATCH handling.
func (p Patch) String(field string, f *Fields) (val *string, present bool) {
	if !p.Has(field) {
		return nil, false
	}
	if p.IsNull(field) {
		return nil, true
	}
	var s string
	if !p.Get(field, &s, f) {
		return nil, true
	}
	return &s, true
}

// Mustf panics with a formatted message; for programmer errors only.
func Mustf(cond bool, format string, a ...any) {
	if !cond {
		panic(fmt.Sprintf(format, a...))
	}
}
