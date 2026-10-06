package core

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/Adron/substratalapps.com/internal/db"
	"github.com/Adron/substratalapps.com/internal/httpx"
	"github.com/Adron/substratalapps.com/internal/schema"
)

// system runs fn as the platform itself (actor "system") in one
// transaction pinned to testMode. Jobs that touch both modes call it once
// per mode, so every event and audit row carries the right mode.
func (s *Server) system(ctx context.Context, testMode bool, settings db.Settings, fn func(c *Call) error) error {
	settings.TestMode = testMode
	c := &Call{s: s, ctx: ctx, now: s.Now().UTC().Truncate(time.Microsecond), headers: map[string][]string{}, sysTestMode: testMode}
	err := s.DB.Tx(ctx, settings, func(q db.Querier) error {
		c.q = q
		return fn(c)
	})
	if err != nil {
		return err
	}
	for _, f := range c.afterCommit {
		f(context.WithoutCancel(ctx))
	}
	return nil
}

func eachMode(fn func(testMode bool) error) error {
	for _, m := range []bool{false, true} {
		if err := fn(m); err != nil {
			return err
		}
	}
	return nil
}

// ── Stripe sync (DEPLOYMENT.md → Webhook handling) ─────────────────────

// StripeSubscription is the subscription state the sync applies, read
// fresh from Stripe by the billing package.
type StripeSubscription struct {
	ID                string
	TenantID          string
	Status            string
	Plan              string // from the product's metadata.substratal_plan
	PeriodStart       *time.Time
	PeriodEnd         *time.Time
	CancelAtPeriodEnd bool
	SeatItemID        string
}

// RecordStripeEvent inserts the event id (insert-or-skip) and reports
// whether it's new.
func (s *Server) RecordStripeEvent(ctx context.Context, id, typ string) (bool, error) {
	var fresh bool
	err := s.system(ctx, false, db.Settings{}, func(c *Call) error {
		n, err := c.q.Exec(ctx, `insert into stripe_events (id, type) values (:id, :t) on conflict (id) do nothing`,
			db.Args{"id": id, "t": typ})
		fresh = n == 1
		return err
	})
	return fresh, err
}

// MarkStripeEventProcessed records a successfully handled event.
func (s *Server) MarkStripeEventProcessed(ctx context.Context, id, tenantID string) error {
	return s.system(ctx, false, db.Settings{}, func(c *Call) error {
		var t any
		if tenantID != "" {
			t = tenantID
		}
		_, err := c.q.Exec(ctx, `update stripe_events set processed_at = now(),
			tenant_id = coalesce(tenant_id, (select id from tenants where id = :t)) where id = :id`, db.Args{"id": id, "t": t})
		return err
	})
}

var lapsed = map[string]bool{"canceled": true, "unpaid": true, "incomplete_expired": true, "paused": true}

