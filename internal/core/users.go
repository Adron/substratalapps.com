package core

import (
	"net/http"
	"strings"
	"time"

	"github.com/Adron/substratalapps.com/internal/api/gen"
	"github.com/Adron/substratalapps.com/internal/db"
	"github.com/Adron/substratalapps.com/internal/httpx"
)

// canReadUser: self, users.list, or an app-confined users.list key for a
// User with an access path to its Application (anyone else 404s).
func (c *Call) canReadUser(u userRow) (bool, error) {
	if c.isSelf(u.ID) || c.can("users.list") || c.can("users.manage") {
		return true, nil
	}
	if app, ok := c.p.AppKey(); ok && c.p.Perms["users.list"] {
		return c.userHasAccessPath(u.ID, app)
	}
	return false, nil
}

func (s *Server) UsersList(w http.ResponseWriter, r *http.Request, params gen.UsersListParams) {
	s.serve(w, r, opts{op: "users.list"}, func(c *Call) error {
		appFilter := params.ApplicationId
		if app, ok := c.p.AppKey(); ok {
			if !c.p.Perms["users.list"] {
				return httpx.Forbidden("users.list")
			}
			appFilter = &app // forced, whatever the caller passed
		} else if err := c.require("users.list"); err != nil {
			if c.require("users.manage") != nil {
				return err
			}
		}
		lim := httpx.Limit(params.Limit)
		filters := httpx.Filters(c.r)
		pos, err := c.s.Cursors.Decode(deref(params.Cursor), filters)
		if err != nil {
			return err
		}
		var where []string
		args := db.Args{"lim": int64(lim + 1)}
		if params.Status != nil {
			st := string(*params.Status)
			if st == "deleted" && !c.can("users.manage") {
				return c.OK(httpx.List[userRow]{Data: []userRow{}})
			}
			where = append(where, "u.status = :status")
			args["status"] = st
		} else {
			where = append(where, "u.status <> 'deleted'")
		}
		if params.Email != nil {
			where = append(where, "u.email = :email")
			args["email"] = *params.Email
		}
		if params.Q != nil {
			if len([]rune(*params.Q)) < 3 {
				var f httpx.Fields
				f.Add("q", "too_short").Min = ip(3)
				return f.Err()
			}
			where = append(where, `(lower(u.email::text) like :q or exists (select 1 from profiles p
				where p.user_id = u.id and lower(p.display_name) like :q))`)
			args["q"] = strings.ToLower(escapeLike(*params.Q)) + "%"
		}
		if appFilter != nil {
			where = append(where, `(exists (select 1 from entitlements e where e.user_id = u.id and e.application_id = :app)
				or exists (select 1 from entitlements e join organization_memberships m on m.organization_id = e.organization_id
				           where m.user_id = u.id and e.application_id = :app))`)
			args["app"] = *appFilter
		}
		if params.OrganizationId != nil {
			where = append(where, `exists (select 1 from organization_memberships m where m.user_id = u.id and m.organization_id = :org)`)
			args["org"] = *params.OrganizationId
		}
		if params.CreatedAfter != nil {
			where = append(where, "u.created_at > :ca::timestamptz")
			args["ca"] = db.Time(*params.CreatedAfter)
		}
		if params.CreatedBefore != nil {
			where = append(where, "u.created_at < :cb::timestamptz")
			args["cb"] = db.Time(*params.CreatedBefore)
		}
		if pos != nil {
			where = append(where, "(u.created_at, u.id) < (:ps::timestamptz, :pi)")
			args["ps"], args["pi"] = pos.Sort, pos.ID
		}
		// The system account is internal; it's never listed.
		where = append(where, "u.id <> 'usr_01JAG0SYSTEM00000000000000'")
		raw, err := c.q.Query(c.ctx, userRowSQL+" where "+strings.Join(where, " and ")+
			" order by u.created_at desc, u.id desc limit :lim", args)
		if err != nil {
			return err
		}
		rows := make([]userRow, 0, len(raw))
		for _, x := range raw {
			u, err := decodeUser(x)
			if err != nil {
				return err
			}
			rows = append(rows, u)
		}
		return c.OK(httpx.Paginate(c.s.Cursors, rows, lim, filters, func(u userRow) (string, string) {
			return httpx.TimeKey(u.CreatedAt), u.ID
		}))
	})
}

func escapeLike(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}

