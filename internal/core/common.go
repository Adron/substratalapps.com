package core

import (
	"context"
	"encoding/json"
	"time"

	"github.com/Adron/substratalapps.com/internal/db"
	"github.com/Adron/substratalapps.com/internal/httpx"
	"github.com/Adron/substratalapps.com/internal/ids"
)

// contextT is context.Context, for the post-commit hooks registered with After.
type contextT = context.Context

// ── Audit (NFR → Audit; Orders & Audit → Audit Event) ──────────────────

// Audit is one Audit Event to write in the current transaction.
type Audit struct {
	Action         string
	TargetType     string
	TargetID       string
	TargetUserID   *string
	ApplicationID  *string
	OrganizationID *string
	TenantID       *string
	Before, After  any
	// Actor overrides the caller (system jobs).
	ActorType, ActorID string
}

// audit writes an Audit Event in the request's transaction, so the change
// and its record commit together or not at all.
func (c *Call) audit(a Audit) error {
	typ, id := c.actor()
	if a.ActorType != "" {
		typ, id = a.ActorType, a.ActorID
	}
	return writeAudit(c, c.q, a, typ, id, c.requestID, c.testMode())
}

func writeAudit(c *Call, q db.Querier, a Audit, actorType, actorID, requestID string, testMode bool) error {
	var before, after any
	if a.Before != nil {
		before = db.JSON(a.Before)
	}
	if a.After != nil {
		after = db.JSON(a.After)
	}
	var rid any
	if requestID != "" {
		rid = requestID
	}
	_, err := q.Exec(c.ctx, `insert into audit_events (id, action, actor_type, actor_id, target_type, target_id,
			target_user_id, application_id, organization_id, tenant_id, before, after, request_id, test_mode)
		values (:id, :action, :atype, :aid, :ttype, :tid, :tuser, :app, :org,
			coalesce(:tenant, (select tenant_id from applications where id = :app)),
			:before::jsonb, :after::jsonb, :rid, :tm)`,
		db.Args{"id": ids.New(ids.AuditEvent), "action": a.Action, "atype": actorType, "aid": actorID,
			"ttype": a.TargetType, "tid": a.TargetID, "tuser": a.TargetUserID, "app": a.ApplicationID,
			"org": a.OrganizationID, "tenant": a.TenantID, "before": before, "after": after,
			"rid": rid, "tm": testMode})
	return err
}

// ── Webhook outbox (Webhooks → Delivery) ───────────────────────────────

// webhookAPIVersion is the current (and only) payload version.
const webhookAPIVersion = "2026-10-05"

// emit inserts a webhook event into the transactional outbox, so a
// committed change can never fail to produce its event, and nudges the
// delivery pipeline once the transaction commits.
func (c *Call) emit(eventType string, appID *string, data any) error {
	return emitWith(c, c.q, eventType, appID, data, c.testMode())
}

func emitWith(c *Call, q db.Querier, eventType string, appID *string, data any, testMode bool) error {
	id := ids.New(ids.WebhookEvent)
	_, err := q.Exec(c.ctx, `insert into webhook_events (id, type, application_id, payload, test_mode)
		values (:id, :type, :app, :payload::jsonb, :tm)`,
		db.Args{"id": id, "type": eventType, "app": appID, "payload": db.JSON(data), "tm": testMode})
	if err != nil {
		return err
	}
	c.After(func(ctx context.Context) { c.s.Notifier.EventCommitted(ctx, id) })
	return nil
}

// ── Tenants & plan limits (Pricing → Enforcement) ──────────────────────

type tenantRow struct {
	ID                  string     `json:"id"`
	OwnerType           string     `json:"owner_type"`
	OwnerUserID         *string    `json:"owner_user_id"`
	OwnerOrganizationID *string    `json:"owner_organization_id"`
	Tier                string     `json:"tier"`
	Plan                string     `json:"plan"`
	Region              string     `json:"region"`
	StripeCustomerID    *string    `json:"stripe_customer_id"`
	StripeSubID         *string    `json:"stripe_subscription_id"`
	SubscriptionStatus  string     `json:"subscription_status"`
	PeriodStart         *time.Time `json:"current_period_start"`
	PeriodEnd           *time.Time `json:"current_period_end"`
	CancelAtPeriodEnd   bool       `json:"cancel_at_period_end"`
	Restricted          bool       `json:"restricted"`
	SeatCountSynced     *int       `json:"seat_count_synced"`
	SeatCountSyncedAt   *time.Time `json:"seat_count_synced_at"`
	Status              string     `json:"status"`
	CreatedAt           time.Time  `json:"created_at"`
	UpdatedAt           time.Time  `json:"updated_at"`
}

