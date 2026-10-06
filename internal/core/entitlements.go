package core

import (
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/Adron/substratalapps.com/internal/access"
	"github.com/Adron/substratalapps.com/internal/api/gen"
	"github.com/Adron/substratalapps.com/internal/db"
	"github.com/Adron/substratalapps.com/internal/httpx"
	"github.com/Adron/substratalapps.com/internal/ids"
)

type grantedVia struct {
	OrganizationID string `json:"organization_id"`
	MemberDecision string `json:"member_decision"`
}

// entitlement is the API Entitlement object.
type entitlement struct {
	ID                  string      `json:"id"`
	UserID              *string     `json:"user_id"`
	OrganizationID      *string     `json:"organization_id"`
	ApplicationID       string      `json:"application_id"`
	Status              string      `json:"status"`
	Source              string      `json:"source"`
	OrderID             *string     `json:"order_id"`
	StartsAt            time.Time   `json:"starts_at"`
	EndsAt              *time.Time  `json:"ends_at"`
	DisabledReason      *string     `json:"disabled_reason"`
	MemberScope         *string     `json:"member_scope"`
	MemberOverrides     []string    `json:"member_overrides"`
	IncludedMemberCount *int        `json:"included_member_count,omitempty"`
	GrantedVia          *grantedVia `json:"granted_via,omitempty"`
	TestMode            bool        `json:"test_mode"`
	CreatedAt           time.Time   `json:"created_at"`
	UpdatedAt           time.Time   `json:"updated_at"`

	version  int
	tenantID string
}

// entSQL selects Entitlements as API objects plus version/tenant and, for
// org grants, how many active members the grant currently includes.
const entSQL = `select to_jsonb(e) || jsonb_build_object('version_', e.version, 'tenant_', e.tenant_id,
	'included_member_count', case when e.organization_id is not null then (
		select count(*) from organization_memberships m
		 where m.organization_id = e.organization_id and m.status = 'active'
		   and (coalesce(e.member_scope, 'all_members') = 'all_members'
		        or (e.member_scope = 'allowlist' and m.user_id = any(e.member_overrides))
		        or (e.member_scope = 'denylist' and not (m.user_id = any(coalesce(e.member_overrides, '{}')))))) end)
	from entitlements e`

func decodeEnt(raw json.RawMessage) (entitlement, error) {
	var x struct {
		entitlement
		V int    `json:"version_"`
		T string `json:"tenant_"`
	}
	if err := json.Unmarshal(raw, &x); err != nil {
		return entitlement{}, err
	}
	x.entitlement.version, x.entitlement.tenantID = x.V, x.T
	return x.entitlement, nil
}

