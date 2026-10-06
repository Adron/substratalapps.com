package core

import (
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/Adron/substratalapps.com/internal/api/gen"
	"github.com/Adron/substratalapps.com/internal/db"
	"github.com/Adron/substratalapps.com/internal/httpx"
	"github.com/Adron/substratalapps.com/internal/ids"
)

type apiKey struct {
	ID                  string     `json:"id"`
	Name                string     `json:"name"`
	Scope               string     `json:"scope"`
	Permissions         []string   `json:"permissions"`
	Mode                string     `json:"mode"`
	IntendedUse         string     `json:"intended_use"`
	RestrictDestructive bool       `json:"restrict_destructive"`
	Secret              string     `json:"secret,omitempty"`
	SecretHint          string     `json:"secret_hint"`
	ExpiresAt           *time.Time `json:"expires_at"`
	CreatedBy           actorRef   `json:"created_by"`
	LastUsedAt          *time.Time `json:"last_used_at"`
	CreatedAt           time.Time  `json:"created_at"`
	RevokedAt           *time.Time `json:"revoked_at"`
	version             int
}

const apiKeySQL = `select jsonb_build_object('id', k.id, 'name', k.name, 'scope', k.scope, 'permissions', k.permissions,
	'mode', k.mode, 'intended_use', k.intended_use, 'restrict_destructive', k.restrict_destructive,
	'secret_hint', k.secret_hint, 'expires_at', k.expires_at,
	'created_by', jsonb_build_object('type', k.created_by_type, 'id', k.created_by_id),
	'last_used_at', k.last_used_at, 'created_at', k.created_at, 'revoked_at', k.revoked_at, 'version_', k.version)
	from api_keys k`

func (c *Call) queryKeys(where string, args db.Args) ([]apiKey, error) {
	raw, err := c.q.Query(c.ctx, apiKeySQL+" where "+where, args)
	if err != nil {
		return nil, err
	}
	out := make([]apiKey, 0, len(raw))
	for _, x := range raw {
		var k struct {
			apiKey
			V int `json:"version_"`
		}
		if err := json.Unmarshal(x, &k); err != nil {
			return nil, err
		}
		k.apiKey.version = k.V
		out = append(out, k.apiKey)
	}
	return out, nil
}

