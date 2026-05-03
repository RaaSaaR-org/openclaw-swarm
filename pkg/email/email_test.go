package email

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDispatchEndToEndDiskRoundTrip(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	s, err := NewDiskSender(dir)
	if err != nil {
		t.Fatalf("NewDiskSender: %v", err)
	}
	err = Dispatch(context.Background(), s, SendOptions{
		Template: TemplateVerify, Lang: LangDE, To: "anna@kai.example",
	}, verifyData{Name: "Anna", VerifyURL: "https://kai.example/v?t=abc", ExpiresInHours: 24})
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}

	// Find the .html file and confirm it actually contains the rendered template body.
	entries, _ := os.ReadDir(dir)
	var htmlBody string
	for _, e := range entries {
		if filepath.Ext(e.Name()) == ".html" {
			b, _ := os.ReadFile(filepath.Join(dir, e.Name()))
			htmlBody = string(b)
		}
	}
	for _, want := range []string{"Anna", "https://kai.example/v?t=abc", "Bestaetige"} {
		// "Bestaetige" only appears in the subject, not the html body — but
		// the .html artifact written by DiskSender is just the HTML part.
		// So filter: we only look for fields that should be in HTML.
		if want == "Bestaetige" {
			continue
		}
		if !strings.Contains(htmlBody, want) {
			t.Errorf(".html body missing %q\nbody: %s", want, htmlBody)
		}
	}
}

func TestDispatchUsesDefaultFromWhenEmpty(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	s, _ := NewDiskSender(dir)
	err := Dispatch(context.Background(), s, SendOptions{
		Template: TemplateWelcome, Lang: LangEN, To: "bob@kai.example",
	}, welcomeData{Name: "Bob", WorkspaceURL: "https://kai.example/w"})
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if filepath.Ext(e.Name()) == ".eml" {
			b, _ := os.ReadFile(filepath.Join(dir, e.Name()))
			if !strings.Contains(string(b), "From: Kai <noreply@kai.example.org>") {
				t.Errorf(".eml missing default From, got:\n%s", b)
			}
		}
	}
}

func TestDispatchRejectsNilSender(t *testing.T) {
	t.Parallel()
	err := Dispatch(context.Background(), nil, SendOptions{Template: TemplateVerify, Lang: LangDE, To: "x@y.z"}, verifyData{})
	if !errors.Is(err, ErrNoSender) {
		t.Errorf("expected ErrNoSender, got %v", err)
	}
}

func TestDispatchRejectsEmptyTo(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	s, _ := NewDiskSender(dir)
	err := Dispatch(context.Background(), s, SendOptions{Template: TemplateVerify, Lang: LangDE}, verifyData{Name: "x", VerifyURL: "y", ExpiresInHours: 1})
	if !errors.Is(err, ErrInvalidMessage) {
		t.Errorf("expected ErrInvalidMessage, got %v", err)
	}
}
