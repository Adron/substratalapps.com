package core

import (
	"encoding/json"
	"net/http"
	"regexp"
	"sort"
	"time"
	_ "time/tzdata" // IANA zones for timezone validation, independent of the host image

	"golang.org/x/text/language"

	"github.com/Adron/substratalapps.com/internal/api/gen"
	"github.com/Adron/substratalapps.com/internal/db"
	"github.com/Adron/substratalapps.com/internal/httpx"
	"github.com/Adron/substratalapps.com/internal/schema"
)

const maxFreeformBytes = 16 << 10

// ── Global Profile ─────────────────────────────────────────────────────

type profile struct {
	UserID       string    `json:"user_id"`
	DisplayName  string    `json:"display_name"`
	AvatarURL    *string   `json:"avatar_url"`
	ContactEmail *string   `json:"contact_email"`
	ContactPhone *string   `json:"contact_phone"`
	Locale       string    `json:"locale"`
	UpdatedAt    time.Time `json:"updated_at"`
	version      int
}

func (c *Call) loadProfile(uid string) (profile, error) {
	var x struct {
		profile
		V int `json:"version_"`
	}
	err := db.One(c.ctx, c.q, &x, `select jsonb_build_object('user_id', p.user_id, 'display_name', p.display_name,
			'avatar_url', p.avatar_url, 'contact_email', p.contact_email, 'contact_phone', p.contact_phone,
			'locale', coalesce(s.locale, 'en-US'), 'updated_at', p.updated_at, 'version_', p.version)
		from profiles p left join settings s on s.user_id = p.user_id where p.user_id = :u`, db.Args{"u": uid})
	if err == db.ErrNotFound {
		return profile{}, httpx.NotFoundResource("user")
	}
	x.profile.version = x.V
	return x.profile, err
}

var e164 = regexp.MustCompile(`^\+[1-9][0-9]{7,14}$`)

func (s *Server) ProfilesGet(w http.ResponseWriter, r *http.Request, id gen.UserIdOrMe) {
	s.serve(w, r, opts{op: "profiles.get"}, func(c *Call) error {
		uid, err := c.selfID(id)
		if err != nil {
			return err
		}
		u, err := c.visibleUser(uid)
		if err != nil {
			return err
		}
		if ok, err := c.canReadUser(u); err != nil {
			return err
		} else if !ok {
			return httpx.NotFoundResource("user")
		}
		p, err := c.loadProfile(uid)
		if err != nil {
			return err
		}
		c.Versioned(p.version)
		return c.OK(p)
	})
}

func (s *Server) ProfilesUpdate(w http.ResponseWriter, r *http.Request, id gen.UserIdOrMe, params gen.ProfilesUpdateParams) {
	s.serve(w, r, opts{op: "profiles.update"}, func(c *Call) error {
		uid, err := c.selfID(id)
		if err != nil {
			return err
		}
		if !c.isSelf(uid) && !c.can("users.manage") {
			return httpx.Forbidden("users.manage")
		}
		if _, err := c.visibleUser(uid); err != nil {
			return err
		}
		before, err := c.loadProfile(uid)
		if err != nil {
			return err
		}
		if err := httpx.CheckIfMatch(c.r, before.version); err != nil {
			return err
		}
		p, err := c.Patch()
		if err != nil {
			return err
		}
		if err := p.RejectReadOnly("locale", "user_id", "updated_at"); err != nil {
			return err
		}
		var f httpx.Fields
		next := before
		if v, present := p.String("display_name", &f); present {
			switch {
			case v == nil:
				f.Add("display_name", "required").Message = "this field isn't nullable"
			case len([]rune(*v)) < 1:
				f.Add("display_name", "too_short").Min = ip(1)
			case len([]rune(*v)) > 100:
				f.Max("display_name", "too_long", 100)
			default:
				next.DisplayName = *v
			}
		}
		if v, present := p.String("avatar_url", &f); present {
			if v != nil && (len(*v) > 2048 || !httpsURL(*v)) {
				f.Add("avatar_url", "invalid_format").Allowed = []string{"https"}
			}
			next.AvatarURL = v
		}
		if v, present := p.String("contact_email", &f); present {
			if v != nil {
				if _, ok := validEmail(*v); !ok {
					f.Add("contact_email", "invalid_format")
				}
			}
			next.ContactEmail = v
		}
		if v, present := p.String("contact_phone", &f); present {
			if v != nil && !e164.MatchString(*v) {
				f.Add("contact_phone", "invalid_format").Pattern = "E.164"
			}
			next.ContactPhone = v
		}
		if err := f.Err(); err != nil {
			return err
		}
		if _, err := c.q.Exec(c.ctx, `update profiles set display_name = :d, avatar_url = :a, contact_email = :e,
			contact_phone = :p where user_id = :u`,
			db.Args{"d": next.DisplayName, "a": next.AvatarURL, "e": next.ContactEmail, "p": next.ContactPhone, "u": uid}); err != nil {
			return err
		}
		after, err := c.loadProfile(uid)
		if err != nil {
			return err
		}
		if !c.isSelf(uid) {
			if err := c.audit(Audit{Action: "profile.updated", TargetType: "user", TargetID: uid, TargetUserID: &uid,
				Before: before, After: after}); err != nil {
				return err
			}
		}
		c.Versioned(after.version)
		return c.OK(after)
	})
}

