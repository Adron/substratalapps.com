package access

import (
	"fmt"
	"testing"
	"time"
)

var now = time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)

func ptr[T any](v T) *T { return &v }

func personal(status string) Path {
	return Path{EntitlementID: "ent_p", Source: "purchase", Status: status, StartsAt: now.Add(-time.Hour), CreatedAt: now.Add(-time.Hour)}
}

func org(id, status, scope string, overrides ...string) Path {
	return Path{EntitlementID: "ent_" + id, Source: "org_seat", Status: status, OrganizationID: ptr(id),
		StartsAt: now.Add(-time.Hour), MemberScope: ptr(scope), MemberOverrides: overrides,
		MembershipActive: true, CreatedAt: now.Add(-2 * time.Hour)}
}

// Every combination of user status × personal entitlement status × app
// role set, per NFR → Testing strategy.
func TestAllowExhaustive(t *testing.T) {
	userStatuses := []string{"active", "invited", "suspended", "deleted"}
	entStatuses := []string{"", "active", "disabled", "expired", "revoked"}
	roleSets := [][]AppRole{
		nil,
		{{ID: "role_timetrack_member", Permissions: []string{"app.timetrack.view"}}},
		{{ID: "role_timetrack_admin", Permissions: []string{"app.timetrack.export", "app.timetrack.view"}},
			{ID: "role_timetrack_member", Permissions: []string{"app.timetrack.view"}}},
	}
	for _, us := range userStatuses {
		for _, es := range entStatuses {
			for ri, roles := range roleSets {
				t.Run(fmt.Sprintf("%s/%s/roles%d", us, es, ri), func(t *testing.T) {
					var paths []Path
					if es != "" {
						paths = append(paths, personal(es))
					}
					r := Resolve("usr_1", paths, now)
					wantStatus := es
					if es == "" {
						wantStatus = "none"
					}
					if r.Status != wantStatus {
						t.Fatalf("status = %s, want %s", r.Status, wantStatus)
					}
					allowed := Allowed(us, r)
					if want := us == "active" && es == "active"; allowed != want {
						t.Fatalf("allowed = %v, want %v", allowed, want)
					}
					_, perms := EffectivePermissions(allowed, roles)
					if !allowed && len(perms) != 0 {
						t.Fatalf("permissions leaked without access: %v", perms)
					}
					if allowed && len(roles) > 0 && len(perms) == 0 {
						t.Fatal("roles held but no permissions")
					}
				})
			}
		}
	}
}

func TestWorkedExampleMultiOrg(t *testing.T) {
	// docs/access-control.md → Worked example: multi-org, one Application.
	paths := []Path{
		org("org_acme", "active", "all_members"),
		org("org_beta", "active", "denylist", "usr_jordan"),
	}
	r := Resolve("usr_jordan", paths, now)
	if r.Status != "active" || *r.ActiveVia.OrganizationID != "org_acme" {
		t.Fatalf("want active via org_acme, got %+v", r)
	}
	// Not a member of org_acme: only org_beta's exclusion remains.
	r = Resolve("usr_jordan", paths[1:], now)
	if r.Status != "none" {
		t.Fatalf("want none, got %s", r.Status)
	}
}

func TestWorkedExampleMemberScope(t *testing.T) {
	// docs/domain-model/entitlements.md → who gets access through one org grant.
	sam := "usr_sam"
	cases := []struct {
		scope                    string
		jordan, samAcc, rileyOrg bool
	}{
		{"all_members", true, true, true},
		{"allowlist", false, true, false},
		{"denylist", true, false, true},
	}
	for _, tc := range cases {
		g := org("org_acme", "active", tc.scope, sam)
		if got := Resolve("usr_jordan", []Path{g}, now).Status == "active"; got != tc.jordan {
			t.Errorf("%s jordan = %v", tc.scope, got)
		}
		if got := Resolve(sam, []Path{g}, now).Status == "active"; got != tc.samAcc {
			t.Errorf("%s sam = %v", tc.scope, got)
		}
		// Riley holds a personal Entitlement too: always access.
		riley := Resolve("usr_riley", []Path{g, personal("active")}, now)
		if riley.Status != "active" || riley.ActiveVia.IsOrg() {
			t.Errorf("%s riley should be active via personal path, got %+v", tc.scope, riley)
		}
		if got := MemberDecision(g, "usr_riley") == Included; got != tc.rileyOrg {
			t.Errorf("%s riley org decision = %v", tc.scope, got)
		}
	}
}

func TestScheduledAndPrecedence(t *testing.T) {
	future := personal("active")
	future.StartsAt = now.Add(time.Hour)
	if s := Resolve("u", []Path{future}, now).Status; s != "scheduled" {
		t.Fatalf("future start = %s, want scheduled", s)
	}
	if s := Resolve("u", []Path{future, personal("disabled")}, now).Status; s != "scheduled" {
		t.Fatalf("scheduled beats disabled, got %s", s)
	}
	paths := []Path{personal("revoked"), personal("expired"), personal("disabled")}
	if s := Resolve("u", paths, now).Status; s != "disabled" {
		t.Fatalf("precedence = %s, want disabled", s)
	}
	if s := Resolve("u", paths[:2], now).Status; s != "expired" {
		t.Fatalf("precedence = %s, want expired", s)
	}
	lapsed := personal("active")
	lapsed.EndsAt = ptr(now.Add(-time.Second))
	if s := Resolve("u", []Path{lapsed}, now).Status; s != "expired" {
		t.Fatalf("ends_at in the past = %s, want expired", s)
	}
}

func TestOrgPathRequiresActiveMembership(t *testing.T) {
	g := org("org_a", "active", "all_members")
	g.MembershipActive = false
	if s := Resolve("u", []Path{g}, now).Status; s != "none" {
		t.Fatalf("pending membership = %s, want none", s)
	}
	g = org("org_a", "active", "all_members")
	g.SeatLimited = true
	if MemberDecision(g, "u") != SeatLimit {
		t.Fatal("seat-limited decision")
	}
	if s := Resolve("u", []Path{g}, now).Status; s != "none" {
		t.Fatalf("seat-limited = %s, want none", s)
	}
}

func TestAttributionPrefersPersonalThenOldestOrg(t *testing.T) {
	older := org("org_old", "active", "all_members")
	newer := org("org_new", "active", "all_members")
	newer.CreatedAt = now.Add(-time.Minute)
	r := Resolve("u", []Path{newer, older}, now)
	if *r.ActiveVia.OrganizationID != "org_old" {
		t.Fatalf("want oldest org grant, got %s", *r.ActiveVia.OrganizationID)
	}
	r = Resolve("u", []Path{newer, personal("active"), older}, now)
	if r.ActiveVia.IsOrg() {
		t.Fatal("personal path must win attribution")
	}
}
