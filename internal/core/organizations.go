package core

import (
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/Adron/substratalapps.com/internal/api/gen"
	"github.com/Adron/substratalapps.com/internal/db"
	"github.com/Adron/substratalapps.com/internal/email"
	"github.com/Adron/substratalapps.com/internal/httpx"
	"github.com/Adron/substratalapps.com/internal/ids"
)

type orgRow struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Status    string    `json:"status"`
	CreatedBy string    `json:"created_by"`
	TestMode  bool      `json:"test_mode"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
	Version   int       `json:"version"`
}

// organization is the API Organization object. Pending rows in a member's
// own list show only id, name, my_role, and my_membership_status.
type organization struct {
	ID                 string     `json:"id"`
	Name               string     `json:"name"`
	Status             *string    `json:"status,omitempty"`
	MemberCount        *int       `json:"member_count,omitempty"`
	MyRole             *string    `json:"my_role,omitempty"`
	MyMembershipStatus *string    `json:"my_membership_status,omitempty"`
	CreatedBy          *string    `json:"created_by,omitempty"`
	TestMode           *bool      `json:"test_mode,omitempty"`
	CreatedAt          *time.Time `json:"created_at,omitempty"`
	UpdatedAt          *time.Time `json:"updated_at,omitempty"`
}

func (c *Call) loadOrg(id string) (orgRow, error) {
	var o orgRow
	err := db.One(c.ctx, c.q, &o, `select to_jsonb(o) from organizations o where id = :id`, db.Args{"id": id})
	if err == db.ErrNotFound {
		return o, httpx.NotFoundResource("organization")
	}
	return o, err
}

func (c *Call) memberCount(orgID string) (int, error) {
	var n int
	err := db.One(c.ctx, c.q, &n, `select to_jsonb(count(*)) from organization_memberships
		where organization_id = :o and status = 'active'`, db.Args{"o": orgID})
	return n, err
}

func (c *Call) orgObject(o orgRow, myRole, myStatus *string) (organization, error) {
	n, err := c.memberCount(o.ID)
	if err != nil {
		return organization{}, err
	}
	st, tm, cb, ca, ua := o.Status, o.TestMode, o.CreatedBy, o.CreatedAt, o.UpdatedAt
	return organization{ID: o.ID, Name: o.Name, Status: &st, MemberCount: &n, MyRole: myRole, MyMembershipStatus: myStatus,
		CreatedBy: &cb, TestMode: &tm, CreatedAt: &ca, UpdatedAt: &ua}, nil
}

type membership struct {
	UserID string `json:"user_id"`
	Role   string `json:"role"`
	Status string `json:"status"`
}

func (c *Call) myMembership(orgID string) (*membership, error) {
	if c.p == nil || !c.p.IsUser() {
		return nil, nil
	}
	var m membership
	err := db.One(c.ctx, c.q, &m, `select jsonb_build_object('user_id', user_id, 'role', role, 'status', status)
		from organization_memberships where organization_id = :o and user_id = :u`, db.Args{"o": orgID, "u": c.p.UserID})
	if err == db.ErrNotFound {
		return nil, nil
	}
	return &m, err
}

// orgReadable: active members and organizations.manage; anyone else
// (including a pending invitee) gets 404.
func (c *Call) orgReadable(orgID string) (orgRow, *membership, error) {
	o, err := c.loadOrg(orgID)
	if err != nil {
		return o, nil, err
	}
	m, err := c.myMembership(orgID)
	if err != nil {
		return o, nil, err
	}
	if c.can("organizations.manage") || (m != nil && m.Status == "active") {
		return o, m, nil
	}
	return o, m, httpx.NotFoundResource("organization")
}

// orgAdminWrite authorizes an org admin action: organizations.manage, or an
// active org_admin of a non-suspended Organization.
func (c *Call) orgAdminWrite(o orgRow, m *membership) error {
	if c.can("organizations.manage") {
		return nil
	}
	if m == nil || m.Status != "active" || m.Role != "org_admin" {
		return httpx.Forbidden("organizations.manage")
	}
	if o.Status == "suspended" {
		return httpx.E(403, "organization_suspended", "This Organization is suspended.")
	}
	return nil
}

func (s *Server) OrganizationsList(w http.ResponseWriter, r *http.Request, params gen.OrganizationsListParams) {
	s.serve(w, r, opts{op: "organizations.list"}, func(c *Call) error {
		lim := httpx.Limit(params.Limit)
		filters := httpx.Filters(c.r)
		pos, err := c.s.Cursors.Decode(deref(params.Cursor), filters)
		if err != nil {
			return err
		}
		args := db.Args{"lim": int64(lim + 1)}
		var where []string
		admin := c.can("organizations.manage")
		if !admin {
			if !c.p.IsUser() {
				return c.OK(httpx.List[organization]{Data: []organization{}})
			}
			where = append(where, "o.id in (select organization_id from organization_memberships where user_id = :me)")
			args["me"] = c.p.UserID
		} else {
			if params.Status != nil {
				where = append(where, "o.status = :st")
				args["st"] = string(*params.Status)
			}
			if params.Q != nil {
				where = append(where, "lower(o.name) like :q")
				args["q"] = strings.ToLower(escapeLike(*params.Q)) + "%"
			}
		}
		if pos != nil {
			where = append(where, "(o.created_at, o.id) < (:ps::timestamptz, :pi)")
			args["ps"], args["pi"] = pos.Sort, pos.ID
		}
		if len(where) == 0 {
			where = []string{"true"}
		}
		rows, err := db.All[orgRow](c.ctx, c.q, `select to_jsonb(o) from organizations o where `+
			strings.Join(where, " and ")+` order by o.created_at desc, o.id desc limit :lim`, args)
		if err != nil {
			return err
		}
		out := make([]organization, 0, len(rows))
		for _, o := range rows {
			if admin {
				obj, err := c.orgObject(o, nil, nil)
				if err != nil {
					return err
				}
				out = append(out, obj)
				continue
			}
			m, err := c.myMembership(o.ID)
			if err != nil {
				return err
			}
			if m == nil {
				continue
			}
			if m.Status == "pending" {
				out = append(out, organization{ID: o.ID, Name: o.Name, MyRole: &m.Role, MyMembershipStatus: &m.Status})
				continue
			}
			obj, err := c.orgObject(o, &m.Role, &m.Status)
			if err != nil {
				return err
			}
			out = append(out, obj)
		}
		page := httpx.Paginate(c.s.Cursors, out, lim, filters, func(x organization) (string, string) { return "", "" })
		if page.Page.HasMore {
			last := rows[lim-1]
			cur := c.s.Cursors.Encode(httpx.TimeKey(last.CreatedAt), last.ID, filters)
			page.Page.NextCursor = &cur
		}
		return c.OK(page)
	})
}

func (s *Server) OrganizationsCreate(w http.ResponseWriter, r *http.Request, params gen.OrganizationsCreateParams) {
	s.serve(w, r, opts{op: "organizations.create", userOnly: true, idem: idemOptional}, func(c *Call) error {
		var in struct {
			Name string `json:"name"`
		}
		if err := c.Decode(&in); err != nil {
			return err
		}
		n := len([]rune(strings.TrimSpace(in.Name)))
		if n < 1 || n > 100 {
			var f httpx.Fields
			if n < 1 {
				f.Add("name", "required")
			} else {
				f.Max("name", "too_long", 100)
			}
			return f.Err()
		}
		if err := c.hit("orgcreate:"+c.p.UserID, perDay(10), false); err != nil {
			return err
		}
		id := ids.New(ids.Organization)
		if _, err := c.q.Exec(c.ctx, `insert into organizations (id, name, created_by, test_mode) values (:id, :n, :u, :tm)`,
			db.Args{"id": id, "n": strings.TrimSpace(in.Name), "u": c.p.UserID, "tm": c.testMode()}); err != nil {
			return err
		}
		if _, err := c.q.Exec(c.ctx, `insert into organization_memberships (user_id, organization_id, role, status, joined_at, test_mode)
			values (:u, :o, 'org_admin', 'active', now(), :tm)`, db.Args{"u": c.p.UserID, "o": id, "tm": c.testMode()}); err != nil {
			return err
		}
		o, err := c.loadOrg(id)
		if err != nil {
			return err
		}
		obj, err := c.orgObject(o, nil, nil)
		if err != nil {
			return err
		}
		if err := c.audit(Audit{Action: "organization.created", TargetType: "organization", TargetID: id,
			OrganizationID: &id, After: obj}); err != nil {
			return err
		}
		c.Versioned(o.Version)
		return c.Created(obj)
	})
}

func (s *Server) OrganizationsGet(w http.ResponseWriter, r *http.Request, id string) {
	s.serve(w, r, opts{op: "organizations.get"}, func(c *Call) error {
		o, m, err := c.orgReadable(id)
		if err != nil {
			return err
		}
		var role, status *string
		if m != nil {
			role, status = &m.Role, &m.Status
		}
		obj, err := c.orgObject(o, role, status)
		if err != nil {
			return err
		}
		c.Versioned(o.Version)
		return c.OK(obj)
	})
}

func (s *Server) OrganizationsUpdate(w http.ResponseWriter, r *http.Request, id string, params gen.OrganizationsUpdateParams) {
	s.serve(w, r, opts{op: "organizations.update"}, func(c *Call) error {
		o, m, err := c.orgReadable(id)
		if err != nil {
			return err
		}
		if err := httpx.CheckIfMatch(c.r, o.Version); err != nil {
			return err
		}
		p, err := c.Patch()
		if err != nil {
			return err
		}
		if err := p.RejectReadOnly("id", "member_count", "created_by", "test_mode", "created_at", "updated_at"); err != nil {
			return err
		}
		var f httpx.Fields
		p.RequireNonNull(&f, "name", "status")
		next := o
		if v, present := p.String("name", &f); present && v != nil {
			if n := len([]rune(strings.TrimSpace(*v))); n < 1 || n > 100 {
				f.Max("name", "too_long", 100)
			}
			next.Name = strings.TrimSpace(*v)
		}
		if v, present := p.String("status", &f); present && v != nil {
			if !c.can("organizations.manage") {
				return httpx.Forbidden("organizations.manage")
			}
			if *v != "active" && *v != "suspended" {
				f.Enum("status", "active", "suspended")
			}
			next.Status = *v
		}
		var reason *string
		if p.Has("reason") {
			reason, _ = p.String("reason", &f)
		}
		if err := f.Err(); err != nil {
			return err
		}
		if err := c.orgAdminWrite(o, m); err != nil {
			return err
		}
		if err := c.destructiveIf(next.Status == "suspended" && o.Status != "suspended"); err != nil {
			return err
		}
		if _, err := c.q.Exec(c.ctx, `update organizations set name = :n, status = :s where id = :id`,
			db.Args{"n": next.Name, "s": next.Status, "id": o.ID}); err != nil {
			return err
		}
		after, err := c.loadOrg(o.ID)
		if err != nil {
			return err
		}
		auditAfter := map[string]any{"name": after.Name, "status": after.Status}
		if reason != nil {
			auditAfter["reason"] = *reason
		}
		if err := c.audit(Audit{Action: "organization.updated", TargetType: "organization", TargetID: o.ID,
			OrganizationID: &o.ID, Before: map[string]any{"name": o.Name, "status": o.Status}, After: auditAfter}); err != nil {
			return err
		}
		var role, status *string
		if m != nil {
			role, status = &m.Role, &m.Status
		}
		obj, err := c.orgObject(after, role, status)
		if err != nil {
			return err
		}
		c.Versioned(after.Version)
		return c.OK(obj)
	})
}

// ── Members ────────────────────────────────────────────────────────────

type member struct {
	UserID           string     `json:"user_id"`
	Email            string     `json:"email"`
	DisplayName      *string    `json:"display_name"`
	MembershipStatus string     `json:"membership_status"`
	Role             string     `json:"role"`
	InvitedAt        time.Time  `json:"invited_at"`
	JoinedAt         *time.Time `json:"joined_at"`
}

// memberSQL deliberately reveals nothing about a pending invitee's
// account: display_name is null until the membership is active.
const memberSQL = `select jsonb_build_object('user_id', m.user_id, 'email', u.email,
	'display_name', case when m.status = 'active' then p.display_name end,
	'membership_status', m.status, 'role', m.role, 'invited_at', m.invited_at, 'joined_at', m.joined_at)
	from organization_memberships m join users u on u.id = m.user_id left join profiles p on p.user_id = m.user_id`

func (c *Call) loadMember(orgID, userID string) (member, error) {
	var x member
	err := db.One(c.ctx, c.q, &x, memberSQL+` where m.organization_id = :o and m.user_id = :u`, db.Args{"o": orgID, "u": userID})
	if err == db.ErrNotFound {
		return x, httpx.NotFoundResource("member")
	}
	return x, err
}

func (s *Server) OrganizationsListMembers(w http.ResponseWriter, r *http.Request, id string, params gen.OrganizationsListMembersParams) {
	s.serve(w, r, opts{op: "organizations.listMembers"}, func(c *Call) error {
		if _, _, err := c.orgReadable(id); err != nil {
			return err
		}
		lim := httpx.Limit(params.Limit)
		filters := httpx.Filters(c.r)
		pos, err := c.s.Cursors.Decode(deref(params.Cursor), filters)
		if err != nil {
			return err
		}
		where := []string{"m.organization_id = :o"}
		args := db.Args{"o": id, "lim": int64(lim + 1)}
		if params.Role != nil {
			where = append(where, "m.role = :role")
			args["role"] = string(*params.Role)
		}
		if params.MembershipStatus != nil {
			where = append(where, "m.status = :ms")
			args["ms"] = string(*params.MembershipStatus)
		}
		if params.Q != nil {
			where = append(where, "(lower(u.email::text) like :q or (m.status = 'active' and lower(p.display_name) like :q))")
			args["q"] = strings.ToLower(escapeLike(*params.Q)) + "%"
		}
		if pos != nil {
			where = append(where, "(m.invited_at, m.user_id) < (:ps::timestamptz, :pi)")
			args["ps"], args["pi"] = pos.Sort, pos.ID
		}
		rows, err := db.All[member](c.ctx, c.q, memberSQL+" where "+strings.Join(where, " and ")+
			" order by m.invited_at desc, m.user_id desc limit :lim", args)
		if err != nil {
			return err
		}
		return c.OK(httpx.Paginate(c.s.Cursors, rows, lim, filters, func(x member) (string, string) {
			return httpx.TimeKey(x.InvitedAt), x.UserID
		}))
	})
}

func (s *Server) OrganizationsAddMember(w http.ResponseWriter, r *http.Request, id string) {
	s.serve(w, r, opts{op: "organizations.addMember", idem: idemOptional}, func(c *Call) error {
		o, m, err := c.orgReadable(id)
		if err != nil {
			return err
		}
		if err := c.orgAdminWrite(o, m); err != nil {
			return err
		}
		if o.Status == "suspended" {
			return httpx.E(403, "organization_suspended", "This Organization is suspended; no new members can join.")
		}
		var in struct {
			UserID *string `json:"user_id"`
			Email  *string `json:"email"`
			Role   *string `json:"role"`
		}
		if err := c.Decode(&in); err != nil {
			return err
		}
		if (in.UserID == nil) == (in.Email == nil) {
			return httpx.Invalid("Send exactly one of user_id or email.")
		}
		role := "member"
		if in.Role != nil {
			role = *in.Role
			if role != "member" && role != "org_admin" {
				var f httpx.Fields
				f.Enum("role", "org_admin", "member")
				return f.Err()
			}
		}
		var uid string
		if in.UserID != nil {
			if !c.can("organizations.manage") {
				return httpx.Forbidden("organizations.manage")
			}
			u, err := c.visibleUser(*in.UserID)
			if err != nil || u.Status == "deleted" {
				return httpx.NotFoundResource("user")
			}
			uid = u.ID
		} else {
			addr, ok := validEmail(*in.Email)
			if !ok {
				var f httpx.Fields
				f.Add("email", "invalid_format")
				return f.Err()
			}
			u, taken, err := c.emailTaken(addr)
			if err != nil {
				return err
			}
			if taken {
				uid = u.ID
			} else {
				// The one way a non-platform caller can cause a User to be
				// created: as an invited account for this invitation.
				nu, err := c.createUser(newUser{Email: addr, Status: "invited"})
				if err != nil {
					return err
				}
				uid = nu.ID
				if err := c.sendInvitation(uid, addr, nil); err != nil {
					return err
				}
			}
			if taken && u.Status != "invited" {
				c.mail(email.Message{To: u.Email, Kind: "org_invitation",
					Subject: fmt.Sprintf("You've been invited to join %s on Substratal", o.Name),
					Text:    fmt.Sprintf("You've been invited to join %s. Sign in to accept the invitation.", o.Name)})
			} else if taken && u.Status == "invited" {
				if err := c.sendInvitation(uid, u.Email, nil); err != nil {
					return err
				}
			}
		}
		if existing, err := c.loadMember(o.ID, uid); err == nil {
			_ = existing
			return httpx.Conflict("already_member", "This user is already a member or has a pending invitation.")
		}
		var invitedBy any
		if c.p.IsUser() {
			invitedBy = c.p.UserID
		}
		direct := in.UserID != nil
		if _, err := c.q.Exec(c.ctx, `insert into organization_memberships (user_id, organization_id, role, status, invited_by, test_mode)
			values (:u, :o, :r, 'pending', :by, :tm)`,
			db.Args{"u": uid, "o": o.ID, "r": role, "by": invitedBy, "tm": c.testMode()}); err != nil {
			if _, ok := db.UniqueViolation(err); ok {
				return httpx.Conflict("already_member", "This user is already a member or has a pending invitation.")
			}
			return err
		}
		if err := c.audit(Audit{Action: "organization.member_invited", TargetType: "organization", TargetID: o.ID,
			TargetUserID: &uid, OrganizationID: &o.ID, After: map[string]any{"user_id": uid, "role": role}}); err != nil {
			return err
		}
		if direct {
			pairs, err := c.orgAppPairs(uid, o.ID)
			if err != nil {
				return err
			}
			watch, err := c.watchAccess(pairs)
			if err != nil {
				return err
			}
			if err := c.activateMembership(uid, o.ID, ""); err != nil {
				return err
			}
			if err := c.flip(watch, "org_member_added", "org_member_removed"); err != nil {
				return err
			}
		}
		x, err := c.loadMember(o.ID, uid)
		if err != nil {
			return err
		}
		return c.Created(x)
	})
}

// activateMembership turns a pending membership active and records it.
func (c *Call) activateMembership(userID, orgID, actorUserID string) error {
	var role string
	if err := db.One(c.ctx, c.q, &role, `update organization_memberships set status = 'active', joined_at = now()
		where user_id = :u and organization_id = :o and status = 'pending' returning to_jsonb(role)`,
		db.Args{"u": userID, "o": orgID}); err != nil {
		if err == db.ErrNotFound {
			return nil
		}
		return err
	}
	a := Audit{Action: "organization.member_added", TargetType: "organization", TargetID: orgID,
		TargetUserID: &userID, OrganizationID: &orgID, After: map[string]any{"user_id": userID, "role": role}}
	if actorUserID != "" {
		a.ActorType, a.ActorID = "user", actorUserID
	}
	if err := c.audit(a); err != nil {
		return err
	}
	return c.emit("organization.member_added", nil, map[string]any{"organization_id": orgID, "user_id": userID, "role": role})
}

func (s *Server) OrganizationsAcceptMembership(w http.ResponseWriter, r *http.Request, id string) {
	s.serve(w, r, opts{op: "organizations.acceptMembership", userOnly: true}, func(c *Call) error {
		o, err := c.loadOrg(id)
		if err != nil {
			return err
		}
		m, err := c.myMembership(id)
		if err != nil {
			return err
		}
		if m == nil {
			return httpx.NotFoundResource("member")
		}
		if m.Status == "pending" {
			if o.Status == "suspended" {
				return httpx.E(403, "organization_suspended", "This Organization is suspended.")
			}
			pairs, err := c.orgAppPairs(c.p.UserID, id)
			if err != nil {
				return err
			}
			watch, err := c.watchAccess(pairs)
			if err != nil {
				return err
			}
			if err := c.activateMembership(c.p.UserID, id, ""); err != nil {
				return err
			}
			if err := c.flip(watch, "org_member_added", "org_member_removed"); err != nil {
				return err
			}
		}
		x, err := c.loadMember(id, c.p.UserID)
		if err != nil {
			return err
		}
		return c.OK(x)
	})
}

func (c *Call) activeAdminCount(orgID string) (int, error) {
	var n int
	err := db.One(c.ctx, c.q, &n, `select to_jsonb(count(*)) from organization_memberships
		where organization_id = :o and role = 'org_admin' and status = 'active'`, db.Args{"o": orgID})
	return n, err
}

func (s *Server) OrganizationsUpdateMember(w http.ResponseWriter, r *http.Request, id string, userID string) {
	s.serve(w, r, opts{op: "organizations.updateMember"}, func(c *Call) error {
		o, m, err := c.orgReadable(id)
		if err != nil {
			return err
		}
		if err := c.orgAdminWrite(o, m); err != nil {
			return err
		}
		uid, err := c.selfID(userID)
		if err != nil {
			return err
		}
		x, err := c.loadMember(id, uid)
		if err != nil {
			return err
		}
		var in struct {
			Role string `json:"role"`
		}
		if err := c.Decode(&in); err != nil {
			return err
		}
		if in.Role != "member" && in.Role != "org_admin" {
			var f httpx.Fields
			f.Enum("role", "org_admin", "member")
			return f.Err()
		}
		if in.Role == x.Role {
			return c.OK(x)
		}
		demotes := x.Role == "org_admin" && in.Role == "member"
		if err := c.destructiveIf(demotes); err != nil {
			return err
		}
		if demotes && x.MembershipStatus == "active" {
			if n, err := c.activeAdminCount(id); err != nil {
				return err
			} else if n <= 1 {
				return httpx.Conflict("last_org_admin", "An Organization always has at least one active org_admin.")
			}
		}
		if _, err := c.q.Exec(c.ctx, `update organization_memberships set role = :r where organization_id = :o and user_id = :u`,
			db.Args{"r": in.Role, "o": id, "u": uid}); err != nil {
			return err
		}
		if err := c.audit(Audit{Action: "organization.member_role_changed", TargetType: "organization", TargetID: id,
			TargetUserID: &uid, OrganizationID: &id, Before: map[string]any{"role": x.Role},
			After: map[string]any{"role": in.Role}}); err != nil {
			return err
		}
		after, err := c.loadMember(id, uid)
		if err != nil {
			return err
		}
		return c.OK(after)
	})
}

func (s *Server) OrganizationsRemoveMember(w http.ResponseWriter, r *http.Request, id string, userID string) {
	s.serve(w, r, opts{op: "organizations.removeMember"}, func(c *Call) error {
		uid, err := c.selfID(userID)
		if err != nil {
			return err
		}
		o, err := c.loadOrg(id)
		if err != nil {
			return err
		}
		m, err := c.myMembership(id)
		if err != nil {
			return err
		}
		if !c.isSelf(uid) {
			if !c.can("organizations.manage") && (m == nil || m.Status != "active") {
				return httpx.NotFoundResource("organization")
			}
			if err := c.orgAdminWrite(o, m); err != nil {
				return err
			}
		}
		x, err := c.loadMember(id, uid)
		if err != nil {
			return err
		}
		if x.Role == "org_admin" && x.MembershipStatus == "active" {
			if n, err := c.activeAdminCount(id); err != nil {
				return err
			} else if n <= 1 {
				return httpx.Conflict("last_org_admin", "Promote another org_admin before removing the last one.")
			}
		}
		active := x.MembershipStatus == "active"
		var watch *accessWatch
		if active {
			pairs, err := c.orgAppPairs(uid, id)
			if err != nil {
				return err
			}
			if watch, err = c.watchAccess(pairs); err != nil {
				return err
			}
		}
		if _, err := c.q.Exec(c.ctx, `delete from organization_memberships where organization_id = :o and user_id = :u`,
			db.Args{"o": id, "u": uid}); err != nil {
			return err
		}
		if _, err := c.q.Exec(c.ctx, `update entitlements set member_overrides = array_remove(member_overrides, :u)
			where organization_id = :o and :u = any(member_overrides)`, db.Args{"o": id, "u": uid}); err != nil {
			return err
		}
		if err := c.audit(Audit{Action: "organization.member_removed", TargetType: "organization", TargetID: id,
			TargetUserID: &uid, OrganizationID: &id, Before: map[string]any{"user_id": uid, "role": x.Role,
				"membership_status": x.MembershipStatus}}); err != nil {
			return err
		}
		if active {
			if err := c.emit("organization.member_removed", nil, map[string]any{"organization_id": id, "user_id": uid, "role": x.Role}); err != nil {
				return err
			}
			if err := c.flip(watch, "org_member_added", "org_member_removed"); err != nil {
				return err
			}
		}
		return c.NoContent()
	})
}

// ── Org-wide grants ────────────────────────────────────────────────────

func (s *Server) OrganizationsListEntitlements(w http.ResponseWriter, r *http.Request, id string, params gen.OrganizationsListEntitlementsParams) {
	s.serve(w, r, opts{op: "organizations.listEntitlements"}, func(c *Call) error {
		if _, isKey := c.p.AppKey(); !isKey {
			if _, _, err := c.orgReadable(id); err != nil {
				return err
			}
		}
		lim := httpx.Limit(params.Limit)
		filters := httpx.Filters(c.r)
		pos, err := c.s.Cursors.Decode(deref(params.Cursor), filters)
		if err != nil {
			return err
		}
		where := []string{"e.organization_id = :o"}
		args := db.Args{"o": id, "lim": int64(lim + 1)}
		app := params.ApplicationId
		if k, isKey := c.p.AppKey(); isKey {
			app = &k
		}
		where, args = entFilters(where, args, app, enumStr(params.Status), nil, params.IncludeInactive)
		if params.OrderId != nil {
			where = append(where, "e.order_id = :ord")
			args["ord"] = *params.OrderId
		}
		if params.EndsBefore != nil {
			where = append(where, "e.ends_at < :eb::timestamptz")
			args["eb"] = db.Time(*params.EndsBefore)
		}
		if pos != nil {
			where = append(where, "(e.created_at, e.id) < (:ps::timestamptz, :pi)")
			args["ps"], args["pi"] = pos.Sort, pos.ID
		}
		rows, err := c.queryEnts(strings.Join(where, " and ")+" order by e.created_at desc, e.id desc limit :lim", args)
		if err != nil {
			return err
		}
		return c.OK(httpx.Paginate(c.s.Cursors, rows, lim, filters, func(e entitlement) (string, string) {
			return httpx.TimeKey(e.CreatedAt), e.ID
		}))
	})
}

func (s *Server) OrganizationsGrantEntitlement(w http.ResponseWriter, r *http.Request, id string, params gen.OrganizationsGrantEntitlementParams) {
	s.serve(w, r, opts{op: "organizations.grantEntitlement", idem: idemRequired}, func(c *Call) error {
		var in struct {
			ApplicationID   string          `json:"application_id"`
			OrderID         *string         `json:"order_id"`
			StartsAt        *time.Time      `json:"starts_at"`
			EndsAt          *time.Time      `json:"ends_at"`
			MemberScope     *string         `json:"member_scope"`
			MemberOverrides []string        `json:"member_overrides"`
			Source          json.RawMessage `json:"source"`
		}
		if err := c.Decode(&in); err != nil {
			return err
		}
		if in.Source != nil {
			return httpx.E(422, "invalid_source", "source is always org_seat here and isn't accepted.")
		}
		if in.ApplicationID == "" {
			return httpx.Invalid("application_id is required.")
		}
		o, err := c.loadOrg(id)
		if err != nil {
			return err
		}
		a, err := c.loadApp(in.ApplicationID)
		if err == db.ErrNotFound {
			return httpx.NotFoundResource("application")
		}
		if err != nil {
			return err
		}
		// Whoever controls the Application, never the Organization itself.
		if ok, err := c.canManageApp("entitlements.manage", a); err != nil {
			return err
		} else if !ok {
			return httpx.Forbidden("entitlements.manage")
		}
		if err := c.grantable(a); err != nil {
			return err
		}
		starts := c.now
		if in.StartsAt != nil {
			starts = in.StartsAt.UTC()
		}
		if err := validateTerm("admin_grant", starts, in.EndsAt, in.OrderID, c.now); err != nil {
			return err
		}
		scope := "all_members"
		if in.MemberScope != nil {
			scope = *in.MemberScope
			if scope != "all_members" && scope != "allowlist" && scope != "denylist" {
				var f httpx.Fields
				f.Enum("member_scope", "all_members", "allowlist", "denylist")
				return f.Err()
			}
		}
		overrides := dedupe(in.MemberOverrides)
		if len(overrides) > 1000 {
			var f httpx.Fields
			f.Max("member_overrides", "too_long", 1000)
			return f.Err()
		}
		if err := c.checkOverridesAreMembers(o.ID, overrides); err != nil {
			return err
		}
		t, err := c.loadTenant(a.TenantID)
		if err != nil {
			return err
		}
		if err := tenantWritable(t); err != nil {
			return err
		}
		live, err := c.queryEnts(`e.organization_id = :o and e.application_id = :a and e.status in ('active','disabled')`,
			db.Args{"o": o.ID, "a": a.ID})
		if err != nil {
			return err
		}
		if len(live) > 0 {
			return httpx.Conflict("entitlement_already_exists", "This Organization already has a live grant to this Application.").
				With("existing_entitlement_id", live[0].ID)
		}
		// Plan checks on the Application's Tenant: every newly included
		// active member who isn't already a seat counts as a new one.
		if !c.testMode() && (t.Restricted || plans[t.Plan].Seats != nil) {
			members, err := db.All[string](c.ctx, c.q, `select to_jsonb(m.user_id) from organization_memberships m
				join users u on u.id = m.user_id
				where m.organization_id = :o and m.status = 'active' and u.status = 'active' and not u.test_mode`,
				db.Args{"o": o.ID})
			if err != nil {
				return err
			}
			ss, err := c.seats(t.ID)
			if err != nil {
				return err
			}
			added := 0
			for _, uid := range members {
				included := scope == "all_members" ||
					(scope == "allowlist" && slices.Contains(overrides, uid)) ||
					(scope == "denylist" && !slices.Contains(overrides, uid))
				if included && !ss.isSeat(uid) {
					added++
				}
			}
			if added > 0 && t.Restricted {
				return subscriptionRequired(t)
			}
			if l := plans[t.Plan].Seats; l != nil && added > 0 && ss.count()+added > *l {
				return planLimitErr("seats", *l, ss.count(), t)
			}
		}
		pairs, err := c.orgMemberPairs(o.ID, a.ID)
		if err != nil {
			return err
		}
		watch, err := c.watchAccess(pairs)
		if err != nil {
			return err
		}
		eid := ids.New(ids.Entitlement)
		var mo any
		if scope != "all_members" || len(overrides) > 0 {
			mo = db.TextArray(overrides)
		}
		if _, err := c.q.Exec(c.ctx, `insert into entitlements (id, organization_id, application_id, tenant_id, status, source,
				order_id, starts_at, ends_at, member_scope, member_overrides, test_mode)
			values (:id, :o, :a, :t, 'active', 'org_seat', :ord, :st::timestamptz, :en::timestamptz, :ms, :mo::text[], :tm)`,
			db.Args{"id": eid, "o": o.ID, "a": a.ID, "t": a.TenantID, "ord": in.OrderID, "st": db.Time(starts),
				"en": db.NullTime(in.EndsAt), "ms": scope, "mo": mo, "tm": c.testMode()}); err != nil {
			if _, ok := db.UniqueViolation(err); ok {
				return httpx.Conflict("entitlement_already_exists", "This Organization already has a live grant to this Application.")
			}
			return err
		}
		e, err := c.loadEnt(eid)
		if err != nil {
			return err
		}
		if err := c.entitlementEffects("entitlement.granted", nil, e, nil); err != nil {
			return err
		}
		if err := c.flip(watch, "entitlement_granted", "org_grant_changed"); err != nil {
			return err
		}
		c.Versioned(e.version)
		return c.Created(e)
	})
}
