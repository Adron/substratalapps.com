package core

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/Adron/substratalapps.com/internal/api/gen"
	"github.com/Adron/substratalapps.com/internal/db"
	"github.com/Adron/substratalapps.com/internal/httpx"
)

type auditEvent struct {
	ID             string          `json:"id"`
	Action         string          `json:"action"`
	Actor          actorRef        `json:"actor"`
	Target         actorRef        `json:"target"`
	TargetUserID   *string         `json:"target_user_id"`
	ApplicationID  *string         `json:"application_id"`
	OrganizationID *string         `json:"organization_id"`
	TenantID       *string         `json:"tenant_id"`
	Before         json.RawMessage `json:"before"`
	After          json.RawMessage `json:"after"`
	RequestID      *string         `json:"request_id"`
	Timestamp      time.Time       `json:"timestamp"`
}

const auditSQL = `select jsonb_build_object('id', a.id, 'action', a.action,
	'actor', jsonb_build_object('type', a.actor_type, 'id', a.actor_id),
	'target', jsonb_build_object('type', a.target_type, 'id', a.target_id),
	'target_user_id', a.target_user_id, 'application_id', a.application_id, 'organization_id', a.organization_id,
	'tenant_id', a.tenant_id, 'before', a.before, 'after', a.after, 'request_id', a.request_id,
	'timestamp', a.timestamp) from audit_events a`

// ownsTenant: the owner_user_id User, or an org_admin of the owning Organization.
func (c *Call) ownsTenant(t tenantRow) (bool, error) {
	if c.p == nil || !c.p.IsUser() {
		return false, nil
	}
	if t.OwnerUserID != nil {
		return *t.OwnerUserID == c.p.UserID, nil
	}
	return c.isOrgAdmin(*t.OwnerOrganizationID)
}

// auditScope returns the forced scope for the caller, or 403.
func (c *Call) auditScope(targetUser, tenant *string) (where []string, args db.Args, err error) {
	args = db.Args{}
	if c.can("audit.view") {
		return nil, args, nil
	}
	if app, ok := c.p.AppKey(); ok {
		if !c.p.Perms["audit.view"] {
			return nil, nil, httpx.Forbidden("audit.view")
		}
		args["forced_app"] = app
		return []string{"a.application_id = :forced_app"}, args, nil
	}
	if c.p.IsUser() && targetUser != nil && *targetUser == c.p.UserID {
		args["forced_user"] = c.p.UserID
		return []string{"a.target_user_id = :forced_user"}, args, nil
	}
	if tenant != nil {
		t, err := c.loadTenant(*tenant)
		if err == nil {
			if owner, err := c.ownsTenant(t); err != nil {
				return nil, nil, err
			} else if owner {
				args["forced_tenant"] = t.ID
				return []string{"a.tenant_id = :forced_tenant"}, args, nil
			}
		}
	}
	return nil, nil, httpx.Forbidden("audit.view")
}

func (s *Server) AuditList(w http.ResponseWriter, r *http.Request, params gen.AuditListParams) {
	s.serve(w, r, opts{op: "audit.list"}, func(c *Call) error {
		var targetUser *string
		if params.TargetUserId != nil {
			uid, err := c.selfID(*params.TargetUserId)
			if err != nil {
				return err
			}
			targetUser = &uid
		}
		where, args, err := c.auditScope(targetUser, params.TenantId)
		if err != nil {
			return err
		}
		add := func(cond, key string, v *string) {
			if v != nil {
				where = append(where, cond)
				args[key] = *v
			}
		}
		add("a.target_user_id = :tu", "tu", targetUser)
		add("a.target_type = :tt", "tt", enumStr(params.TargetType))
		add("a.target_id = :ti", "ti", params.TargetId)
		add("a.actor_id = :ai", "ai", params.ActorId)
		add("a.actor_type = :at", "at", enumStr(params.ActorType))
		add("a.application_id = :app", "app", params.ApplicationId)
		add("a.organization_id = :org", "org", params.OrganizationId)
		add("a.tenant_id = :tnt", "tnt", params.TenantId)
		if params.Action != nil && len(*params.Action) > 0 {
			acts := make([]string, len(*params.Action))
			for i, a := range *params.Action {
				acts[i] = string(a)
			}
			where = append(where, "a.action = any(:acts::text[])")
			args["acts"] = db.TextArray(acts)
		}
		if params.Since != nil {
			where = append(where, "a.timestamp >= :since::timestamptz")
			args["since"] = db.Time(*params.Since)
		}
		if params.Until != nil {
			where = append(where, "a.timestamp < :until::timestamptz")
			args["until"] = db.Time(*params.Until)
		}
		lim := httpx.Limit(params.Limit)
		args["lim"] = int64(lim + 1)
		filters := httpx.Filters(c.r)
		pos, err := c.s.Cursors.Decode(deref(params.Cursor), filters)
		if err != nil {
			return err
		}
		if pos != nil {
			where = append(where, "(a.timestamp, a.id) < (:ps::timestamptz, :pi)")
			args["ps"], args["pi"] = pos.Sort, pos.ID
		}
		if len(where) == 0 {
			where = []string{"true"}
		}
		rows, err := db.All[auditEvent](c.ctx, c.q, auditSQL+" where "+strings.Join(where, " and ")+
			" order by a.timestamp desc, a.id desc limit :lim", args)
		if err != nil {
			return err
		}
		if params.TenantId != nil {
			if t, err := c.loadTenant(*params.TenantId); err == nil {
				if days := plans[t.Plan].AuditHotDays; days != nil {
					c.headers.Set("Audit-Hot-Window-Start", c.now.AddDate(0, 0, -*days).Format(time.RFC3339))
				}
			}
		}
		return c.OK(httpx.Paginate(c.s.Cursors, rows, lim, filters, func(x auditEvent) (string, string) {
			return httpx.TimeKey(x.Timestamp), x.ID
		}))
	})
}

func (s *Server) AuditGet(w http.ResponseWriter, r *http.Request, id string) {
	s.serve(w, r, opts{op: "audit.get"}, func(c *Call) error {
		var ev auditEvent
		if err := db.One(c.ctx, c.q, &ev, auditSQL+" where a.id = :id", db.Args{"id": id}); err == db.ErrNotFound {
			return httpx.NotFoundResource("audit_event")
		} else if err != nil {
			return err
		}
		where, args, err := c.auditScope(ev.TargetUserID, ev.TenantID)
		if err != nil {
			return httpx.NotFoundResource("audit_event")
		}
		if len(where) > 0 {
			args["id"] = id
			rows, err := c.q.Query(c.ctx, `select to_jsonb(a.id) from audit_events a where a.id = :id and `+
				strings.Join(where, " and "), args)
			if err != nil {
				return err
			}
			if len(rows) == 0 {
				return httpx.NotFoundResource("audit_event")
			}
		}
		return c.OK(ev)
	})
}