// ApplyStripeSubscription is the subscription sync: Stripe is the only
// writer of plan after a Tenant is created. A lapse drops a user-owned
// Tenant that fits Starter back to Starter; anything else becomes
// restricted. No end user ever loses access over billing.
func (s *Server) ApplyStripeSubscription(ctx context.Context, sub StripeSubscription) error {
	return s.system(ctx, false, db.Settings{}, func(c *Call) error {
		var t tenantRow
		err := db.One(ctx, c.q, &t, `select to_jsonb(t) from tenants t where id = :id or stripe_subscription_id = :sub
			order by (id = :id) desc limit 1`, db.Args{"id": sub.TenantID, "sub": sub.ID})
		if err != nil {
			return errf("stripe sync: no tenant for subscription %s: %w", sub.ID, err)
		}
		before := t
		next := t
		next.SubscriptionStatus = sub.Status
		next.PeriodStart, next.PeriodEnd, next.CancelAtPeriodEnd = sub.PeriodStart, sub.PeriodEnd, sub.CancelAtPeriodEnd
		subID := &sub.ID
		switch {
		case lapsed[sub.Status]:
			u, err := c.tenantUsage(t)
			if err != nil {
				return err
			}
			fitsStarter := false
			for _, p := range fitsPlans(t, u) {
				fitsStarter = fitsStarter || p == "starter"
			}
			if t.OwnerType == "user" && fitsStarter && t.Tier == "shared" {
				next.Plan, next.Restricted, subID = "starter", false, nil
				next.SubscriptionStatus = "canceled"
			} else {
				next.Restricted = true
			}
		default:
			if sub.Plan == "team" || sub.Plan == "enterprise" {
				next.Plan = sub.Plan
			}
			if sub.Status == "active" || sub.Status == "trialing" || sub.Status == "past_due" {
				next.Restricted = false
			}
		}
		if _, err := c.q.Exec(ctx, `update tenants set plan = :plan, subscription_status = :st, stripe_subscription_id = :sub,
				current_period_start = :ps::timestamptz, current_period_end = :pe::timestamptz,
				cancel_at_period_end = :cap, restricted = :r where id = :id`,
			db.Args{"plan": next.Plan, "st": next.SubscriptionStatus, "sub": db.NullString(subID),
				"ps": db.NullTime(next.PeriodStart), "pe": db.NullTime(next.PeriodEnd), "cap": next.CancelAtPeriodEnd,
				"r": next.Restricted, "id": t.ID}); err != nil {
			return err
		}
		if before.Plan != next.Plan {
			if err := c.audit(Audit{Action: "tenant.plan_changed", TargetType: "tenant", TargetID: t.ID, TenantID: &t.ID,
				Before: map[string]any{"plan": before.Plan}, After: map[string]any{"plan": next.Plan}}); err != nil {
				return err
			}
		}
		if before.SubscriptionStatus != next.SubscriptionStatus || before.Restricted != next.Restricted {
			if err := c.audit(Audit{Action: "tenant.subscription_status_changed", TargetType: "tenant", TargetID: t.ID,
				TenantID: &t.ID, Before: map[string]any{"subscription_status": before.SubscriptionStatus, "restricted": before.Restricted},
				After: map[string]any{"subscription_status": next.SubscriptionStatus, "restricted": next.Restricted}}); err != nil {
				return err
			}
		}
		return nil
	})
}

// UnprocessedStripeEvents lists recorded Stripe events still unprocessed
// five minutes after arrival, for the retry job.
func (s *Server) UnprocessedStripeEvents(ctx context.Context) ([]string, error) {
	var out []string
	err := s.system(ctx, false, db.Settings{}, func(c *Call) error {
		var err error
		out, err = db.All[string](ctx, c.q, `select to_jsonb(id) from stripe_events where processed_at is null
			and received_at < now() - interval '5 minutes' order by received_at limit 100`, nil)
		return err
	})
	return out, err
}

// SeatTarget is one paid Tenant's seat count for the daily seat sync.
type SeatTarget struct {
	TenantID       string
	SubscriptionID string
	Seats          int
	LastSynced     *int
}

// PaidTenantSeats lists every Team/Enterprise Tenant's current seat count.
func (s *Server) PaidTenantSeats(ctx context.Context) ([]SeatTarget, error) {
	var out []SeatTarget
	err := s.system(ctx, false, db.Settings{}, func(c *Call) error {
		ts, err := db.All[tenantRow](ctx, c.q, `select to_jsonb(t) from tenants t
			where plan in ('team','enterprise') and stripe_subscription_id is not null`, nil)
		if err != nil {
			return err
		}
		for _, t := range ts {
			n, err := c.seatCount(t.ID)
			if err != nil {
				return err
			}
			out = append(out, SeatTarget{TenantID: t.ID, SubscriptionID: deref(t.StripeSubID), Seats: n, LastSynced: t.SeatCountSynced})
		}
		return nil
	})
	return out, err
}

// RecordSeatSync stores the quantity last written to Stripe.
func (s *Server) RecordSeatSync(ctx context.Context, tenantID string, seats int) error {
	return s.system(ctx, false, db.Settings{}, func(c *Call) error {
		_, err := c.q.Exec(ctx, `update tenants set seat_count_synced = :n, seat_count_synced_at = now() where id = :t`,
			db.Args{"n": int64(seats), "t": tenantID})
		return err
	})
}

