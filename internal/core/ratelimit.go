package core

import (
	"strconv"
	"strings"
	"time"

	"github.com/Adron/substratalapps.com/internal/db"
	"github.com/Adron/substratalapps.com/internal/httpx"
)

// limit is one rate-limit rule (NFR → Rate limiting).
type limit struct {
	n      int
	window time.Duration
}

var (
	perMinute = func(n int) limit { return limit{n, time.Minute} }
	perHour   = func(n int) limit { return limit{n, time.Hour} }
	perDay    = func(n int) limit { return limit{n, 24 * time.Hour} }
)

// principalLimits overrides the default 100/min per API Key or session.
var principalLimits = map[string]limit{
	"users.create":                   perMinute(20),
	"entitlements.grant":             perMinute(20),
	"organizations.grantEntitlement": perMinute(20),
	"permissions.getEffective":       perMinute(300),
	"auth.createAppToken":            perMinute(300),
}

// publicLimits are per client IP on unauthenticated endpoints.
var publicLimits = map[string]limit{
	"auth.login":                   perMinute(10),
	"auth.signup":                  perMinute(10),
	"auth.verifyMfa":               perMinute(10),
	"auth.forgotPassword":          perMinute(5),
	"auth.resendEmailVerification": perMinute(5),
	"auth.exchangeOAuthToken":      perMinute(300),
	"auth.refreshToken":            perMinute(60), // plus 30/min per session, in the handler
}

const defaultPrincipalLimit = 100
const defaultPublicLimit = 20

// hit increments bucket's counter for the current window and reports
// whether the request is within n. Counters are written with autocommit so
// a request that fails (and rolls back) still counts.
func (c *Call) hit(bucket string, l limit, setHeaders bool) error {
	if c.s.DisableRateLimits {
		return nil
	}
	window := c.now.Truncate(l.window)
	var count int
	rows, err := c.s.DB.Autocommit().Query(c.ctx, `insert into rate_limit_counters (bucket, window_start, count)
		values (:b, :w::timestamptz, 1)
		on conflict (bucket, window_start) do update set count = rate_limit_counters.count + 1
		returning to_jsonb(count)`, db.Args{"b": bucket, "w": db.Time(window)})
	if err != nil {
		return err
	}
	if len(rows) == 1 {
		count, _ = strconv.Atoi(strings.TrimSpace(string(rows[0])))
	}
	reset := int(window.Add(l.window).Sub(c.now).Seconds()) + 1
	if setHeaders {
		remaining := l.n - count
		if remaining < 0 {
			remaining = 0
		}
		c.headers.Set("RateLimit-Limit", strconv.Itoa(l.n))
		c.headers.Set("RateLimit-Remaining", strconv.Itoa(remaining))
		c.headers.Set("RateLimit-Reset", strconv.Itoa(reset))
	}
	if count > l.n {
		return httpx.E(429, "rate_limited", "Too many requests. Retry after the window resets.").RetryAfter(reset)
	}
	return nil
}

func (c *Call) rateLimitPublic() error {
	l, ok := publicLimits[c.op.op]
	if !ok {
		l = perMinute(defaultPublicLimit)
	}
	return c.hit("ip:"+c.ip+":"+c.op.op, l, true)
}

func (c *Call) rateLimitPrincipal() error {
	key := c.p.ID
	if c.p.IsUser() {
		key = c.p.SessionID
	}
	if l, ok := principalLimits[c.op.op]; ok {
		return c.hit(key+":"+c.op.op, l, true)
	}
	return c.hit(key, perMinute(defaultPrincipalLimit), true)
}

// peek reads a bucket's count for the current window without counting
// this request (failure-only limits, such as MFA disable attempts).
func (c *Call) peek(bucket string, l limit) (int, error) {
	if c.s.DisableRateLimits {
		return 0, nil
	}
	window := c.now.Truncate(l.window)
	rows, err := c.s.DB.Autocommit().Query(c.ctx, `select to_jsonb(count) from rate_limit_counters
		where bucket = :b and window_start = :w::timestamptz`, db.Args{"b": bucket, "w": db.Time(window)})
	if err != nil || len(rows) == 0 {
		return 0, err
	}
	n, _ := strconv.Atoi(strings.TrimSpace(string(rows[0])))
	return n, nil
}
