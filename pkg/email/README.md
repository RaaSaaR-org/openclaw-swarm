# pkg/email

Transactional email — small surface, two providers, embedded templates.

## API

```go
sender, _ := email.NewResendSender(os.Getenv("RESEND_API_KEY"))
err := email.Dispatch(ctx, sender, email.SendOptions{
    Template: email.TemplateVerify,
    Lang:     email.LangDE,
    To:       "anna@example.org",
}, struct {
    Name           string
    VerifyURL      string
    ExpiresInHours int
}{Name: "Anna", VerifyURL: "https://kai.example/v?t=abc", ExpiresInHours: 24})
```

## Senders

| Constructor | Use when |
|---|---|
| `NewDiskSender(dir)` | Local dev (`EMAIL_PROVIDER=disk`); writes `.eml`+`.html`+`.txt` artifacts under `dir` so you can grep for "To: alice" or open the HTML in a browser. |
| `NewResendSender(apiKey)` | Production. Talks to `https://api.resend.com/emails` over plain `net/http` — no Resend SDK dependency. |

Implement your own `Sender` for tests or alternative providers — the interface is one method.

## Templates

Files live under `templates/<name>/<lang>.{subject,html,txt}.tmpl`. All three artifacts are required for every (template, lang) pair — missing one yields `ErrUnknownTemplate`. Adding a new template: drop the six files (DE+EN × 3 parts), add a `Template` const in `email.go`, ship.

| Template | Data fields | Lifecycle owner |
|---|---|---|
| `verify` | `.Name`, `.VerifyURL`, `.ExpiresInHours` | TASK-013 (signup) |
| `welcome` | `.Name`, `.WorkspaceURL` | TASK-013 (signup, post-verification) |

Five more templates from TASK-020 (`reset`, `billing-receipt`, `payment-failed`, `usage-warning`, `account-deleted`) ship in later phases when the upstream flows that need them land — see TASK-013 / 016 / 019 / 021.

## Why no JS build step?

The task's open questions called out the trade-off between Go `text/template` (no DX polish, no Node toolchain) and `react-email` (slick templates, requires Node in build). For the public swarm repo we picked Go: the email package stays a pure-Go single-module, consumers don't need to install Node to run tests, and the visual quality bar at this stage is "renders in a dark-mode email client" not "design-system-grade typography." If the bar rises later, swap the renderer behind the same `Render` signature.

## Sending domain + DNS

DNS setup (SPF/DKIM/DMARC) lives in the deployment overlay (`swarm-cloud` for the public SaaS, `swarm-emai` for internal tenants), not here. The public swarm repo ships only the templates + transport. See `docs/architecture.md` for the deployment-overlay seam.
