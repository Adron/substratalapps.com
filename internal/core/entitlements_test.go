package core_test

import (
	"testing"
	"time"

	"github.com/Adron/substratalapps.com/internal/testenv"
)

const system = "usr_01JAG0SYSTEM00000000000000"

func TestEntitlementLifecycle(t *testing.T) {
	e := testenv.New(t)
	u := e.Signup()
	app := e.App(system, nil)
	path := "/v1/users/" + u.ID + "/entitlements"

	e.Post(path, e.Admin, map[string]any{"application_id": app, "source": "admin_grant"}).ExpectErr(400, "idempotency_key_required")
	e.PostIdem(path, e.Admin, testenv.Unique(), map[string]any{"application_id": app, "source": "org_seat"}).ExpectErr(422, "invalid_source")
	e.PostIdem(path, e.Admin, testenv.Unique(), map[string]any{"application_id": app, "source": "trial"}).ExpectErr(422, "ends_at_required")

	// Idempotency: same key + body replays; same key + different body conflicts.
	key := testenv.Unique()
	body := map[string]any{"application_id": app, "source": "purchase", "order_id": "sub_" + testenv.Unique()}
	first := e.PostIdem(path, e.Admin, key, body).Expect(201)
	again := e.PostIdem(path, e.Admin, key, body).Expect(201)
	if again.Str("id") != first.Str("id") || again.Header.Get("Idempotent-Replayed") != "true" {
		t.Fatalf("replay = %s (%v)", again.Body, again.Header)
	}
	e.PostIdem(path, e.Admin, key, map[string]any{"application_id": app, "source": "admin_grant"}).ExpectErr(409, "idempotency_key_reused")
	// One live personal row per (user, app).
	dup := e.PostIdem(path, e.Admin, testenv.Unique(), map[string]any{"application_id": app, "source": "admin_grant"}).
		ExpectErr(409, "entitlement_already_exists")
	if dup.JSON()["error"].(map[string]any)["details"].(map[string]any)["existing_entitlement_id"] != first.Str("id") {
		t.Fatalf("details = %s", dup.Body)
	}

	id := first.Str("id")
	ep := "/v1/entitlements/" + id
	// ETag / If-Match.
	g := e.Get(ep, e.Admin).Expect(200)
	etag := g.Header.Get("ETag")
	e.Do(testenv.Req{Method: "PATCH", Path: ep, Token: e.Admin, Body: map[string]any{"status": "disabled"},
		Header: map[string]string{"If-Match": etag}}).ExpectErr(422, "disabled_reason_required")
	e.Do(testenv.Req{Method: "PATCH", Path: ep, Token: e.Admin, Body: map[string]any{"status": "disabled", "disabled_reason": "billing_dispute"},
		Header: map[string]string{"If-Match": `W/"999"`}}).ExpectErr(409, "version_conflict")
	off := e.Do(testenv.Req{Method: "PATCH", Path: ep, Token: e.Admin, Body: map[string]any{"status": "disabled", "disabled_reason": "billing_dispute"},
		Header: map[string]string{"If-Match": etag}}).Expect(200)
	if off.Str("disabled_reason") != "billing_dispute" {
		t.Fatalf("disabled = %s", off.Body)
	}
	on := e.Patch(ep, e.Admin, map[string]any{"status": "active"}).Expect(200)
	if on.JSON()["disabled_reason"] != nil {
		t.Fatal("re-enable should clear disabled_reason")
	}
	// Immutable fields and read-only fields.
	e.Patch(ep, e.Admin, map[string]any{"order_id": "sub_other"}).ExpectErr(422, "immutable_field")
	e.Patch(ep, e.Admin, map[string]any{"source": "trial"}).ExpectErr(422, "immutable_field")
	e.Patch(ep, e.Admin, map[string]any{"user_id": "usr_x"}).ExpectErr(422, "read_only_field")
	// Term change is entitlement.updated.
	e.Patch(ep, e.Admin, map[string]any{"ends_at": time.Now().Add(240 * time.Hour).UTC().Format(time.RFC3339)}).Expect(200)
	if len(e.Events("entitlement.updated")) == 0 {
		t.Fatal("expected entitlement.updated")
	}
	// Order-linked rows can't be hard-deleted.
	e.Delete(ep, e.Admin).ExpectErr(409, "entitlement_order_linked")
	// Revoked is terminal.
	e.Patch(ep, e.Admin, map[string]any{"status": "revoked", "disabled_reason": "refunded"}).Expect(200)
	e.Patch(ep, e.Admin, map[string]any{"status": "active"}).ExpectErr(409, "invalid_status_transition")
	// A revoked row doesn't block a new grant; default lists hide it.
	e.Grant(e.Admin, u.ID, app)
	list := e.Get(path, e.Admin).Expect(200).JSON()["data"].([]any)
	if len(list) != 1 {
		t.Fatalf("default list = %d rows", len(list))
	}
	all := e.Get(path+"?include_inactive=true", e.Admin).Expect(200).JSON()["data"].([]any)
	if len(all) != 2 {
		t.Fatalf("include_inactive list = %d rows", len(all))
	}
}

