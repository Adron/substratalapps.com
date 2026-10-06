package core_test

import (
	"testing"

	"github.com/Adron/substratalapps.com/internal/testenv"
)

func accessEventsFor(e *testenv.Env, typ, userID, appID string) int {
	n := 0
	for _, ev := range e.Events(typ) {
		p := ev["payload"].(map[string]any)
		if p["user_id"] == userID && p["application_id"] == appID {
			n++
		}
	}
	return n
}

func TestOrganizations(t *testing.T) {
	e := testenv.New(t)
	admin := e.Signup()
	org := e.Post("/v1/organizations", admin.Token, map[string]any{"name": "Acme Co."}).Expect(201)
	oid := org.Str("id")
	if org.JSON()["member_count"].(float64) != 1 {
		t.Fatalf("org = %s", org.Body)
	}

	// Inviting reveals nothing about whether the email had an account.
	existing := e.Signup()
	inv1 := e.Post("/v1/organizations/"+oid+"/members", admin.Token, map[string]any{"email": existing.Email}).Expect(201).JSON()
	newAddr := "new+" + testenv.Unique() + "@example.com"
	inv2 := e.Post("/v1/organizations/"+oid+"/members", admin.Token, map[string]any{"email": newAddr}).Expect(201).JSON()
	for _, m := range []map[string]any{inv1, inv2} {
		if m["membership_status"] != "pending" || m["display_name"] != nil {
			t.Fatalf("invite leaked account state: %v", m)
		}
	}
	e.Post("/v1/organizations/"+oid+"/members", admin.Token, map[string]any{"email": existing.Email}).ExpectErr(409, "already_member")
	e.Post("/v1/organizations/"+oid+"/members", admin.Token, map[string]any{"user_id": existing.ID}).ExpectErr(403, "forbidden")
	// A pending invitee can't see the Organization.
	e.Get("/v1/organizations/"+oid, existing.Token).ExpectErr(404, "organization_not_found")

	// An org admin can't grant their own Organization an app.
	app := e.App(system, nil)
	e.PostIdem("/v1/organizations/"+oid+"/entitlements", admin.Token, testenv.Unique(),
		map[string]any{"application_id": app}).ExpectErr(403, "forbidden")
	grant := e.PostIdem("/v1/organizations/"+oid+"/entitlements", e.Admin, testenv.Unique(),
		map[string]any{"application_id": app}).Expect(201)
	gid := grant.Str("id")
	if accessEventsFor(e, "access.granted", admin.ID, app) != 1 {
		t.Fatal("the active member should get access.granted")
	}

	// Accepting the invitation brings access with it.
	e.Post("/v1/organizations/"+oid+"/members/me/accept", existing.Token, map[string]any{}).Expect(200)
	if accessEventsFor(e, "access.granted", existing.ID, app) != 1 {
		t.Fatal("accepting should fire access.granted")
	}
	eff := e.Get("/v1/users/"+existing.ID+"/apps/"+app+"/effective-permissions", e.Admin).Expect(200).JSON()
	if eff["allowed"] != true {
		t.Fatalf("member effective-permissions = %v", eff)
	}
	ents := e.Get("/v1/users/me/entitlements", existing.Token).Expect(200).JSON()["data"].([]any)
	via := ents[0].(map[string]any)["granted_via"].(map[string]any)
	if via["member_decision"] != "included" || via["organization_id"] != oid {
		t.Fatalf("granted_via = %v", via)
	}

	// Narrowing to a denylist revokes exactly the listed member.
	e.Patch("/v1/entitlements/"+gid, admin.Token, map[string]any{"member_scope": "denylist", "member_overrides": []string{existing.ID}}).Expect(200)
	if accessEventsFor(e, "access.revoked", existing.ID, app) != 1 || accessEventsFor(e, "access.revoked", admin.ID, app) != 0 {
		t.Fatal("denylist should revoke only the listed member")
	}
	if len(e.Events("entitlement.member_scope_changed")) == 0 {
		t.Fatal("expected entitlement.member_scope_changed")
	}
	e.Patch("/v1/entitlements/"+gid, admin.Token, map[string]any{"member_overrides": []string{"usr_01JAG3Z9X8QS3F6K2M4N5P6R7S"}}).
		ExpectErr(422, "member_override_not_a_member")
	// An org admin can toggle but not revoke or re-term.
	e.Patch("/v1/entitlements/"+gid, admin.Token, map[string]any{"status": "revoked", "disabled_reason": "x"}).ExpectErr(403, "forbidden")
	e.Patch("/v1/entitlements/"+gid, admin.Token, map[string]any{"status": "disabled", "disabled_reason": "budget"}).Expect(200)

	// The last org_admin can't leave or be demoted.
	e.Delete("/v1/organizations/"+oid+"/members/me", admin.Token).ExpectErr(409, "last_org_admin")
	e.Patch("/v1/organizations/"+oid+"/members/"+admin.ID, admin.Token, map[string]any{"role": "member"}).ExpectErr(409, "last_org_admin")
	e.Patch("/v1/organizations/"+oid+"/members/"+existing.ID, admin.Token, map[string]any{"role": "org_admin"}).Expect(200)
	e.Delete("/v1/organizations/"+oid+"/members/me", admin.Token).Expect(204)

	// Suspension is platform-only and freezes admin actions.
	e.Patch("/v1/organizations/"+oid, existing.Token, map[string]any{"status": "suspended"}).ExpectErr(403, "forbidden")
	e.Patch("/v1/organizations/"+oid, e.Admin, map[string]any{"status": "suspended", "reason": "ToS"}).Expect(200)
	e.Patch("/v1/organizations/"+oid, existing.Token, map[string]any{"name": "Renamed"}).ExpectErr(403, "organization_suspended")
	members := e.Get("/v1/organizations/"+oid+"/members", existing.Token).Expect(200).JSON()["data"].([]any)
	if len(members) != 2 { // existing (active) + newAddr (pending)
		t.Fatalf("members = %v", members)
	}
}

