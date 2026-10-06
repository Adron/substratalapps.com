// Package db is the one seam between business logic and the database.
//
// Production talks to Aurora Serverless v2 through the RDS Data API;
// laptops and CI talk to plain Postgres through pgx (see README → The two
// database backends). Both drivers implement the same tiny interface, and
// all SQL is written once against it:
//
//   - Parameters are named (:user_id) and are only ever scalars: nil,
//     string, bool, int64, or float64. Arrays and JSON travel as text and
//     are cast in SQL (:perms::text[], :payload::jsonb, :at::timestamptz),
//     because the Data API has no array parameters and its typed
//     parameter mapping differs from pgx's.
//   - Every query that returns rows selects exactly one json/jsonb column,
//     built by Postgres itself (to_jsonb(t), jsonb_build_object(...)). Both
//     drivers hand that back as raw JSON text and callers decode it with
//     encoding/json. That sidesteps the Data API's per-type field mapping
//     entirely: arrays, timestamps, and jsonb all arrive as JSON.
//   - Every unit of work runs inside Tx, which first sets the
//     transaction-local settings Row-Level Security reads
//     (app.current_test_mode). Outside a transaction there is no RLS
//     context, so the interface doesn't offer one.
package db

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
)

// Args are named statement parameters. Values must be nil, string, bool,
// int64, int, or float64; use the helpers below for everything else.
type Args map[string]any

// Querier runs statements inside a transaction.
type Querier interface {
	// Query runs a statement that selects exactly one json/jsonb column and
	// returns each row's JSON text.
	Query(ctx context.Context, sql string, args Args) ([]json.RawMessage, error)
	// Exec runs a statement and returns the number of rows affected.
	Exec(ctx context.Context, sql string, args Args) (int64, error)
}

// Settings are the transaction-local values RLS policies read.
type Settings struct {
	TestMode bool
	// AllModes lets scheduled jobs see test and live rows together.
	// Never set on a request path.
	AllModes bool
	// AuditMaintenance lets the archival and erasure jobs update or delete
	// audit_events, which is otherwise append-only at the database.
	AuditMaintenance bool
}

// ModeSetting is the value of app.current_test_mode for s.
func (s Settings) ModeSetting() string {
	switch {
	case s.AllModes:
		return "any"
	case s.TestMode:
		return "true"
	default:
		return "false"
	}
}

// SetupSQL is the statement each driver runs first in every transaction.
const SetupSQL = `select set_config('app.current_test_mode', :tm, true), set_config('app.audit_maintenance', :am, true)`

// SetupArgs are SetupSQL's parameters for s.
func (s Settings) SetupArgs() Args {
	am := "off"
	if s.AuditMaintenance {
		am = "on"
	}
	return Args{"tm": s.ModeSetting(), "am": am}
}

// DB opens transactions.
type DB interface {
	// Tx runs fn in a transaction. fn's error rolls it back; nil commits.
	Tx(ctx context.Context, s Settings, fn func(q Querier) error) error
	// ExecScript runs statements verbatim (no parameter parsing) in one
	// transaction. Used by migrations.
	ExecScript(ctx context.Context, statements []string) error
	// Autocommit runs single statements outside any transaction and
	// without RLS settings. Only for tables with no RLS policy (rate-limit
	// counters), where a write must survive the request's own rollback.
	Autocommit() Querier
	Close()
}

// ErrNotFound is returned by One when no row matched.
var ErrNotFound = errors.New("db: no rows")

// One decodes the first row into dst, or returns ErrNotFound.
func One(ctx context.Context, q Querier, dst any, sql string, args Args) error {
	rows, err := q.Query(ctx, sql, args)
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return ErrNotFound
	}
	return json.Unmarshal(rows[0], dst)
}

// All decodes every row into a slice of T.
func All[T any](ctx context.Context, q Querier, sql string, args Args) ([]T, error) {
	rows, err := q.Query(ctx, sql, args)
	if err != nil {
		return nil, err
	}
	out := make([]T, 0, len(rows))
	for _, r := range rows {
		var v T
		if err := json.Unmarshal(r, &v); err != nil {
			return nil, fmt.Errorf("db: decode row: %w", err)
		}
		out = append(out, v)
	}
	return out, nil
}