// ── Global Settings ────────────────────────────────────────────────────

type settingsObj struct {
	UserID        string          `json:"user_id"`
	Locale        string          `json:"locale"`
	Timezone      string          `json:"timezone"`
	Theme         string          `json:"theme"`
	Notifications map[string]bool `json:"notifications"`
	UpdatedAt     time.Time       `json:"updated_at"`
	version       int
}

func (c *Call) loadSettings(uid string) (settingsObj, error) {
	var x struct {
		settingsObj
		V int `json:"version"`
	}
	err := db.One(c.ctx, c.q, &x, `select to_jsonb(s) from settings s where user_id = :u`, db.Args{"u": uid})
	if err == db.ErrNotFound {
		return settingsObj{}, httpx.NotFoundResource("user")
	}
	if x.settingsObj.Notifications == nil {
		x.settingsObj.Notifications = map[string]bool{}
	}
	x.settingsObj.version = x.V
	return x.settingsObj, err
}

var channelRe = regexp.MustCompile(`^[a-z][a-z0-9_]{0,31}$`)

func validLocale(s string) bool {
	if s == "" || len(s) > 35 {
		return false
	}
	_, err := language.Parse(s)
	return err == nil
}

func validTimezone(s string) bool {
	if s == "" || s == "Local" {
		return false
	}
	_, err := time.LoadLocation(s)
	return err == nil
}

// mergeNotifications merges a channel map one level deep; null removes.
func mergeNotifications(base map[string]bool, patch map[string]*bool, field string, f *httpx.Fields) map[string]bool {
	out := map[string]bool{}
	for k, v := range base {
		out[k] = v
	}
	for k, v := range patch {
		if !channelRe.MatchString(k) {
			f.Add(field+"."+k, "invalid_format").Pattern = channelRe.String()
			continue
		}
		if v == nil {
			delete(out, k)
		} else {
			out[k] = *v
		}
	}
	if len(out) > 20 {
		f.Max(field, "too_long", 20)
	}
	return out
}

func (s *Server) SettingsGet(w http.ResponseWriter, r *http.Request, id gen.UserIdOrMe) {
	s.serve(w, r, opts{op: "settings.get"}, func(c *Call) error {
		uid, err := c.selfID(id)
		if err != nil {
			return err
		}
		if !c.isSelf(uid) && !c.can("users.list") && !c.can("users.manage") {
			return httpx.Forbidden("users.list")
		}
		if _, err := c.visibleUser(uid); err != nil {
			return err
		}
		st, err := c.loadSettings(uid)
		if err != nil {
			return err
		}
		c.Versioned(st.version)
		return c.OK(st)
	})
}

