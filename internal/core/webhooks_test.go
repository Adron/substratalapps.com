package core_test

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Adron/substratalapps.com/internal/db"
	"github.com/Adron/substratalapps.com/internal/testenv"
)

type receiver struct {
	mu     sync.Mutex
	got    []map[string]any
	sigs   []string
	status int
	srv    *httptest.Server
	raw    [][]byte
}

func newReceiver(t *testing.T) *receiver {
	r := &receiver{status: 200}
	r.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		b, _ := io.ReadAll(req.Body)
		var m map[string]any
		_ = json.Unmarshal(b, &m)
		r.mu.Lock()
		r.got = append(r.got, m)
		r.raw = append(r.raw, b)
		r.sigs = append(r.sigs, req.Header.Get("Substratal-Signature"))
		st := r.status
		r.mu.Unlock()
		w.WriteHeader(st)
	}))
	t.Cleanup(r.srv.Close)
	return r
}

func (r *receiver) types() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []string
	for _, m := range r.got {
		out = append(out, m["type"].(string))
	}
	return out
}

// verify checks the newest delivery's signature as the docs tell receivers to.
func verify(t *testing.T, raw []byte, header string, secrets ...string) bool {
	var ts string
	var sigs []string
	for _, p := range strings.Split(header, ",") {
		k, v, _ := strings.Cut(p, "=")
		if k == "t" {
			ts = v
		} else if k == "v1" {
			sigs = append(sigs, v)
		}
	}
	for _, s := range secrets {
		mac := hmac.New(sha256.New, []byte(s))
		mac.Write([]byte(ts + "."))
		mac.Write(raw)
		want := hex.EncodeToString(mac.Sum(nil))
		for _, got := range sigs {
			if hmac.Equal([]byte(got), []byte(want)) {
				return true
			}
		}
	}
	return false
}