// planLimit is one plan's cap for a resource; nil means unlimited.
type planLimits struct {
	Applications, AppRoles, Webhooks, Seats *int
	SeatsIncluded                           int
	AuditHotDays                            *int
}

func ip(n int) *int { return &n }

var plans = map[string]planLimits{
	"starter":    {Applications: ip(1), AppRoles: ip(3), Webhooks: ip(1), Seats: ip(1000), SeatsIncluded: 1000, AuditHotDays: ip(30)},
	"team":       {Applications: ip(5), Webhooks: ip(10), SeatsIncluded: 25, AuditHotDays: ip(365)},
	"enterprise": {SeatsIncluded: 500},
}

func planLimitErr(resource string, limit, current int, t tenantRow) error {
	msg := map[string]string{
		"applications": "This Tenant's plan doesn't allow more Applications.",
		"app_roles":    "This Tenant's plan doesn't allow more AppRoles on one Application.",
		"webhooks":     "This Tenant's plan doesn't allow more webhook subscriptions.",
		"seats":        "This Tenant's plan doesn't allow more seats.",
	}[resource]
	return httpx.E(409, "plan_limit_reached", msg).
		With("resource", resource).With("limit", limit).With("current", current).
		With("plan", t.Plan).With("tenant_id", t.ID)
}

func subscriptionRequired(t tenantRow) error {
	return httpx.E(402, "subscription_required",
		"This Tenant's subscription has lapsed; resubscribe to add usage.").With("tenant_id", t.ID)
}

func (c *Call) loadTenant(id string) (tenantRow, error) {
	var t tenantRow
	err := db.One(c.ctx, c.q, &t, `select to_jsonb(t) from tenants t where id = :id`, db.Args{"id": id})
	if err == db.ErrNotFound {
		return t, httpx.NotFoundResource("tenant")
	}
	return t, err
}

// tenantWritable enforces Tenant.status on a write to the Tenant's data:
// suspended → 403 tenant_suspended, migrating → 503 with Retry-After.
func tenantWritable(t tenantRow) error {
	switch t.Status {
	case "suspended":
		return httpx.E(403, "tenant_suspended", "This Tenant is suspended by the platform.").With("tenant_id", t.ID)
	case "migrating":
		return httpx.E(503, "service_unavailable", "This Tenant is mid-migration; writes are paused.").
			With("tenant_id", t.ID).RetryAfter(300)
	}
	return nil
}

// ensureTenant resolves (creating on first use) the Tenant for an
// Application owner: tier shared, plan starter for a User owner and team
// for an Organization owner (Tenancy → What it represents).
func (c *Call) ensureTenant(ownerUserID, ownerOrgID *string) (tenantRow, error) {
	var t tenantRow
	var err error
	if ownerUserID != nil {
		err = db.One(c.ctx, c.q, &t, `select to_jsonb(t) from tenants t where owner_user_id = :u`, db.Args{"u": *ownerUserID})
	} else {
		err = db.One(c.ctx, c.q, &t, `select to_jsonb(t) from tenants t where owner_organization_id = :o`, db.Args{"o": *ownerOrgID})
	}
	if err != db.ErrNotFound {
		return t, err
	}
	ownerType, plan := "user", "starter"
	if ownerOrgID != nil {
		ownerType, plan = "organization", "team"
	}
	err = db.One(c.ctx, c.q, &t, `insert into tenants (id, owner_type, owner_user_id, owner_organization_id, plan)
		values (:id, :type, :u, :o, :plan) returning to_jsonb(tenants)`,
		db.Args{"id": ids.New(ids.Tenant), "type": ownerType, "u": ownerUserID, "o": ownerOrgID, "plan": plan})
	if err != nil {
		return t, err
	}
	c.After(func(ctx context.Context) { c.s.provisionStripeCustomer(ctx, t.ID) })
	return t, nil
}

// ── Users ──────────────────────────────────────────────────────────────

type userRow struct {
	ID                  string     `json:"id"`
	Email               string     `json:"email"`
	EmailVerified       bool       `json:"email_verified"`
	PendingEmail        *string    `json:"pending_email"`
	Status              string     `json:"status"`
	MFAEnabled          bool       `json:"mfa_enabled"`
	SignupApplicationID *string    `json:"signup_application_id"`
	TestMode            bool       `json:"test_mode"`
	CreatedAt           time.Time  `json:"created_at"`
	UpdatedAt           time.Time  `json:"updated_at"`
	LastLoginAt         *time.Time `json:"last_login_at"`
	Version             int        `json:"-"`
	DeletedAt           *time.Time `json:"-"`
}

