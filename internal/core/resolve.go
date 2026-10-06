package core

import (
	"encoding/json"
	"slices"
	"time"

	"github.com/Adron/substratalapps.com/internal/access"
	"github.com/Adron/substratalapps.com/internal/db"
)

// pair is one (user, application) access question.
type pair struct{ User, App string }

// resolved is everything Access Control knows about one pair.
type resolved struct {
	UserStatus string
	Res        access.Resolution
	Paths      []access.Path
	Allowed    bool
	TenantID   string
}

type pathRow struct {
	EntitlementID    string     `json:"entitlement_id"`
	Source           string     `json:"source"`
	Status           string     `json:"status"`
	OrganizationID   *string    `json:"organization_id"`
	StartsAt         time.Time  `json:"starts_at"`
	EndsAt           *time.Time `json:"ends_at"`
	MemberScope      *string    `json:"member_scope"`
	MemberOverrides  []string   `json:"member_overrides"`
	CreatedAt        time.Time  `json:"created_at"`
	MembershipActive bool       `json:"membership_active"`
}

// resolvePairs runs resolved_entitlement_status for every pair in one
// query, then applies Starter seat limits where they can matter.
func (c *Call) resolvePairs(pairs []pair) (map[pair]resolved, error) {
	out := make(map[pair]resolved, len(pairs))
	if len(pairs) == 0 {
		return out, nil
	}
	users := make([]string, len(pairs))
	apps := make([]string, len(pairs))
	for i, p := range pairs {
		users[i], apps[i] = p.User, p.App
	}
	rows, err := c.q.Query(c.ctx, `with req as (
			select t.u, t.a from unnest(:users::text[], :apps::text[]) as t(u, a))
		select jsonb_build_object(
			'user_id', req.u, 'application_id', req.a,
			'user_status', (select status from users where id = req.u),
			'tenant_id', (select tenant_id from applications where id = req.a),
			'plan', (select t.plan from tenants t join applications a on a.tenant_id = t.id where a.id = req.a),
			'paths', coalesce((select jsonb_agg(p) from (
				select e.id as entitlement_id, e.source, e.status, null::text as organization_id,
				       e.starts_at, e.ends_at, null::text as member_scope, null::text[] as member_overrides,
				       e.created_at, true as membership_active
				  from entitlements e where e.user_id = req.u and e.application_id = req.a
				union all
				select e.id, e.source, e.status, e.organization_id, e.starts_at, e.ends_at,
				       e.member_scope, e.member_overrides, e.created_at,
				       m.status = 'active'
				  from entitlements e
				  join organization_memberships m on m.organization_id = e.organization_id and m.user_id = req.u
				 where e.application_id = req.a) p), '[]'::jsonb))
		from req`, db.Args{"users": db.TextArray(users), "apps": db.TextArray(apps)})
	if err != nil {
		return nil, err
	}
	seatCache := map[string]*seatSet{}
	for _, raw := range rows {
		var r struct {
			UserID     string    `json:"user_id"`
			AppID      string    `json:"application_id"`
			UserStatus *string   `json:"user_status"`
			TenantID   *string   `json:"tenant_id"`
			Plan       *string   `json:"plan"`
			Paths      []pathRow `json:"paths"`
		}
		if err := json.Unmarshal(raw, &r); err != nil {
			return nil, err
		}
		paths := make([]access.Path, 0, len(r.Paths))
		for _, p := range r.Paths {
			paths = append(paths, access.Path{
				EntitlementID: p.EntitlementID, Source: p.Source, Status: p.Status,
				OrganizationID: p.OrganizationID, StartsAt: p.StartsAt, EndsAt: p.EndsAt,
				MemberScope: p.MemberScope, MemberOverrides: p.MemberOverrides, CreatedAt: p.CreatedAt,
				MembershipActive: p.MembershipActive,
			})
		}
		// Seat limits apply only to org paths on a Starter Tenant, and only
		// in live mode: test-mode Users are never seats (Pricing → What
		// "seat" means), so live seat counts never gate test traffic.
		if r.Plan != nil && *r.Plan == "starter" && r.TenantID != nil && !c.testMode() && hasOrgPath(paths) {
			ss, ok := seatCache[*r.TenantID]
			if !ok {
				if ss, err = c.seats(*r.TenantID); err != nil {
					return nil, err
				}
				seatCache[*r.TenantID] = ss
			}
			if ss.atCap() && !ss.isSeat(r.UserID) {
				for i := range paths {
					if paths[i].IsOrg() {
						paths[i].SeatLimited = true
					}
				}
			}
		}
		res := access.Resolve(r.UserID, paths, c.now)
		status := ""
		if r.UserStatus != nil {
			status = *r.UserStatus
		}
		out[pair{r.UserID, r.AppID}] = resolved{
			UserStatus: status, Res: res, Paths: paths, Allowed: access.Allowed(status, res), TenantID: deref(r.TenantID),
		}
	}
	return out, nil
}

