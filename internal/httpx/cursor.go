package httpx

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Page is the list envelope's page object.
type Page struct {
	NextCursor *string `json:"next_cursor"`
	HasMore    bool    `json:"has_more"`
}

// List is the list envelope (Conventions → Pagination).
type List[T any] struct {
	Data []T  `json:"data"`
	Page Page `json:"page"`
}

// Cursors signs and verifies opaque pagination cursors. A cursor binds the
// position (sort key + id), the filters it was issued under, and an expiry,
// so a tampered, expired, or filter-mismatched cursor is invalid_cursor.
type Cursors struct{ Key []byte }

// CursorTTL is how long a cursor stays valid.
const CursorTTL = 24 * time.Hour

type cursorBody struct {
	Sort string `json:"s"` // the sort key of the last row returned (timestamp, RFC3339Nano)
	ID   string `json:"i"` // its id, the tie-breaker
	F    string `json:"f"` // filter fingerprint
	Exp  int64  `json:"e"`
}

// Position is a decoded cursor: rows strictly after (Sort, ID) in the
// list's newest-first order.
type Position struct {
	Sort string
	ID   string
}

// Encode returns a cursor for the row after which the next page starts.
func (c Cursors) Encode(sortKey, id, filters string) string {
	b, _ := json.Marshal(cursorBody{Sort: sortKey, ID: id, F: filters, Exp: time.Now().Add(CursorTTL).Unix()})
	payload := base64.RawURLEncoding.EncodeToString(b)
	mac := hmac.New(sha256.New, c.Key)
	mac.Write([]byte(payload))
	return payload + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil)[:16])
}

// Decode verifies a cursor. Empty input means "first page".
func (c Cursors) Decode(cursor, filters string) (*Position, error) {
	if cursor == "" {
		return nil, nil
	}
	payload, sig, ok := strings.Cut(cursor, ".")
	if !ok {
		return nil, ErrInvalidCursor()
	}
	mac := hmac.New(sha256.New, c.Key)
	mac.Write([]byte(payload))
	want := base64.RawURLEncoding.EncodeToString(mac.Sum(nil)[:16])
	if !hmac.Equal([]byte(sig), []byte(want)) {
		return nil, ErrInvalidCursor()
	}
	raw, err := base64.RawURLEncoding.DecodeString(payload)
	if err != nil {
		return nil, ErrInvalidCursor()
	}
	var b cursorBody
	if err := json.Unmarshal(raw, &b); err != nil || b.F != filters || time.Now().Unix() > b.Exp {
		return nil, ErrInvalidCursor()
	}
	return &Position{Sort: b.Sort, ID: b.ID}, nil
}

// Limit clamps a requested page size to 1–100, default 25.
func Limit(p *int) int {
	if p == nil || *p < 1 {
		if p != nil && *p < 1 {
			return 1
		}
		return 25
	}
	if *p > 100 {
		return 100
	}
	return *p
}

// Filters fingerprints a request's filter parameters (everything but
// cursor and limit), so a cursor is only valid under the filters it came
// from.
func Filters(r *http.Request, exclude ...string) string {
	q := r.URL.Query()
	skip := map[string]bool{"cursor": true, "limit": true}
	for _, e := range exclude {
		skip[e] = true
	}
	var keys []string
	for k := range q {
		if !skip[k] {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	var b strings.Builder
	b.WriteString(r.URL.Path)
	for _, k := range keys {
		b.WriteString("&" + k + "=" + strings.Join(q[k], ","))
	}
	sum := sha256.Sum256([]byte(b.String()))
	return base64.RawURLEncoding.EncodeToString(sum[:12])
}

// Paginate trims a limit+1 result to limit rows and builds the page
// object. key returns a row's (sort key, id).
func Paginate[T any](c Cursors, rows []T, limit int, filters string, key func(T) (string, string)) List[T] {
	out := List[T]{Data: rows}
	if out.Data == nil {
		out.Data = []T{}
	}
	if len(rows) > limit {
		out.Data = rows[:limit]
		s, id := key(rows[limit-1])
		cur := c.Encode(s, id, filters)
		out.Page = Page{NextCursor: &cur, HasMore: true}
	}
	return out
}

// TimeKey formats a timestamp as a cursor sort key.
func TimeKey(t time.Time) string { return t.UTC().Format(time.RFC3339Nano) }

// IntKey formats an integer sort key.
func IntKey(n int64) string { return strconv.FormatInt(n, 10) }