func TestWebhookDelivery(t *testing.T) {
	e := testenv.New(t)
	rcv := newReceiver(t)
	app := e.App(system, nil)
	sub := e.Post("/v1/webhooks", e.Admin, map[string]any{"scope": app, "url": rcv.srv.URL,
		"events": []string{"access.granted", "access.revoked", "role.removed"}}).Expect(201)
	secret := sub.Str("signing_secret")
	if !strings.HasPrefix(secret, "whsec_") {
		t.Fatalf("secret = %s", secret)
	}
	if e.Get("/v1/webhooks/"+sub.Str("id"), e.Admin).Str("signing_secret") != "" {
		t.Fatal("a GET must never return the signing secret")
	}
	e.Post("/v1/webhooks", e.Admin, map[string]any{"scope": app, "url": rcv.srv.URL, "events": []string{"nope"}}).
		ExpectErr(422, "unknown_event_type")

	// A grant fires access.granted to the app's subscription, signed.
	u := e.Signup()
	ent := e.Grant(e.Admin, u.ID, app)
	e.Deliver()
	if got := rcv.types(); len(got) != 1 || got[0] != "access.granted" {
		t.Fatalf("delivered = %v", got)
	}
	if !verify(t, rcv.raw[0], rcv.sigs[0], secret) {
		t.Fatal("signature doesn't verify")
	}
	env := rcv.got[0]
	if env["api_version"] != "2026-10-05" || env["data"].(map[string]any)["reason"] != "entitlement_granted" {
		t.Fatalf("envelope = %v", env)
	}

	// Rotation: both secrets sign for 24h.
	rot := e.Post("/v1/webhooks/"+sub.Str("id")+"/rotate-secret", e.Admin, map[string]any{}).Expect(200)
	e.Patch("/v1/entitlements/"+ent, e.Admin, map[string]any{"status": "disabled", "disabled_reason": "test"}).Expect(200)
	e.Deliver()
	last := len(rcv.raw) - 1
	if rcv.types()[last] != "access.revoked" || !verify(t, rcv.raw[last], rcv.sigs[last], secret) ||
		!verify(t, rcv.raw[last], rcv.sigs[last], rot.Str("signing_secret")) {
		t.Fatalf("rotation signing: %s", rcv.sigs[last])
	}

	// Another app's events never arrive.
	other := e.App(system, nil)
	e.Grant(e.Admin, u.ID, other)
	e.Deliver()
	for _, m := range rcv.got {
		if m["application_id"] == other {
			t.Fatal("received another Application's event")
		}
	}

	// Failure schedules a retry; 410 disables the subscription.
	rcv.mu.Lock()
	rcv.status = 500
	rcv.mu.Unlock()
	e.Patch("/v1/entitlements/"+ent, e.Admin, map[string]any{"status": "active"}).Expect(200)
	e.Deliver()
	dl := e.Get("/v1/webhooks/"+sub.Str("id")+"/deliveries?status=failed", e.Admin).Expect(200).JSON()["data"].([]any)
	if len(dl) == 0 || dl[0].(map[string]any)["next_retry_at"] == nil {
		t.Fatalf("failed delivery should carry next_retry_at: %v", dl)
	}
	// Force the retry due, then answer 410.
	_ = e.DB.Tx(context.Background(), db.Settings{AllModes: true}, func(q db.Querier) error {
		_, err := q.Exec(context.Background(), `update webhook_deliveries set next_retry_at = now() where status = 'pending'`, nil)
		return err
	})
	rcv.mu.Lock()
	rcv.status = 410
	rcv.mu.Unlock()
	e.Deliver()
	if e.Get("/v1/webhooks/"+sub.Str("id"), e.Admin).Str("status") != "disabled" {
		t.Fatal("410 should disable the subscription")
	}
	// A test event still goes to a disabled subscription.
	rcv.mu.Lock()
	rcv.status = 200
	rcv.mu.Unlock()
	n := len(rcv.got)
	e.Post("/v1/webhooks/"+sub.Str("id")+"/test", e.Admin, map[string]any{}).Expect(202)
	e.Deliver()
	if len(rcv.got) != n+1 || rcv.types()[n] != "webhook.test" {
		t.Fatalf("test event = %v", rcv.types())
	}
	e.Patch("/v1/webhooks/"+sub.Str("id"), e.Admin, map[string]any{"status": "unhealthy"}).ExpectErr(409, "invalid_status_transition")
	e.Patch("/v1/webhooks/"+sub.Str("id"), e.Admin, map[string]any{"status": "healthy"}).Expect(200)
	// Starter plan limit: Substratal's Tenant is Enterprise, so use a Starter owner.
	owner := e.Signup()
	ownApp := e.App(owner.ID, nil)
	e.Post("/v1/webhooks", owner.Token, map[string]any{"scope": ownApp, "url": rcv.srv.URL, "events": []string{"*"}}).Expect(201)
	e.Post("/v1/webhooks", owner.Token, map[string]any{"scope": ownApp, "url": rcv.srv.URL, "events": []string{"*"}}).
		ExpectErr(409, "plan_limit_reached")
	e.Post("/v1/webhooks", owner.Token, map[string]any{"scope": "platform", "url": rcv.srv.URL, "events": []string{"*"}}).
		ExpectErr(403, "forbidden")
}

