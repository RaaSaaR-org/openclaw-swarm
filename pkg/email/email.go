// Package email is the transactional-email surface for EmAI swarm. It exposes
// a tiny Sender interface and two implementations: DiskSender for local
// development (writes rendered messages under a directory so they show up in
// the file watcher) and ResendSender for production (per PROP-002's choice of
// Resend over Postmark).
//
// Templates live under pkg/email/templates/ and are rendered via Go
// text/template — no JavaScript build step in the email package, which keeps
// the multi-module pattern from TASK-004 (each pkg/<lib>/ stays a small Go
// module that consumers `replace` against). Each template ships a German and
// an English variant; the caller picks one via the Lang field on Message.
package email

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// Lang is the supported template-language enum. German is primary per CLAUDE.md.
type Lang string

const (
	LangDE Lang = "de"
	LangEN Lang = "en"
)

// Template names known to the rendered template set. Adding a new template
// requires (a) a file under pkg/email/templates/ for each Lang and each part
// (subject/html/text) and (b) a const here so callers can't typo the name.
type Template string

const (
	TemplateVerify         Template = "verify"
	TemplateWelcome        Template = "welcome"
	TemplateReset          Template = "reset"
	TemplateBillingReceipt Template = "billing-receipt"
	TemplatePaymentFailed  Template = "payment-failed"
	TemplateUsageWarning   Template = "usage-warning"
	TemplateAccountDeleted Template = "account-deleted"
)

// Message is the rendered output of a Template + data + Lang. To and From are
// addresses (RFC 5322 — display name optional). Subject, HTML, and Text are
// the three parts of the email; senders that lack HTML support fall back to
// Text.
type Message struct {
	To      string
	From    string
	ReplyTo string
	Subject string
	HTML    string
	Text    string
}

// Sender is what callers depend on. Implementations must be safe for
// concurrent use. The context is honored for transport-level cancellation;
// implementations should not buffer beyond what's needed for one Send call.
type Sender interface {
	Send(ctx context.Context, m Message) error
}

// SendOptions ties a Template + Lang + recipient to the Renderer; useful so
// callers say `email.Dispatch(ctx, sender, opts, data)` instead of constructing
// the Message themselves.
type SendOptions struct {
	Template Template
	Lang     Lang
	To       string
	From     string // optional; falls back to defaultFrom
	ReplyTo  string // optional
}

// defaultFrom is what callers get when SendOptions.From is empty. The seam is
// here on purpose — the public swarm repo doesn't know the production sending
// address; the deployment overlay sets EMAIL_FROM on each web app and passes
// it into NewDispatcher (or each call site sets From explicitly).
const defaultFrom = "Kai <noreply@kai.example.org>"

// Dispatch is the convenience wrapper: render the template, populate a
// Message, and hand it to the Sender. Errors are returned as-is so callers can
// distinguish render failures from transport failures via errors.Is.
func Dispatch(ctx context.Context, s Sender, opts SendOptions, data any) error {
	if s == nil {
		return ErrNoSender
	}
	if opts.To == "" {
		return fmt.Errorf("%w: missing To", ErrInvalidMessage)
	}
	subject, html, text, err := Render(opts.Template, opts.Lang, data)
	if err != nil {
		return err
	}
	from := opts.From
	if from == "" {
		from = defaultFrom
	}
	return s.Send(ctx, Message{
		To:      opts.To,
		From:    from,
		ReplyTo: opts.ReplyTo,
		Subject: strings.TrimSpace(subject),
		HTML:    html,
		Text:    text,
	})
}

// Sentinel errors so callers can branch on the failure mode without parsing
// strings. ErrUnknownTemplate covers a misspelled / new-but-not-shipped name;
// ErrInvalidMessage covers a Sender rejecting a malformed Message; ErrNoSender
// catches nil-Sender misuse.
var (
	ErrUnknownTemplate = errors.New("email: unknown template")
	ErrInvalidMessage  = errors.New("email: invalid message")
	ErrNoSender        = errors.New("email: nil Sender")
)
