package core

import (
	"context"
	"fmt"
	"net/url"
	"time"

	"github.com/CompositeCode/substratalapps.com/internal/db"
	"github.com/CompositeCode/substratalapps.com/internal/email"
	"github.com/CompositeCode/substratalapps.com/internal/ids"
)

// mail queues a transactional email to send after the transaction commits.
// A send failure is logged, never surfaced: the change already happened,
// and every email here can be re-requested (resend, forgot, invitation).
func (c *Call) mail(m email.Message) {
	c.After(func(ctx context.Context) {
		if err := c.s.Email.Send(ctx, m); err != nil {
			c.s.Log.Error("email send failed", "kind", m.Kind, "request_id", c.requestID, "err", err)
		}
	})
}

// link builds a dashboard link carrying a single-use token.
func (c *Call) link(path, token string) string {
	return c.s.DashboardURL + path + "?token=" + url.QueryEscape(token)
}

// appBranding returns an Application's email_from_name for messages
// triggered from inside it (Auth → Email verification).
func (c *Call) appBranding(appID *string) string {
	if appID == nil {
		return ""
	}
	a, err := c.loadApp(*appID)
	if err != nil || a.EmailFromName == nil {
		return ""
	}
	return *a.EmailFromName
}

// issueToken stores a single-use emailed/challenge token (hash only) and
// invalidates the user's earlier unconsumed tokens of the same kind.
func (c *Call) issueToken(kind, prefix, userID string, ttl time.Duration, payload map[string]any) (string, error) {
	if _, err := c.q.Exec(c.ctx, `update auth_tokens set consumed_at = now()
		where user_id = :u and kind = :k and consumed_at is null`, db.Args{"u": userID, "k": kind}); err != nil {
		return "", err
	}
	tok := ids.Secret(prefix)
	if payload == nil {
		payload = map[string]any{}
	}
	_, err := c.q.Exec(c.ctx, `insert into auth_tokens (token_hash, kind, user_id, payload, expires_at)
		values (:h, :k, :u, :p::jsonb, :exp::timestamptz)`,
		db.Args{"h": ids.Hash(tok), "k": kind, "u": userID, "p": db.JSON(payload), "exp": db.Time(c.now.Add(ttl))})
	return tok, err
}

func (c *Call) sendVerification(userID, to string, appID *string, payload map[string]any) error {
	tok, err := c.issueToken("email_verify", ids.EmailVerify, userID, 24*time.Hour, payload)
	if err != nil {
		return err
	}
	c.mail(email.Message{To: to, FromName: c.appBranding(appID), Kind: "email_verify",
		Subject: "Verify your email address",
		Text:    fmt.Sprintf("Confirm this email address for your Substratal account:\n\n%s\n\nThis link expires in 24 hours.", c.link("/verify-email", tok))})
	return nil
}

func (c *Call) sendInvitation(userID, to string, appID *string) error {
	tok, err := c.issueToken("invitation", ids.Invitation, userID, 7*24*time.Hour, nil)
	if err != nil {
		return err
	}
	c.mail(email.Message{To: to, FromName: c.appBranding(appID), Kind: "invitation",
		Subject: "You've been invited to Substratal",
		Text:    fmt.Sprintf("Set your password and activate your account:\n\n%s\n\nThis invitation expires in 7 days.", c.link("/accept-invitation", tok))})
	return nil
}

func (c *Call) notice(to, kind, subject, text string) {
	c.mail(email.Message{To: to, Kind: kind, Subject: subject, Text: text})
}
