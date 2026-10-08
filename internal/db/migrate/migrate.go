// Package migrate applies the numbered SQL files in migrations/, in order,
// tracked in schema_migrations. The same runner and the same files run
// against local Postgres (pgx) and Aurora (Data API); see README →
// Technology stack for why this isn't a third-party tool.
//
// Migrations are forward-only, and each file applies in one transaction.
// Statements are split here, rather than sent as one script, because the
// Data API executes exactly one statement per call.
package migrate

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"path"
	"regexp"
	"sort"
	"strings"

	"github.com/CompositeCode/substratalapps.com/internal/db"
)

var fileName = regexp.MustCompile(`^(\d{4})_[a-z0-9_]+\.sql$`)

// Migration is one numbered file.
type Migration struct {
	Version string // "0001"
	Name    string // "0001_init.sql"
	SQL     string
}

// Load reads every migration from fsys (the migrations directory), sorted.
func Load(fsys fs.FS) ([]Migration, error) {
	entries, err := fs.ReadDir(fsys, ".")
	if err != nil {
		return nil, err
	}
	var out []Migration
	seen := map[string]string{}
	for _, e := range entries {
		if e.IsDir() || path.Ext(e.Name()) != ".sql" {
			continue
		}
		m := fileName.FindStringSubmatch(e.Name())
		if m == nil {
			return nil, fmt.Errorf("migrate: %s doesn't match NNNN_name.sql", e.Name())
		}
		if prev, dup := seen[m[1]]; dup {
			return nil, fmt.Errorf("migrate: %s and %s share version %s", prev, e.Name(), m[1])
		}
		seen[m[1]] = e.Name()
		b, err := fs.ReadFile(fsys, e.Name())
		if err != nil {
			return nil, err
		}
		out = append(out, Migration{Version: m[1], Name: e.Name(), SQL: string(b)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Version < out[j].Version })
	return out, nil
}

// Up applies every migration not yet recorded. It returns the names applied.
func Up(ctx context.Context, d db.DB, migrations []Migration) ([]string, error) {
	if err := d.ExecScript(ctx, []string{`create table if not exists schema_migrations (
		version text primary key,
		name text not null,
		applied_at timestamptz not null default now()
	)`}); err != nil {
		return nil, err
	}
	var applied map[string]bool
	err := d.Tx(ctx, db.Settings{AllModes: true}, func(q db.Querier) error {
		rows, err := q.Query(ctx, `select to_jsonb(version) from schema_migrations`, nil)
		if err != nil {
			return err
		}
		applied = make(map[string]bool, len(rows))
		for _, r := range rows {
			var v string
			if err := json.Unmarshal(r, &v); err != nil {
				return err
			}
			applied[v] = true
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	var done []string
	for _, m := range migrations {
		if applied[m.Version] {
			continue
		}
		stmts := Split(m.SQL)
		stmts = append(stmts, fmt.Sprintf(`insert into schema_migrations (version, name) values ('%s', '%s')`,
			m.Version, strings.ReplaceAll(m.Name, "'", "''")))
		if err := d.ExecScript(ctx, stmts); err != nil {
			return done, fmt.Errorf("migrate: %s: %w", m.Name, err)
		}
		done = append(done, m.Name)
	}
	return done, nil
}

// Split breaks a SQL script into statements on top-level semicolons,
// respecting quotes, comments, and dollar-quoted bodies.
func Split(script string) []string {
	var out []string
	var cur strings.Builder
	flush := func() {
		s := strings.TrimSpace(cur.String())
		if s != "" && !onlyComments(s) {
			out = append(out, s)
		}
		cur.Reset()
	}
	for i := 0; i < len(script); i++ {
		c := script[i]
		switch {
		case c == '-' && i+1 < len(script) && script[i+1] == '-':
			end := strings.IndexByte(script[i:], '\n')
			if end < 0 {
				end = len(script) - i
			}
			cur.WriteString(script[i : i+end])
			i += end - 1
		case c == '/' && i+1 < len(script) && script[i+1] == '*':
			end := strings.Index(script[i+2:], "*/")
			if end < 0 {
				end = len(script) - i - 2
			}
			cur.WriteString(script[i : i+2+end+2])
			i += 2 + end + 1
		case c == '\'' || c == '"':
			j := i + 1
			for j < len(script) {
				if script[j] == c {
					if j+1 < len(script) && script[j+1] == c { // doubled quote escape
						j += 2
						continue
					}
					break
				}
				j++
			}
			cur.WriteString(script[i:min(j+1, len(script))])
			i = j
		case c == '$':
			tag := dollarTag(script[i:])
			if tag == "" {
				cur.WriteByte(c)
				continue
			}
			end := strings.Index(script[i+len(tag):], tag)
			if end < 0 {
				cur.WriteString(script[i:])
				i = len(script)
				continue
			}
			stop := i + len(tag) + end + len(tag)
			cur.WriteString(script[i:stop])
			i = stop - 1
		case c == ';':
			flush()
		default:
			cur.WriteByte(c)
		}
	}
	flush()
	return out
}

var dollarTagRe = regexp.MustCompile(`^\$[A-Za-z_]*\$`)

func dollarTag(s string) string { return dollarTagRe.FindString(s) }

func onlyComments(s string) bool {
	for _, line := range strings.Split(s, "\n") {
		t := strings.TrimSpace(line)
		if t != "" && !strings.HasPrefix(t, "--") {
			return false
		}
	}
	return true
}