// Time encodes a timestamp parameter; cast it with ::timestamptz in SQL.
func Time(t time.Time) string { return t.UTC().Format(time.RFC3339Nano) }

// NullTime encodes an optional timestamp (nil → SQL null).
func NullTime(t *time.Time) any {
	if t == nil {
		return nil
	}
	return Time(*t)
}

// NullString encodes an optional string (nil → SQL null).
func NullString(s *string) any {
	if s == nil {
		return nil
	}
	return *s
}

// JSON encodes a value as JSON text; cast it with ::jsonb in SQL.
func JSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		panic(fmt.Sprintf("db.JSON: %v", err))
	}
	return string(b)
}

// TextArray encodes a []string as a Postgres array literal; cast it with
// ::text[] in SQL. nil and empty both encode as '{}'.
func TextArray(ss []string) string {
	var b strings.Builder
	b.WriteByte('{')
	for i, s := range ss {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteByte('"')
		for _, r := range s {
			if r == '"' || r == '\\' {
				b.WriteByte('\\')
			}
			b.WriteRune(r)
		}
		b.WriteByte('"')
	}
	b.WriteByte('}')
	return b.String()
}

// NullTextArray encodes an optional []string (nil → SQL null).
func NullTextArray(ss []string) any {
	if ss == nil {
		return nil
	}
	return TextArray(ss)
}

var namedParam = regexp.MustCompile(`(::?)([a-zA-Z_][a-zA-Z0-9_]*)`)

// Positional rewrites :name parameters to $1..$n for drivers that need
// positional placeholders, leaving ::casts untouched. It returns the
// rewritten SQL and the ordered argument values.
func Positional(sql string, args Args) (string, []any, error) {
	index := map[string]int{}
	var ordered []any
	var missing []string
	out := namedParam.ReplaceAllStringFunc(sql, func(m string) string {
		if strings.HasPrefix(m, "::") {
			return m
		}
		name := m[1:]
		if i, ok := index[name]; ok {
			return fmt.Sprintf("$%d", i)
		}
		v, ok := args[name]
		if !ok {
			missing = append(missing, name)
			return m
		}
		ordered = append(ordered, normalize(v))
		index[name] = len(ordered)
		return fmt.Sprintf("$%d", len(ordered))
	})
	if len(missing) > 0 {
		return "", nil, fmt.Errorf("db: missing parameters %v", missing)
	}
	return out, ordered, nil
}

// normalize narrows the accepted Go types so both drivers see the same set.
func normalize(v any) any {
	switch t := v.(type) {
	case int:
		return int64(t)
	case int32:
		return int64(t)
	case *string:
		return NullString(t)
	case time.Time:
		return Time(t)
	case *time.Time:
		return NullTime(t)
	default:
		return v
	}
}

// Normalize is normalize, exported for drivers outside this package.
func Normalize(v any) any { return normalize(v) }

// UniqueViolation reports whether err is a Postgres unique-constraint
// violation, and if so which constraint (empty when the driver can't say).
func UniqueViolation(err error) (constraint string, ok bool) {
	var ce *ConstraintError
	if errors.As(err, &ce) && ce.Code == "23505" {
		return ce.Constraint, true
	}
	return "", false
}

// ConstraintError is the driver-neutral form of a Postgres integrity error.
type ConstraintError struct {
	Code       string // SQLSTATE, e.g. 23505 unique_violation, 23514 check_violation
	Constraint string
	Message    string
}

func (e *ConstraintError) Error() string {
	return fmt.Sprintf("db: %s (%s): %s", e.Code, e.Constraint, e.Message)
}

// CheckViolation reports whether err is a check/FK/not-null violation.
func CheckViolation(err error) (*ConstraintError, bool) {
	var ce *ConstraintError
	if errors.As(err, &ce) && strings.HasPrefix(ce.Code, "23") && ce.Code != "23505" {
		return ce, true
	}
	return nil, false
}
