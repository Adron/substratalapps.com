package core

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/Adron/substratalapps.com/internal/api/gen"
	"github.com/Adron/substratalapps.com/internal/db"
	"github.com/Adron/substratalapps.com/internal/httpx"
	"github.com/Adron/substratalapps.com/internal/schema"
)

// UsersExport is GDPR Article 20 / CCPA right-to-know: everything keyed to
// the User, reading the same tables the hard-delete cascade clears (plus
// global Settings, which it deliberately keeps).
func (s *Server) UsersExport(w http.ResponseWriter, r *http.Request, id gen.UserIdOrMe) {
	s.serve(w, r, opts{op: "users.export"}, func(c *Call) error {
		uid, err := c.selfID(id)
		if err != nil {
			return err
		}
		if !c.isSelf(uid) && !c.can("users.manage") {
			return httpx.Forbidden("users.manage")
		}
		u, err := c.visibleUser(uid)
		if err != nil {
			return err
		}
		if err := c.hit("export:"+uid, perDay(5), false); err != nil {
			return err
		}
		out := map[string]any{"exported_at": c.now.Format(time.RFC3339), "user": u}
		if p, err := c.loadProfile(uid); err == nil {
			out["profile"] = p
		}
		if st, err := c.loadSettings(uid); err == nil {
			out["settings"] = st
		}
		sections := map[string]string{
			"identities": `select jsonb_build_object('method', method, 'mfa_enabled', mfa_enabled, 'last_used_at', last_used_at)
				from user_identities where user_id = :u order by created_at`,
			"sessions": `select jsonb_build_object('id', id, 'created_at', created_at, 'last_seen_at', last_seen_at,
				'expires_at', least(idle_expires_at, absolute_expires_at), 'ip_address', host(ip_address),
				'user_agent', user_agent, 'amr', amr) from sessions where user_id = :u order by created_at`,
			"organizations": `select jsonb_build_object('organization_id', organization_id, 'role', role,
				'membership_status', status, 'joined_at', joined_at) from organization_memberships where user_id = :u`,
		}
		for key, q := range sections {
			rows, err := c.q.Query(c.ctx, q, db.Args{"u": uid})
			if err != nil {
				return err
			}
			out[key] = rawList(rows)
		}
		roles, err := c.loadAssignments(uid, nil)
		if err != nil {
			return err
		}
		out["roles"] = roles
		apps, err := db.All[string](c.ctx, c.q, `select to_jsonb(a) from (
				select application_id as a from entitlements where user_id = :u
				union select application_id from app_profiles where user_id = :u
				union select application_id from app_settings where user_id = :u) x order by a`, db.Args{"u": uid})
		if err != nil {
			return err
		}
		var appData []map[string]any
		for _, appID := range apps {
			entry := map[string]any{"application_id": appID}
			if es, err := c.queryEnts("e.user_id = :u and e.application_id = :a order by e.created_at desc",
				db.Args{"u": uid, "a": appID}); err == nil && len(es) > 0 {
				entry["entitlement"] = es[0]
			}
			if ap, err := c.loadAppProfile(uid, appID); err == nil {
				entry["app_profile"] = ap
			}
			var ov json.RawMessage
			if err := db.One(c.ctx, c.q, &ov, `select overrides from app_settings where user_id = :u and application_id = :a`,
				db.Args{"u": uid, "a": appID}); err == nil {
				entry["app_settings"] = map[string]any{"overrides": ov}
			}
			appData = append(appData, entry)
		}
		if appData == nil {
			appData = []map[string]any{}
		}
		out["applications"] = appData
		events, err := db.All[auditEvent](c.ctx, c.q, auditSQL+` where a.target_user_id = :u order by a.timestamp desc, a.id desc`,
			db.Args{"u": uid})
		if err != nil {
			return err
		}
		out["audit_events"] = events
		return c.OK(out)
	})
}

func rawList(rows []json.RawMessage) []json.RawMessage {
	if rows == nil {
		return []json.RawMessage{}
	}
	return rows
}

// ── Typed settings projection (Database Schema → Typed fields) ─────────

func hashName(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])[:12]
}

// ProjectionDDL returns the statements that (re)build one Application's
// typed settings view and per-property partial expression indexes. Names
// are hashes (identifiers cap at 63 bytes); comments record readable names.
func ProjectionDDL(appID string, rawSchema json.RawMessage, existingIndexes []string) []string {
	sch, _, _ := schema.Parse(rawSchema)
	a := hashName(appID)
	view := "app_settings_" + a
	quotedApp := strings.ReplaceAll(appID, "'", "''")
	cols := []string{"user_id"}
	var stmts []string
	want := map[string]bool{}
	cast := map[string]string{"boolean": "try_bool", "integer": "try_int", "number": "try_num", "string": "try_text"}
	for _, k := range sch.Order {
		p := sch.Props[k]
		if !p.Scalar() {
			continue
		}
		fn := cast[p.Type]
		if p.Type == "string" && p.Format == "date-time" {
			fn = "try_timestamptz"
		}
		expr := fmt.Sprintf("substratal_util.%s(overrides->'%s')", fn, k)
		cols = append(cols, fmt.Sprintf("%s as %s", expr, k))
		idx := view + "_" + hashName(k)
		want[idx] = true
		stmts = append(stmts,
			fmt.Sprintf("create index concurrently if not exists %s on app_settings ((%s)) where application_id = '%s'", idx, expr, quotedApp),
			fmt.Sprintf("comment on index %s is '%s.%s'", idx, quotedApp, k))
	}
	pre := []string{
		fmt.Sprintf("drop view if exists %s", view),
		fmt.Sprintf("create view %s as select %s from app_settings where application_id = '%s'", view, strings.Join(cols, ", "), quotedApp),
		fmt.Sprintf("comment on view %s is '%s'", view, quotedApp),
	}
	for _, idx := range existingIndexes {
		if !want[idx] {
			pre = append(pre, "drop index concurrently if exists "+idx)
		}
	}
	return append(pre, stmts...)
}

// rebuildSettingsProjection runs after the transaction that changed an
// Application's schema commits, never inside the request itself. Index
// builds run CONCURRENTLY, so they go through autocommit, one statement
// at a time. Failures are logged; the next schema change or the nightly
// job retries.
func (s *Server) rebuildSettingsProjection(ctx context.Context, appID string) {
	if err := RebuildSettingsProjection(ctx, s.DB, appID); err != nil {
		s.Log.Error("settings projection rebuild failed", "application", appID, "err", err)
	}
}

// RebuildSettingsProjection is rebuildSettingsProjection for jobs.
func RebuildSettingsProjection(ctx context.Context, d db.DB, appID string) error {
	var raw json.RawMessage
	var existing []string
	err := d.Tx(ctx, db.Settings{AllModes: true}, func(q db.Querier) error {
		if err := db.One(ctx, q, &raw, `select settings_schema from applications where id = :a`, db.Args{"a": appID}); err != nil {
			return err
		}
		var err error
		existing, err = db.All[string](ctx, q, `select to_jsonb(indexname) from pg_indexes
			where tablename = 'app_settings' and indexname like :p`, db.Args{"p": "app_settings_" + hashName(appID) + "_%"})
		return err
	})
	if err != nil {
		return err
	}
	for _, st := range ProjectionDDL(appID, raw, existing) {
		if _, err := d.Autocommit().Exec(ctx, st, nil); err != nil {
			return fmt.Errorf("%s: %w", st, err)
		}
	}
	return nil
}