func (s *Server) SettingsUpdate(w http.ResponseWriter, r *http.Request, id gen.UserIdOrMe, params gen.SettingsUpdateParams) {
	s.serve(w, r, opts{op: "settings.update"}, func(c *Call) error {
		uid, err := c.selfID(id)
		if err != nil {
			return err
		}
		if !c.isSelf(uid) && !c.can("users.manage") {
			return httpx.Forbidden("users.manage")
		}
		if _, err := c.visibleUser(uid); err != nil {
			return err
		}
		before, err := c.loadSettings(uid)
		if err != nil {
			return err
		}
		if err := httpx.CheckIfMatch(c.r, before.version); err != nil {
			return err
		}
		p, err := c.Patch()
		if err != nil {
			return err
		}
		if err := p.RejectReadOnly("user_id", "updated_at"); err != nil {
			return err
		}
		var f httpx.Fields
		p.RequireNonNull(&f, "locale", "timezone", "theme", "notifications")
		next := before
		if v, present := p.String("locale", &f); present && v != nil {
			if !validLocale(*v) {
				f.Add("locale", "unknown_value")
			}
			next.Locale = *v
		}
		if v, present := p.String("timezone", &f); present && v != nil {
			if !validTimezone(*v) {
				f.Add("timezone", "unknown_value")
			}
			next.Timezone = *v
		}
		if v, present := p.String("theme", &f); present && v != nil {
			if *v != "light" && *v != "dark" && *v != "system" {
				f.Enum("theme", "light", "dark", "system")
			}
			next.Theme = *v
		}
		if p.Has("notifications") && !p.IsNull("notifications") {
			var n map[string]*bool
			if p.Get("notifications", &n, &f) {
				next.Notifications = mergeNotifications(before.Notifications, n, "notifications", &f)
			}
		}
		if err := f.Err(); err != nil {
			return err
		}
		if _, err := c.q.Exec(c.ctx, `update settings set locale = :l, timezone = :tz, theme = :th,
			notifications = :n::jsonb where user_id = :u`,
			db.Args{"l": next.Locale, "tz": next.Timezone, "th": next.Theme, "n": db.JSON(next.Notifications), "u": uid}); err != nil {
			return err
		}
		after, err := c.loadSettings(uid)
		if err != nil {
			return err
		}
		if !c.isSelf(uid) {
			if err := c.audit(Audit{Action: "settings.updated", TargetType: "user", TargetID: uid, TargetUserID: &uid,
				Before: before, After: after}); err != nil {
				return err
			}
		}
		c.Versioned(after.version)
		return c.OK(after)
	})
}

// ── Per-app access gate (Profiles/Settings: self, app key, support) ────

// appDataGate authorizes per-app profile/settings access: the User and the
// app's own key need the User to have active access right now; users.list
// reads and users.manage reads/writes regardless.
func (c *Call) appDataGate(uid, appID string, write bool) (appRow, error) {
	a, err := c.loadApp(appID)
	if err == db.ErrNotFound {
		return a, httpx.NotFoundResource("application")
	}
	if err != nil {
		return a, err
	}
	if key, ok := c.p.AppKey(); ok && key != appID {
		return a, httpx.NotFoundResource("application")
	}
	u, err := c.visibleUser(uid)
	if err != nil {
		return a, err
	}
	if c.can("users.manage") || (!write && c.can("users.list")) {
		return a, nil
	}
	_, isKey := c.p.AppKey()
	if !isKey && !c.isSelf(uid) {
		if write {
			return a, httpx.Forbidden("users.manage")
		}
		return a, httpx.Forbidden("users.list")
	}
	res, err := c.resolveOne(u.ID, appID)
	if err != nil {
		return a, err
	}
	if !res.Allowed {
		return a, httpx.E(403, "entitlement_required", "This user has no active entitlement to "+appID+".").
			With("application_id", appID).With("entitlement_status", res.Res.Status)
	}
	return a, nil
}

// adminWrite reports whether a write to another User's data should be
// audited (an admin acting, not the User or the app's own key).
func (c *Call) adminWrite(uid string) bool {
	_, isKey := c.p.AppKey()
	return !isKey && !c.isSelf(uid)
}

// ── AppProfile ─────────────────────────────────────────────────────────

type appProfile struct {
	UserID               string          `json:"user_id"`
	ApplicationID        string          `json:"application_id"`
	DisplayHandle        *string         `json:"display_handle"`
	EffectiveDisplayName string          `json:"effective_display_name"`
	Custom               json.RawMessage `json:"custom"`
	UpdatedAt            *time.Time      `json:"updated_at"`
	version              int
}

func (c *Call) loadAppProfile(uid, appID string) (appProfile, error) {
	var x struct {
		appProfile
		V *int `json:"version_"`
	}
	err := db.One(c.ctx, c.q, &x, `select jsonb_build_object('user_id', p.user_id, 'application_id', :a::text,
			'display_handle', ap.display_handle,
			'effective_display_name', coalesce(ap.display_handle, p.display_name),
			'custom', coalesce(ap.custom, '{}'::jsonb), 'updated_at', ap.updated_at, 'version_', ap.version)
		from profiles p left join app_profiles ap on ap.user_id = p.user_id and ap.application_id = :a
		where p.user_id = :u`, db.Args{"u": uid, "a": appID})
	if err == db.ErrNotFound {
		return appProfile{}, httpx.NotFoundResource("user")
	}
	if x.V != nil {
		x.appProfile.version = *x.V
	}
	return x.appProfile, err
}

