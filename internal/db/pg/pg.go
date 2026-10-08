// Package pg implements db.DB over a direct Postgres connection with pgx.
// It's what laptops and CI run; production uses the Data API driver.
package pg

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/CompositeCode/substratalapps.com/internal/db"
)

// DB is a pgx connection pool.
type DB struct{ pool *pgxpool.Pool }

// Open connects to url (a postgres:// connection string).
func Open(ctx context.Context, url string) (*DB, error) {
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		return nil, err
	}
	// Arguments are always strings/scalars cast in SQL, so simple protocol
	// keeps pgx from inferring types differently than the Data API would.
	cfg.ConnConfig.DefaultQueryExecMode = pgx.QueryExecModeSimpleProtocol
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, err
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, err
	}
	return &DB{pool: pool}, nil
}

// Close closes the pool.
func (d *DB) Close() { d.pool.Close() }

// Tx implements db.DB.
func (d *DB) Tx(ctx context.Context, s db.Settings, fn func(db.Querier) error) error {
	tx, err := d.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op after commit
	q := querier{tx: tx}
	if _, err := q.Query(ctx, `select to_jsonb(x) from (`+db.SetupSQL+`) x`, s.SetupArgs()); err != nil {
		return err
	}
	if err := fn(q); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// ExecScript implements db.DB.
func (d *DB) ExecScript(ctx context.Context, statements []string) error {
	tx, err := d.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op after commit
	for _, st := range statements {
		if _, err := tx.Exec(ctx, st); err != nil {
			return fmt.Errorf("%w\n--- statement ---\n%s", translate(err), st)
		}
	}
	return tx.Commit(ctx)
}

// Autocommit implements db.DB.
func (d *DB) Autocommit() db.Querier { return querier{tx: d.pool} }

// execer is what querier needs: a pgx.Tx or the pool itself (autocommit).
type execer interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

type querier struct{ tx execer }

func (q querier) Query(ctx context.Context, sql string, args db.Args) ([]json.RawMessage, error) {
	stmt, vals, err := db.Positional(sql, args)
	if err != nil {
		return nil, err
	}
	rows, err := q.tx.Query(ctx, stmt, vals...)
	if err != nil {
		return nil, translate(err)
	}
	defer rows.Close()
	var out []json.RawMessage
	for rows.Next() {
		var s *string
		if err := rows.Scan(&s); err != nil {
			return nil, fmt.Errorf("pg: scan (query must select one json column): %w", err)
		}
		if s == nil {
			out = append(out, json.RawMessage("null"))
			continue
		}
		out = append(out, json.RawMessage(*s))
	}
	return out, translate(rows.Err())
}

func (q querier) Exec(ctx context.Context, sql string, args db.Args) (int64, error) {
	stmt, vals, err := db.Positional(sql, args)
	if err != nil {
		return 0, err
	}
	tag, err := q.tx.Exec(ctx, stmt, vals...)
	if err != nil {
		return 0, translate(err)
	}
	return tag.RowsAffected(), nil
}

func translate(err error) error {
	var pe *pgconn.PgError
	if errors.As(err, &pe) && len(pe.Code) == 5 && pe.Code[:2] == "23" {
		return &db.ConstraintError{Code: pe.Code, Constraint: pe.ConstraintName, Message: pe.Message}
	}
	return err
}