func (c *Call) resolveOne(userID, appID string) (resolved, error) {
	m, err := c.resolvePairs([]pair{{userID, appID}})
	if err != nil {
		return resolved{}, err
	}
	return m[pair{userID, appID}], nil
}

func hasOrgPath(paths []access.Path) bool {
	for _, p := range paths {
		if p.IsOrg() {
			return true
		}
	}
	return false
}

// seatSet is a Starter Tenant's seat occupancy (Pricing → What "seat"
// means): personal-path Users are always seats; org-only Users fill the
// remaining capacity in membership order.
type seatSet struct {
	cap      int
	personal []string
	org      []string // org-path-only users, by (joined_at, user_id)
}

func (s *seatSet) count() int {
	n := len(s.personal) + len(s.org)
	if n > s.cap {
		n = max(s.cap, len(s.personal))
	}
	return n
}

func (s *seatSet) atCap() bool { return len(s.personal)+len(s.org) >= s.cap }

func (s *seatSet) isSeat(userID string) bool {
	if slices.Contains(s.personal, userID) {
		return true
	}
	room := s.cap - len(s.personal)
	for i, u := range s.org {
		if i >= room {
			return false
		}
		if u == userID {
			return true
		}
	}
	return false
}

// seats computes a Tenant's seat occupancy from existing Entitlement and
// membership rows, with no separate tracking primitive.
func (c *Call) seats(tenantID string) (*seatSet, error) {
	var r struct {
		Personal []string `json:"personal"`
		Org      []string `json:"org"`
	}
	err := db.One(c.ctx, c.q, &r, seatsSQL, db.Args{"t": tenantID})
	if err != nil {
		return nil, err
	}
	cap := 1 << 30
	if t, err := c.loadTenant(tenantID); err == nil {
		if l := plans[t.Plan].Seats; l != nil {
			cap = *l
		}
	}
	return &seatSet{cap: cap, personal: r.Personal, org: r.Org}, nil
}

// seatsSQL lists a Tenant's seat holders: live, active Users with an
// active personal path, then org-only members included by an active org
// grant (an Organization's suspension doesn't change its members' access).
const seatsSQL = `with apps as (select id from applications where tenant_id = :t),
	grants as (
		select e.* from entitlements e
		 where e.application_id in (select id from apps) and e.status = 'active' and not e.test_mode
		   and e.starts_at <= now() and (e.ends_at is null or e.ends_at > now())),
	personal as (
		select distinct g.user_id as u from grants g join users u on u.id = g.user_id
		 where g.user_id is not null and u.status = 'active' and not u.test_mode),
	orgm as (
		select m.user_id as u, min(m.joined_at) as j from grants g
		  join organization_memberships m on m.organization_id = g.organization_id and m.status = 'active'
		  join users u on u.id = m.user_id and u.status = 'active' and not u.test_mode
		 where g.organization_id is not null
		   and (coalesce(g.member_scope, 'all_members') = 'all_members'
		        or (g.member_scope = 'allowlist' and m.user_id = any(g.member_overrides))
		        or (g.member_scope = 'denylist' and not (m.user_id = any(coalesce(g.member_overrides, '{}')))))
		 group by m.user_id)
	select jsonb_build_object(
		'personal', coalesce((select jsonb_agg(u order by u) from personal), '[]'::jsonb),
		'org', coalesce((select jsonb_agg(u order by j, u) from orgm where u not in (select u from personal)), '[]'::jsonb))`

