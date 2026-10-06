// Package access is the Access Control algorithm, as a pure function over
// already-loaded rows (docs/access-control.md → The algorithm):
//
//	allow(user, application, permission) :=
//	    user.status == "active"
//	    AND resolved_entitlement_status(user, application) == "active"
//	    AND permission ∈ effective_permissions(user, application)
//
// Nothing here touches the database, so every combination of entitlement
// status × platform role × app role can be tested exhaustively and
// cheaply, as NFR → Testing strategy asks.
package access

import (
	"slices"
	"sort"
	"time"
)

// Path is one way a User might reach an Application: their own personal
// Entitlement, or an org-wide grant through an Organization they belong to.
type Path struct {
	EntitlementID   string
	Source          string // purchase | trial | admin_grant | org_seat
	Status          string // active | disabled | expired | revoked
	OrganizationID  *string
	StartsAt        time.Time
	EndsAt          *time.Time
	MemberScope     *string
	MemberOverrides []string
	CreatedAt       time.Time

	// For org paths: whether the User's membership is active (a pending
	// invitation is never a path). A suspended Organization's grants still
	// are: suspension freezes the Organization's admin actions but doesn't
	// touch members' access (Organizations → PATCH, "What it doesn't do").
	MembershipActive bool
	// SeatLimited: the Application is on a Starter Tenant at its seat cap
	// and this User isn't already one of its seats (Pricing → Enforcement).
	SeatLimited bool
}

// Decisions an org path can produce for one member (Entitlements →
// Attribution).
const (
	Included  = "included"
	Excluded  = "excluded"
	SeatLimit = "seat_limit"
)

// IsOrg reports whether p is an org-wide grant.
func (p Path) IsOrg() bool { return p.OrganizationID != nil }

// MemberIncluded is member_included(grant, user).
func MemberIncluded(p Path, userID string) bool {
	scope := "all_members"
	if p.MemberScope != nil {
		scope = *p.MemberScope
	}
	switch scope {
	case "allowlist":
		return slices.Contains(p.MemberOverrides, userID)
	case "denylist":
		return !slices.Contains(p.MemberOverrides, userID)
	default:
		return true
	}
}

// MemberDecision is granted_via.member_decision for an org path.
func MemberDecision(p Path, userID string) string {
	switch {
	case !MemberIncluded(p, userID):
		return Excluded
	case p.SeatLimited:
		return SeatLimit
	default:
		return Included
	}
}

// EffectiveStatus is a row's status as of now: an active or disabled row
// past ends_at is treated as expired immediately, before the sweep writes
// it (Entitlements → Status transitions).
func EffectiveStatus(p Path, now time.Time) string {
	if (p.Status == "active" || p.Status == "disabled") && p.EndsAt != nil && !p.EndsAt.After(now) {
		return "expired"
	}
	return p.Status
}

// pathActive is path_active(e).
func pathActive(p Path, now time.Time) bool {
	return EffectiveStatus(p, now) == "active" && !p.StartsAt.After(now)
}

// counts reports whether p is a path for userID at all: personal paths
// always are; org paths only through an active membership whose grant
// includes the member and isn't seat-limited.
func counts(p Path, userID string) bool {
	if !p.IsOrg() {
		return true
	}
	return p.MembershipActive && MemberDecision(p, userID) == Included
}

// Resolution is resolved_entitlement_status plus its attribution.
type Resolution struct {
	Status string // active | scheduled | disabled | expired | revoked | none
	// ActiveVia is the path that grants access: personal wins; otherwise
	// the org grant created first (Auth → The app token, org_id).
	ActiveVia *Path
}

var precedence = map[string]int{"disabled": 3, "expired": 2, "revoked": 1}

// Resolve is resolved_entitlement_status(user, application) over every
// path the User has to one Application.
func Resolve(userID string, paths []Path, now time.Time) Resolution {
	var active []Path
	scheduled := false
	worst := ""
	for _, p := range paths {
		if !counts(p, userID) {
			continue
		}
		if pathActive(p, now) {
			active = append(active, p)
			continue
		}
		st := EffectiveStatus(p, now)
		if st == "active" && p.StartsAt.After(now) {
			scheduled = true
			continue
		}
		if precedence[st] > precedence[worst] {
			worst = st
		}
	}
	if len(active) > 0 {
		sort.SliceStable(active, func(i, j int) bool {
			if active[i].IsOrg() != active[j].IsOrg() {
				return !active[i].IsOrg()
			}
			return active[i].CreatedAt.Before(active[j].CreatedAt)
		})
		via := active[0]
		return Resolution{Status: "active", ActiveVia: &via}
	}
	if scheduled {
		return Resolution{Status: "scheduled"}
	}
	if worst != "" {
		return Resolution{Status: worst}
	}
	return Resolution{Status: "none"}
}

// ActiveVia returns the active path through a specific Organization, for
// app-tokens' organization_id selection.
func ActiveViaOrg(userID, orgID string, paths []Path, now time.Time) (*Path, bool) {
	for _, p := range paths {
		if p.IsOrg() && *p.OrganizationID == orgID && counts(p, userID) && pathActive(p, now) {
			pp := p
			return &pp, true
		}
	}
	return nil, false
}

// Allowed is the first two clauses of allow(): active User and active
// resolved entitlement. The permission clause is EffectivePermissions.
func Allowed(userStatus string, r Resolution) bool {
	return userStatus == "active" && r.Status == "active"
}

// AppRole is one app-scoped Role a User holds for the Application.
type AppRole struct {
	ID          string
	Permissions []string
}

// EffectivePermissions is effective_permissions(user, application): the
// union of the app-scoped Roles held (explicit assignments plus the
// Application's default_app_role, if set), and empty unless the User and
// the entitlement are both active, so the implicit default role is only
// "held" while access is active. Only app.<slug>.* keys ever appear,
// because app Roles can only hold their own Application's keys.
func EffectivePermissions(allowed bool, roles []AppRole) (roleIDs, perms []string) {
	roleIDs, perms = []string{}, []string{}
	if !allowed {
		return
	}
	seen := map[string]bool{}
	for _, r := range roles {
		roleIDs = append(roleIDs, r.ID)
		for _, p := range r.Permissions {
			if !seen[p] {
				seen[p] = true
				perms = append(perms, p)
			}
		}
	}
	sort.Strings(roleIDs)
	sort.Strings(perms)
	return
}
