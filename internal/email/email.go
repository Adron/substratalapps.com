// Package email sends transactional email (Auth → Email verification):
// SES in AWS, SMTP to Mailpit locally, or an in-memory capture in tests.
package email

import (
	"context"
	"fmt"
	"log/slog"
	"net/mail"
	"net/smtp"
	"strings"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/sesv2"
	sestypes "github.com/aws/aws-sdk-go-v2/service/sesv2/types"
)

// Message is one plain-text email.
type Message struct {
	To       string
	FromName string // display name override, e.g. an Application's email_from_name
	Subject  string
	Text     string
	// Kind tags the message for tests and logs (verify, reset, invitation, …).
	Kind string
}

// Sender delivers messages.
type Sender interface {
	Send(ctx context.Context, m Message) error
}

func fromHeader(defaultFrom, name string) string {
	if name == "" {
		return defaultFrom
	}
	addr, err := mail.ParseAddress(defaultFrom)
	if err != nil {
		return defaultFrom
	}
	return (&mail.Address{Name: name, Address: addr.Address}).String()
}

// SMTP sends through a plain SMTP server (Mailpit locally; no auth, no TLS).
type SMTP struct {
	Addr string
	From string
}

func (s SMTP) Send(_ context.Context, m Message) error {
	from := fromHeader(s.From, m.FromName)
	envelopeFrom := from
	if a, err := mail.ParseAddress(from); err == nil {
		envelopeFrom = a.Address
	}
	var b strings.Builder
	fmt.Fprintf(&b, "From: %s\r\nTo: %s\r\nSubject: %s\r\nDate: %s\r\nMIME-Version: 1.0\r\nContent-Type: text/plain; charset=utf-8\r\nX-Substratal-Kind: %s\r\n\r\n%s",
		from, m.To, m.Subject, time.Now().UTC().Format(time.RFC1123Z), m.Kind, m.Text)
	return smtp.SendMail(s.Addr, nil, envelopeFrom, []string{m.To}, []byte(b.String()))
}

// SES sends through Amazon SES v2.
type SES struct {
	Client *sesv2.Client
	From   string
}

func (s SES) Send(ctx context.Context, m Message) error {
	from := fromHeader(s.From, m.FromName)
	_, err := s.Client.SendEmail(ctx, &sesv2.SendEmailInput{
		FromEmailAddress: &from,
		Destination:      &sestypes.Destination{ToAddresses: []string{m.To}},
		Content: &sestypes.EmailContent{Simple: &sestypes.Message{
			Subject: &sestypes.Content{Data: &m.Subject},
			Body:    &sestypes.Body{Text: &sestypes.Content{Data: &m.Text}},
		}},
	})
	return err
}

// Log writes messages to the structured log instead of sending them.
type Log struct{}

func (Log) Send(_ context.Context, m Message) error {
	slog.Info("email (log backend)", "to", m.To, "kind", m.Kind, "subject", m.Subject)
	return nil
}

// Capture records messages in memory (tests).
type Capture struct {
	mu   sync.Mutex
	sent []Message
}

func (c *Capture) Send(_ context.Context, m Message) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.sent = append(c.sent, m)
	return nil
}

// Sent returns every message so far.
func (c *Capture) Sent() []Message {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]Message(nil), c.sent...)
}

// Last returns the most recent message to `to` of `kind`, if any.
func (c *Capture) Last(to, kind string) (Message, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for i := len(c.sent) - 1; i >= 0; i-- {
		if strings.EqualFold(c.sent[i].To, to) && (kind == "" || c.sent[i].Kind == kind) {
			return c.sent[i], true
		}
	}
	return Message{}, false
}