func TestEntitlementDeleteAndScheduled(t *testing.T) {
	e := testenv.New(t)
	u := e.Signup()
	app := e.App(system, nil)
	id := e.Grant(e.Admin, u.ID, app)
	e.Delete("/v1/entitlements/"+id, e.Admin).Expect(204)
	e.Get("/v1/entitlements/"+id, e.Admin).ExpectErr(404, "entitlement_not_found")
	if len(e.Events("entitlement.deleted")) == 0 {
		t.Fatal("expected entitlement.deleted")
	}
	// A future starts_at resolves as scheduled.
	future := time.Now().Add(48 * time.Hour).UTC().Format(time.RFC3339)
	e.PostIdem("/v1/users/"+u.ID+"/entitlements", e.Admin, testenv.Unique(),
		map[string]any{"application_id": app, "source": "admin_grant", "starts_at": future}).Expect(201)
	eff := e.Get("/v1/users/"+u.ID+"/apps/"+app+"/effective-permissions", e.Admin).Expect(200).JSON()
	if eff["entitlement_status"] != "scheduled" || eff["allowed"] != false {
		t.Fatalf("scheduled = %v", eff)
	}
	e.Post("/v1/auth/app-tokens", u.Token, map[string]any{"application_id": app}).ExpectErr(403, "entitlement_required")
}

func TestAppConfinedKey(t *testing.T) {
	e := testenv.New(t)
	app := e.App(system, nil)
	other := e.App(system, nil)
	key := e.Post("/v1/api-keys", e.Admin, map[string]any{"name": "backend", "scope": app,
		"permissions": []string{"entitlements.manage", "users.list"}}).Expect(201).Str("secret")
	e.Post("/v1/api-keys", e.Admin, map[string]any{"name": "bad", "scope": app, "permissions": []string{"users.manage"}}).
		ExpectErr(422, "permission_not_grantable_to_scope")
	u := e.Signup()
	e.Grant(key, u.ID, app)
	// Another Application's rows don't exist for this key.
	e.PostIdem("/v1/users/"+u.ID+"/entitlements", key, testenv.Unique(), map[string]any{"application_id": other, "source": "admin_grant"}).
		ExpectErr(404, "application_not_found")
	otherEnt := e.Grant(e.Admin, u.ID, other)
	e.Get("/v1/entitlements/"+otherEnt, key).ExpectErr(404, "entitlement_not_found")
	// users.list is forced to its own Application.
	users := e.Get("/v1/users", key).Expect(200).JSON()["data"].([]any)
	if len(users) != 1 || users[0].(map[string]any)["id"] != u.ID {
		t.Fatalf("app-confined users = %v", users)
	}
	// A key can't manage API Keys.
	e.Post("/v1/api-keys", key, map[string]any{"name": "x", "scope": app, "permissions": []string{}}).ExpectErr(400, "user_token_required")
	// me needs a user token.
	e.Get("/v1/users/me", key).ExpectErr(400, "user_token_required")
}

func TestRestrictDestructive(t *testing.T) {
	e := testenv.New(t)
	app := e.App(system, nil)
	agent := e.Post("/v1/api-keys", e.Admin, map[string]any{"name": "agent", "scope": "platform", "intended_use": "agent",
		"permissions": []string{"entitlements.manage", "users.list"}}).Expect(201)
	if agent.JSON()["restrict_destructive"] != true {
		t.Fatal("agent keys default to restrict_destructive")
	}
	key := agent.Str("secret")
	u := e.Signup()
	id := e.Grant(key, u.ID, app) // non-destructive writes are fine
	e.Patch("/v1/entitlements/"+id, key, map[string]any{"status": "disabled", "disabled_reason": "x"}).
		ExpectErr(403, "destructive_operation_restricted")
	e.Patch("/v1/entitlements/"+id, key, map[string]any{"ends_at": time.Now().Add(72 * time.Hour).UTC().Format(time.RFC3339)}).Expect(200)
	e.Delete("/v1/entitlements/"+id, key).ExpectErr(403, "destructive_operation_restricted")
	// Turning the safety off on an agent key is audited.
	e.Patch("/v1/api-keys/"+agent.Str("id"), e.Admin, map[string]any{"restrict_destructive": false}).Expect(200)
	audit := e.Get("/v1/audit-events?action=api_key.restrict_destructive_disabled&target_id="+agent.Str("id"), e.Admin).Expect(200)
	if len(audit.JSON()["data"].([]any)) != 1 {
		t.Fatalf("audit = %s", audit.Body)
	}
	e.Patch("/v1/entitlements/"+id, key, map[string]any{"status": "disabled", "disabled_reason": "x"}).Expect(200)
}

