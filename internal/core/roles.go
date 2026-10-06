package core

import (
	"encoding/json"
	"net/http"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/Adron/substratalapps.com/internal/access"
	"github.com/Adron/substratalapps.com/internal/api/gen"
	"github.com/Adron/substratalapps.com/internal/db"
	"github.com/Adron/substratalapps.com/internal/httpx"
)

// PlatformPermission is one built-in platform permission key.
type PlatformPermission struct {
	Key         string `json:"key"`
	Scope       string `json:"scope"`
	Description string `json:"description"`
}

// PlatformPermissions is the platform permission catalog (Domain Model →
// Roles & Permissions → Platform permission catalog).
var PlatformPermissions = []PlatformPermission{
	{"users.list", "platform", "List/search User accounts. On an app-scoped key, confined to users with an access path to that Application."},
	{"users.manage", "platform", "Create, update, suspend, delete User accounts."},
	{"entitlements.manage", "platform", "Grant, toggle, and revoke Entitlements for any user. On an app-scoped key, confined to that Application."},
	{"applications.manage", "platform", "Create and edit Application catalog entries."},
	{"roles.manage", "platform", "Define Roles and assign/remove them on any user. On an app-scoped key, confined to that Application's Roles."},
	{"organizations.manage", "platform", "Manage any Organization: suspend/reactivate, membership, and member scope."},
	{"billing.manage", "platform", "View any Tenant's platform subscription and usage, and open its billing portal."},
	{"billing.refund", "platform", "Issue platform-subscription refunds and credits."},
	{"audit.view", "platform", "Query the Audit log for any user. On an app-scoped key, confined to that Application's events."},
	{"webhooks.manage", "platform", "Manage any webhook subscription, and create platform-scoped ones."},
	{"api_keys.manage", "platform", "Create, rotate, and revoke any API Key."},
	{"tenants.manage", "platform", "View any Tenant and request a tier change on a customer's behalf."},
}

func isPlatformPermission(k string) bool {
	for _, p := range PlatformPermissions {
		if p.Key == k {
			return true
		}
	}
	return false
}

// appConfinable are the four platform permissions an app-scoped key may
// carry in confined form (API Keys → App-confined permissions).
var appConfinable = []string{"entitlements.manage", "roles.manage", "users.list", "audit.view"}

// ── GET /v1/permissions ────────────────────────────────────────────────

func (s *Server) PermissionsList(w http.ResponseWriter, r *http.Request, params gen.PermissionsListParams) {
	s.serve(w, r, opts{op: "permissions.list"}, func(c *Call) error {
		out := []PlatformPermission{}
		scope := deref(params.Scope)
		if scope == "" || scope == "platform" {
			out = append(out, PlatformPermissions...)
		}
		if scope != "platform" {
			where, args := "true", db.Args{}
			if app, ok := c.p.AppKey(); ok {
				where, args = "a.id = :app", db.Args{"app": app}
			}
			if scope != "" {
				where = "(" + where + ") and a.id = :scope"
				args["scope"] = scope
			}
			apps, err := db.All[appRow](c.ctx, c.q, `select to_jsonb(a) from applications a where `+where+` order by a.id`, args)
			if err != nil {
				return err
			}
			for _, a := range apps {
				if _, isKey := c.p.AppKey(); !isKey {
					if ok, err := c.appVisible(a); err != nil {
						return err
					} else if !ok {
						continue
					}
				}
				for _, p := range a.Permissions {
					out = append(out, PlatformPermission{Key: p.Key, Scope: a.ID, Description: deref(p.Description)})
				}
			}
		}
		return c.OK(httpx.List[PlatformPermission]{Data: out})
	})
}

// ── Roles ──────────────────────────────────────────────────────────────