// userRowSQL selects a user with its computed mfa_enabled, as the API
// User object plus version/deleted_at.
const userRowSQL = `select to_jsonb(u) || jsonb_build_object(
	'mfa_enabled', exists (select 1 from user_identities i where i.user_id = u.id and i.mfa_enabled),
	'version_', u.version, 'deleted_at_', u.deleted_at) from users u`

type userRowDB struct {
	userRow
	V  int        `json:"version_"`
	DA *time.Time `json:"deleted_at_"`
}

func decodeUser(raw []byte) (userRow, error) {
	var r userRowDB
	if err := json.Unmarshal(raw, &r); err != nil {
		return userRow{}, err
	}
	r.userRow.Version, r.userRow.DeletedAt = r.V, r.DA
	return r.userRow, nil
}

func (c *Call) findUser(where string, args db.Args) (userRow, error) {
	rows, err := c.q.Query(c.ctx, userRowSQL+" where "+where, args)
	if err != nil {
		return userRow{}, err
	}
	if len(rows) == 0 {
		return userRow{}, db.ErrNotFound
	}
	return decodeUser(rows[0])
}

// visibleUser loads a User by id for the caller: a soft-deleted User 404s
// for everyone but users.manage (Conventions → Filtering).
func (c *Call) visibleUser(id string) (userRow, error) {
	u, err := c.findUser("u.id = :id", db.Args{"id": id})
	if err == db.ErrNotFound || (err == nil && u.Status == "deleted" && !c.can("users.manage")) {
		return u, httpx.NotFoundResource("user")
	}
	return u, err
}

// ── Applications ───────────────────────────────────────────────────────

type appRow struct {
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
	Version             int             `json:"version"`
}

type appPermission struct {
	Key         string  `json:"key"`
	Description *string `json:"description,omitempty"`
}

func (c *Call) loadApp(id string) (appRow, error) {
	var a appRow
	err := db.One(c.ctx, c.q, &a, `select to_jsonb(a) from applications a where id = :id`, db.Args{"id": id})
	return a, err
}

// isAppOwner: the owner_user_id User, or any active org_admin of
// owner_organization_id, acting with a User token.
func (c *Call) isAppOwner(a appRow) (bool, error) {
	if c.p == nil || !c.p.IsUser() {
		return false, nil
	}
	if a.OwnerUserID != nil {
		return *a.OwnerUserID == c.p.UserID, nil
	}
	if a.OwnerOrganizationID != nil {
		return c.isOrgAdmin(*a.OwnerOrganizationID)
	}
	return false, nil
}

// isOrgAdmin reports whether the calling User is an active org_admin.
func (c *Call) isOrgAdmin(orgID string) (bool, error) {
	if c.p == nil || !c.p.IsUser() {
		return false, nil
	}
	rows, err := c.q.Query(c.ctx, `select to_jsonb(true) from organization_memberships
		where organization_id = :o and user_id = :u and role = 'org_admin' and status = 'active'`,
		db.Args{"o": orgID, "u": c.p.UserID})
	return len(rows) > 0, err
}

// canManageApp is "entitlements.manage (platform or app-confined), or the
// Application's owner" for a given permission, the pattern Entitlements,
// Roles, and Webhooks all share.
func (c *Call) canManageApp(perm string, a appRow) (bool, error) {
	if c.can(perm) || c.appKeyCan(perm, a.ID) {
		return true, nil
	}
	return c.isAppOwner(a)
}

// userHasAccessPath reports whether a User has any access path (personal
// Entitlement in any status, or membership in an Organization holding a
// grant) to an Application, the visibility rule for app-confined callers.
func (c *Call) userHasAccessPath(userID, appID string) (bool, error) {
	rows, err := c.q.Query(c.ctx, `select to_jsonb(true) where exists (
			select 1 from entitlements e where e.user_id = :u and e.application_id = :a)
		or exists (
			select 1 from entitlements e join organization_memberships m on m.organization_id = e.organization_id
			where m.user_id = :u and e.application_id = :a)`, db.Args{"u": userID, "a": appID})
	return len(rows) > 0, err
}

func strp(s string) *string { return &s }

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