func (c *Call) queryEnts(where string, args db.Args) ([]entitlement, error) {
	raw, err := c.q.Query(c.ctx, entSQL+" where "+where, args)
	if err != nil {
		return nil, err
	}
	out := make([]entitlement, 0, len(raw))
	for _, r := range raw {
		e, err := decodeEnt(r)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, nil
}

func (c *Call) loadEnt(id string) (entitlement, error) {
	es, err := c.queryEnts("e.id = :id", db.Args{"id": id})
	if err != nil {
		return entitlement{}, err
	}
	if len(es) == 0 {
		return entitlement{}, httpx.NotFoundResource("entitlement")
	}
	return es[0], nil
}

// eventEntitlement is the entitlement as it appears in webhook payloads
// and audit snapshots (no computed per-reader fields).
func (e entitlement) snapshot() entitlement {
	e.GrantedVia = nil
	return e
}

// ── POST /v1/users/{id}/entitlements ───────────────────────────────────

func (s *Server) EntitlementsGrant(w http.ResponseWriter, r *http.Request, id gen.UserIdOrMe, params gen.EntitlementsGrantParams) {
	s.serve(w, r, opts{op: "entitlements.grant", idem: idemRequired}, func(c *Call) error {
		uid, err := c.selfID(id)
		if err != nil {
			return err
		}
		var in struct {
			ApplicationID string     `json:"application_id"`
			Source        string     `json:"source"`
			OrderID       *string    `json:"order_id"`
			StartsAt      *time.Time `json:"starts_at"`
			EndsAt        *time.Time `json:"ends_at"`
		}
		if err := c.Decode(&in); err != nil {
			return err
		}
		if in.ApplicationID == "" || in.Source == "" {
			return httpx.Invalid("application_id and source are required.")
		}
		a, err := c.loadApp(in.ApplicationID)
		if err == db.ErrNotFound {
			return httpx.NotFoundResource("application")
		}
		if err != nil {
			return err
		}
		if ok, err := c.canManageApp("entitlements.manage", a); err != nil {
			return err
		} else if !ok {
			if _, isKey := c.p.AppKey(); isKey {
				return httpx.NotFoundResource("application")
			}
			return httpx.Forbidden("entitlements.manage")
		}
		if err := c.grantable(a); err != nil {
			return err
		}
		starts := c.now
		if in.StartsAt != nil {
			starts = in.StartsAt.UTC()
		}
		if err := validateTerm(in.Source, starts, in.EndsAt, in.OrderID, c.now); err != nil {
			return err
		}
		u, err := c.visibleUser(uid)
		if err != nil {
			return err
		}
		if u.Status == "deleted" {
			return httpx.NotFoundResource("user")
		}
		t, err := c.loadTenant(a.TenantID)
		if err != nil {
			return err
		}
		if err := tenantWritable(t); err != nil {
			return err
		}
		live, err := c.queryEnts(`e.user_id = :u and e.application_id = :a and e.status in ('active','disabled')`,
			db.Args{"u": uid, "a": a.ID})
		if err != nil {
			return err
		}
		if len(live) > 0 {
			return httpx.Conflict("entitlement_already_exists", "This user already has a live Entitlement to this Application.").
				With("existing_entitlement_id", live[0].ID).With("status", live[0].Status)
		}
		if err := c.checkNewSeat(t, uid); err != nil {
			return err
		}
		watch, err := c.watchAccess([]pair{{uid, a.ID}})
		if err != nil {
			return err
		}
		eid := ids.New(ids.Entitlement)
		if _, err := c.q.Exec(c.ctx, `insert into entitlements (id, user_id, application_id, tenant_id, status, source,
				order_id, starts_at, ends_at, test_mode)
			values (:id, :u, :a, :t, 'active', :src, :ord, :st::timestamptz, :en::timestamptz, :tm)`,
			db.Args{"id": eid, "u": uid, "a": a.ID, "t": a.TenantID, "src": in.Source, "ord": in.OrderID,
				"st": db.Time(starts), "en": db.NullTime(in.EndsAt), "tm": c.testMode()}); err != nil {
			if _, ok := db.UniqueViolation(err); ok {
				return httpx.Conflict("entitlement_already_exists", "This user already has a live Entitlement to this Application.")
			}
			return err
		}
		e, err := c.loadEnt(eid)
		if err != nil {
			return err
		}
		if err := c.entitlementEffects("entitlement.granted", nil, e, nil); err != nil {
			return err
		}
		if err := c.flip(watch, "entitlement_granted", "entitlement_revoked"); err != nil {
			return err
		}
		c.Versioned(e.version)
		return c.Created(e)
	})
}

// grantable: the Application must be approved, and an internal app only
// accepts grants from platform entitlements.manage.
func (c *Call) grantable(a appRow) error {
	if a.ReviewStatus != "approved" {
		return httpx.Conflict("application_not_available", "This Application isn't approved for new grants.").
			With("review_status", a.ReviewStatus)
	}
	if a.Visibility == "internal" && !c.can("entitlements.manage") {
		return httpx.Conflict("application_not_available", "Only platform entitlements.manage can grant an internal Application.")
	}
	return nil
}

func validateTerm(source string, starts time.Time, ends *time.Time, orderID *string, now time.Time) error {
	var f httpx.Fields
	switch source {
	case "purchase", "trial", "admin_grant":
	case "org_seat":
		return httpx.E(422, "invalid_source", "org_seat grants are created through POST /v1/organizations/{id}/entitlements.")
	default:
		f.Enum("source", "purchase", "trial", "admin_grant")
	}
	if source == "trial" && ends == nil {
		return httpx.E(422, "ends_at_required", "A trial needs an ends_at.")
	}
	if starts.After(now.AddDate(1, 0, 0)) {
		f.Add("starts_at", "out_of_range").Message = "at most 1 year ahead"
	}
	if ends != nil && !ends.After(starts) {
		f.Add("ends_at", "out_of_range").Message = "must be after starts_at"
	}
	if orderID != nil && len(*orderID) > 255 {
		f.Max("order_id", "too_long", 255)
	}
	return f.Err()
}

// checkNewSeat enforces the Starter seat cap and restricted mode for a
// write that would make userID a new seat (Pricing → Enforcement). Test
// mode never consumes seats.
func (c *Call) checkNewSeat(t tenantRow, userID string) error {
	if c.testMode() {
		return nil
	}
	if !t.Restricted && plans[t.Plan].Seats == nil {
		return nil
	}
	ss, err := c.seats(t.ID)
	if err != nil {
		return err
	}
	if ss.isSeat(userID) {
		return nil
	}
	if t.Restricted {
		return subscriptionRequired(t)
	}
	if l := plans[t.Plan].Seats; l != nil && ss.count() >= *l {
		return planLimitErr("seats", *l, ss.count(), t)
	}
	return nil
}

// entitlementEffects writes the audit event and resource webhook for one
// Entitlement change, in the same transaction.
func (c *Call) entitlementEffects(action string, before *entitlement, after entitlement, extra map[string]any) error {
	app := after.ApplicationID
	a := Audit{Action: action, TargetType: "entitlement", TargetID: after.ID, TargetUserID: after.UserID,
		ApplicationID: &app, OrganizationID: after.OrganizationID, TenantID: strp(after.tenantID), After: after.snapshot()}
	if before != nil {
		a.Before = before.snapshot()
	}
	if err := c.audit(a); err != nil {
		return err
	}
	data := map[string]any{"entitlement": after.snapshot()}
	for k, v := range extra {
		data[k] = v
	}
	return c.emit(action, &app, data)
}

// affectedPairs: a personal row affects its holder; an org grant affects
// every member of the holding Organization.
func (c *Call) affectedPairs(e entitlement) ([]pair, error) {
	if e.UserID != nil {
		return []pair{{*e.UserID, e.ApplicationID}}, nil
	}
	return c.orgMemberPairs(*e.OrganizationID, e.ApplicationID)
}

// ── Reading ────────────────────────────────────────────────────────────

// entAccess describes how the caller may act on one Entitlement.
type entAccess struct {
	manage   bool // entitlements.manage (platform/app-confined) or app owner
	orgAdmin bool // org_admin of the holding Organization (org rows only)
	read     bool
}

func (c *Call) entAccessFor(e entitlement) (entAccess, error) {
	var ea entAccess
	if app, ok := c.p.AppKey(); ok {
		if app != e.ApplicationID {
			return ea, nil
		}
		ea.manage = c.p.Perms["entitlements.manage"]
		ea.read = ea.manage || c.p.Perms["users.list"]
		return ea, nil
	}
	if c.can("entitlements.manage") {
		ea.manage, ea.read = true, true
		return ea, nil
	}
	a, err := c.loadApp(e.ApplicationID)
	if err != nil {
		return ea, err
	}
	if owner, err := c.isAppOwner(a); err != nil {
		return ea, err
	} else if owner {
		ea.manage, ea.read = true, true
		return ea, nil
	}
	if !c.p.IsUser() {
		return ea, nil
	}
	if e.UserID != nil && *e.UserID == c.p.UserID {
		ea.read = true
	}
	if e.OrganizationID != nil {
		if admin, err := c.isOrgAdmin(*e.OrganizationID); err != nil {
			return ea, err
		} else if admin {
			ea.orgAdmin, ea.read = true, true
		}
		if member, err := c.isActiveMember(*e.OrganizationID, c.p.UserID); err != nil {
			return ea, err
		} else if member {
			ea.read = true
		}
	}
	return ea, nil
}

func (c *Call) isActiveMember(orgID, userID string) (bool, error) {
	rows, err := c.q.Query(c.ctx, `select to_jsonb(true) from organization_memberships
		where organization_id = :o and user_id = :u and status = 'active'`, db.Args{"o": orgID, "u": userID})
	return len(rows) > 0, err
}

// withGrantedVia annotates an org row with one member's decision.
func (c *Call) withGrantedVia(e entitlement, userID string) (entitlement, error) {
	if e.OrganizationID == nil {
		return e, nil
	}
	res, err := c.resolveOne(userID, e.ApplicationID)
	if err != nil {
		return e, err
	}
	for _, p := range res.Paths {
		if p.EntitlementID == e.ID {
			e.GrantedVia = &grantedVia{OrganizationID: *e.OrganizationID, MemberDecision: access.MemberDecision(p, userID)}
		}
	}
	return e, nil
}

func (s *Server) EntitlementsGet(w http.ResponseWriter, r *http.Request, id string) {
	s.serve(w, r, opts{op: "entitlements.get"}, func(c *Call) error {
		e, err := c.loadEnt(id)
		if err != nil {
			return err
		}
		ea, err := c.entAccessFor(e)
		if err != nil {
			return err
		}
		if !ea.read {
			return httpx.NotFoundResource("entitlement")
		}
		if e.OrganizationID != nil && c.p.IsUser() && !ea.manage {
			if member, _ := c.isActiveMember(*e.OrganizationID, c.p.UserID); member {
				if e, err = c.withGrantedVia(e, c.p.UserID); err != nil {
					return err
				}
			}
		}
		c.Versioned(e.version)
		return c.OK(e)
	})
}

func (s *Server) EntitlementsListForUser(w http.ResponseWriter, r *http.Request, id gen.UserIdOrMe, params gen.EntitlementsListForUserParams) {
	s.serve(w, r, opts{op: "entitlements.listForUser"}, func(c *Call) error {
		uid, err := c.selfID(id)
		if err != nil {
			return err
		}
		appFilter := params.ApplicationId
		if app, ok := c.p.AppKey(); ok {
			if !c.p.Perms["entitlements.manage"] && !c.p.Perms["users.list"] {
				return httpx.Forbidden("entitlements.manage")
			}
			appFilter = &app
		} else if !c.isSelf(uid) && !c.can("entitlements.manage") {
			return httpx.Forbidden("entitlements.manage")
		}
		if _, err := c.visibleUser(uid); err != nil {
			return err
		}
		lim := httpx.Limit(params.Limit)
		filters := httpx.Filters(c.r)
		pos, err := c.s.Cursors.Decode(deref(params.Cursor), filters)
		if err != nil {
			return err
		}
		where := []string{`(e.user_id = :u or e.organization_id in (select organization_id from organization_memberships
			where user_id = :u and status = 'active'))`}
		args := db.Args{"u": uid, "lim": int64(lim + 1)}
		where, args = entFilters(where, args, appFilter, enumStr(params.Status), enumStr(params.Source), params.IncludeInactive)
		if pos != nil {
			where = append(where, "(e.created_at, e.id) < (:ps::timestamptz, :pi)")
			args["ps"], args["pi"] = pos.Sort, pos.ID
		}
		rows, err := c.queryEnts(strings.Join(where, " and ")+" order by e.created_at desc, e.id desc limit :lim", args)
		if err != nil {
			return err
		}
		for i := range rows {
			if rows[i], err = c.withGrantedVia(rows[i], uid); err != nil {
				return err
			}
		}
		return c.OK(httpx.Paginate(c.s.Cursors, rows, lim, filters, func(e entitlement) (string, string) {
			return httpx.TimeKey(e.CreatedAt), e.ID
		}))
	})
}

func entFilters(where []string, args db.Args, app, status, source *string, includeInactive *bool) ([]string, db.Args) {
	if app != nil {
		where = append(where, "e.application_id = :app")
		args["app"] = *app
	}
	if status != nil {
		where = append(where, "e.status = :status")
		args["status"] = *status
	} else if includeInactive == nil || !*includeInactive {
		where = append(where, "e.status in ('active','disabled')")
	}
	if source != nil {
		where = append(where, "e.source = :source")
		args["source"] = *source
	}
	return where, args
}

func enumStr[T ~string](p *T) *string {
	if p == nil {
		return nil
	}
	s := string(*p)
	return &s
}

func (s *Server) EntitlementsList(w http.ResponseWriter, r *http.Request, params gen.EntitlementsListParams) {
	s.serve(w, r, opts{op: "entitlements.list"}, func(c *Call) error {
		appFilter := params.ApplicationId
		var where []string
		args := db.Args{}
		if app, ok := c.p.AppKey(); ok {
			if !c.p.Perms["entitlements.manage"] {
				return httpx.Forbidden("entitlements.manage")
			}
			appFilter = &app
		} else if !c.can("entitlements.manage") {
			if !c.p.IsUser() {
				return httpx.Forbidden("entitlements.manage")
			}
			// An Application owner sees only their own Applications' rows.
			where = append(where, `e.application_id in (select a.id from applications a where a.owner_user_id = :me
				or a.owner_organization_id in (select organization_id from organization_memberships
				    where user_id = :me and role = 'org_admin' and status = 'active'))`)
			args["me"] = c.p.UserID
		}
		lim := httpx.Limit(params.Limit)
		args["lim"] = int64(lim + 1)
		filters := httpx.Filters(c.r)
		pos, err := c.s.Cursors.Decode(deref(params.Cursor), filters)
		if err != nil {
			return err
		}
		where, args = entFilters(where, args, appFilter, enumStr(params.Status), enumStr(params.Source), params.IncludeInactive)
		if params.UserId != nil {
			where = append(where, "e.user_id = :uid")
			args["uid"] = *params.UserId
		}
		if params.OrganizationId != nil {
			where = append(where, "e.organization_id = :oid")
			args["oid"] = *params.OrganizationId
		}
		if params.OrderId != nil {
			where = append(where, "e.order_id = :ord")
			args["ord"] = *params.OrderId
		}
		if params.EndsBefore != nil {
			where = append(where, "e.ends_at < :eb::timestamptz")
			args["eb"] = db.Time(*params.EndsBefore)
		}
		if pos != nil {
			where = append(where, "(e.created_at, e.id) < (:ps::timestamptz, :pi)")
			args["ps"], args["pi"] = pos.Sort, pos.ID
		}
		if len(where) == 0 {
			where = append(where, "true")
		}
		rows, err := c.queryEnts(strings.Join(where, " and ")+" order by e.created_at desc, e.id desc limit :lim", args)
		if err != nil {
			return err
		}
		return c.OK(httpx.Paginate(c.s.Cursors, rows, lim, filters, func(e entitlement) (string, string) {
			return httpx.TimeKey(e.CreatedAt), e.ID
		}))
	})
}

// ── PATCH /v1/entitlements/{id} — the toggle ───────────────────────────

func (s *Server) EntitlementsUpdate(w http.ResponseWriter, r *http.Request, id string, params gen.EntitlementsUpdateParams) {
	s.serve(w, r, opts{op: "entitlements.update", idem: idemOptional}, func(c *Call) error {
		e, err := c.loadEnt(id)
		if err != nil {
			return err
		}
		ea, err := c.entAccessFor(e)
		if err != nil {
			return err
		}
		if !ea.read {
			return httpx.NotFoundResource("entitlement")
		}
		if !ea.manage && !ea.orgAdmin {
			return httpx.Forbidden("entitlements.manage")
		}
		if err := httpx.CheckIfMatch(c.r, e.version); err != nil {
			return err
		}
		p, err := c.Patch()
		if err != nil {
			return err
		}
		if err := p.RejectReadOnly("id", "starts_at", "user_id", "organization_id", "application_id", "test_mode",
			"created_at", "updated_at", "included_member_count", "granted_via"); err != nil {
			return err
		}
		if !ea.manage {
			// An org admin may change only status (active ⇄ disabled) and
			// member scope, on their own Organization's org_seat row.
			for k := range p {
				if k != "status" && k != "disabled_reason" && k != "member_scope" && k != "member_overrides" {
					return httpx.Forbidden("entitlements.manage")
				}
			}
		}
		var f httpx.Fields
		p.RequireNonNull(&f, "status", "source")
		next := e
		var newStatus string
		if p.Get("status", &newStatus, &f) && newStatus != e.Status {
			next.Status = newStatus
		}
		if p.Has("disabled_reason") {
			next.DisabledReason, _ = p.String("disabled_reason", &f)
		}
		if p.Has("ends_at") {
			if p.IsNull("ends_at") {
				next.EndsAt = nil
			} else {
				var t time.Time
				if p.Get("ends_at", &t, &f) {
					t = t.UTC()
					next.EndsAt = &t
				}
			}
		}
		if p.Has("source") {
			var src string
			if p.Get("source", &src, &f) && src != e.Source {
				if !(e.Source == "trial" && src == "purchase") {
					return httpx.E(422, "immutable_field", "source can only change from trial to purchase.").With("field", "source")
				}
				next.Source = src
			}
		}
		if p.Has("order_id") {
			ord, _ := p.String("order_id", &f)
			if e.OrderID != nil && (ord == nil || *ord != *e.OrderID) {
				return httpx.E(422, "immutable_field", "order_id is set once and then immutable.").With("field", "order_id")
			}
			if ord != nil && len(*ord) > 255 {
				f.Max("order_id", "too_long", 255)
			}
			next.OrderID = ord
		}
		scopeChanged := false
		if p.Has("member_scope") || p.Has("member_overrides") {
			if e.OrganizationID == nil {
				return httpx.E(422, "not_an_org_grant", "member_scope and member_overrides apply only to org_seat rows.")
			}
			if p.Has("member_scope") {
				var ms string
				if p.Get("member_scope", &ms, &f) {
					if ms != "all_members" && ms != "allowlist" && ms != "denylist" {
						f.Enum("member_scope", "all_members", "allowlist", "denylist")
					}
					next.MemberScope = &ms
				}
			}
			if p.Has("member_overrides") {
				if p.IsNull("member_overrides") {
					next.MemberOverrides = nil
				} else {
					var mo []string
					if p.Get("member_overrides", &mo, &f) {
						if len(mo) > 1000 {
							f.Max("member_overrides", "too_long", 1000)
						}
						next.MemberOverrides = dedupe(mo)
					}
				}
			}
			if err := f.Err(); err != nil {
				return err
			}
			if err := c.checkOverridesAreMembers(*e.OrganizationID, next.MemberOverrides); err != nil {
				return err
			}
			scopeChanged = deref(next.MemberScope) != deref(e.MemberScope) || !slices.Equal(next.MemberOverrides, e.MemberOverrides)
		}
		if err := f.Err(); err != nil {
			return err
		}
		statusChanged := next.Status != e.Status
		if statusChanged {
			if err := checkEntTransition(e, &next, ea.manage, p.Has("ends_at"), c.now); err != nil {
				return err
			}
			if err := c.destructiveIf(next.Status == "disabled" || next.Status == "revoked"); err != nil {
				return err
			}
		} else if e.Status == "revoked" && len(p) > 0 {
			return httpx.Conflict("invalid_status_transition", "A revoked Entitlement is terminal; grant a new one instead.")
		}
		// ends_at must follow starts_at; a past value is allowed and simply
		// expires the row on the next sweep.
		if p.Has("ends_at") && next.EndsAt != nil && !next.EndsAt.After(e.StartsAt) {
			f.Add("ends_at", "out_of_range").Message = "must be after starts_at"
			return f.Err()
		}
		if next.Source == "trial" && next.EndsAt == nil {
			return httpx.E(422, "ends_at_required", "A trial needs an ends_at.")
		}
		a, err := c.loadApp(e.ApplicationID)
		if err != nil {
			return err
		}
		t, err := c.loadTenant(a.TenantID)
		if err != nil {
			return err
		}
		if err := tenantWritable(t); err != nil {
			return err
		}
		if statusChanged && next.Status == "active" {
			if e.UserID != nil {
				if err := c.checkNewSeat(t, *e.UserID); err != nil {
					return err
				}
				if e.Status == "expired" {
					live, err := c.queryEnts(`e.user_id = :u and e.application_id = :a and e.status in ('active','disabled') and e.id <> :id`,
						db.Args{"u": *e.UserID, "a": e.ApplicationID, "id": e.ID})
					if err != nil {
						return err
					}
					if len(live) > 0 {
						return httpx.Conflict("entitlement_already_exists", "A newer live Entitlement exists; change that one instead.").
							With("existing_entitlement_id", live[0].ID)
					}
				}
			} else if t.Restricted && !c.testMode() {
				return subscriptionRequired(t)
			}
		}
		pairs, err := c.affectedPairs(e)
		if err != nil {
			return err
		}
		watch, err := c.watchAccess(pairs)
		if err != nil {
			return err
		}
		if next.Status == "active" {
			next.DisabledReason = nil // cleared automatically on re-enable
		}
		if _, err := c.q.Exec(c.ctx, `update entitlements set status = :st, disabled_reason = :dr, ends_at = :en::timestamptz,
				source = :src, order_id = :ord, member_scope = :ms, member_overrides = :mo::text[]
			where id = :id`,
			db.Args{"st": next.Status, "dr": next.DisabledReason, "en": db.NullTime(next.EndsAt), "src": next.Source,
				"ord": next.OrderID, "ms": next.MemberScope, "mo": db.NullTextArray(next.MemberOverrides), "id": e.ID}); err != nil {
			if _, ok := db.UniqueViolation(err); ok {
				return httpx.Conflict("entitlement_already_exists", "A live Entitlement already exists for this holder and Application.")
			}
			return err
		}
		after, err := c.loadEnt(e.ID)
		if err != nil {
			return err
		}
		changed := changedFields(e, after)
		switch {
		case statusChanged:
			action := map[string]string{"active": "entitlement.granted", "disabled": "entitlement.disabled",
				"revoked": "entitlement.revoked"}[next.Status]
			if err := c.entitlementEffects(action, &e, after, nil); err != nil {
				return err
			}
		case len(changed) > 0:
			if err := c.entitlementEffects("entitlement.updated", &e, after, map[string]any{"changed_fields": changed}); err != nil {
				return err
			}
		}
		if scopeChanged {
			if err := c.entitlementEffects("entitlement.member_scope_changed", &e, after, nil); err != nil {
				return err
			}
		}
		granted, revoked := entReasons(e, next.Status)
		if e.OrganizationID != nil {
			granted, revoked = "org_grant_changed", "org_grant_changed"
		}
		if err := c.flip(watch, granted, revoked); err != nil {
			return err
		}
		c.Versioned(after.version)
		return c.OK(after)
	})
}

// entReasons are the access.* reasons for a personal row's change.
func entReasons(before entitlement, to string) (granted, revoked string) {
	switch to {
	case "revoked":
		return "entitlement_enabled", "entitlement_revoked"
	case "disabled":
		return "entitlement_enabled", "entitlement_disabled"
	case before.Status:
		// A term change: ends_at moved into the past expires access, or
		// moved forward restores it.
		return "entitlement_enabled", "entitlement_expired"
	default:
		return "entitlement_enabled", "entitlement_disabled"
	}
}

// checkEntTransition applies the Entitlements status transition table.
func checkEntTransition(e entitlement, next *entitlement, manage, endsSent bool, now time.Time) error {
	bad := func() error {
		return httpx.Conflict("invalid_status_transition", "The status change from "+e.Status+" to "+next.Status+" isn't allowed.")
	}
	reasonRequired := func() error {
		if next.DisabledReason == nil || *next.DisabledReason == "" {
			return httpx.E(422, "disabled_reason_required", "disabled_reason is required for this transition.")
		}
		if len([]rune(*next.DisabledReason)) > 255 {
			var f httpx.Fields
			f.Max("disabled_reason", "too_long", 255)
			return f.Err()
		}
		return nil
	}
	switch {
	case e.Status == "revoked":
		return bad()
	case e.Status == "active" && next.Status == "disabled":
		return reasonRequired()
	case e.Status == "disabled" && next.Status == "active":
		return nil
	case next.Status == "revoked" && (e.Status == "active" || e.Status == "disabled" || e.Status == "expired"):
		if !manage {
			return httpx.Forbidden("entitlements.manage")
		}
		return reasonRequired()
	case e.Status == "expired" && next.Status == "active":
		if !manage {
			return httpx.Forbidden("entitlements.manage")
		}
		if !endsSent || (next.EndsAt != nil && !next.EndsAt.After(now)) {
			return httpx.Conflict("invalid_status_transition", "Renewing an expired Entitlement needs a future ends_at, or ends_at: null.")
		}
		return nil
	}
	return bad()
}

func (c *Call) checkOverridesAreMembers(orgID string, overrides []string) error {
	if len(overrides) == 0 {
		return nil
	}
	members, err := db.All[string](c.ctx, c.q, `select to_jsonb(user_id) from organization_memberships
		where organization_id = :o and status = 'active' and user_id = any(:ids::text[])`,
		db.Args{"o": orgID, "ids": db.TextArray(overrides)})
	if err != nil {
		return err
	}
	for _, id := range overrides {
		if !slices.Contains(members, id) {
			return httpx.E(422, "member_override_not_a_member", "Every member_overrides id must be a current active member.").
				With("user_id", id)
		}
	}
	return nil
}

func changedFields(a, b entitlement) []string {
	var out []string
	if !timePtrEq(a.EndsAt, b.EndsAt) {
		out = append(out, "ends_at")
	}
	if a.Source != b.Source {
		out = append(out, "source")
	}
	if deref(a.OrderID) != deref(b.OrderID) {
		out = append(out, "order_id")
	}
	return out
}

func timePtrEq(a, b *time.Time) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return a.Equal(*b)
}

