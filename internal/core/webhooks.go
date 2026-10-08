package core

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/CompositeCode/substratalapps.com/internal/api/gen"
	"github.com/CompositeCode/substratalapps.com/internal/db"
	"github.com/CompositeCode/substratalapps.com/internal/httpx"
	"github.com/CompositeCode/substratalapps.com/internal/ids"
)

// WebhookEventTypes is every event type a subscription can name.
var WebhookEventTypes = []string{
	"access.granted", "access.revoked",
	"entitlement.granted", "entitlement.disabled", "entitlement.revoked", "entitlement.expired",
	"entitlement.updated", "entitlement.member_scope_changed", "entitlement.deleted",
	"role.assigned", "role.removed", "user.suspended", "user.reactivated", "user.deleted",
	"organization.member_added", "organization.member_removed", "application.review_status_changed", "webhook.test",
}

// PublishedWebhookVersions are the payload versions a subscription may pin.
var PublishedWebhookVersions = []string{"2026-10-05"}

type webhook struct {
	ID                  string     `json:"id"`
	Scope               string     `json:"scope"`
	URL                 string     `json:"url"`
	Events              []string   `json:"events"`
	Description         *string    `json:"description"`
	Status              string     `json:"status"`
	SigningSecret       string     `json:"signing_secret,omitempty"`
	APIVersion          string     `json:"api_version"`
	TestMode            bool       `json:"test_mode"`
	ConsecutiveFailures int        `json:"consecutive_failures"`
	LastDeliveryAt      *time.Time `json:"last_delivery_at"`
	CreatedAt           time.Time  `json:"created_at"`
	UpdatedAt           time.Time  `json:"updated_at"`
	version             int
	tenantID            *string
}

const webhookSQL = `select jsonb_build_object('id', w.id, 'scope', w.scope, 'url', w.url, 'events', w.events,
	'description', w.description, 'status', w.status, 'api_version', w.api_version, 'test_mode', w.test_mode,
	'consecutive_failures', w.consecutive_failures, 'last_delivery_at', w.last_delivery_at,
	'created_at', w.created_at, 'updated_at', w.updated_at, 'version_', w.version, 'tenant_', w.tenant_id)
	from webhook_subscriptions w`

func (c *Call) queryWebhooks(where string, args db.Args) ([]webhook, error) {
	raw, err := c.q.Query(c.ctx, webhookSQL+" where "+where, args)
	if err != nil {
		return nil, err
	}
	out := make([]webhook, 0, len(raw))
	for _, x := range raw {
		var w struct {
			webhook
			V int     `json:"version_"`
			T *string `json:"tenant_"`
		}
		if err := json.Unmarshal(x, &w); err != nil {
			return nil, err
		}
		w.webhook.version, w.webhook.tenantID = w.V, w.T
		out = append(out, w.webhook)
	}
	return out, nil
}

// canManageWebhookScope: app scope → its own keys and its owner (no extra
// permission), plus webhooks.manage; platform scope → webhooks.manage.
func (c *Call) canManageWebhookScope(scope string) (bool, error) {
	if c.can("webhooks.manage") {
		return true, nil
	}
	if scope == "platform" {
		return false, nil
	}
	if app, ok := c.p.AppKey(); ok {
		return app == scope, nil
	}
	a, err := c.loadApp(scope)
	if err != nil {
		return false, nil
	}
	return c.isAppOwner(a)
}

func (c *Call) loadManagedWebhook(id string) (webhook, error) {
	ws, err := c.queryWebhooks("w.id = :id", db.Args{"id": id})
	if err != nil {
		return webhook{}, err
	}
	if len(ws) == 0 {
		return webhook{}, httpx.NotFoundResource("webhook")
	}
	if ok, err := c.canManageWebhookScope(ws[0].Scope); err != nil {
		return webhook{}, err
	} else if !ok {
		return webhook{}, httpx.NotFoundResource("webhook")
	}
	return ws[0], nil
}

func invalidWebhookURL(why string) error {
	return httpx.E(422, "invalid_webhook_url", "The webhook URL isn't allowed: "+why+".")
}

// AllowPrivateWebhookTargets disables the public-IP check, for local
// development and tests only (a receiver on localhost). Never in production.
var AllowPrivateWebhookTargets = false