// TenantsMissingCustomer lists Tenants whose Stripe Customer creation failed.
func (s *Server) TenantsMissingCustomer(ctx context.Context) ([]string, error) {
	var out []string
	err := s.system(ctx, false, db.Settings{}, func(c *Call) error {
		var err error
		out, err = db.All[string](ctx, c.q, `select to_jsonb(id) from tenants where stripe_customer_id is null
			and created_at < now() - interval '1 minute' order by created_at limit 100`, nil)
		return err
	})
	return out, err
}

// ProvisionStripeCustomer is the retry path for one Tenant.
func (s *Server) ProvisionStripeCustomer(ctx context.Context, tenantID string) {
	s.provisionStripeCustomer(ctx, tenantID)
}

// ── Entitlement sweep (every 5 minutes) ────────────────────────────────

// SweepEntitlements expires rows past ends_at and fires access.granted
// for future-dated grants whose starts_at has arrived. Resolution already
// treats both as effective immediately; this makes the stored status and
// the events catch up.
func (s *Server) SweepEntitlements(ctx context.Context) (expired, started int, err error) {
	err = eachMode(func(tm bool) error {
		return s.system(ctx, tm, db.Settings{}, func(c *Call) error {
			rows, err := c.queryEnts(`e.status in ('active','disabled') and e.ends_at <= now()
				order by e.ends_at limit 500 for update skip locked`, db.Args{})
			if err != nil {
				return err
			}
			for _, e := range rows {
				pairs, err := c.affectedPairs(e)
				if err != nil {
					return err
				}
				// Resolution already treats this row as expired, so "before"
				// is resolved as of the instant before ends_at.
				now := c.now
				c.now = e.EndsAt.Add(-time.Nanosecond)
				watch, err := c.watchAccess(pairs)
				c.now = now
				if err != nil {
					return err
				}
				if _, err := c.q.Exec(ctx, `update entitlements set status = 'expired' where id = :id`, db.Args{"id": e.ID}); err != nil {
					return err
				}
				after, err := c.loadEnt(e.ID)
				if err != nil {
					return err
				}
				if err := c.entitlementEffects("entitlement.expired", &e, after, nil); err != nil {
					return err
				}
				reason := "entitlement_expired"
				if e.OrganizationID != nil {
					reason = "org_grant_changed"
				}
				if e.Status == "active" {
					if err := c.flip(watch, "entitlement_enabled", reason); err != nil {
						return err
					}
				}
				expired++
			}
			starts, err := c.queryEnts(`e.status = 'active' and e.starts_at <= now() and e.start_notified_at is null
				and e.starts_at > e.created_at order by e.starts_at limit 500 for update skip locked`, db.Args{})
			if err != nil {
				return err
			}
			for _, e := range starts {
				pairs, err := c.affectedPairs(e)
				if err != nil {
					return err
				}
				res, err := c.resolvePairs(pairs)
				if err != nil {
					return err
				}
				for _, p := range pairs {
					r := res[p]
					if r.Allowed && r.Res.ActiveVia != nil && r.Res.ActiveVia.EntitlementID == e.ID {
						app := p.App
						if err := c.emit("access.granted", &app, map[string]any{"user_id": p.User, "application_id": p.App,
							"reason": "entitlement_started", "entitlement_status": "active"}); err != nil {
							return err
						}
					}
				}
				if _, err := c.q.Exec(ctx, `update entitlements set start_notified_at = now() where id = :id`, db.Args{"id": e.ID}); err != nil {
					return err
				}
				started++
			}
			return nil
		})
	})
	return
}

// ── Erasure cascade (NFR → Hard-delete cascade) ────────────────────────

// RunErasures runs every scheduled erasure whose 7 days have passed. Each
// step is idempotent, and each step commits before the next, so a failed
// run resumes safely from the start.
func (s *Server) RunErasures(ctx context.Context) (int, error) {
	done := 0
	err := eachMode(func(tm bool) error {
		var due []string
		if err := s.system(ctx, tm, db.Settings{}, func(c *Call) error {
			var err error
			due, err = db.All[string](ctx, c.q, `select to_jsonb(e.user_id) from erasure_requests e
				join users u on u.id = e.user_id
				where e.status = 'scheduled' and e.scheduled_for <= now() order by e.scheduled_for limit 50`, nil)
			return err
		}); err != nil {
			return err
		}
		for _, uid := range due {
			if err := s.erase(ctx, tm, uid); err != nil {
				return errf("erasure %s: %w", uid, err)
			}
			done++
		}
		return nil
	})
	return done, err
}