// seatCount is the number of seats a Tenant currently uses.
func (c *Call) seatCount(tenantID string) (int, error) {
	ss, err := c.seats(tenantID)
	if err != nil {
		return 0, err
	}
	return ss.count(), nil
}

// appRolesFor loads the app-scoped Roles a User holds for an Application:
// explicit assignments plus the implicit default_app_role.
func (c *Call) appRolesFor(userID, appID string) ([]access.AppRole, error) {
	type role struct {
		ID          string   `json:"id"`
		Permissions []string `json:"permissions"`
	}
	rs, err := db.All[role](c.ctx, c.q, `select jsonb_build_object('id', r.id, 'permissions', r.permissions)
		from roles r where r.scope = :app and (
			r.id in (select role_id from user_role_assignments where user_id = :u and application_id = :app)
			or r.name = (select default_app_role from applications where id = :app))
		order by r.id`, db.Args{"u": userID, "app": appID})
	if err != nil {
		return nil, err
	}
	out := make([]access.AppRole, len(rs))
	for i, r := range rs {
		out[i] = access.AppRole{ID: r.ID, Permissions: r.Permissions}
	}
	return out, nil
}

// ── Derived access events (Webhooks → Event types) ─────────────────────

// accessWatch snapshots resolved access for pairs before a mutation; flip
// compares after it and emits access.granted / access.revoked for every
// pair whose resolved access actually changed.
type accessWatch struct {
	pairs  []pair
	before map[pair]resolved
}

func (c *Call) watchAccess(pairs []pair) (*accessWatch, error) {
	before, err := c.resolvePairs(pairs)
	if err != nil {
		return nil, err
	}
	return &accessWatch{pairs: pairs, before: before}, nil
}

// flip emits an access.* event for each pair that changed, with reason
// for grants (granted) and revocations (revoked).
func (c *Call) flip(w *accessWatch, grantedReason, revokedReason string) error {
	after, err := c.resolvePairs(w.pairs)
	if err != nil {
		return err
	}
	for _, p := range w.pairs {
		b, a := w.before[p].Allowed, after[p].Allowed
		if b == a {
			continue
		}
		app := p.App
		if a {
			err = c.emit("access.granted", &app, map[string]any{
				"user_id": p.User, "application_id": p.App, "reason": grantedReason, "entitlement_status": "active"})
		} else {
			err = c.emit("access.revoked", &app, map[string]any{
				"user_id": p.User, "application_id": p.App, "reason": revokedReason,
				"entitlement_status": after[p].Res.Status})
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// orgMemberPairs is every member (active or pending) of an Organization ×
// one Application: the users an org grant change can affect.
func (c *Call) orgMemberPairs(orgID, appID string) ([]pair, error) {
	us, err := db.All[string](c.ctx, c.q, `select to_jsonb(user_id) from organization_memberships
		where organization_id = :o order by user_id`, db.Args{"o": orgID})
	if err != nil {
		return nil, err
	}
	out := make([]pair, len(us))
	for i, u := range us {
		out[i] = pair{u, appID}
	}
	return out, nil
}

// userAppPairs is one User × every Application they have any path to.
func (c *Call) userAppPairs(userID string) ([]pair, error) {
	as, err := db.All[string](c.ctx, c.q, `select to_jsonb(a) from (
			select application_id as a from entitlements where user_id = :u
			union
			select e.application_id from entitlements e
			  join organization_memberships m on m.organization_id = e.organization_id
			 where m.user_id = :u) x order by a`, db.Args{"u": userID})
	if err != nil {
		return nil, err
	}
	out := make([]pair, len(as))
	for i, a := range as {
		out[i] = pair{userID, a}
	}
	return out, nil
}

// orgAppPairs is one User × every Application an Organization holds a grant to.
func (c *Call) orgAppPairs(userID, orgID string) ([]pair, error) {
	as, err := db.All[string](c.ctx, c.q, `select to_jsonb(a) from (select distinct application_id as a from entitlements
		where organization_id = :o) x order by a`, db.Args{"o": orgID})
	if err != nil {
		return nil, err
	}
	out := make([]pair, len(as))
	for i, a := range as {
		out[i] = pair{userID, a}
	}
	return out, nil
}