// validateWebhookURL: https, no userinfo, resolving only to public
// addresses (SSRF guard). The delivery worker re-checks at send time.
func validateWebhookURL(ctx context.Context, raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return invalidWebhookURL("not a URL")
	}
	if u.User != nil {
		return invalidWebhookURL("credentials in the URL")
	}
	if AllowPrivateWebhookTargets {
		if u.Scheme != "https" && u.Scheme != "http" {
			return invalidWebhookURL("not http(s)")
		}
		return nil
	}
	if u.Scheme != "https" {
		return invalidWebhookURL("not https")
	}
	return CheckPublicHost(ctx, u.Hostname())
}

// CheckPublicHost resolves host and rejects private, loopback, link-local,
// and other non-public addresses.
func CheckPublicHost(ctx context.Context, host string) error {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	addrs, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil || len(addrs) == 0 {
		return invalidWebhookURL("doesn't resolve")
	}
	for _, a := range addrs {
		ip := a.IP
		if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
			ip.IsUnspecified() || ip.IsMulticast() || ip.IsInterfaceLocalMulticast() || isCGNAT(ip) {
			return invalidWebhookURL("resolves to a non-public address")
		}
	}
	return nil
}

func isCGNAT(ip net.IP) bool {
	_, n, _ := net.ParseCIDR("100.64.0.0/10")
	return n.Contains(ip)
}

func validateEvents(events []string) error {
	if len(events) == 0 {
		var f httpx.Fields
		f.Add("events", "required")
		return f.Err()
	}
	if len(events) == 1 && events[0] == "*" {
		return nil
	}
	seen := map[string]bool{}
	for _, e := range events {
		if !slices.Contains(WebhookEventTypes, e) {
			return httpx.E(422, "unknown_event_type", "Unknown event type: "+e+".").With("event", e)
		}
		if seen[e] {
			var f httpx.Fields
			f.Add("events", "not_unique").Message = e
			return f.Err()
		}
		seen[e] = true
	}
	return nil
}

func (s *Server) WebhooksList(w http.ResponseWriter, r *http.Request, params gen.WebhooksListParams) {
	s.serve(w, r, opts{op: "webhooks.list"}, func(c *Call) error {
		where, args := []string{"true"}, db.Args{}
		if !c.can("webhooks.manage") {
			if app, ok := c.p.AppKey(); ok {
				where = append(where, "w.scope = :app")
				args["app"] = app
			} else if c.p.IsUser() {
				where = append(where, `w.scope in (select a.id from applications a where a.owner_user_id = :me
					or a.owner_organization_id in (select organization_id from organization_memberships
					   where user_id = :me and role = 'org_admin' and status = 'active'))`)
				args["me"] = c.p.UserID
			} else {
				return c.OK(httpx.List[webhook]{Data: []webhook{}})
			}
		}
		if params.ApplicationId != nil {
			where = append(where, "w.scope = :scope")
			args["scope"] = *params.ApplicationId
		}
		lim := httpx.Limit(params.Limit)
		args["lim"] = int64(lim + 1)
		filters := httpx.Filters(c.r)
		pos, err := c.s.Cursors.Decode(deref(params.Cursor), filters)
		if err != nil {
			return err
		}
		if pos != nil {
			where = append(where, "(w.created_at, w.id) < (:ps::timestamptz, :pi)")
			args["ps"], args["pi"] = pos.Sort, pos.ID
		}
		rows, err := c.queryWebhooks(strings.Join(where, " and ")+" order by w.created_at desc, w.id desc limit :lim", args)
		if err != nil {
			return err
		}
		return c.OK(httpx.Paginate(c.s.Cursors, rows, lim, filters, func(x webhook) (string, string) {
			return httpx.TimeKey(x.CreatedAt), x.ID
		}))
	})
}

