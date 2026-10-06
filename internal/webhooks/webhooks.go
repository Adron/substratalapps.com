// Package webhooks delivers outbound webhook events (API Reference →
// Webhooks → Delivery): it fans committed outbox events out to matching
// subscriptions, signs and POSTs each delivery, and runs the retry and
// subscription-health rules.
//
// It's driven the same way everywhere: the API nudges it after a commit
// (SQS in AWS, an in-process channel locally), and a scheduled sweep picks
// up anything a nudge missed, so the outbox is the only source of truth.
package webhooks

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/Adron/substratalapps.com/internal/auth"
	"github.com/Adron/substratalapps.com/internal/core"
	"github.com/Adron/substratalapps.com/internal/db"
	"github.com/Adron/substratalapps.com/internal/email"
	"github.com/Adron/substratalapps.com/internal/ids"
)

// RetrySchedule is the delay before attempts 2 through 6.
var RetrySchedule = []time.Duration{time.Minute, 5 * time.Minute, 30 * time.Minute, 2 * time.Hour, 12 * time.Hour}

const (
	maxAttempts      = 6
	unhealthyAfter   = 3
	disableAfterDays = 5
	leaseFor         = 2 * time.Minute
	timeout          = 5 * time.Second
)

// Deliverer runs dispatch and delivery.
type Deliverer struct {
	DB        db.DB
	Encrypter auth.Encrypter
	Email     email.Sender
	Log       *slog.Logger
	Client    *http.Client
	Now       func() time.Time
	// AllowPrivate skips the send-time public-address check (local only).
	AllowPrivate bool
}

// New returns a Deliverer with a client that never follows redirects.
func New(d db.DB, enc auth.Encrypter, mail email.Sender, log *slog.Logger) *Deliverer {
	return &Deliverer{DB: d, Encrypter: enc, Email: mail, Log: log, Now: time.Now,
		Client: &http.Client{Timeout: timeout, CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		}}}
}

type event struct {
	ID            string          `json:"id"`
	Type          string          `json:"type"`
	ApplicationID *string         `json:"application_id"`
	Payload       json.RawMessage `json:"payload"`
	TestMode      bool            `json:"test_mode"`
	CreatedAt     time.Time       `json:"created_at"`
}

// Dispatch fans every undispatched outbox event out to its subscriptions,
// creating attempt-1 deliveries. It returns how many events it handled.
func (d *Deliverer) Dispatch(ctx context.Context, limit int) (int, error) {
	handled := 0
	err := d.DB.Tx(ctx, db.Settings{AllModes: true}, func(q db.Querier) error {
		evs, err := db.All[event](ctx, q, `select to_jsonb(e) from webhook_events e
			where dispatched_at is null order by created_at limit :lim for update skip locked`, db.Args{"lim": int64(limit)})
		if err != nil {
			return err
		}
		for _, ev := range evs {
			subs, err := matchingSubscriptions(ctx, q, ev)
			if err != nil {
				return err
			}
			for _, sid := range subs {
				if _, err := q.Exec(ctx, `insert into webhook_deliveries (id, subscription_id, event_id, attempt, status, next_retry_at, test_mode)
					values (:id, :s, :e, 1, 'pending', now(), :tm)`,
					db.Args{"id": ids.New(ids.Delivery), "s": sid, "e": ev.ID, "tm": ev.TestMode}); err != nil {
					return err
				}
			}
			if _, err := q.Exec(ctx, `update webhook_events set dispatched_at = now() where id = :id`, db.Args{"id": ev.ID}); err != nil {
				return err
			}
			handled++
		}
		return nil
	})
	return handled, err
}

// matchingSubscriptions applies "Who owns a subscription, and what it
// receives": platform subscriptions get every event; an app-scoped one
// gets events for its Application, and user.* / organization.member_*
// events only when the user has an access path to that Application. Test
// and live never cross.
func matchingSubscriptions(ctx context.Context, q db.Querier, ev event) ([]string, error) {
	var data struct {
		UserID string `json:"user_id"`
	}
	_ = json.Unmarshal(ev.Payload, &data)
	userScoped := ev.ApplicationID == nil && data.UserID != "" &&
		(strings.HasPrefix(ev.Type, "user.") || strings.HasPrefix(ev.Type, "organization.member_"))
	return db.All[string](ctx, q, `select to_jsonb(w.id) from webhook_subscriptions w
		where w.test_mode = :tm and w.status <> 'disabled'
		  and (w.events @> array[:type]::text[] or w.events = '{*}')
		  and (w.scope = 'platform'
		       or (:app::text is not null and w.scope = :app)
		       or (:userscoped and (
		            exists (select 1 from entitlements e where e.user_id = :u and e.application_id = w.scope)
		            or exists (select 1 from entitlements e join organization_memberships m on m.organization_id = e.organization_id
		                       where m.user_id = :u and e.application_id = w.scope))))
		order by w.id`,
		db.Args{"tm": ev.TestMode, "type": ev.Type, "app": ev.ApplicationID, "userscoped": userScoped, "u": data.UserID})
}