// canManageKeyScope: platform keys need api_keys.manage; app keys need it
// or the Application's owner. API Keys never manage API Keys (enforced by
// userOnly on every write).
func (c *Call) canManageKeyScope(scope string) (bool, error) {
	if c.can("api_keys.manage") {
		return true, nil
	}
	if scope == "platform" {
		return false, nil
	}
	a, err := c.loadApp(scope)
	if err == db.ErrNotFound {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return c.isAppOwner(a)
}

func (c *Call) loadManagedKey(id string) (apiKey, error) {
	ks, err := c.queryKeys("k.id = :id", db.Args{"id": id})
	if err != nil {
		return apiKey{}, err
	}
	if len(ks) == 0 {
		return apiKey{}, httpx.NotFoundResource("api_key")
	}
	if ok, err := c.canManageKeyScope(ks[0].Scope); err != nil {
		return apiKey{}, err
	} else if !ok {
		return apiKey{}, httpx.NotFoundResource("api_key")
	}
	return ks[0], nil
}

func secretHint(secret string) string { return "…" + secret[len(secret)-4:] }

func newKeySecret(mode string) string {
	prefix := ids.APIKeyLive
	if mode == "test" {
		prefix = ids.APIKeyTest
	}
	return ids.Secret(prefix)
}

// validateKeyPermissions applies API Keys → App-confined permissions.
func (c *Call) validateKeyPermissions(scope string, perms []string) error {
	var a *appRow
	if scope != "platform" {
		x, err := c.loadApp(scope)
		if err == db.ErrNotFound {
			var f httpx.Fields
			f.Add("scope", "unknown_value")
			return f.Err()
		}
		if err != nil {
			return err
		}
		a = &x
	}
	for _, p := range perms {
		switch {
		case isPlatformPermission(p):
			if a != nil && !slices.Contains(appConfinable, p) {
				return httpx.E(422, "permission_not_grantable_to_scope", "An app-scoped key can't carry "+p+".").With("permission", p)
			}
			if a == nil && !c.can(p) {
				return httpx.E(403, "role_escalation_forbidden", "A platform key can't carry a permission its creator doesn't hold.").With("permission", p)
			}
		case strings.HasPrefix(p, "app."):
			declared := false
			if a != nil {
				for _, ap := range a.Permissions {
					declared = declared || ap.Key == p
				}
			}
			if !declared {
				return httpx.E(422, "unknown_permission", "Unknown permission, or another Application's key.").With("permission", p)
			}
		default:
			return httpx.E(422, "unknown_permission", "Unknown permission.").With("permission", p)
		}
	}
	return nil
}

func (s *Server) ApiKeysList(w http.ResponseWriter, r *http.Request, params gen.ApiKeysListParams) {
	s.serve(w, r, opts{op: "apiKeys.list", userOnly: true}, func(c *Call) error {
		where, args := []string{"true"}, db.Args{}
		if !c.can("api_keys.manage") {
			where = append(where, `k.scope in (select a.id from applications a where a.owner_user_id = :me
				or a.owner_organization_id in (select organization_id from organization_memberships
				   where user_id = :me and role = 'org_admin' and status = 'active'))`)
			args["me"] = c.p.UserID
		}
		if params.Scope != nil {
			where = append(where, "k.scope = :scope")
			args["scope"] = *params.Scope
		}
		if params.Mode != nil {
			where = append(where, "k.mode = :mode")
			args["mode"] = string(*params.Mode)
		}
		if params.IntendedUse != nil {
			where = append(where, "k.intended_use = :iu")
			args["iu"] = string(*params.IntendedUse)
		}
		if params.IncludeInactive == nil || !*params.IncludeInactive {
			where = append(where, "k.revoked_at is null and (k.expires_at is null or k.expires_at > now())")
		}
		lim := httpx.Limit(params.Limit)
		args["lim"] = int64(lim + 1)
		filters := httpx.Filters(c.r)
		pos, err := c.s.Cursors.Decode(deref(params.Cursor), filters)
		if err != nil {
			return err
		}
		if pos != nil {
			where = append(where, "(k.created_at, k.id) < (:ps::timestamptz, :pi)")
			args["ps"], args["pi"] = pos.Sort, pos.ID
		}
		rows, err := c.queryKeys(strings.Join(where, " and ")+" order by k.created_at desc, k.id desc limit :lim", args)
		if err != nil {
			return err
		}
		return c.OK(httpx.Paginate(c.s.Cursors, rows, lim, filters, func(k apiKey) (string, string) {
			return httpx.TimeKey(k.CreatedAt), k.ID
		}))
	})
}

func (s *Server) ApiKeysCreate(w http.ResponseWriter, r *http.Request) {
	s.serve(w, r, opts{op: "apiKeys.create", userOnly: true, idem: idemOptional}, func(c *Call) error {
		var in struct {
			Name                string     `json:"name"`
			Scope               string     `json:"scope"`
			Permissions         []string   `json:"permissions"`
			Mode                *string    `json:"mode"`
			IntendedUse         *string    `json:"intended_use"`
			RestrictDestructive *bool      `json:"restrict_destructive"`
			ExpiresAt           *time.Time `json:"expires_at"`
		}
		if err := c.Decode(&in); err != nil {
			return err
		}
		var f httpx.Fields
		if in.Name == "" {
			f.Add("name", "required")
		} else if len([]rune(in.Name)) > 100 {
			f.Max("name", "too_long", 100)
		}
		if in.Scope == "" {
			f.Add("scope", "required")
		}
		if in.Permissions == nil {
			f.Add("permissions", "required")
		}
		mode := "live"
		if in.Mode != nil {
			mode = *in.Mode
			if mode != "live" && mode != "test" {
				f.Enum("mode", "live", "test")
			}
		}
		use := "service"
		if in.IntendedUse != nil {
			use = *in.IntendedUse
			if use != "service" && use != "agent" {
				f.Enum("intended_use", "service", "agent")
			}
		}
		if in.ExpiresAt != nil && !in.ExpiresAt.After(c.now) {
			f.Add("expires_at", "out_of_range").Message = "must be in the future"
		}
		if err := f.Err(); err != nil {
			return err
		}
		if ok, err := c.canManageKeyScope(in.Scope); err != nil {
			return err
		} else if !ok {
			return httpx.Forbidden("api_keys.manage")
		}
		perms := dedupe(in.Permissions)
		if err := c.validateKeyPermissions(in.Scope, perms); err != nil {
			return err
		}
		restrict := use == "agent"
		if in.RestrictDestructive != nil {
			restrict = *in.RestrictDestructive
		}
		secret := newKeySecret(mode)
		id := ids.New(ids.APIKey)
		if _, err := c.q.Exec(c.ctx, `insert into api_keys (id, name, scope, permissions, mode, intended_use,
				restrict_destructive, secret_hash, secret_hint, expires_at, created_by_type, created_by_id, test_mode)
			values (:id, :n, :s, :p::text[], :m, :iu, :rd, :h, :hint, :exp::timestamptz, 'user', :by, :tm)`,
			db.Args{"id": id, "n": in.Name, "s": in.Scope, "p": db.TextArray(perms), "m": mode, "iu": use, "rd": restrict,
				"h": ids.Hash(secret), "hint": secretHint(secret), "exp": db.NullTime(in.ExpiresAt), "by": c.p.UserID,
				"tm": mode == "test"}); err != nil {
			return err
		}
		ks, err := c.queryKeys("k.id = :id", db.Args{"id": id})
		if err != nil {
			return err
		}
		k := ks[0]
		app := appIDOf(in.Scope)
		if err := c.audit(Audit{Action: "api_key.created", TargetType: "api_key", TargetID: id, ApplicationID: app, After: k}); err != nil {
			return err
		}
		if use == "agent" && !restrict {
			if err := c.audit(Audit{Action: "api_key.restrict_destructive_disabled", TargetType: "api_key", TargetID: id,
				ApplicationID: app, After: map[string]any{"restrict_destructive": false}}); err != nil {
				return err
			}
		}
		k.Secret = secret
		c.Versioned(k.version)
		return c.Created(k)
	})
}

func (s *Server) ApiKeysGet(w http.ResponseWriter, r *http.Request, id string) {
	s.serve(w, r, opts{op: "apiKeys.get", userOnly: true}, func(c *Call) error {
		k, err := c.loadManagedKey(id)
		if err != nil {
			return err
		}
		c.Versioned(k.version)
		return c.OK(k)
	})
}

func keyInactive(k apiKey, now time.Time) bool {
	return k.RevokedAt != nil || (k.ExpiresAt != nil && !k.ExpiresAt.After(now))
}

func (s *Server) ApiKeysUpdate(w http.ResponseWriter, r *http.Request, id string) {
	s.serve(w, r, opts{op: "apiKeys.update", userOnly: true}, func(c *Call) error {
		k, err := c.loadManagedKey(id)
		if err != nil {
			return err
		}
		if keyInactive(k, c.now) {
			return httpx.Conflict("api_key_revoked", "This key is revoked or expired.")
		}
		if err := httpx.CheckIfMatch(c.r, k.version); err != nil {
			return err
		}
		p, err := c.Patch()
		if err != nil {
			return err
		}
		if err := p.RejectReadOnly("id", "scope", "mode", "intended_use", "secret_hint", "created_by", "created_at",
			"last_used_at", "revoked_at", "expires_at", "secret"); err != nil {
			return err
		}
		var f httpx.Fields
		p.RequireNonNull(&f, "name", "permissions", "restrict_destructive")
		next := k
		if v, present := p.String("name", &f); present && v != nil {
			if *v == "" || len([]rune(*v)) > 100 {
				f.Max("name", "too_long", 100)
			}
			next.Name = *v
		}
		if p.Has("permissions") {
			var perms []string
			if p.Get("permissions", &perms, &f) {
				next.Permissions = dedupe(perms)
			}
		}
		if p.Has("restrict_destructive") {
			p.Get("restrict_destructive", &next.RestrictDestructive, &f)
		}
		if err := f.Err(); err != nil {
			return err
		}
		if err := c.validateKeyPermissions(k.Scope, next.Permissions); err != nil {
			return err
		}
		disabling := k.RestrictDestructive && !next.RestrictDestructive
		if err := c.destructiveIf(disabling); err != nil {
			return err
		}
		if _, err := c.q.Exec(c.ctx, `update api_keys set name = :n, permissions = :p::text[], restrict_destructive = :rd
			where id = :id`, db.Args{"n": next.Name, "p": db.TextArray(next.Permissions), "rd": next.RestrictDestructive,
			"id": k.ID}); err != nil {
			return err
		}
		ks, err := c.queryKeys("k.id = :id", db.Args{"id": k.ID})
		if err != nil {
			return err
		}
		after := ks[0]
		app := appIDOf(k.Scope)
		if err := c.audit(Audit{Action: "api_key.updated", TargetType: "api_key", TargetID: k.ID, ApplicationID: app,
			Before: k, After: after}); err != nil {
			return err
		}
		if disabling && k.IntendedUse == "agent" {
			if err := c.audit(Audit{Action: "api_key.restrict_destructive_disabled", TargetType: "api_key", TargetID: k.ID,
				ApplicationID: app, Before: map[string]any{"restrict_destructive": true},
				After: map[string]any{"restrict_destructive": false}}); err != nil {
				return err
			}
		}
		c.Versioned(after.version)
		return c.OK(after)
	})
}

func (s *Server) ApiKeysRotate(w http.ResponseWriter, r *http.Request, id string) {
	s.serve(w, r, opts{op: "apiKeys.rotate", userOnly: true}, func(c *Call) error {
		k, err := c.loadManagedKey(id)
		if err != nil {
			return err
		}
		if keyInactive(k, c.now) {
			return httpx.Conflict("api_key_revoked", "This key is revoked or expired.")
		}
		secret := newKeySecret(k.Mode)
		if _, err := c.q.Exec(c.ctx, `update api_keys set secret_hash = :h, secret_hint = :hint where id = :id`,
			db.Args{"h": ids.Hash(secret), "hint": secretHint(secret), "id": k.ID}); err != nil {
			return err
		}
		ks, err := c.queryKeys("k.id = :id", db.Args{"id": k.ID})
		if err != nil {
			return err
		}
		after := ks[0]
		if err := c.audit(Audit{Action: "api_key.rotated", TargetType: "api_key", TargetID: k.ID, ApplicationID: appIDOf(k.Scope),
			After: map[string]any{"secret_hint": after.SecretHint}}); err != nil {
			return err
		}
		after.Secret = secret
		c.Versioned(after.version)
		return c.OK(after)
	})
}

func (s *Server) ApiKeysDelete(w http.ResponseWriter, r *http.Request, id string) {
	s.serve(w, r, opts{op: "apiKeys.delete", userOnly: true}, func(c *Call) error {
		k, err := c.loadManagedKey(id)
		if err != nil {
			return err
		}
		if keyInactive(k, c.now) {
			return httpx.Conflict("api_key_revoked", "This key is already revoked or expired.")
		}
		if err := httpx.CheckIfMatch(c.r, k.version); err != nil {
			return err
		}
		if _, err := c.q.Exec(c.ctx, `update api_keys set revoked_at = now() where id = :id`, db.Args{"id": k.ID}); err != nil {
			return err
		}
		if err := c.audit(Audit{Action: "api_key.revoked", TargetType: "api_key", TargetID: k.ID, ApplicationID: appIDOf(k.Scope),
			Before: map[string]any{"revoked_at": nil}, After: map[string]any{"revoked_at": c.now}}); err != nil {
			return err
		}
		return c.NoContent()
	})
}