func (s *Server) WebhooksCreate(w http.ResponseWriter, r *http.Request) {
	s.serve(w, r, opts{op: "webhooks.create", idem: idemOptional}, func(c *Call) error {
		var in struct {
			Scope       *string  `json:"scope"`
			URL         string   `json:"url"`
			Events      []string `json:"events"`
			Description *string  `json:"description"`
		}
		if err := c.Decode(&in); err != nil {
			return err
		}
		scope := ""
		if in.Scope != nil {
			scope = *in.Scope
		}
		if app, ok := c.p.AppKey(); ok {
			if scope == "" {
				scope = app
			}
			if scope != app {
				return httpx.Forbidden("")
			}
		}
		if scope == "" {
			return httpx.Invalid("scope is required.")
		}
		if ok, err := c.canManageWebhookScope(scope); err != nil {
			return err
		} else if !ok {
			return httpx.Forbidden("webhooks.manage")
		}
		if in.Description != nil && len([]rune(*in.Description)) > 2000 {
			var f httpx.Fields
			f.Max("description", "too_long", 2000)
			return f.Err()
		}
		if err := validateEvents(in.Events); err != nil {
			return err
		}
		if err := validateWebhookURL(c.ctx, in.URL); err != nil {
			return err
		}
		var tenantID *string
		if scope != "platform" {
			a, err := c.loadApp(scope)
			if err != nil {
				return httpx.NotFoundResource("application")
			}
			t, err := c.loadTenant(a.TenantID)
			if err != nil {
				return err
			}
			if err := tenantWritable(t); err != nil {
				return err
			}
			if t.Restricted {
				return subscriptionRequired(t)
			}
			if l := plans[t.Plan].Webhooks; l != nil {
				var n int
				if err := db.One(c.ctx, c.q, &n, `select to_jsonb(count(*)) from webhook_subscriptions where tenant_id = :t`,
					db.Args{"t": t.ID}); err != nil {
					return err
				}
				if n >= *l {
					return planLimitErr("webhooks", *l, n, t)
				}
			}
			tenantID = &t.ID
		}
		secret := ids.Secret(ids.WebhookSecret)
		enc, err := c.s.Encrypter.Encrypt(c.ctx, secret)
		if err != nil {
			return err
		}
		id := ids.New(ids.Webhook)
		if _, err := c.q.Exec(c.ctx, `insert into webhook_subscriptions (id, scope, tenant_id, url, events, description,
				api_version, secret_ciphertext, test_mode)
			values (:id, :s, :t, :u, :e::text[], :d, :v, :sec, :tm)`,
			db.Args{"id": id, "s": scope, "t": tenantID, "u": in.URL, "e": db.TextArray(in.Events), "d": in.Description,
				"v": webhookAPIVersion, "sec": enc, "tm": c.testMode()}); err != nil {
			return err
		}
		ws, err := c.queryWebhooks("w.id = :id", db.Args{"id": id})
		if err != nil {
			return err
		}
		out := ws[0]
		if err := c.audit(Audit{Action: "webhook.created", TargetType: "webhook", TargetID: id, ApplicationID: appIDOf(scope),
			TenantID: tenantID, After: out}); err != nil {
			return err
		}
		out.SigningSecret = secret
		c.Versioned(out.version)
		return c.Created(out)
	})
}

func (s *Server) WebhooksGet(w http.ResponseWriter, r *http.Request, id string) {
	s.serve(w, r, opts{op: "webhooks.get"}, func(c *Call) error {
		x, err := c.loadManagedWebhook(id)
		if err != nil {
			return err
		}
		c.Versioned(x.version)
		return c.OK(x)
	})
}

func (s *Server) WebhooksUpdate(w http.ResponseWriter, r *http.Request, id string) {
	s.serve(w, r, opts{op: "webhooks.update"}, func(c *Call) error {
		x, err := c.loadManagedWebhook(id)
		if err != nil {
			return err
		}
		if err := httpx.CheckIfMatch(c.r, x.version); err != nil {
			return err
		}
		p, err := c.Patch()
		if err != nil {
			return err
		}
		if err := p.RejectReadOnly("id", "scope", "test_mode", "created_at", "updated_at", "consecutive_failures",
			"last_delivery_at", "signing_secret"); err != nil {
			return err
		}
		var f httpx.Fields
		p.RequireNonNull(&f, "url", "events", "api_version", "status")
		next := x
		if v, present := p.String("url", &f); present && v != nil {
			if err := validateWebhookURL(c.ctx, *v); err != nil {
				return err
			}
			next.URL = *v
		}
		if p.Has("events") {
			var events []string // fresh: next.Events shares x's backing array
			if p.Get("events", &events, &f) {
				if err := validateEvents(events); err != nil {
					return err
				}
				next.Events = events
			}
		}
		if p.Has("description") {
			next.Description, _ = p.String("description", &f)
		}
		if v, present := p.String("api_version", &f); present && v != nil {
			idx := slices.Index(PublishedWebhookVersions, *v)
			if idx < 0 || idx < slices.Index(PublishedWebhookVersions, x.APIVersion) {
				f.Add("api_version", "unknown_value").Allowed = PublishedWebhookVersions
			}
			next.APIVersion = *v
		}
		reenable := false
		if v, present := p.String("status", &f); present && v != nil && *v != x.Status {
			if !(x.Status == "disabled" && *v == "healthy") {
				return httpx.Conflict("invalid_status_transition", "Only disabled → healthy is allowed.")
			}
			next.Status, reenable = *v, true
		}
		if err := f.Err(); err != nil {
			return err
		}
		resetSQL := ""
		if reenable {
			resetSQL = ", consecutive_failures = 0, unhealthy_since = null"
		}
		if _, err := c.q.Exec(c.ctx, `update webhook_subscriptions set url = :u, events = :e::text[], description = :d,
			api_version = :v, status = :st`+resetSQL+` where id = :id`,
			db.Args{"u": next.URL, "e": db.TextArray(next.Events), "d": next.Description, "v": next.APIVersion,
				"st": next.Status, "id": x.ID}); err != nil {
			return err
		}
		after, err := c.loadManagedWebhook(x.ID)
		if err != nil {
			return err
		}
		if err := c.audit(Audit{Action: "webhook.updated", TargetType: "webhook", TargetID: x.ID, ApplicationID: appIDOf(x.Scope),
			TenantID: x.tenantID, Before: x, After: after}); err != nil {
			return err
		}
		c.Versioned(after.version)
		return c.OK(after)
	})
}