func (s *Server) UsersCreate(w http.ResponseWriter, r *http.Request, params gen.UsersCreateParams) {
	s.serve(w, r, opts{op: "users.create", idem: idemOptional}, func(c *Call) error {
		if err := c.require("users.manage"); err != nil {
			return err
		}
		var in struct {
			Email          string  `json:"email"`
			DisplayName    *string `json:"display_name"`
			Status         *string `json:"status"`
			SendInvitation *bool   `json:"send_invitation"`
		}
		if err := c.Decode(&in); err != nil {
			return err
		}
		var f httpx.Fields
		addr, ok := validEmail(in.Email)
		if in.Email == "" {
			f.Add("email", "required")
		} else if !ok {
			f.Add("email", "invalid_format")
		}
		status := "invited"
		if in.Status != nil {
			status = *in.Status
			if status != "invited" && status != "active" {
				f.Enum("status", "invited", "active")
			}
		}
		if in.DisplayName != nil && len([]rune(*in.DisplayName)) > 100 {
			f.Max("display_name", "too_long", 100)
		}
		if err := f.Err(); err != nil {
			return err
		}
		if _, taken, err := c.emailTaken(addr); err != nil {
			return err
		} else if taken {
			return httpx.Conflict("email_taken", "A user with this email already exists.")
		}
		u, err := c.createUser(newUser{Email: addr, Status: status, DisplayName: deref(in.DisplayName)})
		if err != nil {
			return err
		}
		if status == "invited" && (in.SendInvitation == nil || *in.SendInvitation) {
			if err := c.sendInvitation(u.ID, u.Email, nil); err != nil {
				return err
			}
		}
		c.Versioned(u.Version)
		return c.Created(u)
	})
}

func (s *Server) UsersGet(w http.ResponseWriter, r *http.Request, id gen.UserIdOrMe) {
	s.serve(w, r, opts{op: "users.get"}, func(c *Call) error {
		uid, err := c.selfID(id)
		if err != nil {
			return err
		}
		u, err := c.visibleUser(uid)
		if err != nil {
			return err
		}
		if ok, err := c.canReadUser(u); err != nil {
			return err
		} else if !ok {
			return httpx.NotFoundResource("user")
		}
		c.Versioned(u.Version)
		return c.OK(u)
	})
}

func (s *Server) UsersUpdate(w http.ResponseWriter, r *http.Request, id gen.UserIdOrMe, params gen.UsersUpdateParams) {
	s.serve(w, r, opts{op: "users.update"}, func(c *Call) error {
		uid, err := c.selfID(id)
		if err != nil {
			return err
		}
		admin := c.can("users.manage")
		if !admin && !c.isSelf(uid) {
			return httpx.Forbidden("users.manage")
		}
		u, err := c.visibleUser(uid)
		if err != nil {
			return err
		}
		if err := httpx.CheckIfMatch(c.r, u.Version); err != nil {
			return err
		}
		p, err := c.Patch()
		if err != nil {
			return err
		}
		if err := p.RejectReadOnly("id", "email_verified", "pending_email", "mfa_enabled", "signup_application_id",
			"test_mode", "created_at", "updated_at", "last_login_at"); err != nil {
			return err
		}
		var f httpx.Fields
		p.RequireNonNull(&f, "email", "status")
		var newEmail, newStatus string
		if p.Has("email") && !p.IsNull("email") {
			var e string
			if p.Get("email", &e, &f) {
				if addr, ok := validEmail(e); ok {
					newEmail = addr
				} else {
					f.Add("email", "invalid_format")
				}
			}
		}
		if p.Has("status") && !p.IsNull("status") {
			if !admin {
				return httpx.E(403, "status_change_forbidden", "Only users.manage can change a User's status.")
			}
			if p.Get("status", &newStatus, &f) {
				switch newStatus {
				case "active", "invited", "suspended", "deleted":
				default:
					f.Enum("status", "active", "invited", "suspended", "deleted")
				}
			}
		}
		if err := f.Err(); err != nil {
			return err
		}
		if err := c.destructiveIf(newStatus == "suspended" && u.Status != "suspended"); err != nil {
			return err
		}
		if newEmail != "" && !strings.EqualFold(newEmail, u.Email) {
			if _, taken, err := c.emailTaken(newEmail); err != nil {
				return err
			} else if taken {
				return httpx.Conflict("email_taken", "A user with this email already exists.")
			}
		}
		if newStatus != "" && newStatus != u.Status {
			if err := c.transitionUser(u, newStatus, newEmail); err != nil {
				return err
			}
			if newEmail != "" {
				newEmail = "" // applied inside the restore
			}
		}
		if newEmail != "" && !strings.EqualFold(newEmail, u.Email) {
			if admin {
				if _, err := c.q.Exec(c.ctx, `update users set email = :e, email_verified = false, pending_email = null
					where id = :u`, db.Args{"e": newEmail, "u": u.ID}); err != nil {
					if _, ok := db.UniqueViolation(err); ok {
						return httpx.Conflict("email_taken", "A user with this email already exists.")
					}
					return err
				}
				if err := c.sendVerification(u.ID, newEmail, nil, nil); err != nil {
					return err
				}
				if err := c.audit(Audit{Action: "user.email_changed", TargetType: "user", TargetID: u.ID, TargetUserID: &u.ID,
					Before: map[string]any{"email": u.Email}, After: map[string]any{"email": newEmail}}); err != nil {
					return err
				}
			} else {
				if _, err := c.q.Exec(c.ctx, `update users set pending_email = :e where id = :u`,
					db.Args{"e": newEmail, "u": u.ID}); err != nil {
					return err
				}
				if err := c.sendVerification(u.ID, newEmail, nil, map[string]any{"new_email": newEmail}); err != nil {
					return err
				}
				c.notice(u.Email, "email_change_requested", "Your Substratal email is changing",
					"A change of your account's email address to "+newEmail+" was requested. If this wasn't you, contact support.")
			}
		}
		after, err := c.findUser("u.id = :id", db.Args{"id": u.ID})
		if err != nil {
			return err
		}
		c.Versioned(after.Version)
		return c.OK(after)
	})
}

