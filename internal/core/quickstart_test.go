package core_test

import (
	"os"
	"slices"
	"testing"

	"github.com/CompositeCode/substratalapps.com/internal/testenv"
)

func TestMain(m *testing.M) {
	code := m.Run()
	testenv.Drop()
	os.Exit(code)
}

// TestQuickstart runs docs/quickstart.md end to end: PLAN.md's Phase 1
// acceptance test.
func TestQuickstart(t *testing.T) {
	e := testenv.New(t)
	app := e.App("usr_01JAG0SYSTEM00000000000000", map[string]any{"default_app_role": nil})
	addr := "jordan+" + testenv.Unique() + "@example.com"

	// 1. Create a user.
	u := e.Post("/v1/users", e.Admin, map[string]any{"email": addr, "status": "invited"}).Expect(201)
	uid := u.Str("id")
	if u.Str("status") != "invited" {
		t.Fatalf("status = %s", u.Str("status"))
	}

	// 2. They own nothing yet.
	list := e.Get("/v1/users/"+uid+"/entitlements", e.Admin).Expect(200).JSON()
	if len(list["data"].([]any)) != 0 {
		t.Fatalf("expected no entitlements: %v", list)
	}

	// 3. Grant access.
	ent := e.PostIdem("/v1/users/"+uid+"/entitlements", e.Admin, testenv.Unique(),
		map[string]any{"application_id": app, "source": "admin_grant"}).Expect(201)
	if ent.Str("status") != "active" {
		t.Fatalf("entitlement status = %s", ent.Str("status"))
	}
	entID := ent.Str("id")

	// 4. Not allowed while invited; allowed once active.
	eff := e.Get("/v1/users/"+uid+"/apps/"+app+"/effective-permissions", e.Admin).Expect(200).JSON()
	if eff["allowed"] != false || eff["user_status"] != "invited" || eff["entitlement_status"] != "active" {
		t.Fatalf("invited effective-permissions: %v", eff)
	}
	e.Patch("/v1/users/"+uid, e.Admin, map[string]any{"status": "active"}).Expect(200)
	eff = e.Get("/v1/users/"+uid+"/apps/"+app+"/effective-permissions", e.Admin).Expect(200).JSON()
	if eff["allowed"] != true || len(eff["effective_permissions"].([]any)) != 0 {
		t.Fatalf("active effective-permissions: %v", eff)
	}

	// 5. Assign a role inside the app; its permissions appear.
	slug := app[len("app_"):]
	role := "role_" + slug + "_admin"
	e.Patch("/v1/roles/"+role, e.Admin, map[string]any{"permissions": []string{"app." + slug + ".export"}}).Expect(200)
	e.Post("/v1/users/"+uid+"/roles/"+role, e.Admin, map[string]any{}).Expect(201)
	eff = e.Get("/v1/users/"+uid+"/apps/"+app+"/effective-permissions", e.Admin).Expect(200).JSON()
	perms := toStrings(eff["effective_permissions"])
	if !slices.Contains(perms, "app."+slug+".export") {
		t.Fatalf("permissions = %v", perms)
	}

	// 6. Turn it off: not allowed, permissions empty, role untouched.
	e.Patch("/v1/entitlements/"+entID, e.Admin, map[string]any{"status": "disabled", "disabled_reason": "quickstart_demo"}).Expect(200)
	eff = e.Get("/v1/users/"+uid+"/apps/"+app+"/effective-permissions", e.Admin).Expect(200).JSON()
	if eff["allowed"] != false || eff["entitlement_status"] != "disabled" || len(eff["effective_permissions"].([]any)) != 0 {
		t.Fatalf("disabled effective-permissions: %v", eff)
	}
	roles := e.Get("/v1/users/"+uid+"/roles", e.Admin).Expect(200).JSON()["data"].([]any)
	found := false
	for _, r := range roles {
		found = found || r.(map[string]any)["role_id"] == role
	}
	if !found {
		t.Fatal("role assignment should survive the disable")
	}

	// The toggle wrote its audit event and webhook events.
	if len(e.Events("entitlement.disabled")) == 0 || len(e.Events("access.revoked")) == 0 {
		t.Fatal("expected entitlement.disabled and access.revoked outbox events")
	}
	audit := e.Get("/v1/audit-events?target_user_id="+uid+"&action=entitlement.disabled", e.Admin).Expect(200).JSON()
	if len(audit["data"].([]any)) != 1 {
		t.Fatalf("audit = %v", audit)
	}
}

func toStrings(v any) []string {
	var out []string
	for _, x := range v.([]any) {
		out = append(out, x.(string))
	}
	return out
}
