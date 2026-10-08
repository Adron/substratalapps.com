package core

import (
	"net/mail"
	"strings"

	"github.com/CompositeCode/substratalapps.com/internal/db"
	"github.com/CompositeCode/substratalapps.com/internal/httpx"
	"github.com/CompositeCode/substratalapps.com/internal/ids"
)

// newUser is everything createUser needs. Signup, POST /v1/users, and an
// Organization inviting an unknown email all create Users the same way.
type newUser struct {
	Email        string
	Status       string // active | invited
	DisplayName  string
	SignupApp    *string
	EmailVerify  bool // email_verified at creation
	PasswordHash string
	ActorType    string // system for signup
	ActorID      string
}

// validEmail checks an address's syntax and normalizes it.
func validEmail(e string) (string, bool) {
	e = strings.TrimSpace(e)
	a, err := mail.ParseAddress(e)
	if err != nil || a.Address != e || len(e) > 255 || !strings.Contains(e[strings.LastIndex(e, "@"):], ".") {
		return "", false
	}
	return e, true
}

// emailTaken returns the existing live (non-deleted) User with this email
// in the transaction's mode, if any.
func (c *Call) emailTaken(addr string) (userRow, bool, error) {
	u, err := c.findUser("u.email = :e and u.deleted_at is null", db.Args{"e": addr})
	if err == db.ErrNotFound {
		return u, false, nil
	}
	return u, err == nil, err
}

// createUser creates the User, an optional password identity, a default
// Profile and Settings, and the member platform Role, in the current
// transaction (NFR → Transaction boundaries), and writes user.created.
func (c *Call) createUser(n newUser) (userRow, error) {
	id := ids.New(ids.User)
	display := n.DisplayName
	if display == "" {
		display = n.Email[:strings.Index(n.Email, "@")]
	}
	tm := c.testMode()
	if _, err := c.q.Exec(c.ctx, `insert into users (id, email, email_verified, status, signup_application_id, test_mode)
		values (:id, :e, :v, :st, :app, :tm)`,
		db.Args{"id": id, "e": n.Email, "v": n.EmailVerify, "st": n.Status, "app": n.SignupApp, "tm": tm}); err != nil {
		if _, ok := db.UniqueViolation(err); ok {
			return userRow{}, httpx.Conflict("email_taken", "A user with this email already exists.")
		}
		return userRow{}, err
	}
	if n.PasswordHash != "" {
		if _, err := c.q.Exec(c.ctx, `insert into user_identities (id, user_id, method, password_hash)
			values (:id, :u, 'password', :h)`, db.Args{"id": ids.New(ids.UserIdentity), "u": id, "h": n.PasswordHash}); err != nil {
			return userRow{}, err
		}
	}
	if _, err := c.q.Exec(c.ctx, `insert into profiles (user_id, display_name, test_mode) values (:u, :d, :tm)`,
		db.Args{"u": id, "d": truncate(display, 100), "tm": tm}); err != nil {
		return userRow{}, err
	}
	if _, err := c.q.Exec(c.ctx, `insert into settings (user_id, test_mode) values (:u, :tm)`,
		db.Args{"u": id, "tm": tm}); err != nil {
		return userRow{}, err
	}
	if _, err := c.q.Exec(c.ctx, `insert into user_role_assignments (user_id, role_id, application_id,
			assigned_by_type, assigned_by_id, test_mode)
		values (:u, 'role_platform_member', null, 'system', 'system', :tm)`, db.Args{"u": id, "tm": tm}); err != nil {
		return userRow{}, err
	}
	u, err := c.findUser("u.id = :id", db.Args{"id": id})
	if err != nil {
		return u, err
	}
	a := Audit{Action: "user.created", TargetType: "user", TargetID: id, TargetUserID: &id, After: u}
	if n.ActorType != "" {
		a.ActorType, a.ActorID = n.ActorType, n.ActorID
	}
	return u, c.audit(a)
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) > n {
		return string(r[:n])
	}
	return s
}