func (s *Server) erase(ctx context.Context, tm bool, uid string) error {
	steps := []struct {
		settings db.Settings
		fn       func(c *Call) error
	}{
		{db.Settings{}, func(c *Call) error { // 1. credentials go, rows stay
			if _, err := c.q.Exec(ctx, `delete from mfa_recovery_codes where user_identity_id in
				(select id from user_identities where user_id = :u)`, db.Args{"u": uid}); err != nil {
				return err
			}
			_, err := c.q.Exec(ctx, `update user_identities set password_hash = null, mfa_secret = null, mfa_pending_secret = null,
				external_subject_id = null, mfa_enabled = false where user_id = :u`, db.Args{"u": uid})
			return err
		}},
		{db.Settings{}, func(c *Call) error { // 2. profile, sessions, tokens
			for _, q := range []string{
				`delete from profiles where user_id = :u`,
				`delete from refresh_tokens where session_id in (select id from sessions where user_id = :u)`,
				`delete from sessions where user_id = :u`,
				`delete from auth_tokens where user_id = :u`,
			} {
				if _, err := c.q.Exec(ctx, q, db.Args{"u": uid}); err != nil {
					return err
				}
			}
			return nil
		}},
		{db.Settings{}, func(c *Call) error { // 3. AppProfile fully, AppSettings x-pii keys
			if _, err := c.q.Exec(ctx, `update app_profiles set display_handle = null, custom = '{}' where user_id = :u`,
				db.Args{"u": uid}); err != nil {
				return err
			}
			type row struct {
				App    string          `json:"application_id"`
				Schema json.RawMessage `json:"settings_schema"`
			}
			rows, err := db.All[row](ctx, c.q, `select jsonb_build_object('application_id', s.application_id,
				'settings_schema', a.settings_schema) from app_settings s join applications a on a.id = s.application_id
				where s.user_id = :u`, db.Args{"u": uid})
			if err != nil {
				return err
			}
			for _, r := range rows {
				sch, _, _ := schema.Parse(r.Schema)
				if pii := sch.PIIKeys(); len(pii) > 0 {
					if _, err := c.q.Exec(ctx, `update app_settings set overrides = overrides - :keys::text[]
						where user_id = :u and application_id = :a`,
						db.Args{"keys": db.TextArray(pii), "u": uid, "a": r.App}); err != nil {
						return err
					}
				}
			}
			return nil
		}},
		{db.Settings{AuditMaintenance: true}, func(c *Call) error { // 4. redact snapshots, keep the shape
			_, err := c.q.Exec(ctx, `update audit_events set before = null, after = null
				where target_user_id = :u and (before is not null or after is not null)`, db.Args{"u": uid})
			return err
		}},
		{db.Settings{}, func(c *Call) error { // 5. memberships, overrides, email
			orgs, err := db.All[string](ctx, c.q, `select to_jsonb(organization_id) from organization_memberships
				where user_id = :u and role = 'org_admin' and status = 'active'`, db.Args{"u": uid})
			if err != nil {
				return err
			}
			if _, err := c.q.Exec(ctx, `delete from organization_memberships where user_id = :u`, db.Args{"u": uid}); err != nil {
				return err
			}
			for _, org := range orgs {
				// Never leave an Organization unmanageable: promote its
				// longest-standing active member if it lost its last admin.
				if _, err := c.q.Exec(ctx, `update organization_memberships set role = 'org_admin'
					where organization_id = :o and user_id = (select user_id from organization_memberships
						where organization_id = :o and status = 'active' order by joined_at, user_id limit 1)
					and not exists (select 1 from organization_memberships where organization_id = :o
						and role = 'org_admin' and status = 'active')`, db.Args{"o": org}); err != nil {
					return err
				}
			}
			if _, err := c.q.Exec(ctx, `update entitlements set member_overrides = array_remove(member_overrides, :u)
				where :u = any(member_overrides)`, db.Args{"u": uid}); err != nil {
				return err
			}
			_, err = c.q.Exec(ctx, `update users set email = 'erased+' || id || '@invalid.substratal', pending_email = null,
				status = 'deleted', deleted_at = coalesce(deleted_at, now()) where id = :u`, db.Args{"u": uid})
			return err
		}},
		// 6. Entitlement rows stay, pseudonymized by the steps above.
		{db.Settings{}, func(c *Call) error { // 7. complete
			if _, err := c.q.Exec(ctx, `update erasure_requests set status = 'completed', completed_at = now()
				where user_id = :u and status = 'scheduled'`, db.Args{"u": uid}); err != nil {
				return err
			}
			return c.audit(Audit{Action: "user.erased", TargetType: "user", TargetID: uid, TargetUserID: &uid,
				ActorType: "system", ActorID: "system"})
		}},
	}
	for _, st := range steps {
		if err := s.system(ctx, tm, st.settings, st.fn); err != nil {
			return err
		}
	}
	return nil
}