func (s *Server) AppProfilesGet(w http.ResponseWriter, r *http.Request, id gen.UserIdOrMe, appID gen.AppId) {
	s.serve(w, r, opts{op: "appProfiles.get"}, func(c *Call) error {
		uid, err := c.selfID(id)
		if err != nil {
			return err
		}
		if _, err := c.appDataGate(uid, appID, false); err != nil {
			return err
		}
		ap, err := c.loadAppProfile(uid, appID)
		if err != nil {
			return err
		}
		if ap.version > 0 {
			c.Versioned(ap.version)
		}
		return c.OK(ap)
	})
}

func (s *Server) AppProfilesUpdate(w http.ResponseWriter, r *http.Request, id gen.UserIdOrMe, appID gen.AppId, params gen.AppProfilesUpdateParams) {
	s.serve(w, r, opts{op: "appProfiles.update"}, func(c *Call) error {
		uid, err := c.selfID(id)
		if err != nil {
			return err
		}
		a, err := c.appDataGate(uid, appID, true)
		if err != nil {
			return err
		}
		before, err := c.loadAppProfile(uid, appID)
		if err != nil {
			return err
		}
		if before.version > 0 {
			if err := httpx.CheckIfMatch(c.r, before.version); err != nil {
				return err
			}
		}
		t, err := c.loadTenant(a.TenantID)
		if err != nil {
			return err
		}
		if err := tenantWritable(t); err != nil {
			return err
		}
		p, err := c.Patch()
		if err != nil {
			return err
		}
		if err := p.RejectReadOnly("effective_display_name", "user_id", "application_id", "updated_at"); err != nil {
			return err
		}
		var f httpx.Fields
		p.RequireNonNull(&f, "custom")
		handle := before.DisplayHandle
		if v, present := p.String("display_handle", &f); present {
			if v != nil && (len([]rune(*v)) < 1 || len([]rune(*v)) > 64) {
				f.Max("display_handle", "too_long", 64)
			}
			handle = v
		}
		custom := map[string]json.RawMessage{}
		_ = json.Unmarshal(before.Custom, &custom)
		if p.Has("custom") && !p.IsNull("custom") {
			var patch map[string]json.RawMessage
			if p.Get("custom", &patch, &f) {
				for k, v := range patch {
					if string(v) == "null" {
						delete(custom, k)
					} else {
						custom[k] = v
					}
				}
			}
		}
		customJSON := db.JSON(custom)
		if len(customJSON) > maxFreeformBytes {
			f.Max("custom", "too_long", maxFreeformBytes)
		}
		if err := f.Err(); err != nil {
			return err
		}
		if _, err := c.q.Exec(c.ctx, `insert into app_profiles (user_id, application_id, tenant_id, display_handle, custom, test_mode)
			values (:u, :a, :t, :h, :c::jsonb, :tm)
			on conflict (user_id, application_id) do update set display_handle = excluded.display_handle, custom = excluded.custom`,
			db.Args{"u": uid, "a": appID, "t": a.TenantID, "h": handle, "c": customJSON, "tm": c.testMode()}); err != nil {
			return err
		}
		after, err := c.loadAppProfile(uid, appID)
		if err != nil {
			return err
		}
		if c.adminWrite(uid) {
			if err := c.audit(Audit{Action: "profile.updated", TargetType: "user", TargetID: uid, TargetUserID: &uid,
				ApplicationID: &a.ID, Before: before, After: after}); err != nil {
				return err
			}
		}
		c.Versioned(after.version)
		return c.OK(after)
	})
}

// ── AppSettings (Settings → Resolution rules) ──────────────────────────

type appSettingsResolved struct {
	UserID         string                     `json:"user_id"`
	ApplicationID  string                     `json:"application_id"`
	Resolved       map[string]any             `json:"resolved"`
	Sources        map[string]string          `json:"sources"`
	Overrides      map[string]json.RawMessage `json:"overrides"`
	StaleOverrides []string                   `json:"stale_overrides"`
	UpdatedAt      *time.Time                 `json:"updated_at"`
	version        int
}

