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
type templateVars struct {
	CustomerName string
	CustomerSlug string
	ProjectName  string
}

// renderTemplate reads a template file and replaces {{PLACEHOLDERS}}.
func renderTemplate(name string, vars templateVars) (string, error) {
	content, err := templateFS.ReadFile("templates/" + name)
	if err != nil {
		return "", fmt.Errorf("reading template %s: %w", name, err)
	}
	result := string(content)
	result = strings.ReplaceAll(result, "{{CUSTOMER_NAME}}", vars.CustomerName)
	result = strings.ReplaceAll(result, "{{CUSTOMER_SLUG}}", vars.CustomerSlug)
	result = strings.ReplaceAll(result, "{{PROJECT_NAME}}", vars.ProjectName)
	return result, nil
}

// renderedTemplates holds all rendered template content.
type renderedTemplates struct {
	SoulMD       string
	HeartbeatMD  string
	OpenClawJSON string
}

// renderAllTemplates renders all agent templates and returns them.
func renderAllTemplates(vars templateVars) (*renderedTemplates, error) {
	soul, err := renderTemplate("SOUL.md.tmpl", vars)
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
	return &renderedTemplates{
		SoulMD:       soul,
		HeartbeatMD:  heartbeat,
		OpenClawJSON: config,
	}, nil
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