// ── Audit archival (NFR → Audit log lifecycle) ─────────────────────────

// ArchivedEvent is the shape-only record kept in cold storage forever:
// every field but the before/after snapshot.
type ArchivedEvent struct {
	ID             string    `json:"id"`
	Action         string    `json:"action"`
	ActorType      string    `json:"actor_type"`
	ActorID        string    `json:"actor_id"`
	TargetType     string    `json:"target_type"`
	TargetID       string    `json:"target_id"`
	TargetUserID   *string   `json:"target_user_id"`
	ApplicationID  *string   `json:"application_id"`
	OrganizationID *string   `json:"organization_id"`
	TenantID       *string   `json:"tenant_id"`
	RequestID      *string   `json:"request_id"`
	TestMode       bool      `json:"test_mode"`
	Timestamp      time.Time `json:"timestamp"`
}

// Archiver writes a batch of shape-only events to cold storage (S3 Glacier
// Deep Archive in AWS; a local directory otherwise).
type Archiver func(ctx context.Context, batch []ArchivedEvent) error

// ArchiveAudit moves events older than their Tenant's hot window to cold
// storage, shape only, and deletes them from the hot table. Events with no
// Tenant use the longest non-negotiated window (Team's 1 year); Enterprise
// windows are negotiated and kept until set per contract.
func (s *Server) ArchiveAudit(ctx context.Context, archive Archiver) (int, error) {
	total := 0
	err := eachMode(func(tm bool) error {
		for {
			var batch []ArchivedEvent
			err := s.system(ctx, tm, db.Settings{AuditMaintenance: true}, func(c *Call) error {
				var err error
				batch, err = db.All[ArchivedEvent](ctx, c.q, `select to_jsonb(a) - 'before' - 'after' from audit_events a
					left join tenants t on t.id = a.tenant_id
					where a.timestamp < now() - (case coalesce(t.plan, 'team')
						when 'starter' then interval '30 days' when 'team' then interval '365 days'
						else interval '100 years' end)
					order by a.timestamp limit 1000 for update of a skip locked`, nil)
				if err != nil || len(batch) == 0 {
					return err
				}
				if err := archive(ctx, batch); err != nil {
					return err
				}
				ids := make([]string, len(batch))
				for i, e := range batch {
					ids[i] = e.ID
				}
				_, err = c.q.Exec(ctx, `delete from audit_events where id = any(:ids::text[])`, db.Args{"ids": db.TextArray(ids)})
				return err
			})
			if err != nil {
				return err
			}
			total += len(batch)
			if len(batch) < 1000 {
				return nil
			}
		}
	})
	return total, err
}

// ── Cleanup (nightly) ──────────────────────────────────────────────────