// validReservedOverride applies the global Settings rules to a per-app
// override of a reserved key.
func validReservedOverride(key string, v json.RawMessage) *schema.Issue {
	path := "overrides." + key
	switch key {
	case "locale", "timezone":
		var s string
		if json.Unmarshal(v, &s) != nil || (key == "locale" && !validLocale(s)) || (key == "timezone" && !validTimezone(s)) {
			return &schema.Issue{Path: path, Code: "unknown_value"}
		}
	case "theme":
		var s string
		if json.Unmarshal(v, &s) != nil || (s != "light" && s != "dark" && s != "system") {
			return &schema.Issue{Path: path, Code: "enum_mismatch", Allowed: []string{"light", "dark", "system"}}
		}
	case "notifications":
		var m map[string]bool
		if json.Unmarshal(v, &m) != nil {
			return &schema.Issue{Path: path, Code: "invalid_format"}
		}
		for k := range m {
			if !channelRe.MatchString(k) {
				return &schema.Issue{Path: path + "." + k, Code: "invalid_format", Pattern: channelRe.String()}
			}
		}
	}
	return nil
}

func (c *Call) resolveAppSettings(uid string, a appRow) (appSettingsResolved, error) {
	global, err := c.loadSettings(uid)
	if err != nil {
		return appSettingsResolved{}, err
	}
	var row struct {
		Overrides map[string]json.RawMessage `json:"overrides"`
		UpdatedAt *time.Time                 `json:"updated_at"`
		Version   int                        `json:"version"`
	}
	err = db.One(c.ctx, c.q, &row, `select jsonb_build_object('overrides', overrides, 'updated_at', updated_at,
		'version', version) from app_settings where user_id = :u and application_id = :a`, db.Args{"u": uid, "a": a.ID})
	if err != nil && err != db.ErrNotFound {
		return appSettingsResolved{}, err
	}
	if row.Overrides == nil {
		row.Overrides = map[string]json.RawMessage{}
	}
	sch, _, _ := schema.Parse(a.SettingsSchema)
	out := appSettingsResolved{UserID: uid, ApplicationID: a.ID, Resolved: map[string]any{}, Sources: map[string]string{},
		Overrides: row.Overrides, StaleOverrides: []string{}, UpdatedAt: row.UpdatedAt, version: row.Version}
	valid := func(k string) (json.RawMessage, bool) {
		v, ok := row.Overrides[k]
		if !ok {
			return nil, false
		}
		var bad bool
		if schema.IsReserved(k) {
			bad = validReservedOverride(k, v) != nil
		} else if p, declared := sch.Props[k]; !declared || len(p.Validate(v, k)) > 0 {
			bad = true
		}
		if bad {
			out.StaleOverrides = append(out.StaleOverrides, k)
			return nil, false
		}
		return v, true
	}
	globals := map[string]any{"locale": global.Locale, "timezone": global.Timezone, "theme": global.Theme}
	for _, k := range []string{"locale", "timezone", "theme"} {
		if v, ok := valid(k); ok {
			var x any
			_ = json.Unmarshal(v, &x)
			out.Resolved[k], out.Sources[k] = x, "app_override"
		} else {
			out.Resolved[k], out.Sources[k] = globals[k], "global"
		}
	}
	notif := map[string]bool{}
	for k, v := range global.Notifications {
		notif[k] = v
	}
	out.Sources["notifications"] = "global"
	if v, ok := valid("notifications"); ok {
		var m map[string]bool
		_ = json.Unmarshal(v, &m)
		for k, b := range m {
			notif[k] = b
		}
		out.Sources["notifications"] = "app_override"
	}
	out.Resolved["notifications"] = notif
	for _, k := range sch.Order {
		if v, ok := valid(k); ok {
			var x any
			_ = json.Unmarshal(v, &x)
			out.Resolved[k], out.Sources[k] = x, "app_override"
		} else if d := sch.Props[k].Default; d != nil {
			var x any
			_ = json.Unmarshal(d, &x)
			out.Resolved[k], out.Sources[k] = x, "app_default"
		}
	}
	// Overrides for keys no longer declared at all are stale too.
	for k := range row.Overrides {
		if _, declared := sch.Props[k]; !declared && !schema.IsReserved(k) {
			out.StaleOverrides = append(out.StaleOverrides, k)
		}
	}
	sort.Strings(out.StaleOverrides)
	out.StaleOverrides = dedupe(out.StaleOverrides)
	return out, nil
}

func (s *Server) AppSettingsGet(w http.ResponseWriter, r *http.Request, id gen.UserIdOrMe, appID gen.AppId) {
	s.serve(w, r, opts{op: "appSettings.get"}, func(c *Call) error {
		uid, err := c.selfID(id)
		if err != nil {
			return err
		}
		a, err := c.appDataGate(uid, appID, false)
		if err != nil {
			return err
		}
		out, err := c.resolveAppSettings(uid, a)
		if err != nil {
			return err
		}
		if out.version > 0 {
			c.Versioned(out.version)
		}
		return c.OK(out)
	})
}