type due struct {
	ID             string     `json:"id"`
	SubscriptionID string     `json:"subscription_id"`
	EventID        string     `json:"event_id"`
	Attempt        int        `json:"attempt"`
	URL            string     `json:"url"`
	APIVersion     string     `json:"api_version"`
	Secret         string     `json:"secret_ciphertext"`
	PrevSecret     *string    `json:"previous_secret_ciphertext"`
	PrevExpires    *time.Time `json:"previous_secret_expires_at"`
	SubStatus      string     `json:"sub_status"`
	Event          event      `json:"event"`
}

// DeliverDue sends every pending delivery whose time has come. Each is
// claimed with a short lease in its own transaction, so no transaction is
// held open across the HTTP call. It returns how many it attempted.
func (d *Deliverer) DeliverDue(ctx context.Context, limit int) (int, error) {
	var batch []due
	err := d.DB.Tx(ctx, db.Settings{AllModes: true}, func(q db.Querier) error {
		var err error
		batch, err = db.All[due](ctx, q, `select jsonb_build_object('id', d.id, 'subscription_id', d.subscription_id,
				'event_id', d.event_id, 'attempt', d.attempt, 'url', w.url, 'api_version', w.api_version,
				'secret_ciphertext', w.secret_ciphertext, 'previous_secret_ciphertext', w.previous_secret_ciphertext,
				'previous_secret_expires_at', w.previous_secret_expires_at, 'sub_status', w.status, 'event', to_jsonb(e))
			from webhook_deliveries d
			join webhook_subscriptions w on w.id = d.subscription_id
			join webhook_events e on e.id = d.event_id
			where d.status = 'pending' and d.next_retry_at <= now()
			order by d.next_retry_at limit :lim for update of d skip locked`, db.Args{"lim": int64(limit)})
		if err != nil {
			return err
		}
		for _, x := range batch {
			if _, err := q.Exec(ctx, `update webhook_deliveries set next_retry_at = now() + :lease::interval where id = :id`,
				db.Args{"lease": fmt.Sprintf("%d seconds", int(leaseFor.Seconds())), "id": x.ID}); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	for _, x := range batch {
		d.deliver(ctx, x)
	}
	return len(batch), nil
}

// Envelope builds the body POSTed to a subscriber for its api_version.
// 2026-10-05 is the only published version, so there's one shape.
func Envelope(ev event, apiVersion string) []byte {
	var data any
	_ = json.Unmarshal(ev.Payload, &data)
	b, _ := json.Marshal(map[string]any{
		"id": ev.ID, "type": ev.Type, "api_version": apiVersion,
		"created_at": ev.CreatedAt.UTC().Format(time.RFC3339), "application_id": ev.ApplicationID,
		"test_mode": ev.TestMode, "data": data,
	})
	return b
}

// Sign returns the Substratal-Signature header value: t=<unix>,v1=<hex>
// for each secret (two during a rotation overlap).
func Sign(body []byte, t time.Time, secrets ...string) string {
	ts := strconv.FormatInt(t.Unix(), 10)
	parts := []string{"t=" + ts}
	for _, s := range secrets {
		mac := hmac.New(sha256.New, []byte(s))
		mac.Write([]byte(ts + "."))
		mac.Write(body)
		parts = append(parts, "v1="+hex.EncodeToString(mac.Sum(nil)))
	}
	return strings.Join(parts, ",")
}

func (d *Deliverer) deliver(ctx context.Context, x due) {
	now := d.Now()
	isTest := x.Event.Type == "webhook.test"
	if x.SubStatus == "disabled" && !isTest {
		d.finish(ctx, x, "failed", nil, 0, strp("subscription disabled"), false)
		return
	}
	secrets, err := d.secrets(ctx, x, now)
	if err != nil {
		d.Log.Error("webhook secret decrypt failed", "subscription", x.SubscriptionID, "err", err)
		d.finish(ctx, x, "failed", nil, 0, strp("internal signing error"), !isTest)
		return
	}
	body := Envelope(x.Event, x.APIVersion)
	start := time.Now()
	status, errText := d.post(ctx, x, body, secrets, now)
	dur := int(time.Since(start).Milliseconds())
	switch {
	case status >= 200 && status < 300:
		d.finish(ctx, x, "succeeded", &status, dur, nil, false)
	case status == http.StatusGone:
		d.finish(ctx, x, "failed", &status, dur, strp("410 Gone: subscription disabled"), false)
		d.disable(ctx, x.SubscriptionID, "410")
	default:
		d.finish(ctx, x, "failed", statusOrNil(status), dur, &errText, !isTest)
	}
}

func statusOrNil(s int) *int {
	if s == 0 {
		return nil
	}
	return &s
}

func strp(s string) *string { return &s }

func (d *Deliverer) secrets(ctx context.Context, x due, now time.Time) ([]string, error) {
	cur, err := d.Encrypter.Decrypt(ctx, x.Secret)
	if err != nil {
		return nil, err
	}
	out := []string{cur}
	if x.PrevSecret != nil && x.PrevExpires != nil && x.PrevExpires.After(now) {
		if prev, err := d.Encrypter.Decrypt(ctx, *x.PrevSecret); err == nil {
			out = append(out, prev)
		}
	}
	return out, nil
}

func (d *Deliverer) post(ctx context.Context, x due, body []byte, secrets []string, now time.Time) (int, string) {
	u, err := url.Parse(x.URL)
	if err != nil {
		return 0, "invalid url"
	}
	if !d.AllowPrivate {
		if u.Scheme != "https" {
			return 0, "url is not https"
		}
		if err := core.CheckPublicHost(ctx, u.Hostname()); err != nil {
			return 0, "url resolves to a non-public address"
		}
	}
	rctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(rctx, http.MethodPost, x.URL, bytes.NewReader(body))
	if err != nil {
		return 0, err.Error()
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "Substratal-Webhooks/1.0")
	req.Header.Set("Substratal-Event-Id", x.Event.ID)
	req.Header.Set("Substratal-Event-Type", x.Event.Type)
	req.Header.Set("Substratal-Delivery-Id", x.ID)
	req.Header.Set("Substratal-Delivery-Attempt", strconv.Itoa(x.Attempt))
	req.Header.Set("Substratal-Signature", Sign(body, now, secrets...))
	resp, err := d.Client.Do(req)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) || strings.Contains(err.Error(), "Timeout") {
			return 0, "timeout"
		}
		return 0, truncate(err.Error(), 1024)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
		return resp.StatusCode, ""
	}
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
	return resp.StatusCode, truncate(fmt.Sprintf("HTTP %d: %s", resp.StatusCode, b), 1024)
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

// finish records an attempt's outcome, schedules the next attempt on a
// retryable failure, and updates subscription health. countsTowardHealth
// is false for test events and for 410 handling.
func (d *Deliverer) finish(ctx context.Context, x due, status string, code *int, dur int, errText *string, countsTowardHealth bool) {
	err := d.DB.Tx(ctx, db.Settings{AllModes: true}, func(q db.Querier) error {
		var durArg any
		if dur > 0 {
			durArg = int64(dur)
		}
		var codeArg any
		if code != nil {
			codeArg = int64(*code)
		}
		if _, err := q.Exec(ctx, `update webhook_deliveries set status = :st, response_status = :code, duration_ms = :dur,
			error = :err, next_retry_at = null where id = :id`,
			db.Args{"st": status, "code": codeArg, "dur": durArg, "err": errText, "id": x.ID}); err != nil {
			return err
		}
		if _, err := q.Exec(ctx, `update webhook_subscriptions set last_delivery_at = now() where id = :s`,
			db.Args{"s": x.SubscriptionID}); err != nil {
			return err
		}
		if status == "succeeded" {
			if x.Event.Type != "webhook.test" {
				_, err := q.Exec(ctx, `update webhook_subscriptions set consecutive_failures = 0, unhealthy_since = null,
					status = case when status = 'unhealthy' then 'healthy' else status end where id = :s`, db.Args{"s": x.SubscriptionID})
				return err
			}
			return nil
		}
		if !countsTowardHealth {
			return nil
		}
		if x.Attempt < maxAttempts {
			next := d.Now().Add(RetrySchedule[x.Attempt-1])
			_, err := q.Exec(ctx, `insert into webhook_deliveries (id, subscription_id, event_id, attempt, status, next_retry_at, test_mode)
				values (:id, :s, :e, :a, 'pending', :at::timestamptz, (select test_mode from webhook_events where id = :e))`,
				db.Args{"id": ids.New(ids.Delivery), "s": x.SubscriptionID, "e": x.EventID, "a": int64(x.Attempt + 1),
					"at": db.Time(next)})
			if err == nil {
				_, err = q.Exec(ctx, `update webhook_deliveries set next_retry_at = :at::timestamptz where id = :id`,
					db.Args{"at": db.Time(next), "id": x.ID})
			}
			return err
		}
		// Every retry exhausted: this delivery counts toward unhealthy.
		_, err := q.Exec(ctx, `update webhook_subscriptions set consecutive_failures = consecutive_failures + 1,
			status = case when consecutive_failures + 1 >= :n and status = 'healthy' then 'unhealthy' else status end,
			unhealthy_since = case when consecutive_failures + 1 >= :n and unhealthy_since is null then now() else unhealthy_since end
			where id = :s`, db.Args{"n": int64(unhealthyAfter), "s": x.SubscriptionID})
		return err
	})
	if err != nil {
		d.Log.Error("record webhook delivery", "delivery", x.ID, "err", err)
	}
}

func (d *Deliverer) disable(ctx context.Context, subID, why string) {
	err := d.DB.Tx(ctx, db.Settings{AllModes: true}, func(q db.Querier) error {
		_, err := q.Exec(ctx, `update webhook_subscriptions set status = 'disabled' where id = :s`, db.Args{"s": subID})
		return err
	})
	if err != nil {
		d.Log.Error("disable webhook subscription", "subscription", subID, "err", err)
		return
	}
	d.Log.Warn("webhook subscription disabled", "subscription", subID, "reason", why)
}

// DisableLongUnhealthy disables subscriptions unhealthy for 5 consecutive
// days and emails the Application's owner. Run by the scheduled jobs.
func (d *Deliverer) DisableLongUnhealthy(ctx context.Context) (int, error) {
	type row struct {
		ID     string   `json:"id"`
		Scope  string   `json:"scope"`
		URL    string   `json:"url"`
		Owners []string `json:"owners"`
	}
	var rows []row
	err := d.DB.Tx(ctx, db.Settings{AllModes: true}, func(q db.Querier) error {
		var err error
		rows, err = db.All[row](ctx, q, `update webhook_subscriptions w set status = 'disabled'
			where w.status = 'unhealthy' and w.unhealthy_since < now() - interval '5 days'
			returning jsonb_build_object('id', w.id, 'scope', w.scope, 'url', w.url,
				'owners', coalesce((select jsonb_agg(u.email) from applications a
					join users u on u.id = a.owner_user_id where a.id = w.scope), '[]'::jsonb)
					|| coalesce((select jsonb_agg(u.email) from applications a
					join organization_memberships m on m.organization_id = a.owner_organization_id
					 and m.role = 'org_admin' and m.status = 'active'
					join users u on u.id = m.user_id where a.id = w.scope), '[]'::jsonb))`, nil)
		return err
	})
	if err != nil {
		return 0, err
	}
	for _, r := range rows {
		for _, to := range r.Owners {
			if err := d.Email.Send(ctx, email.Message{To: to, Kind: "webhook_disabled",
				Subject: "A Substratal webhook subscription was disabled",
				Text: fmt.Sprintf("Webhook %s (%s) for %s was disabled after %d days of failed deliveries. "+
					"Fix the endpoint, then PATCH its status back to healthy.", r.ID, r.URL, r.Scope, disableAfterDays)}); err != nil {
				d.Log.Error("email webhook owner", "subscription", r.ID, "err", err)
			}
		}
	}
	return len(rows), nil
}

// Run processes until nothing is left to do: dispatch, then deliver.
func (d *Deliverer) Run(ctx context.Context) error {
	for i := 0; i < 50; i++ {
		n, err := d.Dispatch(ctx, 100)
		if err != nil {
			return err
		}
		m, err := d.DeliverDue(ctx, 50)
		if err != nil {
			return err
		}
		if n == 0 && m == 0 {
			return nil
		}
	}
	return nil
}
