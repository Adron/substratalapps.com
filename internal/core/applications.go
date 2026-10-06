package core

import (
	"encoding/json"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/Adron/substratalapps.com/internal/api/gen"
	"github.com/Adron/substratalapps.com/internal/db"
	"github.com/Adron/substratalapps.com/internal/httpx"
	"github.com/Adron/substratalapps.com/internal/schema"
)

// application is the full API Application object.
type application struct {
	ID                  string          `json:"id"`
	Slug                string          `json:"slug"`
	Name                string          `json:"name"`
	Description         *string         `json:"description"`
	IconURL             *string         `json:"icon_url"`
	LaunchURL           string          `json:"launch_url"`
	RedirectURIs        []string        `json:"redirect_uris"`
	SupportURL          *string         `json:"support_url"`
	EmailFromName       *string         `json:"email_from_name"`
	SettingsSchema      json.RawMessage `json:"settings_schema"`
	SettingsSchemaStats map[string]any  `json:"settings_schema_stats"`
	Permissions         []appPermission `json:"permissions"`
	AvailableAppRoles   []string        `json:"available_app_roles"`
	DefaultAppRole      *string         `json:"default_app_role"`
	Visibility          string          `json:"visibility"`
	OwnerUserID         *string         `json:"owner_user_id"`
	OwnerOrganizationID *string         `json:"owner_organization_id"`
	ReviewStatus        string          `json:"review_status"`
	ReviewNotes         *string         `json:"review_notes"`
	TenantID            string          `json:"tenant_id"`
	TestMode            bool            `json:"test_mode"`
	CreatedAt           time.Time       `json:"created_at"`
	UpdatedAt           time.Time       `json:"updated_at"`
}

type appSummary struct {
	ID           string  `json:"id"`
	Slug         string  `json:"slug"`
	Name         string  `json:"name"`
	Description  *string `json:"description"`
	IconURL      *string `json:"icon_url"`
	Visibility   string  `json:"visibility"`
	ReviewStatus string  `json:"review_status"`
}