func (s *Server) AppSettingsUpdate(w http.ResponseWriter, r *http.Request, id gen.UserIdOrMe, appID gen.AppId, params gen.AppSettingsUpdateParams) {
	s.serve(w, r, opts{op: "appSettings.update"}, func(c *Call) error {
		uid, err := c.selfID(id)
		if err != nil {
			return err
		}
		a, err := c.appDataGate(uid, appID, true)
		if err != nil {
			return err
		}
		before, err := c.resolveAppSettings(uid, a)
		if err != nil {
			return err
		}
		if before.version > 0 {
			if err := httpx.CheckIfMatch(c.r, before.version); err != nil {
				return err
			}
		}
		t, err := c.loadTenant(a.TenantID)
		if err != nil {
			return err
		}
		if err := tenantWritable(t); err != nil {
			return err
		}
		p, err := c.Patch()
		if err != nil {
			return err
		}
		if err := p.RejectReadOnly("resolved", "sources", "stale_overrides", "user_id", "application_id", "updated_at"); err != nil {
			return err
		}
		var patch map[string]json.RawMessage
		var f httpx.Fields
		if !p.Get("overrides", &patch, &f) {
			if err := f.Err(); err != nil {
				return err
			}
			return httpx.Invalid("overrides is required.")
		}
		sch, _, _ := schema.Parse(a.SettingsSchema)
		var issues []schema.Issue
		merged := map[string]json.RawMessage{}
		for k, v := range before.Overrides {
			merged[k] = v
		}
		keys := make([]string, 0, len(patch))
		for k := range patch {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			v := patch[k]
			if string(v) == "null" {
				delete(merged, k) // null always clears, never validated
				continue
			}
			if schema.IsReserved(k) {
				if is := validReservedOverride(k, v); is != nil {
					issues = append(issues, *is)
					continue
				}
				if k == "notifications" {
					// One level deep over the existing override.
					var cur, add map[string]*bool
					_ = json.Unmarshal(merged[k], &cur)
					_ = json.Unmarshal(v, &add)
					if cur == nil {
						cur = map[string]*bool{}
					}
					for ck, cv := range add {
						cur[ck] = cv
					}
					v = json.RawMessage(db.JSON(cur))
				}
				merged[k] = v
				continue
			}
			prop, declared := sch.Props[k]
			if !declared {
				issues = append(issues, schema.Issue{Path: "overrides." + k, Code: "undeclared_key"})
				continue
			}
			if is := prop.Validate(v, "overrides."+k); len(is) > 0 {
				issues = append(issues, is...)
				continue
			}
			merged[k] = v
		}
		if len(issues) > 0 {
			msg := "1 override failed validation against " + a.ID + "'s settings_schema."
			if len(issues) > 1 {
				msg = itoa(len(issues)) + " overrides failed validation against " + a.ID + "'s settings_schema."
			}
			return httpx.E(422, "settings_schema_violation", msg).With("fields", issues)
		}
		mergedJSON := db.JSON(merged)
		if len(mergedJSON) > maxFreeformBytes {
			f.Max("overrides", "too_long", maxFreeformBytes)
			return f.Err()
		}
		if _, err := c.q.Exec(c.ctx, `insert into app_settings (user_id, application_id, tenant_id, overrides, test_mode)
			values (:u, :a, :t, :o::jsonb, :tm)
			on conflict (user_id, application_id) do update set overrides = excluded.overrides`,
			db.Args{"u": uid, "a": a.ID, "t": a.TenantID, "o": mergedJSON, "tm": c.testMode()}); err != nil {
			return err
		}
		after, err := c.resolveAppSettings(uid, a)
		if err != nil {
			return err
		}
		if c.adminWrite(uid) {
			if err := c.audit(Audit{Action: "settings.updated", TargetType: "user", TargetID: uid, TargetUserID: &uid,
				ApplicationID: &a.ID, Before: map[string]any{"overrides": before.Overrides},
				After: map[string]any{"overrides": after.Overrides}}); err != nil {
				return err
			}
		}
		c.Versioned(after.version)
		return c.OK(after)
	})
}

func itoa(n int) string { return jsonNumber(n) }

func jsonNumber(n int) string {
	b, _ := json.Marshal(n)
	return string(b)
}