func TestUserLifecycle(t *testing.T) {
	e := testenv.New(t)
	u := e.Signup()
	app := e.App(system, nil)
	e.Grant(e.Admin, u.ID, app)
	e.Post("/v1/users/"+u.ID+"/suspend", e.Admin, map[string]any{"reason": "SUP-1"}).Expect(200)
	e.Get("/v1/users/me", u.Token).ExpectErr(401, "session_revoked")
	if accessEventsFor(e, "access.revoked", u.ID, app) != 1 {
		t.Fatal("suspension should fire access.revoked")
	}
	e.Post("/v1/auth/login", "", map[string]any{"email": u.Email, "password": u.Password}).ExpectErr(403, "account_suspended")
	e.Patch("/v1/users/"+u.ID, e.Admin, map[string]any{"status": "active"}).Expect(200)
	if accessEventsFor(e, "access.granted", u.ID, app) != 2 {
		t.Fatal("reactivation should fire access.granted")
	}
	s := e.Login(u.Email, u.Password)
	e.Patch("/v1/users/me", s.Token(), map[string]any{"status": "suspended"}).ExpectErr(403, "status_change_forbidden")
	// Self-service email change is pending until verified.
	newAddr := "changed+" + testenv.Unique() + "@example.com"
	pend := e.Patch("/v1/users/me", s.Token(), map[string]any{"email": newAddr}).Expect(200)
	if pend.Str("email") != u.Email || pend.Str("pending_email") != newAddr {
		t.Fatalf("pending = %s", pend.Body)
	}
	e.Post("/v1/auth/email/verify", "", map[string]any{"token": e.Token(newAddr, "email_verify")}).Expect(204)
	if e.Get("/v1/users/me", s.Token()).Str("email") != newAddr {
		t.Fatal("email change should complete on verify")
	}
	// Export, then soft-delete, then erasure.
	ex := e.Get("/v1/users/me/export", s.Token()).Expect(200).JSON()
	for _, k := range []string{"user", "profile", "settings", "identities", "sessions", "roles", "applications", "audit_events"} {
		if _, ok := ex[k]; !ok {
			t.Fatalf("export missing %s", k)
		}
	}
	e.Post("/v1/users/me/erasure-requests", s.Token(), map[string]any{"reason": "please"}).Expect(202)
	e.Get("/v1/users/"+u.ID, e.Admin).Expect(200) // users.manage still sees it
	other := e.Signup()
	e.Get("/v1/users/"+u.ID, other.Token).ExpectErr(404, "user_not_found")
	e.Patch("/v1/users/"+u.ID, e.Admin, map[string]any{"status": "active"}).ExpectErr(409, "erasure_scheduled")
	// Run the cascade now.
	_ = e.DB.Tx(context.Background(), db.Settings{AllModes: true}, func(q db.Querier) error {
		_, err := q.Exec(context.Background(), `update erasure_requests set scheduled_for = now() where user_id = :u`, db.Args{"u": u.ID})
		return err
	})
	if n, err := e.Core.RunErasures(context.Background()); err != nil || n != 1 {
		t.Fatalf("erasures = %d, %v", n, err)
	}
	gone := e.Get("/v1/users/"+u.ID, e.Admin).Expect(200)
	if !strings.HasPrefix(gone.Str("email"), "erased+") {
		t.Fatalf("erased user = %s", gone.Body)
	}
	ev := e.Get("/v1/audit-events?target_user_id="+u.ID+"&action=entitlement.granted", e.Admin).Expect(200).JSON()["data"].([]any)
	if len(ev) != 1 || ev[0].(map[string]any)["after"] != nil {
		t.Fatalf("snapshots should be redacted, shape kept: %v", ev)
	}
	e.Patch("/v1/users/"+u.ID, e.Admin, map[string]any{"status": "active"}).ExpectErr(404, "user_not_found")
}

func TestAuditVisibility(t *testing.T) {
	e := testenv.New(t)
	owner := e.Signup()
	app := e.App(owner.ID, nil)
	u := e.Signup()
	e.Grant(e.Admin, u.ID, app)
	e.Get("/v1/audit-events", u.Token).ExpectErr(403, "forbidden")
	mine := e.Get("/v1/audit-events?target_user_id=me", u.Token).Expect(200).JSON()["data"].([]any)
	if len(mine) == 0 {
		t.Fatal("a user can read their own trail")
	}
	tenant := e.Get("/v1/applications/"+app, e.Admin).Str("tenant_id")
	own := e.Get("/v1/audit-events?tenant_id="+tenant, owner.Token).Expect(200)
	if len(own.JSON()["data"].([]any)) == 0 || own.Header.Get("Audit-Hot-Window-Start") == "" {
		t.Fatalf("tenant owner view = %s", own.Body)
	}
	e.Get("/v1/audit-events?tenant_id="+tenant, u.Token).ExpectErr(403, "forbidden")
	id := mine[0].(map[string]any)["id"].(string)
	e.Get("/v1/audit-events/"+id, u.Token).Expect(200)
	e.Get("/v1/audit-events/"+id, owner.Token).Expect(200) // the Tenant owner's view of their own app
	stranger := e.Signup()
	e.Get("/v1/audit-events/"+id, stranger.Token).ExpectErr(404, "audit_event_not_found")
}

