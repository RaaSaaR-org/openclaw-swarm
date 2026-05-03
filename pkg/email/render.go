package email

import (
	"bytes"
	"embed"
	"fmt"
	"html/template"
	texttemplate "text/template"
)

//go:embed templates/*
var templateFS embed.FS

// Render produces the three message parts (subject, HTML, plaintext) for a
// given template + language + data payload. Subject is rendered via
// text/template; HTML via html/template (so user-provided fields auto-escape);
// Text via text/template. Missing files yield ErrUnknownTemplate so callers
// can detect typos rather than silently sending an empty email.
//
// Per-template files live at:
//
//	pkg/email/templates/<name>/<lang>.subject.tmpl
//	pkg/email/templates/<name>/<lang>.html.tmpl
//	pkg/email/templates/<name>/<lang>.txt.tmpl
//
// All three are required for every (name, lang) combination that's exposed to
// users — a missing text part means accessibility tools can't read the email.
func Render(name Template, lang Lang, data any) (subject, html, text string, err error) {
	if !knownLang(lang) {
		return "", "", "", fmt.Errorf("%w: unsupported lang %q", ErrUnknownTemplate, lang)
	}
	dir := "templates/" + string(name)
	prefix := dir + "/" + string(lang)

	subjRaw, err := templateFS.ReadFile(prefix + ".subject.tmpl")
	if err != nil {
		return "", "", "", fmt.Errorf("%w: %s/%s.subject", ErrUnknownTemplate, name, lang)
	}
	htmlRaw, err := templateFS.ReadFile(prefix + ".html.tmpl")
	if err != nil {
		return "", "", "", fmt.Errorf("%w: %s/%s.html", ErrUnknownTemplate, name, lang)
	}
	textRaw, err := templateFS.ReadFile(prefix + ".txt.tmpl")
	if err != nil {
		return "", "", "", fmt.Errorf("%w: %s/%s.txt", ErrUnknownTemplate, name, lang)
	}

	subject, err = renderText("subject", string(subjRaw), data)
	if err != nil {
		return "", "", "", err
	}
	html, err = renderHTML("html", string(htmlRaw), data)
	if err != nil {
		return "", "", "", err
	}
	text, err = renderText("text", string(textRaw), data)
	if err != nil {
		return "", "", "", err
	}
	return subject, html, text, nil
}

func renderText(name, body string, data any) (string, error) {
	t, err := texttemplate.New(name).Option("missingkey=error").Parse(body)
	if err != nil {
		return "", fmt.Errorf("parse %s template: %w", name, err)
	}
	var buf bytes.Buffer
	if err := t.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("execute %s template: %w", name, err)
	}
	return buf.String(), nil
}

func renderHTML(name, body string, data any) (string, error) {
	t, err := template.New(name).Option("missingkey=error").Parse(body)
	if err != nil {
		return "", fmt.Errorf("parse %s template: %w", name, err)
	}
	var buf bytes.Buffer
	if err := t.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("execute %s template: %w", name, err)
	}
	return buf.String(), nil
}

func knownLang(l Lang) bool {
	return l == LangDE || l == LangEN
}