func (a appRow) full(stats map[string]int) application {
	if stats == nil {
		stats = map[string]int{}
	}
	perms := a.Permissions
	if perms == nil {
		perms = []appPermission{}
	}
	return application{ID: a.ID, Slug: a.Slug, Name: a.Name, Description: a.Description, IconURL: a.IconURL,
		LaunchURL: a.LaunchURL, RedirectURIs: nonNil(a.RedirectURIs), SupportURL: a.SupportURL,
		EmailFromName: a.EmailFromName, SettingsSchema: a.SettingsSchema,
		SettingsSchemaStats: map[string]any{"stale_override_counts": stats}, Permissions: perms,
		AvailableAppRoles: nonNil(a.AvailableAppRoles), DefaultAppRole: a.DefaultAppRole, Visibility: a.Visibility,
		OwnerUserID: a.OwnerUserID, OwnerOrganizationID: a.OwnerOrganizationID, ReviewStatus: a.ReviewStatus,
		ReviewNotes: a.ReviewNotes, TenantID: a.TenantID, TestMode: a.TestMode, CreatedAt: a.CreatedAt, UpdatedAt: a.UpdatedAt}
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// staleOverrideCounts counts, per key, users whose override no longer
// validates against the current schema (Settings → stale overrides).
func (c *Call) staleOverrideCounts(a appRow) (map[string]int, error) {
	sch, _, _ := schema.Parse(a.SettingsSchema)
	rows, err := db.All[map[string]json.RawMessage](c.ctx, c.q, `select overrides from app_settings where application_id = :a`,
		db.Args{"a": a.ID})
	if err != nil {
		return nil, err
	}
	out := map[string]int{}
	for _, ov := range rows {
		for k, v := range ov {
			if schema.IsReserved(k) {
				if validReservedOverride(k, v) != nil {
					out[k]++
				}
				continue
			}
			p, ok := sch.Props[k]
			if !ok || len(p.Validate(v, k)) > 0 {
				out[k]++
			}
		}
	}
	return out, nil
}

func (c *Call) loadVisibleApp(id string) (appRow, error) {
	a, err := c.loadApp(id)
	if err == db.ErrNotFound {
		return a, httpx.NotFoundResource("application")
	}
	if err != nil {
		return a, err
	}
	if ok, err := c.appVisible(a); err != nil {
		return a, err
	} else if !ok {
		return a, httpx.NotFoundResource("application")
	}
	return a, nil
}

func (s *Server) ApplicationsList(w http.ResponseWriter, r *http.Request, params gen.ApplicationsListParams) {
	s.serve(w, r, opts{op: "applications.list"}, func(c *Call) error {
		admin := c.can("applications.manage")
		where, args := []string{"true"}, db.Args{}
		if params.Visibility != nil {
			where = append(where, "a.visibility = :vis")
			args["vis"] = string(*params.Visibility)
		}
		queue := false
		if params.ReviewStatus != nil {
			where = append(where, "a.review_status = :rs")
			args["rs"] = string(*params.ReviewStatus)
			queue = admin && *params.ReviewStatus == "pending_review"
		}
		if params.Owned != nil && *params.Owned {
			if !c.p.IsUser() {
				return c.OK(httpx.List[appSummary]{Data: []appSummary{}})
			}
			where = append(where, `(a.owner_user_id = :me or a.owner_organization_id in (select organization_id
				from organization_memberships where user_id = :me and role = 'org_admin' and status = 'active'))`)
			args["me"] = c.p.UserID
		}
		if admin || c.can("tenants.manage") {
			if params.TenantId != nil {
				where = append(where, "a.tenant_id = :t")
				args["t"] = *params.TenantId
			}
			if params.OwnerUserId != nil {
				where = append(where, "a.owner_user_id = :ou")
				args["ou"] = *params.OwnerUserId
			}
			if params.OwnerOrganizationId != nil {
				where = append(where, "a.owner_organization_id = :oo")
				args["oo"] = *params.OwnerOrganizationId
			}
		}
		lim := httpx.Limit(params.Limit)
		filters := httpx.Filters(c.r)
		pos, err := c.s.Cursors.Decode(deref(params.Cursor), filters)
		if err != nil {
			return err
		}
		// The review queue is oldest first; every other list is newest first.
		order, cmp := "a.created_at desc, a.id desc", "<"
		if queue {
			order, cmp = "a.created_at asc, a.id asc", ">"
		}
		if pos != nil {
			where = append(where, "(a.created_at, a.id) "+cmp+" (:ps::timestamptz, :pi)")
			args["ps"], args["pi"] = pos.Sort, pos.ID
		}
		all, err := db.All[appRow](c.ctx, c.q, `select to_jsonb(a) from applications a where `+
			strings.Join(where, " and ")+" order by "+order, args)
		if err != nil {
			return err
		}
		var out []appSummary
		var keys []appRow
		for _, a := range all {
			if ok, err := c.appVisible(a); err != nil {
				return err
			} else if !ok {
				continue
			}
			out = append(out, appSummary{a.ID, a.Slug, a.Name, a.Description, a.IconURL, a.Visibility, a.ReviewStatus})
			keys = append(keys, a)
			if len(out) > lim {
				break
			}
		}
		page := httpx.Paginate(c.s.Cursors, out, lim, filters, func(x appSummary) (string, string) { return "", "" })
		if page.Page.HasMore {
			last := keys[lim-1]
			cur := c.s.Cursors.Encode(httpx.TimeKey(last.CreatedAt), last.ID, filters)
			page.Page.NextCursor = &cur
		}
		return c.OK(page)
	})
}

func (s *Server) ApplicationsGet(w http.ResponseWriter, r *http.Request, id string) {
	s.serve(w, r, opts{op: "applications.get"}, func(c *Call) error {
		a, err := c.loadVisibleApp(id)
		if err != nil {
			return err
		}
		stats, err := c.staleOverrideCounts(a)
		if err != nil {
			return err
		}
		c.Versioned(a.Version)
		return c.OK(a.full(stats))
	})
}

// ── Validation (Applications → The Application object) ─────────────────

var (
	slugRe     = regexp.MustCompile(`^[a-z][a-z0-9-]{1,63}$`)
	privateURI = regexp.MustCompile(`^[a-z][a-z0-9+.-]*\.[a-z0-9+.-]+:/`)
)

func httpsURL(s string) bool {
	u, err := url.Parse(s)
	return err == nil && u.Scheme == "https" && u.Host != "" && u.User == nil
}

func validRedirect(s string, testMode bool) bool {
	if httpsURL(s) {
		return true
	}
	if privateURI.MatchString(s) {
		return true
	}
	if testMode {
		u, err := url.Parse(s)
		if err == nil && u.Scheme == "http" && (u.Hostname() == "localhost" || u.Hostname() == "127.0.0.1") {
			return true
		}
	}
	return false
}

// validateAppFields checks every writable field present in p against the
// Application rules, reporting every failure at once.
func validateAppFields(p httpx.Patch, slug string, testMode bool, f *httpx.Fields) (sch *schema.Schema, schemaErr error) {
	str := func(field string, min, max int) {
		if v, present := p.String(field, f); present && v != nil {
			n := len([]rune(*v))
			if n < min {
				f.Add(field, "too_short").Min = &min
			} else if n > max {
				f.Max(field, "too_long", max)
			}
		}
	}
	str("name", 1, 100)
	str("description", 0, 2000)
	str("email_from_name", 1, 50)
	str("review_notes", 0, 2000)
	for _, field := range []string{"icon_url", "launch_url", "support_url"} {
		if v, present := p.String(field, f); present && v != nil && !httpsURL(*v) {
			f.Add(field, "invalid_format").Allowed = []string{"https"}
		}
	}
	if p.Has("redirect_uris") {
		var uris []string
		if p.Get("redirect_uris", &uris, f) {
			if len(uris) > 10 {
				f.Max("redirect_uris", "too_long", 10)
			}
			for _, u := range uris {
				if !validRedirect(u, testMode) {
					f.Add("redirect_uris", "invalid_format").Message = u
					break
				}
			}
		}
	}
	if p.Has("permissions") {
		var perms []appPermission
		if p.Get("permissions", &perms, f) {
			if len(perms) > 100 {
				f.Max("permissions", "too_long", 100)
			}
			keyRe := regexp.MustCompile(`^app\.` + regexp.QuoteMeta(slug) + `\.[a-z0-9_.]{1,64}$`)
			seen := map[string]bool{}
			for _, pm := range perms {
				if !keyRe.MatchString(pm.Key) {
					f.Add("permissions", "invalid_format").Pattern = keyRe.String()
					break
				}
				if seen[pm.Key] {
					f.Add("permissions", "not_unique").Message = pm.Key
					break
				}
				seen[pm.Key] = true
				if pm.Description != nil && len([]rune(*pm.Description)) > 255 {
					f.Max("permissions.description", "too_long", 255)
					break
				}
			}
		}
	}
	if p.Has("available_app_roles") {
		var roles []string
		if p.Get("available_app_roles", &roles, f) {
			if len(roles) > 20 {
				f.Max("available_app_roles", "too_long", 20)
			}
			for _, r := range roles {
				if !roleNameRe.MatchString(r) {
					f.Add("available_app_roles", "invalid_format").Pattern = roleNameRe.String()
					break
				}
			}
		}
	}
	if v, present := p.String("visibility", f); present {
		if v == nil || (*v != "public" && *v != "invite_only" && *v != "internal") {
			f.Enum("visibility", "public", "invite_only", "internal")
		}
	}
	if v, present := p.String("review_status", f); present {
		if v == nil || !slices.Contains([]string{"approved", "pending_review", "rejected", "suspended"}, *v) {
			f.Enum("review_status", "approved", "pending_review", "rejected", "suspended")
		}
	}
	if p.Has("settings_schema") {
		raw := p["settings_schema"]
		if p.IsNull("settings_schema") {
			f.Add("settings_schema", "required")
		} else {
			parsed, issues, reserved := schema.Parse(raw)
			if len(reserved) > 0 {
				return nil, httpx.E(422, "reserved_settings_key",
					"settings_schema may not declare the reserved global keys locale, timezone, theme, or notifications.").
					With("keys", reserved)
			}
			if len(issues) > 0 {
				return nil, httpx.E(422, "invalid_settings_schema", "settings_schema breaks a schema rule.").With("fields", issues)
			}
			sch = parsed
		}
	}
	return sch, nil
}

// ── POST /v1/applications ──────────────────────────────────────────────

func (s *Server) ApplicationsCreate(w http.ResponseWriter, r *http.Request, params gen.ApplicationsCreateParams) {
	s.serve(w, r, opts{op: "applications.create", idem: idemOptional}, func(c *Call) error {
		if err := c.require("applications.manage"); err != nil {
			return err
		}
		p, err := c.Patch()
		if err != nil {
			return err
		}
		if err := p.RejectReadOnly("id", "tenant_id", "created_at", "updated_at", "settings_schema_stats", "test_mode"); err != nil {
			return err
		}
		var f httpx.Fields
		var slug string
		if !p.Get("slug", &slug, &f) || slug == "" {
			f.Add("slug", "required")
		} else if slug == "platform" {
			f.Add("slug", "unknown_value").Message = "anything but platform"
		} else if !slugRe.MatchString(slug) {
			f.Add("slug", "invalid_format").Pattern = slugRe.String()
		}
		for _, req := range []string{"name", "launch_url"} {
			if v, present := p.String(req, &f); !present || v == nil {
				f.Add(req, "required")
			}
		}
		ownerUser, _ := p.String("owner_user_id", &f)
		ownerOrg, _ := p.String("owner_organization_id", &f)
		if (ownerUser == nil) == (ownerOrg == nil) {
			f.Add("owner_user_id", "required").Message = "exactly one of owner_user_id or owner_organization_id"
		}
		sch, err := validateAppFields(p, slug, c.testMode(), &f)
		if err != nil {
			return err
		}
		if err := f.Err(); err != nil {
			return err
		}
		var in struct {
			Name              string          `json:"name"`
			Description       *string         `json:"description"`
			IconURL           *string         `json:"icon_url"`
			LaunchURL         string          `json:"launch_url"`
			RedirectURIs      []string        `json:"redirect_uris"`
			SupportURL        *string         `json:"support_url"`
			EmailFromName     *string         `json:"email_from_name"`
			SettingsSchema    json.RawMessage `json:"settings_schema"`
			Permissions       []appPermission `json:"permissions"`
			AvailableAppRoles []string        `json:"available_app_roles"`
			DefaultAppRole    *string         `json:"default_app_role"`
			Visibility        *string         `json:"visibility"`
			ReviewStatus      *string         `json:"review_status"`
			ReviewNotes       *string         `json:"review_notes"`
		}
		if err := c.Decode(&in); err != nil {
			return err
		}
		roles := dedupe(in.AvailableAppRoles)
		if in.DefaultAppRole != nil && !slices.Contains(roles, *in.DefaultAppRole) {
			f.Add("default_app_role", "unknown_value").Allowed = roles
			return f.Err()
		}
		if in.SettingsSchema == nil {
			in.SettingsSchema = json.RawMessage(`{"type":"object","properties":{}}`)
		}
		_ = sch
		visibility := "public"
		if in.Visibility != nil {
			visibility = *in.Visibility
		}
		review := "approved"
		if in.ReviewStatus != nil {
			review = *in.ReviewStatus
		}
		if (review == "rejected" || review == "suspended") && (in.ReviewNotes == nil || *in.ReviewNotes == "") {
			return httpx.E(422, "review_notes_required", "review_notes is required for rejected or suspended.")
		}
		// The owner must exist.
		if ownerUser != nil {
			if _, err := c.visibleUser(*ownerUser); err != nil {
				f.Add("owner_user_id", "unknown_value")
				return f.Err()
			}
		} else if _, err := c.loadOrg(*ownerOrg); err != nil {
			f.Add("owner_organization_id", "unknown_value")
			return f.Err()
		}
		t, err := c.ensureTenant(ownerUser, ownerOrg)
		if err != nil {
			return err
		}
		if err := tenantWritable(t); err != nil {
			return err
		}
		if t.Restricted {
			return subscriptionRequired(t)
		}
		lim := plans[t.Plan]
		if lim.Applications != nil {
			var n int
			if err := db.One(c.ctx, c.q, &n, `select to_jsonb(count(*)) from applications where tenant_id = :t`,
				db.Args{"t": t.ID}); err != nil {
				return err
			}
			if n >= *lim.Applications {
				return planLimitErr("applications", *lim.Applications, n, t)
			}
		}
		if lim.AppRoles != nil && len(roles) > *lim.AppRoles {
			return planLimitErr("app_roles", *lim.AppRoles, len(roles), t)
		}
		perms := in.Permissions
		if perms == nil {
			perms = []appPermission{}
		}
		id := "app_" + slug
		if _, err := c.q.Exec(c.ctx, `insert into applications (id, slug, name, description, icon_url, launch_url,
				redirect_uris, support_url, email_from_name, settings_schema, permissions, available_app_roles,
				default_app_role, visibility, owner_user_id, owner_organization_id, review_status, review_notes,
				tenant_id, test_mode)
			values (:id, :slug, :name, :desc, :icon, :launch, :redir::text[], :support, :from, :schema::jsonb,
				:perms::jsonb, :roles::text[], :defrole, :vis, :ou, :oo, :rs, :rn, :t, :tm)`,
			db.Args{"id": id, "slug": slug, "name": in.Name, "desc": in.Description, "icon": in.IconURL,
				"launch": in.LaunchURL, "redir": db.TextArray(in.RedirectURIs), "support": in.SupportURL,
				"from": in.EmailFromName, "schema": string(in.SettingsSchema), "perms": db.JSON(perms),
				"roles": db.TextArray(roles), "defrole": in.DefaultAppRole, "vis": visibility, "ou": ownerUser,
				"oo": ownerOrg, "rs": review, "rn": in.ReviewNotes, "t": t.ID, "tm": c.testMode()}); err != nil {
			if _, ok := db.UniqueViolation(err); ok {
				return httpx.Conflict("slug_taken", "An Application with this slug already exists.")
			}
			return err
		}
		if err := c.seedAppRoles(id, roles); err != nil {
			return err
		}
		a, err := c.loadApp(id)
		if err != nil {
			return err
		}
		if err := c.audit(Audit{Action: "application.created", TargetType: "application", TargetID: id,
			ApplicationID: &id, TenantID: &t.ID, After: a.full(nil)}); err != nil {
			return err
		}
		c.After(func(ctx contextT) { c.s.rebuildSettingsProjection(ctx, id) })
		c.Versioned(a.Version)
		return c.Created(a.full(nil))
	})
}

// seedAppRoles creates the empty role_<slug>_<name> for each new name.
func (c *Call) seedAppRoles(appID string, names []string) error {
	for _, n := range names {
		if _, err := c.q.Exec(c.ctx, `insert into roles (id, name, scope, permissions, test_mode)
			values (:id, :n, :s, '{}', false) on conflict (id) do nothing`,
			db.Args{"id": roleID(appID, n), "n": n, "s": appID}); err != nil {
			return err
		}
	}
	return nil
}

// ── PATCH /v1/applications/{id} ────────────────────────────────────────

var ownerFields = []string{"name", "description", "icon_url", "launch_url", "redirect_uris", "support_url",
	"email_from_name", "settings_schema", "permissions", "available_app_roles", "default_app_role"}
var moderationFields = []string{"visibility", "review_status", "review_notes", "owner_user_id", "owner_organization_id"}

func (s *Server) ApplicationsUpdate(w http.ResponseWriter, r *http.Request, id string, params gen.ApplicationsUpdateParams) {
	s.serve(w, r, opts{op: "applications.update"}, func(c *Call) error {
		a, err := c.loadVisibleApp(id)
		if err != nil {
			return err
		}
		admin := c.can("applications.manage")
		owner, err := c.isAppOwner(a)
		if err != nil {
			return err
		}
		if !admin && !owner {
			return httpx.Forbidden("applications.manage")
		}
		if err := httpx.CheckIfMatch(c.r, a.Version); err != nil {
			return err
		}
		p, err := c.Patch()
		if err != nil {
			return err
		}
		if err := p.RejectReadOnly("id", "slug", "tenant_id", "created_at", "updated_at", "settings_schema_stats", "test_mode"); err != nil {
			return err
		}
		if !admin {
			for _, mf := range moderationFields {
				if p.Has(mf) {
					return httpx.E(403, "moderation_field_forbidden", "Only applications.manage can change "+mf+".").With("field", mf)
				}
			}
		}
		var f httpx.Fields
		p.RequireNonNull(&f, "name", "launch_url", "redirect_uris", "settings_schema", "permissions",
			"available_app_roles", "visibility", "review_status")
		if _, err := validateAppFields(p, a.Slug, a.TestMode, &f); err != nil {
			return err
		}
		if err := f.Err(); err != nil {
			return err
		}
		next := a
		setStr := func(field string, dst **string) {
			if v, present := p.String(field, &f); present {
				*dst = v
			}
		}
		if v, present := p.String("name", &f); present && v != nil {
			next.Name = *v
		}
		if v, present := p.String("launch_url", &f); present && v != nil {
			next.LaunchURL = *v
		}
		setStr("description", &next.Description)
		setStr("icon_url", &next.IconURL)
		setStr("support_url", &next.SupportURL)
		setStr("email_from_name", &next.EmailFromName)
		setStr("default_app_role", &next.DefaultAppRole)
		setStr("review_notes", &next.ReviewNotes)
		// Decode into fresh slices: next shares a's backing arrays, and
		// decoding in place would rewrite a too.
		var redirects, roles []string
		var perms []appPermission
		if p.Get("redirect_uris", &redirects, &f) {
			next.RedirectURIs = redirects
		}
		if p.Get("permissions", &perms, &f) {
			next.Permissions = perms
		}
		if p.Get("available_app_roles", &roles, &f) {
			next.AvailableAppRoles = dedupe(roles)
		}
		if p.Has("settings_schema") {
			next.SettingsSchema = p["settings_schema"]
		}
		if v, present := p.String("visibility", &f); present && v != nil {
			next.Visibility = *v
		}
		ownerChange := p.Has("owner_user_id") || p.Has("owner_organization_id")
		if ownerChange {
			setStr("owner_user_id", &next.OwnerUserID)
			setStr("owner_organization_id", &next.OwnerOrganizationID)
			if p.Has("owner_user_id") && next.OwnerUserID != nil && !p.Has("owner_organization_id") {
				next.OwnerOrganizationID = nil
			}
			if p.Has("owner_organization_id") && next.OwnerOrganizationID != nil && !p.Has("owner_user_id") {
				next.OwnerUserID = nil
			}
			if (next.OwnerUserID == nil) == (next.OwnerOrganizationID == nil) {
				f.Add("owner_user_id", "required").Message = "exactly one of owner_user_id or owner_organization_id"
			}
		}
		if next.DefaultAppRole != nil && !slices.Contains(next.AvailableAppRoles, *next.DefaultAppRole) {
			f.Add("default_app_role", "unknown_value").Allowed = next.AvailableAppRoles
		}
		if err := f.Err(); err != nil {
			return err
		}
		// review_status transitions.
		reviewChanged := false
		if v, present := p.String("review_status", &f); present && v != nil && *v != a.ReviewStatus {
			allowed := map[string][]string{
				"pending_review": {"approved", "rejected"}, "approved": {"suspended"}, "suspended": {"approved"},
			}
			if !slices.Contains(allowed[a.ReviewStatus], *v) {
				return httpx.Conflict("invalid_review_transition", "The review_status change from "+a.ReviewStatus+" to "+*v+" isn't allowed.")
			}
			next.ReviewStatus, reviewChanged = *v, true
		}
		if (next.ReviewStatus == "rejected" || next.ReviewStatus == "suspended") && reviewChanged &&
			(next.ReviewNotes == nil || *next.ReviewNotes == "") {
			return httpx.E(422, "review_notes_required", "review_notes is required for rejected or suspended.")
		}
		configEdited := false
		for _, of := range ownerFields {
			if p.Has(of) {
				configEdited = true
			}
		}
		if a.ReviewStatus == "rejected" && configEdited && !reviewChanged {
			// The owner's next configuration edit resubmits it.
			next.ReviewStatus, reviewChanged = "pending_review", true
		}
		removedRoles := minus(a.AvailableAppRoles, next.AvailableAppRoles)
		removedPerms := minus(permKeys(a.Permissions), permKeys(next.Permissions))
		if err := c.destructiveIf((reviewChanged && (next.ReviewStatus == "rejected" || next.ReviewStatus == "suspended")) ||
			len(removedRoles) > 0 || len(removedPerms) > 0); err != nil {
			return err
		}
		t, err := c.loadTenant(a.TenantID)
		if err != nil {
			return err
		}
		if err := tenantWritable(t); err != nil {
			return err
		}
		addedRoles := minus(next.AvailableAppRoles, a.AvailableAppRoles)
		if len(addedRoles) > 0 {
			if t.Restricted && !c.testMode() {
				return subscriptionRequired(t)
			}
			if l := plans[t.Plan].AppRoles; l != nil && len(next.AvailableAppRoles) > *l {
				return planLimitErr("app_roles", *l, len(next.AvailableAppRoles), t)
			}
		}
		for _, name := range removedRoles {
			var n int
			if err := db.One(c.ctx, c.q, &n, `select to_jsonb(count(*)) from user_role_assignments where role_id = :r`,
				db.Args{"r": roleID(a.ID, name)}); err != nil {
				return err
			}
			if n > 0 {
				return httpx.Conflict("app_role_in_use", "A role name being removed still has assignments.").
					With("role_id", roleID(a.ID, name))
			}
		}
		if len(removedPerms) > 0 {
			inUse, err := db.All[string](c.ctx, c.q, `select to_jsonb(id) from roles where scope = :a
				and permissions && :p::text[] order by id`, db.Args{"a": a.ID, "p": db.TextArray(removedPerms)})
			if err != nil {
				return err
			}
			if len(inUse) > 0 {
				return httpx.Conflict("permission_in_use", "A permission being removed is still held by a Role.").With("role_ids", inUse)
			}
		}
		newTenant := a.TenantID
		if ownerChange && (deref(next.OwnerUserID) != deref(a.OwnerUserID) || deref(next.OwnerOrganizationID) != deref(a.OwnerOrganizationID)) {
			nt, err := c.ensureTenant(next.OwnerUserID, next.OwnerOrganizationID)
			if err != nil {
				return err
			}
			if t.Tier != "shared" || nt.Tier != "shared" {
				return httpx.Conflict("ownership_change_requires_migration", "Ownership can move only between shared-tier Tenants.")
			}
			newTenant = nt.ID
		}
		if next.Permissions == nil {
			next.Permissions = []appPermission{}
		}
		if _, err := c.q.Exec(c.ctx, `update applications set name = :name, description = :desc, icon_url = :icon,
				launch_url = :launch, redirect_uris = :redir::text[], support_url = :support, email_from_name = :from,
				settings_schema = :schema::jsonb, permissions = :perms::jsonb, available_app_roles = :roles::text[],
				default_app_role = :defrole, visibility = :vis, owner_user_id = :ou, owner_organization_id = :oo,
				review_status = :rs, review_notes = :rn, tenant_id = :t
			where id = :id`,
			db.Args{"name": next.Name, "desc": next.Description, "icon": next.IconURL, "launch": next.LaunchURL,
				"redir": db.TextArray(next.RedirectURIs), "support": next.SupportURL, "from": next.EmailFromName,
				"schema": string(next.SettingsSchema), "perms": db.JSON(next.Permissions),
				"roles": db.TextArray(next.AvailableAppRoles), "defrole": next.DefaultAppRole, "vis": next.Visibility,
				"ou": next.OwnerUserID, "oo": next.OwnerOrganizationID, "rs": next.ReviewStatus, "rn": next.ReviewNotes,
				"t": newTenant, "id": a.ID}); err != nil {
			return err
		}
		if newTenant != a.TenantID {
			for _, table := range []string{"entitlements", "app_profiles", "app_settings"} {
				if _, err := c.q.Exec(c.ctx, `update `+table+` set tenant_id = :t where application_id = :a`,
					db.Args{"t": newTenant, "a": a.ID}); err != nil {
					return err
				}
			}
			if _, err := c.q.Exec(c.ctx, `update webhook_subscriptions set tenant_id = :t where scope = :a`,
				db.Args{"t": newTenant, "a": a.ID}); err != nil {
				return err
			}
		}
		for _, name := range removedRoles {
			if _, err := c.q.Exec(c.ctx, `delete from roles where id = :r`, db.Args{"r": roleID(a.ID, name)}); err != nil {
				return err
			}
		}
		if err := c.seedAppRoles(a.ID, addedRoles); err != nil {
			return err
		}
		after, err := c.loadApp(a.ID)
		if err != nil {
			return err
		}
		if err := c.audit(Audit{Action: "application.updated", TargetType: "application", TargetID: a.ID,
			ApplicationID: &a.ID, Before: a.full(nil), After: after.full(nil)}); err != nil {
			return err
		}
		if reviewChanged {
			if err := c.audit(Audit{Action: "application.review_status_changed", TargetType: "application", TargetID: a.ID,
				ApplicationID: &a.ID, Before: map[string]any{"review_status": a.ReviewStatus},
				After: map[string]any{"review_status": after.ReviewStatus, "review_notes": after.ReviewNotes}}); err != nil {
				return err
			}
			if err := c.emit("application.review_status_changed", &a.ID, map[string]any{"application_id": a.ID,
				"review_status": after.ReviewStatus, "review_notes": after.ReviewNotes}); err != nil {
				return err
			}
		}
		if p.Has("settings_schema") {
			c.After(func(ctx contextT) { c.s.rebuildSettingsProjection(ctx, a.ID) })
		}
		stats, err := c.staleOverrideCounts(after)
		if err != nil {
			return err
		}
		c.Versioned(after.Version)
		return c.OK(after.full(stats))
	})
}

func permKeys(ps []appPermission) []string {
	out := make([]string, len(ps))
	for i, p := range ps {
		out[i] = p.Key
	}
	return out
}

// minus is a − b.
func minus(a, b []string) []string {
	var out []string
	for _, x := range a {
		if !slices.Contains(b, x) {
			out = append(out, x)
		}
	}
	return out
}