type role struct {
	ID              string    `json:"id"`
	Name            string    `json:"name"`
	Scope           string    `json:"scope"`
	Description     *string   `json:"description"`
	Permissions     []string  `json:"permissions"`
	Seed            bool      `json:"seed"`
	AssignmentCount int       `json:"assignment_count"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
	version         int
}

const roleSQL = `select to_jsonb(r) || jsonb_build_object('version_', r.version,
	'assignment_count', (select count(*) from user_role_assignments a where a.role_id = r.id)) from roles r`

func (c *Call) queryRoles(where string, args db.Args) ([]role, error) {
	raw, err := c.q.Query(c.ctx, roleSQL+" where "+where, args)
	if err != nil {
		return nil, err
	}
	out := make([]role, 0, len(raw))
	for _, x := range raw {
		var r struct {
			role
			V int `json:"version_"`
		}
		if err := json.Unmarshal(x, &r); err != nil {
			return nil, err
		}
		r.role.version = r.V
		if r.role.Permissions == nil {
			r.role.Permissions = []string{}
		}
		out = append(out, r.role)
	}
	return out, nil
}

func (c *Call) loadRole(id string) (role, error) {
	rs, err := c.queryRoles("r.id = :id", db.Args{"id": id})
	if err != nil {
		return role{}, err
	}
	if len(rs) == 0 {
		return role{}, httpx.NotFoundResource("role")
	}
	return rs[0], nil
}

// roleID derives a Role id (Conventions → IDs).
func roleID(scope, name string) string {
	if scope == "platform" {
		return "role_platform_" + name
	}
	return "role_" + strings.TrimPrefix(scope, "app_") + "_" + name
}

// canSeeRole: platform Roles to any User with a platform permission (and
// platform keys); app Roles to the app's owner, its own key, roles.manage.
func (c *Call) canSeeRole(r role) (bool, error) {
	if r.Scope == "platform" {
		return (c.p.IsUser() && (len(c.p.Perms) > 0)) || c.p.PlatformKey(), nil
	}
	if c.can("roles.manage") {
		return true, nil
	}
	if app, ok := c.p.AppKey(); ok {
		return app == r.Scope, nil
	}
	a, err := c.loadApp(r.Scope)
	if err != nil {
		return false, nil
	}
	return c.isAppOwner(a)
}

// canManageRoleScope authorizes defining or changing Roles in scope.
func (c *Call) canManageRoleScope(scope string) (*appRow, error) {
	if scope == "platform" {
		if err := c.require("roles.manage"); err != nil {
			return nil, err
		}
		return nil, nil
	}
	a, err := c.loadApp(scope)
	if err == db.ErrNotFound {
		return nil, httpx.NotFoundResource("application")
	}
	if err != nil {
		return nil, err
	}
	if app, ok := c.p.AppKey(); ok && app != scope {
		return nil, httpx.E(403, "role_scope_mismatch", "An app key can only act on its own Application's Roles.")
	}
	if ok, err := c.canManageApp("roles.manage", a); err != nil {
		return nil, err
	} else if !ok {
		return nil, httpx.Forbidden("roles.manage")
	}
	return &a, nil
}

var roleNameRe = regexp.MustCompile(`^[a-z][a-z0-9_]{1,40}$`)

// validateRolePermissions: platform Roles hold only platform keys, app
// Roles only their Application's declared app.<slug>.* keys; a platform
// Role can't grant what the caller doesn't hold.
func (c *Call) validateRolePermissions(scope string, a *appRow, perms []string) error {
	for _, p := range perms {
		if scope == "platform" {
			if !isPlatformPermission(p) {
				return httpx.E(422, "unknown_permission", "A platform Role can hold only platform permission keys.").With("permission", p)
			}
			if !c.can(p) {
				return httpx.E(403, "role_escalation_forbidden", "You can't grant a permission you don't hold.").With("permission", p)
			}
			continue
		}
		declared := false
		for _, ap := range a.Permissions {
			if ap.Key == p {
				declared = true
			}
		}
		if !declared {
			return httpx.E(422, "unknown_permission", "An app Role can hold only its Application's declared permission keys.").With("permission", p)
		}
	}
	return nil
}

func (s *Server) RolesList(w http.ResponseWriter, r *http.Request, params gen.RolesListParams) {
	s.serve(w, r, opts{op: "roles.list"}, func(c *Call) error {
		where, args := []string{"true"}, db.Args{}
		if params.Scope != nil {
			where = append(where, "r.scope = :scope")
			args["scope"] = *params.Scope
		}
		lim := httpx.Limit(params.Limit)
		filters := httpx.Filters(c.r)
		pos, err := c.s.Cursors.Decode(deref(params.Cursor), filters)
		if err != nil {
			return err
		}
		if pos != nil {
			where = append(where, "(r.created_at, r.id) < (:ps::timestamptz, :pi)")
			args["ps"], args["pi"] = pos.Sort, pos.ID
		}
		all, err := c.queryRoles(strings.Join(where, " and ")+" order by r.created_at desc, r.id desc", args)
		if err != nil {
			return err
		}
		var visible []role
		for _, x := range all {
			if ok, err := c.canSeeRole(x); err != nil {
				return err
			} else if ok {
				visible = append(visible, x)
			}
			if len(visible) > lim {
				break
			}
		}
		return c.OK(httpx.Paginate(c.s.Cursors, visible, lim, filters, func(x role) (string, string) {
			return httpx.TimeKey(x.CreatedAt), x.ID
		}))
	})
}

func (s *Server) RolesGet(w http.ResponseWriter, r *http.Request, id string) {
	s.serve(w, r, opts{op: "roles.get"}, func(c *Call) error {
		x, err := c.loadRole(id)
		if err != nil {
			return err
		}
		if ok, err := c.canSeeRole(x); err != nil {
			return err
		} else if !ok {
			return httpx.NotFoundResource("role")
		}
		c.Versioned(x.version)
		return c.OK(x)
	})
}

func (s *Server) RolesCreate(w http.ResponseWriter, r *http.Request) {
	s.serve(w, r, opts{op: "roles.create", idem: idemOptional}, func(c *Call) error {
		var in struct {
			Name        string   `json:"name"`
			Scope       string   `json:"scope"`
			Description *string  `json:"description"`
			Permissions []string `json:"permissions"`
		}
		if err := c.Decode(&in); err != nil {
			return err
		}
		var f httpx.Fields
		if !roleNameRe.MatchString(in.Name) {
			f.Add("name", "invalid_format").Pattern = roleNameRe.String()
		}
		if in.Scope == "" {
			f.Add("scope", "required")
		}
		if in.Permissions == nil {
			f.Add("permissions", "required")
		}
		if in.Description != nil && len([]rune(*in.Description)) > 2000 {
			f.Max("description", "too_long", 2000)
		}
		if err := f.Err(); err != nil {
			return err
		}
		a, err := c.canManageRoleScope(in.Scope)
		if err != nil {
			return err
		}
		if a != nil {
			if !slices.Contains(a.AvailableAppRoles, in.Name) {
				return httpx.E(422, "app_role_not_declared", "An app Role's name must be one of the Application's available_app_roles.")
			}
			t, err := c.loadTenant(a.TenantID)
			if err != nil {
				return err
			}
			if err := tenantWritable(t); err != nil {
				return err
			}
			if t.Restricted && !c.testMode() {
				return subscriptionRequired(t)
			}
		}
		perms := dedupe(in.Permissions)
		if err := c.validateRolePermissions(in.Scope, a, perms); err != nil {
			return err
		}
		id := roleID(in.Scope, in.Name)
		if _, err := c.q.Exec(c.ctx, `insert into roles (id, name, scope, description, permissions, test_mode)
			values (:id, :n, :s, :d, :p::text[], :tm)`,
			db.Args{"id": id, "n": in.Name, "s": in.Scope, "d": in.Description, "p": db.TextArray(perms), "tm": c.testMode()}); err != nil {
			if _, ok := db.UniqueViolation(err); ok {
				return httpx.Conflict("role_name_taken", "A Role with this name already exists in this scope.")
			}
			return err
		}
		x, err := c.loadRole(id)
		if err != nil {
			return err
		}
		if err := c.audit(Audit{Action: "role.created", TargetType: "role", TargetID: id, ApplicationID: appIDOf(in.Scope), After: x}); err != nil {
			return err
		}
		c.Versioned(x.version)
		return c.Created(x)
	})
}

func appIDOf(scope string) *string {
	if scope == "platform" {
		return nil
	}
	return &scope
}

func (s *Server) RolesUpdate(w http.ResponseWriter, r *http.Request, id string, params gen.RolesUpdateParams) {
	s.serve(w, r, opts{op: "roles.update"}, func(c *Call) error {
		x, err := c.loadRole(id)
		if err != nil {
			return err
		}
		if ok, err := c.canSeeRole(x); err != nil || !ok {
			if err == nil {
				err = httpx.NotFoundResource("role")
			}
			return err
		}
		a, err := c.canManageRoleScope(x.Scope)
		if err != nil {
			return err
		}
		if x.Seed {
			return httpx.E(403, "seed_role_immutable", "The built-in platform Roles can't be changed.")
		}
		if err := httpx.CheckIfMatch(c.r, x.version); err != nil {
			return err
		}
		p, err := c.Patch()
		if err != nil {
			return err
		}
		if err := p.RejectReadOnly("id", "name", "scope", "seed", "assignment_count", "created_at", "updated_at"); err != nil {
			return err
		}
		var f httpx.Fields
		p.RequireNonNull(&f, "permissions")
		next := x
		if p.Has("description") {
			next.Description, _ = p.String("description", &f)
			if next.Description != nil && len([]rune(*next.Description)) > 2000 {
				f.Max("description", "too_long", 2000)
			}
		}
		if p.Has("permissions") {
			var perms []string
			if p.Get("permissions", &perms, &f) {
				next.Permissions = dedupe(perms)
			}
		}
		if err := f.Err(); err != nil {
			return err
		}
		if err := c.validateRolePermissions(x.Scope, a, next.Permissions); err != nil {
			return err
		}
		shrinks := false
		for _, old := range x.Permissions {
			if !slices.Contains(next.Permissions, old) {
				shrinks = true
			}
		}
		if err := c.destructiveIf(shrinks); err != nil {
			return err
		}
		if _, err := c.q.Exec(c.ctx, `update roles set description = :d, permissions = :p::text[] where id = :id`,
			db.Args{"d": next.Description, "p": db.TextArray(next.Permissions), "id": x.ID}); err != nil {
			return err
		}
		after, err := c.loadRole(x.ID)
		if err != nil {
			return err
		}
		if err := c.audit(Audit{Action: "role.updated", TargetType: "role", TargetID: x.ID, ApplicationID: appIDOf(x.Scope),
			Before: x, After: after}); err != nil {
			return err
		}
		c.Versioned(after.version)
		return c.OK(after)
	})
}

func (s *Server) RolesDelete(w http.ResponseWriter, r *http.Request, id string) {
	s.serve(w, r, opts{op: "roles.delete"}, func(c *Call) error {
		x, err := c.loadRole(id)
		if err != nil {
			return err
		}
		if ok, err := c.canSeeRole(x); err != nil || !ok {
			if err == nil {
				err = httpx.NotFoundResource("role")
			}
			return err
		}
		if _, err := c.canManageRoleScope(x.Scope); err != nil {
			return err
		}
		if x.Seed {
			return httpx.E(403, "seed_role_immutable", "The built-in platform Roles can't be deleted.")
		}
		if err := httpx.CheckIfMatch(c.r, x.version); err != nil {
			return err
		}
		if x.AssignmentCount > 0 {
			return httpx.Conflict("role_in_use", "This Role still has assignments.")
		}
		if _, err := c.q.Exec(c.ctx, `delete from roles where id = :id`, db.Args{"id": x.ID}); err != nil {
			return err
		}
		if err := c.audit(Audit{Action: "role.deleted", TargetType: "role", TargetID: x.ID, ApplicationID: appIDOf(x.Scope), Before: x}); err != nil {
			return err
		}
		return c.NoContent()
	})
}

// ── Role assignments ───────────────────────────────────────────────────

type actorRef struct {
	Type string `json:"type"`
	ID   string `json:"id"`
}

type roleAssignment struct {
	UserID        string     `json:"user_id"`
	RoleID        string     `json:"role_id"`
	ApplicationID *string    `json:"application_id"`
	AssignedAt    *time.Time `json:"assigned_at"`
	AssignedBy    *actorRef  `json:"assigned_by"`
	Implicit      bool       `json:"implicit"`
}

func (c *Call) loadAssignments(userID string, appID *string) ([]roleAssignment, error) {
	where, args := "a.user_id = :u", db.Args{"u": userID}
	if appID != nil {
		where += " and a.application_id = :app"
		args["app"] = *appID
	}
	return db.All[roleAssignment](c.ctx, c.q, `select jsonb_build_object('user_id', a.user_id, 'role_id', a.role_id,
			'application_id', a.application_id, 'assigned_at', a.assigned_at,
			'assigned_by', jsonb_build_object('type', a.assigned_by_type, 'id', a.assigned_by_id), 'implicit', false)
		from user_role_assignments a where `+where+` order by a.assigned_at, a.role_id`, args)
}

func (s *Server) RolesListForUser(w http.ResponseWriter, r *http.Request, id gen.UserIdOrMe, params gen.RolesListForUserParams) {
	s.serve(w, r, opts{op: "roles.listForUser"}, func(c *Call) error {
		uid, err := c.selfID(id)
		if err != nil {
			return err
		}
		appFilter := params.ApplicationId
		if app, ok := c.p.AppKey(); ok {
			appFilter = &app
		} else if !c.isSelf(uid) && !c.can("roles.manage") && !c.can("users.list") {
			return httpx.Forbidden("roles.manage")
		}
		if _, err := c.visibleUser(uid); err != nil {
			return err
		}
		rows, err := c.loadAssignments(uid, appFilter)
		if err != nil {
			return err
		}
		// Implicit default_app_role rows, for Applications the user can
		// actively reach right now.
		pairs, err := c.userAppPairs(uid)
		if err != nil {
			return err
		}
		res, err := c.resolvePairs(pairs)
		if err != nil {
			return err
		}
		for _, pr := range pairs {
			if appFilter != nil && pr.App != *appFilter {
				continue
			}
			if !res[pr].Allowed {
				continue
			}
			a, err := c.loadApp(pr.App)
			if err != nil || a.DefaultAppRole == nil {
				continue
			}
			rid := roleID(a.ID, *a.DefaultAppRole)
			explicit := false
			for _, x := range rows {
				if x.RoleID == rid {
					explicit = true
				}
			}
			if !explicit {
				app := a.ID
				rows = append(rows, roleAssignment{UserID: uid, RoleID: rid, ApplicationID: &app, Implicit: true})
			}
		}
		return c.OK(httpx.List[roleAssignment]{Data: rows})
	})
}

// authorizeAssignment applies "Who may assign what".
func (c *Call) authorizeAssignment(x role) error {
	if x.Scope == "platform" {
		if err := c.require("roles.manage"); err != nil {
			return err
		}
		for _, p := range x.Permissions {
			if !c.can(p) {
				return httpx.E(403, "role_escalation_forbidden", "You can't assign a Role granting a permission you don't hold.").With("permission", p)
			}
		}
		return nil
	}
	if app, ok := c.p.AppKey(); ok && app != x.Scope {
		return httpx.E(403, "role_scope_mismatch", "An app key can only assign its own Application's Roles.")
	}
	_, err := c.canManageRoleScope(x.Scope)
	return err
}

func (s *Server) RolesAssign(w http.ResponseWriter, r *http.Request, id gen.UserIdOrMe, roleID string) {
	s.serve(w, r, opts{op: "roles.assign", idem: idemOptional}, func(c *Call) error {
		uid, err := c.selfID(id)
		if err != nil {
			return err
		}
		x, err := c.loadRole(roleID)
		if err != nil {
			return err
		}
		if app, ok := c.p.AppKey(); ok && x.Scope != app {
			return httpx.E(403, "role_scope_mismatch", "An app key can only assign its own Application's Roles.")
		}
		if err := c.authorizeAssignment(x); err != nil {
			return err
		}
		u, err := c.visibleUser(uid)
		if err != nil {
			return err
		}
		if u.Status == "deleted" {
			return httpx.NotFoundResource("user")
		}
		existing, err := c.loadAssignments(uid, nil)
		if err != nil {
			return err
		}
		for _, a := range existing {
			if a.RoleID == x.ID {
				return c.OK(a)
			}
		}
		typ, actorID := c.actor()
		app := appIDOf(x.Scope)
		if _, err := c.q.Exec(c.ctx, `insert into user_role_assignments (user_id, role_id, application_id,
				assigned_by_type, assigned_by_id, test_mode)
			values (:u, :r, :a, :t, :by, :tm)`,
			db.Args{"u": uid, "r": x.ID, "a": app, "t": typ, "by": actorID, "tm": c.testMode()}); err != nil {
			return err
		}
		rows, err := c.loadAssignments(uid, nil)
		if err != nil {
			return err
		}
		var created roleAssignment
		for _, a := range rows {
			if a.RoleID == x.ID {
				created = a
			}
		}
		data := map[string]any{"user_id": uid, "role_id": x.ID, "application_id": app}
		if err := c.audit(Audit{Action: "role.assigned", TargetType: "role_assignment", TargetID: uid + ":" + x.ID,
			TargetUserID: &uid, ApplicationID: app, After: data}); err != nil {
			return err
		}
		if err := c.emit("role.assigned", app, data); err != nil {
			return err
		}
		return c.Created(created)
	})
}

func (s *Server) RolesRemove(w http.ResponseWriter, r *http.Request, id gen.UserIdOrMe, roleID string) {
	s.serve(w, r, opts{op: "roles.remove"}, func(c *Call) error {
		uid, err := c.selfID(id)
		if err != nil {
			return err
		}
		x, err := c.loadRole(roleID)
		if err != nil {
			return err
		}
		if err := c.authorizeAssignment(x); err != nil {
			return err
		}
		if x.ID == "role_platform_member" {
			return httpx.Conflict("member_role_required", "Every User holds the member platform Role.")
		}
		n, err := c.q.Exec(c.ctx, `delete from user_role_assignments where user_id = :u and role_id = :r`,
			db.Args{"u": uid, "r": x.ID})
		if err != nil {
			return err
		}
		if n > 0 {
			app := appIDOf(x.Scope)
			data := map[string]any{"user_id": uid, "role_id": x.ID, "application_id": app}
			if err := c.audit(Audit{Action: "role.removed", TargetType: "role_assignment", TargetID: uid + ":" + x.ID,
				TargetUserID: &uid, ApplicationID: app, Before: data}); err != nil {
				return err
			}
			if err := c.emit("role.removed", app, data); err != nil {
				return err
			}
		}
		return c.NoContent()
	})
}

// ── GET /v1/users/{id}/apps/{appId}/effective-permissions ──────────────

type accessPath struct {
	Source         string     `json:"source"`
	EntitlementID  string     `json:"entitlement_id"`
	Status         string     `json:"status"`
	OrganizationID *string    `json:"organization_id"`
	MemberDecision *string    `json:"member_decision"`
	StartsAt       *time.Time `json:"starts_at,omitempty"`
}

func (s *Server) PermissionsGetEffective(w http.ResponseWriter, r *http.Request, id gen.UserIdOrMe, appID gen.AppId) {
	s.serve(w, r, opts{op: "permissions.getEffective"}, func(c *Call) error {
		uid, err := c.selfID(id)
		if err != nil {
			return err
		}
		if app, ok := c.p.AppKey(); ok {
			if app != appID {
				return httpx.NotFoundResource("application")
			}
		} else if !c.isSelf(uid) && !c.can("users.list") && !c.can("entitlements.manage") {
			return httpx.Forbidden("users.list")
		}
		if _, err := c.loadApp(appID); err == db.ErrNotFound {
			return httpx.NotFoundResource("application")
		} else if err != nil {
			return err
		}
		u, err := c.visibleUser(uid)
		if err != nil {
			return err
		}
		res, err := c.resolveOne(uid, appID)
		if err != nil {
			return err
		}
		paths := make([]accessPath, 0, len(res.Paths))
		for _, p := range res.Paths {
			ap := accessPath{Source: p.Source, EntitlementID: p.EntitlementID, Status: access.EffectiveStatus(p, c.now),
				OrganizationID: p.OrganizationID}
			if p.IsOrg() {
				d := access.MemberDecision(p, uid)
				if !p.MembershipActive {
					d = access.Excluded
				}
				ap.MemberDecision = &d
			}
			if p.StartsAt.After(c.now) {
				st := p.StartsAt
				ap.StartsAt = &st
			}
			paths = append(paths, ap)
		}
		sort.SliceStable(paths, func(i, j int) bool { return paths[i].OrganizationID == nil && paths[j].OrganizationID != nil })
		// roles: explicit assignments for this Application always, plus the
		// implicit default_app_role only while allowed. Permissions are
		// empty unless allowed.
		var roles, perms []string
		if res.Allowed {
			held, err := c.appRolesFor(uid, appID)
			if err != nil {
				return err
			}
			roles, perms = access.EffectivePermissions(true, held)
		} else {
			explicit, err := c.loadAssignments(uid, &appID)
			if err != nil {
				return err
			}
			roles, perms = []string{}, []string{}
			for _, a := range explicit {
				roles = append(roles, a.RoleID)
			}
		}
		return c.OK(map[string]any{
			"user_id": uid, "application_id": appID, "allowed": res.Allowed, "user_status": u.Status,
			"entitlement_status": res.Res.Status, "access_paths": paths, "roles": roles,
			"effective_permissions": perms, "computed_at": c.now.Format(time.RFC3339),
		})
	})
}