// transitionUser applies a users.manage status change (Users → PATCH
// transition table), with its sessions, events, and audit.
func (c *Call) transitionUser(u userRow, to, restoreEmail string) error {
	bad := func() error {
		return httpx.Conflict("invalid_status_transition", "The status change from "+u.Status+" to "+to+" isn't allowed.")
	}
	switch {
	case to == "deleted", to == "invited":
		return bad()
	case u.Status == "active" && to == "suspended":
		return c.suspendUser(u, nil)
	case u.Status == "suspended" && to == "active":
		return c.reactivateUser(u, "user.reactivated", "")
	case u.Status == "invited" && to == "active":
		if _, err := c.q.Exec(c.ctx, `update users set status = 'active' where id = :u`, db.Args{"u": u.ID}); err != nil {
			return err
		}
		return c.audit(Audit{Action: "user.updated", TargetType: "user", TargetID: u.ID, TargetUserID: &u.ID,
			Before: map[string]any{"status": "invited"}, After: map[string]any{"status": "active"}})
	case u.Status == "deleted" && to == "active":
		var er struct {
			Status string `json:"status"`
		}
		err := db.One(c.ctx, c.q, &er, `select to_jsonb(e) from erasure_requests e where user_id = :u`, db.Args{"u": u.ID})
		if err == nil && er.Status == "scheduled" {
			return httpx.Conflict("erasure_scheduled", "Cancel the scheduled erasure before restoring this User.")
		}
		if err == nil && er.Status == "completed" {
			return httpx.NotFoundResource("user")
		}
		if err != nil && err != db.ErrNotFound {
			return err
		}
		email := u.Email
		if restoreEmail != "" {
			email = restoreEmail
		}
		if _, taken, err := c.emailTaken(email); err != nil {
			return err
		} else if taken {
			return httpx.Conflict("email_taken", "A new account has taken this email; change the email in the same request.")
		}
		return c.reactivateUser(u, "user.reactivated", restoreEmail)
	}
	return bad()
}

// suspendUser: revoke every session and fire access.revoked for every
// Application the User had access to; Entitlements are untouched.
func (c *Call) suspendUser(u userRow, reason *string) error {
	pairs, err := c.userAppPairs(u.ID)
	if err != nil {
		return err
	}
	watch, err := c.watchAccess(pairs)
	if err != nil {
		return err
	}
	if _, err := c.q.Exec(c.ctx, `update users set status = 'suspended' where id = :u`, db.Args{"u": u.ID}); err != nil {
		return err
	}
	if err := c.revokeSessions(u.ID, "suspended", ""); err != nil {
		return err
	}
	if err := c.flip(watch, "user_reactivated", "user_suspended"); err != nil {
		return err
	}
	after := map[string]any{"status": "suspended"}
	if reason != nil {
		after["reason"] = *reason
	}
	if err := c.emit("user.suspended", nil, map[string]any{"user_id": u.ID}); err != nil {
		return err
	}
	return c.audit(Audit{Action: "user.suspended", TargetType: "user", TargetID: u.ID, TargetUserID: &u.ID,
		Before: map[string]any{"status": u.Status}, After: after})
}

