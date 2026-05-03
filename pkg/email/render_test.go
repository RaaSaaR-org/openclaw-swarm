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