func (s *Server) WebhooksDelete(w http.ResponseWriter, r *http.Request, id string) {
	s.serve(w, r, opts{op: "webhooks.delete"}, func(c *Call) error {
		x, err := c.loadManagedWebhook(id)
		if err != nil {
			return err
		}
		if err := httpx.CheckIfMatch(c.r, x.version); err != nil {
			return err
		}
		if _, err := c.q.Exec(c.ctx, `delete from webhook_subscriptions where id = :id`, db.Args{"id": x.ID}); err != nil {
			return err
		}
		if err := c.audit(Audit{Action: "webhook.deleted", TargetType: "webhook", TargetID: x.ID, ApplicationID: appIDOf(x.Scope),
			TenantID: x.tenantID, Before: x}); err != nil {
			return err
		}
		return c.NoContent()
	})
}

func (s *Server) WebhooksRotateSecret(w http.ResponseWriter, r *http.Request, id string) {
	s.serve(w, r, opts{op: "webhooks.rotateSecret"}, func(c *Call) error {
		x, err := c.loadManagedWebhook(id)
		if err != nil {
			return err
		}
		secret := ids.Secret(ids.WebhookSecret)
		enc, err := c.s.Encrypter.Encrypt(c.ctx, secret)
		if err != nil {
			return err
		}
		expires := c.now.Add(24 * time.Hour)
		if _, err := c.q.Exec(c.ctx, `update webhook_subscriptions set previous_secret_ciphertext = secret_ciphertext,
			previous_secret_expires_at = :exp::timestamptz, secret_ciphertext = :s where id = :id`,
			db.Args{"exp": db.Time(expires), "s": enc, "id": x.ID}); err != nil {
			return err
		}
		if err := c.audit(Audit{Action: "webhook.secret_rotated", TargetType: "webhook", TargetID: x.ID,
			ApplicationID: appIDOf(x.Scope), TenantID: x.tenantID,
			After: map[string]any{"previous_secret_expires_at": expires}}); err != nil {
			return err
		}
		return c.OK(map[string]any{"id": x.ID, "signing_secret": secret, "previous_secret_expires_at": expires.Format(time.RFC3339)})
	})
}

// deliverNow creates a pending delivery of an existing event to one
// subscription, outside the normal fan-out (test, redeliver).
func (c *Call) deliverNow(subID, eventID string) (string, error) {
	did := ids.New(ids.Delivery)
	_, err := c.q.Exec(c.ctx, `insert into webhook_deliveries (id, subscription_id, event_id, attempt, status, next_retry_at, test_mode)
		values (:id, :s, :e, 1, 'pending', now(), :tm)`, db.Args{"id": did, "s": subID, "e": eventID, "tm": c.testMode()})
	if err != nil {
		return "", err
	}
	c.After(func(ctx contextT) { c.s.Notifier.EventCommitted(ctx, "delivery:"+did) })
	return did, nil
}