func (c *Call) reactivateUser(u userRow, action, newEmail string) error {
	pairs, err := c.userAppPairs(u.ID)
	if err != nil {
		return err
	}
	watch, err := c.watchAccess(pairs)
	if err != nil {
		return err
	}
	if newEmail != "" {
		_, err = c.q.Exec(c.ctx, `update users set status = 'active', deleted_at = null, email = :e, email_verified = false
			where id = :u`, db.Args{"u": u.ID, "e": newEmail})
	} else {
		_, err = c.q.Exec(c.ctx, `update users set status = 'active', deleted_at = null where id = :u`, db.Args{"u": u.ID})
	}
	if err != nil {
		if _, ok := db.UniqueViolation(err); ok {
			return httpx.Conflict("email_taken", "A new account has taken this email; change the email in the same request.")
		}
		return err
	}
	if err := c.flip(watch, "user_reactivated", "user_suspended"); err != nil {
		return err
	}
	if err := c.emit("user.reactivated", nil, map[string]any{"user_id": u.ID}); err != nil {
		return err
	}
	return c.audit(Audit{Action: action, TargetType: "user", TargetID: u.ID, TargetUserID: &u.ID,
		Before: map[string]any{"status": u.Status}, After: map[string]any{"status": "active"}})
}

// softDeleteUser is DELETE /v1/users/{id}'s effect, shared with erasure.
func (c *Call) softDeleteUser(u userRow) error {
	pairs, err := c.userAppPairs(u.ID)
	if err != nil {
		return err
	}
	watch, err := c.watchAccess(pairs)
	if err != nil {
		return err
	}
	if _, err := c.q.Exec(c.ctx, `update users set status = 'deleted', deleted_at = now() where id = :u`, db.Args{"u": u.ID}); err != nil {
		return err
	}
	if err := c.revokeSessions(u.ID, "deleted", ""); err != nil {
		return err
	}
	if err := c.flip(watch, "user_reactivated", "user_deleted"); err != nil {
		return err
	}
	if err := c.emit("user.deleted", nil, map[string]any{"user_id": u.ID}); err != nil {
		return err
	}
	return c.audit(Audit{Action: "user.deleted", TargetType: "user", TargetID: u.ID, TargetUserID: &u.ID,
		Before: map[string]any{"status": u.Status}, After: map[string]any{"status": "deleted"}})
}

func (s *Server) UsersDelete(w http.ResponseWriter, r *http.Request, id gen.UserIdOrMe) {
	s.serve(w, r, opts{op: "users.delete"}, func(c *Call) error {
		uid, err := c.selfID(id)
		if err != nil {
			return err
		}
		if !c.isSelf(uid) && !c.can("users.manage") {
			return httpx.Forbidden("users.manage")
		}
		u, err := c.visibleUser(uid)
		if err != nil {
			return err
		}
		if err := httpx.CheckIfMatch(c.r, u.Version); err != nil {
			return err
		}
		if u.Status == "deleted" {
			return c.NoContent()
		}
		if err := c.softDeleteUser(u); err != nil {
			return err
		}
		return c.NoContent()
	})
}

func (s *Server) UsersSuspend(w http.ResponseWriter, r *http.Request, id gen.UserIdOrMe) {
	s.serve(w, r, opts{op: "users.suspend"}, func(c *Call) error {
		if err := c.require("users.manage"); err != nil {
			return err
		}
		uid, err := c.selfID(id)
		if err != nil {
			return err
		}
		var in struct {
			Reason *string `json:"reason"`
		}
		if err := c.Decode(&in); err != nil {
			return err
		}
		if in.Reason != nil && len([]rune(*in.Reason)) > 2000 {
			var f httpx.Fields
			f.Max("reason", "too_long", 2000)
			return f.Err()
		}
		u, err := c.visibleUser(uid)
		if err != nil {
			return err
		}
		switch u.Status {
		case "suspended":
		case "active":
			if err := c.suspendUser(u, in.Reason); err != nil {
				return err
			}
		default:
			return httpx.Conflict("invalid_status_transition", "Only an active User can be suspended.")
		}
		after, err := c.findUser("u.id = :id", db.Args{"id": u.ID})
		if err != nil {
			return err
		}
		c.Versioned(after.Version)
		return c.OK(after)
	})
}