func TestProfilesAndSettings(t *testing.T) {
	e := testenv.New(t)
	u := e.Signup()
	bad := e.Patch("/v1/users/me/profile", u.Token, map[string]any{"display_name": "", "contact_phone": "303-555-0142",
		"avatar_url": "http://example.com/a.png"}).ExpectErr(422, "validation_failed")
	if n := len(bad.JSON()["error"].(map[string]any)["details"].(map[string]any)["fields"].([]any)); n != 3 {
		t.Fatalf("fields = %d: %s", n, bad.Body)
	}
	e.Patch("/v1/users/me/profile", u.Token, map[string]any{"locale": "fr-FR"}).ExpectErr(422, "read_only_field")
	p := e.Patch("/v1/users/me/profile", u.Token, map[string]any{"display_name": "Jordan A.", "contact_phone": "+13035550142"}).Expect(200)
	if p.Str("display_name") != "Jordan A." {
		t.Fatalf("profile = %s", p.Body)
	}
	e.Patch("/v1/users/me/settings", u.Token, map[string]any{"timezone": "Mountain Time", "theme": "blue"}).ExpectErr(422, "validation_failed")
	st := e.Patch("/v1/users/me/settings", u.Token, map[string]any{"timezone": "America/Denver", "locale": "en-GB",
		"notifications": map[string]any{"sms": true}}).Expect(200).JSON()
	notif := st["notifications"].(map[string]any)
	if notif["email"] != true || notif["sms"] != true {
		t.Fatalf("notifications should merge one level: %v", notif)
	}
	if e.Get("/v1/users/me/profile", u.Token).Str("locale") != "en-GB" {
		t.Fatal("profile.locale should mirror settings")
	}

	app := e.App(system, map[string]any{"settings_schema": map[string]any{"type": "object", "properties": map[string]any{
		"default_billable": map[string]any{"type": "boolean", "default": true},
		"week_start":       map[string]any{"type": "string", "enum": []string{"sunday", "monday"}, "default": "sunday"},
	}}})
	sp := "/v1/users/me/apps/" + app + "/settings"
	e.Get(sp, u.Token).ExpectErr(403, "entitlement_required")
	e.Grant(e.Admin, u.ID, app)
	r := e.Get(sp, u.Token).Expect(200).JSON()
	if r["updated_at"] != nil || r["resolved"].(map[string]any)["week_start"] != "sunday" ||
		r["sources"].(map[string]any)["timezone"] != "global" {
		t.Fatalf("defaults = %v", r)
	}
	viol := e.Patch(sp, u.Token, map[string]any{"overrides": map[string]any{"week_start": "friday", "color": "red"}}).
		ExpectErr(422, "settings_schema_violation")
	if n := len(viol.JSON()["error"].(map[string]any)["details"].(map[string]any)["fields"].([]any)); n != 2 {
		t.Fatalf("violations = %s", viol.Body)
	}
	w := e.Patch(sp, u.Token, map[string]any{"overrides": map[string]any{"week_start": "monday", "theme": "light"}}).Expect(200).JSON()
	if w["resolved"].(map[string]any)["week_start"] != "monday" || w["sources"].(map[string]any)["theme"] != "app_override" {
		t.Fatalf("after write = %v", w)
	}
	// A schema change makes the override stale, never an error.
	e.Patch("/v1/applications/"+app, e.Admin, map[string]any{"settings_schema": map[string]any{"type": "object", "properties": map[string]any{
		"week_start": map[string]any{"type": "integer", "minimum": 0, "maximum": 6, "default": 0},
	}}}).Expect(200)
	stale := e.Get(sp, u.Token).Expect(200).JSON()
	if stale["resolved"].(map[string]any)["week_start"].(float64) != 0 || len(stale["stale_overrides"].([]any)) != 1 {
		t.Fatalf("stale = %v", stale)
	}
	stats := e.Get("/v1/applications/"+app, e.Admin).Expect(200).JSON()["settings_schema_stats"].(map[string]any)
	if stats["stale_override_counts"].(map[string]any)["week_start"].(float64) != 1 {
		t.Fatalf("stats = %v", stats)
	}
	e.Patch(sp, u.Token, map[string]any{"overrides": map[string]any{"week_start": nil}}).Expect(200)

	// AppProfile: lazy defaults, one-level merge, effective name.
	ap := "/v1/users/me/apps/" + app + "/profile"
	if e.Get(ap, u.Token).Expect(200).Str("effective_display_name") != "Jordan A." {
		t.Fatal("effective_display_name should fall back to the global name")
	}
	e.Patch(ap, u.Token, map[string]any{"display_handle": "j.a", "custom": map[string]any{"seat": 14, "dept": "eng"}}).Expect(200)
	got := e.Patch(ap, u.Token, map[string]any{"custom": map[string]any{"dept": nil}}).Expect(200).JSON()
	if got["effective_display_name"] != "j.a" || len(got["custom"].(map[string]any)) != 1 {
		t.Fatalf("app profile = %v", got)
	}
}