func dedupe(ss []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, s := range ss {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

// ── DELETE /v1/entitlements/{id} ───────────────────────────────────────

func (s *Server) EntitlementsDelete(w http.ResponseWriter, r *http.Request, id string) {
	s.serve(w, r, opts{op: "entitlements.delete"}, func(c *Call) error {
		e, err := c.loadEnt(id)
		if err != nil {
			return err
		}
		ea, err := c.entAccessFor(e)
		if err != nil {
			return err
		}
		if !ea.read {
			return httpx.NotFoundResource("entitlement")
		}
		if !ea.manage {
			return httpx.Forbidden("entitlements.manage")
		}
		if err := httpx.CheckIfMatch(c.r, e.version); err != nil {
			return err
		}
		if e.OrderID != nil {
			return httpx.Conflict("entitlement_order_linked", "An order-linked Entitlement is a financial record; revoke it instead.")
		}
		if c.now.Sub(e.CreatedAt) > 24*time.Hour {
			return httpx.Conflict("entitlement_too_old", "This Entitlement is over 24 hours old; revoke it instead.")
		}
		pairs, err := c.affectedPairs(e)
		if err != nil {
			return err
		}
		watch, err := c.watchAccess(pairs)
		if err != nil {
			return err
		}
		if _, err := c.q.Exec(c.ctx, `delete from entitlements where id = :id`, db.Args{"id": e.ID}); err != nil {
			return err
		}
		app := e.ApplicationID
		if err := c.audit(Audit{Action: "entitlement.deleted", TargetType: "entitlement", TargetID: e.ID,
			TargetUserID: e.UserID, ApplicationID: &app, OrganizationID: e.OrganizationID, TenantID: strp(e.tenantID),
			Before: e.snapshot()}); err != nil {
			return err
		}
		if err := c.emit("entitlement.deleted", &app, map[string]any{"entitlement": e.snapshot()}); err != nil {
			return err
		}
		reason := "entitlement_deleted"
		if e.OrganizationID != nil {
			reason = "org_grant_changed"
		}
		if err := c.flip(watch, "entitlement_granted", reason); err != nil {
			return err
		}
		return c.NoContent()
	})
}
