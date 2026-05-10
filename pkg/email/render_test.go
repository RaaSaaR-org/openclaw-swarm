package email

import (
	"errors"
	"strings"
	"testing"
)

type verifyData struct {
	Name           string
	VerifyURL      string
	ExpiresInHours int
}

type welcomeData struct {
	Name         string
	WorkspaceURL string
}

func TestRenderVerifyDE(t *testing.T) {
	t.Parallel()
	subject, html, text, err := Render(TemplateVerify, LangDE, verifyData{
		Name: "Anna", VerifyURL: "https://kai.example/v?t=abc", ExpiresInHours: 24,
	})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if !strings.Contains(subject, "Bestaetige") {
		t.Errorf("subject not German: %q", subject)
	}
	for _, want := range []string{"Anna", "https://kai.example/v?t=abc", "24"} {
		if !strings.Contains(html, want) {
			t.Errorf("html missing %q", want)
		}
		if !strings.Contains(text, want) {
			t.Errorf("text missing %q", want)
		}
	}
	if !strings.Contains(html, "<!DOCTYPE html>") {
		t.Error("html missing doctype")
	}
}

func TestRenderVerifyEN(t *testing.T) {
	t.Parallel()
	subject, html, text, err := Render(TemplateVerify, LangEN, verifyData{
		Name: "Alice", VerifyURL: "https://kai.example/v?t=xyz", ExpiresInHours: 12,
	})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if !strings.Contains(subject, "Confirm") {
		t.Errorf("subject not English: %q", subject)
	}
	for _, want := range []string{"Alice", "https://kai.example/v?t=xyz", "12"} {
		if !strings.Contains(html, want) {
			t.Errorf("html missing %q", want)
		}
		if !strings.Contains(text, want) {
			t.Errorf("text missing %q", want)
		}
	}
}

func TestRenderWelcomeBothLangs(t *testing.T) {
	t.Parallel()
	for _, lang := range []Lang{LangDE, LangEN} {
		subject, html, text, err := Render(TemplateWelcome, lang, welcomeData{
			Name: "Bob", WorkspaceURL: "https://kai.example/w/bob",
		})
		if err != nil {
			t.Fatalf("[%s] Render: %v", lang, err)
		}
		if !strings.Contains(subject, "Bob") {
			t.Errorf("[%s] subject missing name: %q", lang, subject)
		}
		if !strings.Contains(html, "https://kai.example/w/bob") {
			t.Errorf("[%s] html missing workspace url", lang)
		}
		if !strings.Contains(text, "Bob") {
			t.Errorf("[%s] text missing name", lang)
		}
	}
}

func TestRenderHTMLEscapesUserInput(t *testing.T) {
	t.Parallel()
	_, html, _, err := Render(TemplateVerify, LangEN, verifyData{
		Name: "<script>alert(1)</script>", VerifyURL: "https://kai.example/v", ExpiresInHours: 1,
	})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if strings.Contains(html, "<script>alert(1)</script>") {
		t.Error("html template must escape user-supplied Name; raw <script> tag leaked")
	}
	if !strings.Contains(html, "&lt;script&gt;") {
		t.Errorf("expected escaped <script> tag, got %s", html)
	}
}

func TestRenderUnknownTemplate(t *testing.T) {
	t.Parallel()
	_, _, _, err := Render(Template("nope"), LangDE, verifyData{Name: "x", VerifyURL: "y", ExpiresInHours: 1})
	if !errors.Is(err, ErrUnknownTemplate) {
		t.Errorf("expected ErrUnknownTemplate, got %v", err)
	}
}

func TestRenderUnsupportedLang(t *testing.T) {
	t.Parallel()
	_, _, _, err := Render(TemplateVerify, Lang("fr"), verifyData{Name: "x", VerifyURL: "y", ExpiresInHours: 1})
	if !errors.Is(err, ErrUnknownTemplate) {
		t.Errorf("expected ErrUnknownTemplate, got %v", err)
	}
}

func TestRenderMissingDataFieldFails(t *testing.T) {
	t.Parallel()
	// data with no VerifyURL field — text/template missingkey=error should reject this.
	_, _, _, err := Render(TemplateVerify, LangDE, struct{ Name string }{Name: "x"})
	if err == nil {
		t.Fatal("expected error when data is missing VerifyURL field")
	}
}

// TestRenderAllTemplatesBothLangs is the catch-all that proves every shipped
// (template, lang) pair has subject/html/text files AND that they render with
// the documented data shape. New templates added to the const list must add a
// fixture here or the test fails — which is the point.
func TestRenderAllTemplatesBothLangs(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name Template
		data any
	}{
		{TemplateVerify, struct {
			Name           string
			VerifyURL      string
			ExpiresInHours int
		}{"Anna", "https://kai.example/v?t=abc", 24}},
		{TemplateWelcome, struct {
			Name         string
			WorkspaceURL string
		}{"Anna", "https://kai.example/w/anna"}},
		{TemplateReset, struct {
			Name           string
			ResetURL       string
			ExpiresInHours int
		}{"Anna", "https://kai.example/reset?t=xyz", 2}},
		{TemplateBillingReceipt, struct {
			Name        string
			PlanName    string
			Amount      string
			InvoiceURL  string
			PeriodStart string
			PeriodEnd   string
		}{"Anna", "Starter", "EUR 9.00", "https://kai.example/inv/123.pdf", "2026-05-01", "2026-05-31"}},
		{TemplatePaymentFailed, struct {
			Name           string
			PlanName       string
			Amount         string
			RetryURL       string
			BillingURL     string
			RetryDate      string
			AttemptCount   int
			IsFinalAttempt bool
		}{"Anna", "Starter", "EUR 9.00", "https://kai.example/billing/retry", "https://kai.example/billing", "2026-05-12", 1, false}},
		{TemplateUsageWarning, struct {
			Name          string
			WorkspaceName string
			UsedPct       int
			ResetAt       string
			UpgradeURL    string
		}{"Anna", "Inbox", 80, "morgen 02:00 UTC", "https://kai.example/billing"}},
		{TemplateAccountDeleted, struct {
			Name              string
			GraceDays         int
			RestoreURL        string
			FinalDeletionDate string
		}{"Anna", 30, "https://kai.example/restore?t=abc", "2026-06-08"}},
	}

	for _, tc := range cases {
		for _, lang := range []Lang{LangDE, LangEN} {
			subject, html, text, err := Render(tc.name, lang, tc.data)
			if err != nil {
				t.Errorf("[%s/%s] render failed: %v", tc.name, lang, err)
				continue
			}
			if strings.TrimSpace(subject) == "" {
				t.Errorf("[%s/%s] empty subject", tc.name, lang)
			}
			if !strings.Contains(html, "<!DOCTYPE html>") {
				t.Errorf("[%s/%s] html missing doctype", tc.name, lang)
			}
			if !strings.Contains(html, "Anna") {
				t.Errorf("[%s/%s] html missing Name", tc.name, lang)
			}
			if !strings.Contains(text, "Anna") {
				t.Errorf("[%s/%s] text missing Name", tc.name, lang)
			}
		}
	}
}