func (s *Server) UsersResendInvitation(w http.ResponseWriter, r *http.Request, id gen.UserIdOrMe) {
	s.serve(w, r, opts{op: "users.resendInvitation"}, func(c *Call) error {
		if err := c.require("users.manage"); err != nil {
			return err
		}
		uid, err := c.selfID(id)
		if err != nil {
			return err
		}
		u, err := c.visibleUser(uid)
		if err != nil {
			return err
		}
		if u.Status != "invited" {
			return httpx.Conflict("invalid_status_transition", "Only an invited User can be re-sent an invitation.")
		}
		if err := c.sendInvitation(u.ID, u.Email, u.SignupApplicationID); err != nil {
			return err
		}
		return c.Accepted(nil)
	})
}

// ── Erasure (NFR → Hard-delete cascade) ────────────────────────────────

type erasureRow struct {
	UserID       string    `json:"user_id"`
	Status       string    `json:"status"`
	RequestedAt  time.Time `json:"requested_at"`
	ScheduledFor time.Time `json:"scheduled_for"`
}

func (s *Server) UsersRequestErasure(w http.ResponseWriter, r *http.Request, id gen.UserIdOrMe) {
	s.serve(w, r, opts{op: "users.requestErasure", idem: idemOptional}, func(c *Call) error {
		uid, err := c.selfID(id)
		if err != nil {
			return err
		}
		if !c.isSelf(uid) && !c.can("users.manage") {
			return httpx.Forbidden("users.manage")
		}
		var in struct {
			Reason *string `json:"reason"`
		}
		if err := c.Decode(&in); err != nil {
			return err
		}
		u, err := c.visibleUser(uid)
		if err != nil {
			return err
		}
		var existing erasureRow
		err = db.One(c.ctx, c.q, &existing, `select jsonb_build_object('user_id', user_id, 'status', status,
			'requested_at', requested_at, 'scheduled_for', scheduled_for) from erasure_requests where user_id = :u`,
			db.Args{"u": uid})
		if err == nil && existing.Status == "scheduled" {
			return c.Accepted(existing)
		}
		if err == nil && existing.Status == "completed" {
			return httpx.NotFoundResource("user")
		}
		if err != nil && err != db.ErrNotFound {
			return err
		}
		if u.Status != "deleted" {
			if err := c.softDeleteUser(u); err != nil {
				return err
			}
		}
		typ, actorID := c.actor()
		var er erasureRow
		if err := db.One(c.ctx, c.q, &er, `insert into erasure_requests (user_id, requested_by_type, requested_by_id, reason,
				requested_at, scheduled_for, status)
			values (:u, :t, :a, :r, now(), now() + interval '7 days', 'scheduled')
			on conflict (user_id) do update set requested_by_type = excluded.requested_by_type,
				requested_by_id = excluded.requested_by_id, reason = excluded.reason,
				requested_at = now(), scheduled_for = now() + interval '7 days', status = 'scheduled', completed_at = null
			returning jsonb_build_object('user_id', user_id, 'status', status, 'requested_at', requested_at,
				'scheduled_for', scheduled_for)`,
			db.Args{"u": uid, "t": typ, "a": actorID, "r": in.Reason}); err != nil {
			return err
		}
		if err := c.audit(Audit{Action: "user.erasure_requested", TargetType: "user", TargetID: uid, TargetUserID: &uid,
			After: map[string]any{"scheduled_for": er.ScheduledFor}}); err != nil {
			return err
		}
		return c.Accepted(er)
	})
}

func (s *Server) UsersCancelErasure(w http.ResponseWriter, r *http.Request, id gen.UserIdOrMe) {
	s.serve(w, r, opts{op: "users.cancelErasure"}, func(c *Call) error {
		if err := c.require("users.manage"); err != nil {
			return err
		}
		uid, err := c.selfID(id)
		if err != nil {
			return err
		}
		if _, err := c.visibleUser(uid); err != nil {
			return err
		}
		n, err := c.q.Exec(c.ctx, `update erasure_requests set status = 'cancelled'
			where user_id = :u and status = 'scheduled'`, db.Args{"u": uid})
		if err != nil {
			return err
		}
		if n == 0 {
			return httpx.E(404, "erasure_not_scheduled", "No erasure is scheduled for this User.")
		}
		if err := c.audit(Audit{Action: "user.erasure_cancelled", TargetType: "user", TargetID: uid, TargetUserID: &uid}); err != nil {
			return err
		}
		return c.NoContent()
	})
}