func TestApplications(t *testing.T) {
	e := testenv.New(t)
	slug := "inv-" + testenv.Unique()
	bad := e.Post("/v1/applications", e.Admin, map[string]any{"slug": "Bad_Slug", "name": "x", "launch_url": "http://x.example.com",
		"owner_user_id": system, "available_app_roles": make([]string, 21)}).ExpectErr(422, "validation_failed")
	if n := len(bad.JSON()["error"].(map[string]any)["details"].(map[string]any)["fields"].([]any)); n < 3 {
		t.Fatalf("expected every field error at once, got %d: %s", n, bad.Body)
	}
	e.Post("/v1/applications", e.Admin, map[string]any{"slug": "platform", "name": "x", "launch_url": "https://x.example.com",
		"owner_user_id": system}).ExpectErr(422, "validation_failed")
	e.Post("/v1/applications", e.Admin, map[string]any{"slug": slug, "name": "x", "launch_url": "https://x.example.com",
		"owner_user_id": system, "settings_schema": map[string]any{"type": "object", "properties": map[string]any{"theme": map[string]any{"type": "string"}}}}).
		ExpectErr(422, "reserved_settings_key")
	owner := e.Signup()
	created := e.Post("/v1/applications", e.Admin, map[string]any{"slug": slug, "name": "Invoicer", "launch_url": "https://inv.example.com",
		"owner_user_id": owner.ID, "available_app_roles": []string{"admin", "member"},
		"permissions": []map[string]any{{"key": "app." + slug + ".view"}}}).Expect(201)
	app := created.Str("id")
	if app != "app_"+slug {
		t.Fatalf("id = %s", app)
	}
	e.Post("/v1/applications", e.Admin, map[string]any{"slug": slug, "name": "dup", "launch_url": "https://x.example.com",
		"owner_user_id": system}).ExpectErr(409, "slug_taken")
	e.Get("/v1/roles/role_"+slug+"_admin", e.Admin).Expect(200)
	// The owner's Starter Tenant caps Applications at 1 and AppRoles at 3.
	e.Post("/v1/applications", e.Admin, map[string]any{"slug": "second-" + testenv.Unique(), "name": "x",
		"launch_url": "https://x.example.com", "owner_user_id": owner.ID}).ExpectErr(409, "plan_limit_reached")
	e.Patch("/v1/applications/"+app, owner.Token, map[string]any{"available_app_roles": []string{"a1", "a2", "a3", "a4"}}).
		ExpectErr(409, "plan_limit_reached")
	// Owner may configure, not moderate.
	e.Patch("/v1/applications/"+app, owner.Token, map[string]any{"name": "Invoicer 2"}).Expect(200)
	e.Patch("/v1/applications/"+app, owner.Token, map[string]any{"visibility": "internal"}).ExpectErr(403, "moderation_field_forbidden")
	e.Patch("/v1/applications/"+app, owner.Token, map[string]any{"slug": "new"}).ExpectErr(422, "read_only_field")
	// Removing an in-use role name or permission is refused.
	u := e.Signup()
	e.Post("/v1/users/"+u.ID+"/roles/role_"+slug+"_admin", owner.Token, map[string]any{}).Expect(201)
	e.Patch("/v1/applications/"+app, owner.Token, map[string]any{"available_app_roles": []string{"member"}}).ExpectErr(409, "app_role_in_use")
	e.Patch("/v1/roles/role_"+slug+"_member", owner.Token, map[string]any{"permissions": []string{"app." + slug + ".view"}}).Expect(200)
	e.Patch("/v1/applications/"+app, owner.Token, map[string]any{"permissions": []any{}}).ExpectErr(409, "permission_in_use")
	// Review lifecycle.
	e.Patch("/v1/applications/"+app, e.Admin, map[string]any{"review_status": "suspended"}).ExpectErr(422, "review_notes_required")
	e.Patch("/v1/applications/"+app, e.Admin, map[string]any{"review_status": "rejected", "review_notes": "x"}).ExpectErr(409, "invalid_review_transition")
	e.Patch("/v1/applications/"+app, e.Admin, map[string]any{"review_status": "suspended", "review_notes": "abuse"}).Expect(200)
	// Suspension blocks new grants; existing ones are untouched.
	e.PostIdem("/v1/users/"+u.ID+"/entitlements", e.Admin, testenv.Unique(),
		map[string]any{"application_id": app, "source": "admin_grant"}).ExpectErr(409, "application_not_available")
	e.Patch("/v1/applications/"+app, e.Admin, map[string]any{"review_status": "approved"}).Expect(200)
	e.Grant(e.Admin, u.ID, app)
}
