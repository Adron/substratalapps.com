package core

import (
	"context"
	"fmt"

	"github.com/CompositeCode/substratalapps.com/internal/auth"
	"github.com/CompositeCode/substratalapps.com/internal/db"
	"github.com/CompositeCode/substratalapps.com/internal/ids"
)

// BootstrapSuperadmin creates (or promotes) the first superadmin directly
// against the database. The public API can't, by design: nothing can hold
// roles.manage before a superadmin exists to grant it (NFR →
// Authentication → Bootstrapping). Only cmd/admin and tests call this.
func (s *Server) BootstrapSuperadmin(ctx context.Context, email, password string) (string, error) {
	addr, ok := validEmail(email)
	if !ok {
		return "", fmt.Errorf("bootstrap: invalid email %q", email)
	}
	if err := auth.CheckPolicy(password, addr); err != nil {
		return "", fmt.Errorf("bootstrap: %w", err)
	}
	hash, err := auth.HashPassword(password)
	if err != nil {
		return "", err
	}
	var userID string
	err = s.system(ctx, false, db.Settings{}, func(c *Call) error {
		u, err := c.findUser("u.email = :e and u.deleted_at is null", db.Args{"e": addr})
		switch err {
		case nil:
			userID = u.ID
			if _, err := c.setPassword(u.ID, hash); err != nil {
				return err
			}
			if _, err := c.q.Exec(ctx, `update users set status = 'active', email_verified = true where id = :u`, db.Args{"u": u.ID}); err != nil {
				return err
			}
		case db.ErrNotFound:
			nu, err := c.createUser(newUser{Email: addr, Status: "active", EmailVerify: true, PasswordHash: hash,
				ActorType: "system", ActorID: "system"})
			if err != nil {
				return err
			}
			userID = nu.ID
		default:
			return err
		}
		if _, err := c.q.Exec(ctx, `insert into user_role_assignments (user_id, role_id, application_id, assigned_by_type, assigned_by_id)
			values (:u, 'role_platform_superadmin', null, 'system', 'system') on conflict do nothing`, db.Args{"u": userID}); err != nil {
			return err
		}
		return c.audit(Audit{Action: "role.assigned", TargetType: "role_assignment", TargetID: userID + ":role_platform_superadmin",
			TargetUserID: &userID, ActorType: "system", ActorID: "system",
			After: map[string]any{"user_id": userID, "role_id": "role_platform_superadmin", "application_id": nil}})
	})
	return userID, err
}

var _ = ids.New