func TestTenancyAndUsage(t *testing.T) {
	e := testenv.New(t)
	owner := e.Signup()
	app := e.App(owner.ID, nil)
	ts := e.Get("/v1/tenants", owner.Token).Expect(200).JSON()["data"].([]any)
	if len(ts) != 1 {
		t.Fatalf("owner tenants = %v", ts)
	}
	tid := ts[0].(map[string]any)["id"].(string)
	if ts[0].(map[string]any)["plan"] != "starter" || ts[0].(map[string]any)["application_count"].(float64) != 1 {
		t.Fatalf("tenant = %v", ts[0])
	}
	u := e.Signup()
	e.Grant(owner.Token, u.ID, app) // the owner grants with their own User token
	usage := e.Get("/v1/tenants/"+tid+"/usage", owner.Token).Expect(200).JSON()
	limits := usage["limits"].(map[string]any)
	if limits["seats"].(map[string]any)["current"].(float64) != 1 || limits["applications"].(map[string]any)["limit"].(float64) != 1 {
		t.Fatalf("usage = %v", usage)
	}
	sub := e.Get("/v1/tenants/"+tid+"/subscription", owner.Token).Expect(200).JSON()
	if sub["subscription_status"] != "none" || sub["currency"] != "usd" {
		t.Fatalf("subscription = %v", sub)
	}
	e.Get("/v1/tenants/"+tid, u.Token).ExpectErr(404, "tenant_not_found")
	// Checkout with no Stripe configured is billing_unavailable; Enterprise needs sales.
	e.PostIdem("/v1/tenants/"+tid+"/billing/checkout-sessions", owner.Token, testenv.Unique(),
		map[string]any{"plan": "enterprise", "success_url": "https://x.example.com", "cancel_url": "https://x.example.com"}).
		ExpectErr(422, "plan_requires_sales")
	e.PostIdem("/v1/tenants/"+tid+"/billing/checkout-sessions", owner.Token, testenv.Unique(),
		map[string]any{"plan": "team", "success_url": "https://x.example.com", "cancel_url": "https://x.example.com"}).
		ExpectErr(503, "billing_unavailable")
	e.Post("/v1/tenants/"+tid+"/billing/portal-sessions", owner.Token, map[string]any{"return_url": "https://x.example.com"}).
		ExpectErr(409, "no_billing_account")
	// Tier changes: tenants.manage only, Enterprise only, up the ladder only.
	e.Post("/v1/tenants/"+tid+"/tier-change-requests", owner.Token, map[string]any{"requested_tier": "isolated", "reason": "x"}).
		ExpectErr(403, "forbidden")
	e.Post("/v1/tenants/"+tid+"/tier-change-requests", e.Admin, map[string]any{"requested_tier": "isolated", "reason": "x"}).
		ExpectErr(409, "plan_does_not_allow_tier")
	sys := "tnt_01JAG1SYSTEM00000000000000"
	e.Post("/v1/tenants/"+sys+"/tier-change-requests", e.Admin, map[string]any{"requested_tier": "dedicated_region",
		"requested_region": "mars-1", "reason": "x"}).ExpectErr(422, "unsupported_region")
	req := e.Post("/v1/tenants/"+sys+"/tier-change-requests", e.Admin, map[string]any{"requested_tier": "isolated", "reason": "contract"}).Expect(201)
	rp := "/v1/tenants/" + sys + "/tier-change-requests/" + req.Str("id")
	e.Post("/v1/tenants/"+sys+"/tier-change-requests", e.Admin, map[string]any{"requested_tier": "isolated", "reason": "again"}).
		ExpectErr(409, "tier_change_already_pending")
	e.Patch(rp, e.Admin, map[string]any{"status": "completed"}).ExpectErr(409, "invalid_request_transition")
	e.Patch(rp, e.Admin, map[string]any{"status": "in_progress"}).Expect(200)
	if e.Get("/v1/tenants/"+sys, e.Admin).Str("status") != "migrating" {
		t.Fatal("in_progress should mark the Tenant migrating")
	}
	// Writes to a migrating Tenant's data are paused.
	e.Post("/v1/applications", e.Admin, map[string]any{"slug": "mig-" + testenv.Unique(), "name": "x",
		"launch_url": "https://x.example.com", "owner_user_id": system}).ExpectErr(503, "service_unavailable")
	e.Patch(rp, e.Admin, map[string]any{"status": "cancelled"}).ExpectErr(422, "validation_failed")
	e.Patch(rp, e.Admin, map[string]any{"status": "cancelled", "notes": "rollback"}).Expect(200)
	if e.Get("/v1/tenants/"+sys, e.Admin).Str("status") != "active" {
		t.Fatal("cancel should restore the Tenant")
	}
}

