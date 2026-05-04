/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controller

import (
	"crypto/sha256"
	"embed"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

//go:embed templates/*
var templateFS embed.FS

// slugify converts a customer name to a DNS-safe slug.
// Same logic as provision-customer.sh: lowercase, non-alphanumeric to hyphens, collapse, trim.
func slugify(name string) string {
	s := strings.ToLower(name)
	re := regexp.MustCompile(`[^a-z0-9]+`)
	s = re.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-")
	// Collapse multiple hyphens
	re2 := regexp.MustCompile(`-{2,}`)
	s = re2.ReplaceAllString(s, "-")
	return s
}

// templateVars holds the placeholder values for template rendering.
// Field names use the new tenant-* terminology (TASK-024 Phase 2.A);
// the legacy `{{CUSTOMER_NAME}}`/`{{CUSTOMER_SLUG}}` template-side
// placeholders are kept as-is for the embedded customer-template's
// back-compat (renaming those would break existing rendered SOULs in
// running tenants).
type templateVars struct {
	TenantName  string
	TenantSlug  string
	ProjectName string
}

// renderTemplate reads a template file and replaces {{PLACEHOLDERS}}.
func renderTemplate(name string, vars templateVars) (string, error) {
	content, err := templateFS.ReadFile("templates/" + name)
	if err != nil {
		return "", fmt.Errorf("reading template %s: %w", name, err)
	}
	result := string(content)
	result = strings.ReplaceAll(result, "{{CUSTOMER_NAME}}", vars.TenantName)
	result = strings.ReplaceAll(result, "{{CUSTOMER_SLUG}}", vars.TenantSlug)
	result = strings.ReplaceAll(result, "{{PROJECT_NAME}}", vars.ProjectName)
	return result, nil
}

// renderedTemplates holds all rendered template content.
type renderedTemplates struct {
	SoulMD       string
	AgentsMD     string
	ToolsMD      string
	HeartbeatMD  string
	OpenClawJSON string
	SkillMC      string
}

// templateOpts carries reconciler-level config that affects template
// rendering (TASK-018 Phase 1). The catalog dir holds the SaaS catalog
// personas (`agents/catalog/<slug>/SOUL.md.tmpl`); when a KaiInstance
// has `spec.appRef` set, the operator uses that persona's SOUL.md
// instead of the embedded customer-template default. Empty CatalogDir
// or empty AppRef → embedded fallback (legacy behavior preserved).
type templateOpts struct {
	CatalogDir string // e.g. "/etc/swarm/catalog"; ConfigMap-mounted in production
	AppRef     string // catalog persona slug from KaiInstance.Spec.AppRef
}

// renderAllTemplates renders all agent templates and returns them.
//
// SOUL.md sourcing priority:
//   1. Catalog at `<CatalogDir>/<AppRef>/SOUL.md.tmpl` if both set and the
//      file exists. This is the SaaS path (TASK-018 Phase 1).
//   2. Embedded `templates/SOUL.md.tmpl` — the legacy customer-template,
//      used for tenants without `spec.appRef` (internal EmAI workspaces
//      and any pre-SaaS-direction tenant in `swarm-emai`/`swarm-config`).
//
// Other template files (AGENTS, TOOLS, HEARTBEAT, openclaw.json,
// SKILL-mc) always come from the embedded set today — they're operator
// infrastructure, not per-persona content. A future phase can add
// per-persona AGENTS.md if a use case emerges.
func renderAllTemplates(vars templateVars, opts templateOpts) (*renderedTemplates, error) {
	soul, err := renderSoul(vars, opts)
	if err != nil {
		return nil, err
	}
	agents, err := renderTemplate("AGENTS.md.tmpl", vars)
	if err != nil {
		return nil, err
	}
	tools, err := renderTemplate("TOOLS.md.tmpl", vars)
	if err != nil {
		return nil, err
	}
	heartbeat, err := renderTemplate("HEARTBEAT.md.tmpl", vars)
	if err != nil {
		return nil, err
	}
	config, err := renderTemplate("openclaw.json.tmpl", vars)
	if err != nil {
		return nil, err
	}
	// SKILL-mc.md is a static file (no placeholders), read directly
	skillMC, err := templateFS.ReadFile("templates/SKILL-mc.md")
	if err != nil {
		return nil, fmt.Errorf("reading SKILL-mc.md: %w", err)
	}
	return &renderedTemplates{
		SoulMD:       soul,
		AgentsMD:     agents,
		ToolsMD:      tools,
		HeartbeatMD:  heartbeat,
		OpenClawJSON: config,
		SkillMC:      string(skillMC),
	}, nil
}

// renderSoul picks the SOUL.md source per the priority above and renders
// the placeholders. Catalog templates use a richer placeholder set
// (`{{WORKSPACE_NAME}}`, `{{USER_NAME}}`, `{{APP_NAME}}`) per the
// `agents/catalog/README.md` schema; the embedded customer-template uses
// the legacy `{{CUSTOMER_*}}` set. Both are resolved here.
func renderSoul(vars templateVars, opts templateOpts) (string, error) {
	if opts.CatalogDir != "" && opts.AppRef != "" {
		path := filepath.Join(opts.CatalogDir, opts.AppRef, "SOUL.md.tmpl")
		raw, err := os.ReadFile(path)
		if err == nil {
			return renderCatalogPlaceholders(string(raw), vars), nil
		}
		if !os.IsNotExist(err) {
			return "", fmt.Errorf("reading catalog SOUL %s: %w", path, err)
		}
		// Missing file → fall through to embedded default. The catalog can
		// drift behind `spec.appRef` when the catalog ConfigMap hasn't been
		// updated to include a newly-curated persona; a workspace pointing
		// at the missing slug shouldn't break — it should boot with the
		// legacy template and the operator's reconcile log records the
		// fallback for the operator to investigate.
	}
	return renderTemplate("SOUL.md.tmpl", vars)
}

// renderCatalogPlaceholders substitutes the catalog template placeholders
// (per agents/catalog/README.md schema). USER_NAME defaults to the email's
// local part for SaaS workspaces — until pkg/users gains a separate
// display-name field, this is the friendly name we have. APP_NAME is left
// to a future enhancement (needs metadata.yaml lookup).
func renderCatalogPlaceholders(body string, vars templateVars) string {
	userName := vars.TenantName
	if at := strings.Index(userName, "@"); at >= 0 {
		userName = userName[:at]
	}
	body = strings.ReplaceAll(body, "{{WORKSPACE_NAME}}", vars.TenantName)
	body = strings.ReplaceAll(body, "{{USER_NAME}}", userName)
	body = strings.ReplaceAll(body, "{{APP_NAME}}", vars.TenantSlug)
	return body
}

// configHash computes a SHA256 hash of all rendered templates plus the model string.
func configHash(tmpl *renderedTemplates, model string) string {
	h := sha256.New()
	h.Write([]byte(tmpl.SoulMD))
	h.Write([]byte(tmpl.HeartbeatMD))
	h.Write([]byte(tmpl.OpenClawJSON))
	h.Write([]byte(model))
	return fmt.Sprintf("%x", h.Sum(nil))[:16]
}