func (s *Server) WebhooksSendTest(w http.ResponseWriter, r *http.Request, id string) {
	s.serve(w, r, opts{op: "webhooks.sendTest"}, func(c *Call) error {
		x, err := c.loadManagedWebhook(id)
		if err != nil {
			return err
		}
		eid := ids.New(ids.WebhookEvent)
		// Delivered whatever the events filter says, even when disabled, and
		// never fanned out to anyone else: dispatched_at is set up front.
		if _, err := c.q.Exec(c.ctx, `insert into webhook_events (id, type, application_id, payload, test_mode, dispatched_at)
			values (:id, 'webhook.test', :app, :p::jsonb, :tm, now())`,
			db.Args{"id": eid, "app": appIDOf(x.Scope), "p": db.JSON(map[string]any{"message": "Test event from Substratal"}),
				"tm": x.TestMode}); err != nil {
			return err
		}
		did, err := c.deliverNow(x.ID, eid)
		if err != nil {
			return err
		}
		return c.Accepted(map[string]any{"delivery_id": did, "event_id": eid})
	})
}

type delivery struct {
	ID             string     `json:"id"`
	EventID        string     `json:"event_id"`
	EventType      string     `json:"event_type"`
	Attempt        int        `json:"attempt"`
	Status         string     `json:"status"`
	ResponseStatus *int       `json:"response_status"`
	DurationMS     *int       `json:"duration_ms"`
	Error          *string    `json:"error"`
	NextRetryAt    *time.Time `json:"next_retry_at"`
	CreatedAt      time.Time  `json:"created_at"`
}

func (s *Server) WebhooksListDeliveries(w http.ResponseWriter, r *http.Request, id string, params gen.WebhooksListDeliveriesParams) {
	s.serve(w, r, opts{op: "webhooks.listDeliveries"}, func(c *Call) error {
		if _, err := c.loadManagedWebhook(id); err != nil {
			return err
		}
		where := []string{"d.subscription_id = :s", "d.created_at > now() - interval '30 days'"}
		args := db.Args{"s": id}
		if params.EventId != nil {
			where = append(where, "d.event_id = :e")
			args["e"] = *params.EventId
		}
		if params.Status != nil {
			where = append(where, "d.status = :st")
			args["st"] = string(*params.Status)
		}
		if params.Since != nil {
			where = append(where, "d.created_at >= :since::timestamptz")
			args["since"] = db.Time(*params.Since)
		}
		lim := httpx.Limit(params.Limit)
		args["lim"] = int64(lim + 1)
		filters := httpx.Filters(c.r)
		pos, err := c.s.Cursors.Decode(deref(params.Cursor), filters)
		if err != nil {
			return err
		}
		if pos != nil {
			where = append(where, "(d.created_at, d.id) < (:ps::timestamptz, :pi)")
			args["ps"], args["pi"] = pos.Sort, pos.ID
		}
		rows, err := db.All[delivery](c.ctx, c.q, `select jsonb_build_object('id', d.id, 'event_id', d.event_id,
				'event_type', e.type, 'attempt', d.attempt, 'status', d.status, 'response_status', d.response_status,
				'duration_ms', d.duration_ms, 'error', d.error, 'next_retry_at', d.next_retry_at, 'created_at', d.created_at)
			from webhook_deliveries d join webhook_events e on e.id = d.event_id
			where `+strings.Join(where, " and ")+` order by d.created_at desc, d.id desc limit :lim`, args)
		if err != nil {
			return err
		}
		return c.OK(httpx.Paginate(c.s.Cursors, rows, lim, filters, func(x delivery) (string, string) {
			return httpx.TimeKey(x.CreatedAt), x.ID
		}))
	})
}

func (s *Server) WebhooksRedeliver(w http.ResponseWriter, r *http.Request, id string, deliveryID string) {
	s.serve(w, r, opts{op: "webhooks.redeliver"}, func(c *Call) error {
		if _, err := c.loadManagedWebhook(id); err != nil {
			return err
		}
		var eventID string
		err := db.One(c.ctx, c.q, &eventID, `select to_jsonb(event_id) from webhook_deliveries
			where id = :d and subscription_id = :s and created_at > now() - interval '30 days'`, db.Args{"d": deliveryID, "s": id})
		if err == db.ErrNotFound {
			return httpx.NotFoundResource("delivery")
		}
		if err != nil {
			return err
		}
		did, err := c.deliverNow(id, eventID)
		if err != nil {
			return err
		}
		return c.Accepted(map[string]any{"delivery_id": did, "event_id": eventID})
	})
}