func TestPagination(t *testing.T) {
	e := testenv.New(t)
	for i := 0; i < 5; i++ {
		e.Signup()
	}
	seen := map[string]bool{}
	cursor := ""
	pages := 0
	for {
		path := "/v1/users?limit=2"
		if cursor != "" {
			path += "&cursor=" + cursor
		}
		page := e.Get(path, e.Admin).Expect(200).JSON()
		for _, u := range page["data"].([]any) {
			id := u.(map[string]any)["id"].(string)
			if seen[id] {
				t.Fatalf("duplicate %s across pages", id)
			}
			seen[id] = true
		}
		pages++
		p := page["page"].(map[string]any)
		if p["has_more"] != true {
			if p["next_cursor"] != nil {
				t.Fatal("next_cursor must be null when has_more is false")
			}
			break
		}
		cursor = p["next_cursor"].(string)
	}
	if len(seen) < 6 || pages < 3 {
		t.Fatalf("seen %d users across %d pages", len(seen), pages)
	}
	e.Get("/v1/users?limit=2&cursor=garbage", e.Admin).ExpectErr(400, "invalid_cursor")
	first := e.Get("/v1/users?limit=1", e.Admin).JSON()["page"].(map[string]any)["next_cursor"].(string)
	e.Get("/v1/users?limit=1&status=active&cursor="+first, e.Admin).ExpectErr(400, "invalid_cursor")
}

func TestRateLimits(t *testing.T) {
	e := testenv.NewWith(t, testenv.Options{RateLimits: true})
	var last *testenv.Resp
	for i := 0; i < 11; i++ {
		last = e.Post("/v1/auth/login", "", map[string]any{"email": "rl+" + testenv.Unique() + "@example.com", "password": "whatever passphrase"})
	}
	last.ExpectErr(429, "rate_limited")
	if last.Header.Get("Retry-After") == "" || last.Header.Get("RateLimit-Limit") != "10" {
		t.Fatalf("headers = %v", last.Header)
	}
	ok := e.Get("/v1/users/me", e.Admin).Expect(200)
	if ok.Header.Get("RateLimit-Remaining") == "" {
		t.Fatal("every response carries RateLimit headers")
	}
}

func TestSweepExpiresAndStarts(t *testing.T) {
	e := testenv.New(t)
	u := e.Signup()
	app := e.App(system, nil)
	ends := time.Now().Add(time.Second).UTC().Format(time.RFC3339Nano)
	id := e.PostIdem("/v1/users/"+u.ID+"/entitlements", e.Admin, testenv.Unique(),
		map[string]any{"application_id": app, "source": "trial", "ends_at": ends}).Expect(201).Str("id")
	time.Sleep(1200 * time.Millisecond)
	if n, _, err := e.Core.SweepEntitlements(context.Background()); err != nil || n != 1 {
		t.Fatalf("expired = %d, %v", n, err)
	}
	if e.Get("/v1/entitlements/"+id, e.Admin).Str("status") != "expired" {
		t.Fatal("sweep should expire the trial")
	}
	if accessEventsFor(e, "access.revoked", u.ID, app) != 1 || len(e.Events("entitlement.expired")) != 1 {
		t.Fatal("expiry should fire entitlement.expired and access.revoked")
	}
	// Renewal needs a future ends_at.
	e.Patch("/v1/entitlements/"+id, e.Admin, map[string]any{"status": "active"}).ExpectErr(409, "invalid_status_transition")
	e.Patch("/v1/entitlements/"+id, e.Admin, map[string]any{"status": "active",
		"ends_at": time.Now().Add(240 * time.Hour).UTC().Format(time.RFC3339)}).Expect(200)
	if err := e.Core.Cleanup(context.Background()); err != nil {
		t.Fatalf("cleanup: %v", err)
	}
}