// Cleanup removes expired bookkeeping rows and 30-day-old test data.
func (s *Server) Cleanup(ctx context.Context) error {
	if err := s.system(ctx, false, db.Settings{AllModes: true}, func(c *Call) error {
		for _, q := range []string{
			`delete from auth_tokens where expires_at < now() - interval '1 day'`,
			`delete from idempotency_keys where expires_at < now()`,
			`delete from rate_limit_counters where window_start < now() - interval '2 days'`,
			`delete from refresh_tokens where session_id in (select id from sessions
				where coalesce(revoked_at, least(idle_expires_at, absolute_expires_at)) < now() - interval '30 days')`,
			`delete from sessions where coalesce(revoked_at, least(idle_expires_at, absolute_expires_at)) < now() - interval '30 days'`,
			`delete from webhook_deliveries where created_at < now() - interval '30 days'`,
			`delete from webhook_events e where created_at < now() - interval '30 days'
				and not exists (select 1 from webhook_deliveries d where d.event_id = e.id)`,
			`delete from stripe_events where received_at < now() - interval '90 days'`,
		} {
			if _, err := c.q.Exec(ctx, q, nil); err != nil {
				return errf("cleanup %q: %w", strings.Fields(q)[2], err)
			}
		}
		return nil
	}); err != nil {
		return err
	}
	return s.PurgeTestData(ctx)
}

// PurgeTestData deletes test-mode rows older than 30 days (API Keys →
// Test vs. live), children first.
func (s *Server) PurgeTestData(ctx context.Context) error {
	return s.system(ctx, true, db.Settings{AuditMaintenance: true}, func(c *Call) error {
		old := `test_mode and created_at < now() - interval '30 days'`
		users := `(select id from users where ` + old + `)`
		for _, q := range []string{
			`delete from webhook_deliveries where ` + old,
			`delete from webhook_events where ` + old,
			`delete from webhook_subscriptions where ` + old,
			`delete from audit_events where test_mode and timestamp < now() - interval '30 days'`,
			`delete from app_settings where ` + old,
			`delete from app_profiles where ` + old,
			`delete from user_role_assignments where test_mode and assigned_at < now() - interval '30 days'`,
			`delete from entitlements where ` + old,
			`delete from organization_memberships where test_mode and invited_at < now() - interval '30 days'`,
			`delete from roles where ` + old + ` and not exists (select 1 from user_role_assignments a where a.role_id = roles.id)`,
			`delete from api_keys where ` + old,
			`delete from mfa_recovery_codes where user_identity_id in (select id from user_identities where user_id in ` + users + `)`,
			`delete from user_identities where user_id in ` + users,
			`delete from refresh_tokens where session_id in (select id from sessions where user_id in ` + users + `)`,
			`delete from sessions where user_id in ` + users,
			`delete from auth_tokens where user_id in ` + users,
			`delete from erasure_requests where user_id in ` + users,
			`delete from profiles where user_id in ` + users,
			`delete from settings where user_id in ` + users,
		} {
			if _, err := c.q.Exec(ctx, q, nil); err != nil {
				return err
			}
		}
		// Users and Organizations last, only once nothing references them.
		for _, q := range []string{
			`delete from organizations o where ` + old + ` and not exists (select 1 from organization_memberships m where m.organization_id = o.id)
				and not exists (select 1 from entitlements e where e.organization_id = o.id)
				and not exists (select 1 from applications a where a.owner_organization_id = o.id)
				and not exists (select 1 from tenants t where t.owner_organization_id = o.id)`,
			`delete from users u where ` + old + ` and not exists (select 1 from organization_memberships m where m.user_id = u.id)
				and not exists (select 1 from entitlements e where e.user_id = u.id)
				and not exists (select 1 from applications a where a.owner_user_id = u.id)
				and not exists (select 1 from tenants t where t.owner_user_id = u.id)
				and not exists (select 1 from organizations o where o.created_by = u.id)`,
		} {
			if _, err := c.q.Exec(ctx, q, nil); err != nil {
				return err
			}
		}
		return nil
	})
}

// RebuildAllProjections rebuilds every Application's typed settings view
// (nightly safety net for a rebuild that failed after a schema change).
func (s *Server) RebuildAllProjections(ctx context.Context) error {
	var apps []string
	if err := s.system(ctx, false, db.Settings{}, func(c *Call) error {
		var err error
		apps, err = db.All[string](ctx, c.q, `select to_jsonb(id) from applications order by id`, nil)
		return err
	}); err != nil {
		return err
	}
	for _, a := range apps {
		if err := RebuildSettingsProjection(ctx, s.DB, a); err != nil {
			return err
		}
	}
	return nil
}

var _ = httpx.ErrInternal
