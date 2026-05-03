package controller

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSlugify(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"East Side Fab", "east-side-fab"},
		{"ZeMA gGmbH", "zema-ggmbh"},
		{"Test Firma", "test-firma"},
		{"Simple", "simple"},
		{"UPPER CASE", "upper-case"},
		{"  spaces  ", "spaces"},
		{"special!@#chars", "special-chars"},
		{"multiple---hyphens", "multiple-hyphens"},
		{"trailing-", "trailing"},
		{"-leading", "leading"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := slugify(tt.input)
			if got != tt.expected {
				t.Errorf("slugify(%q) = %q, want %q", tt.input, got, tt.expected)
			}
		})
	}
}

func TestRenderTemplate(t *testing.T) {
	vars := templateVars{
		CustomerName: "East Side Fab",
		CustomerSlug: "east-side-fab",
		ProjectName:  "Innovation Project",
	}

	// Test SOUL.md template
	soul, err := renderTemplate("SOUL.md.tmpl", vars)
	if err != nil {
		t.Fatalf("renderTemplate(SOUL.md.tmpl) error: %v", err)
	}
	if !strings.Contains(soul, "East Side Fab") {
		t.Error("SOUL.md should contain customer name")
	}
	if strings.Contains(soul, "{{CUSTOMER_NAME}}") {
		t.Error("SOUL.md should not contain unresolved placeholders")
	}

	// Test AGENTS.md template
	agents, err := renderTemplate("AGENTS.md.tmpl", vars)
	if err != nil {
		t.Fatalf("renderTemplate(AGENTS.md.tmpl) error: %v", err)
	}
	if !strings.Contains(agents, "East Side Fab") {
		t.Error("AGENTS.md should contain customer name")
	}
	if !strings.Contains(agents, "mc -y") {
		t.Error("AGENTS.md should reference mc -y flag")
	}

	// Test TOOLS.md template
	tools, err := renderTemplate("TOOLS.md.tmpl", vars)
	if err != nil {
		t.Fatalf("renderTemplate(TOOLS.md.tmpl) error: %v", err)
	}
	if !strings.Contains(tools, "East Side Fab") {
		t.Error("TOOLS.md should contain customer name")
	}

	// Test openclaw.json template
	config, err := renderTemplate("openclaw.json.tmpl", vars)
	if err != nil {
		t.Fatalf("renderTemplate(openclaw.json.tmpl) error: %v", err)
	}
	if !strings.Contains(config, "kai-east-side-fab") {
		t.Error("openclaw.json should contain agent ID with slug")
	}

	// Verify openclaw.json is valid JSON
	var jsonData map[string]interface{}
	if err := json.Unmarshal([]byte(config), &jsonData); err != nil {
		t.Errorf("openclaw.json should be valid JSON: %v", err)
	}
}

func TestRenderAllTemplates(t *testing.T) {
	vars := templateVars{
		CustomerName: "Test Customer",
		CustomerSlug: "test-customer",
		ProjectName:  "Test Project",
	}

	tmpl, err := renderAllTemplates(vars, templateOpts{})
	if err != nil {
		t.Fatalf("renderAllTemplates() error: %v", err)
	}

	// All fields should be non-empty
	if tmpl.SoulMD == "" {
		t.Error("SoulMD should not be empty")
	}
	if tmpl.AgentsMD == "" {
		t.Error("AgentsMD should not be empty")
	}
	if tmpl.ToolsMD == "" {
		t.Error("ToolsMD should not be empty")
	}
	if tmpl.HeartbeatMD == "" {
		t.Error("HeartbeatMD should not be empty")
	}
	if tmpl.OpenClawJSON == "" {
		t.Error("OpenClawJSON should not be empty")
	}
	if tmpl.SkillMC == "" {
		t.Error("SkillMC should not be empty")
	}

	// No unresolved placeholders in any template
	for name, content := range map[string]string{
		"SoulMD":       tmpl.SoulMD,
		"AgentsMD":     tmpl.AgentsMD,
		"ToolsMD":      tmpl.ToolsMD,
		"HeartbeatMD":  tmpl.HeartbeatMD,
		"OpenClawJSON": tmpl.OpenClawJSON,
	} {
		if strings.Contains(content, "{{CUSTOMER_NAME}}") ||
			strings.Contains(content, "{{CUSTOMER_SLUG}}") ||
			strings.Contains(content, "{{PROJECT_NAME}}") {
			t.Errorf("%s contains unresolved placeholders", name)
		}
	}
}

func TestOpenClawJSONValid(t *testing.T) {
	vars := templateVars{
		CustomerName: "Test",
		CustomerSlug: "test",
		ProjectName:  "Test",
	}

	tmpl, err := renderAllTemplates(vars, templateOpts{})
	if err != nil {
		t.Fatalf("renderAllTemplates() error: %v", err)
	}

	var config map[string]interface{}
	if err := json.Unmarshal([]byte(tmpl.OpenClawJSON), &config); err != nil {
		t.Fatalf("openclaw.json is not valid JSON: %v", err)
	}

	// Check required top-level keys
	requiredKeys := []string{"agents", "channels", "gateway", "session", "messages", "tools"}
	for _, key := range requiredKeys {
		if _, ok := config[key]; !ok {
			t.Errorf("openclaw.json missing required key: %s", key)
		}
	}

	// Check tools profile is "coding"
	tools, ok := config["tools"].(map[string]interface{})
	if !ok {
		t.Fatal("tools should be an object")
	}
	if profile, ok := tools["profile"].(string); !ok || profile != "coding" {
		t.Errorf("tools.profile should be 'coding', got %v", tools["profile"])
	}
}

func TestConfigHash(t *testing.T) {
	vars := templateVars{
		CustomerName: "Test",
		CustomerSlug: "test",
		ProjectName:  "Test",
	}

	tmpl, err := renderAllTemplates(vars, templateOpts{})
	if err != nil {
		t.Fatalf("renderAllTemplates() error: %v", err)
	}

	// Same input should produce same hash
	hash1 := configHash(tmpl, "model-a")
	hash2 := configHash(tmpl, "model-a")
	if hash1 != hash2 {
		t.Error("configHash should be deterministic")
	}

	// Different model should produce different hash
	hash3 := configHash(tmpl, "model-b")
	if hash1 == hash3 {
		t.Error("configHash should differ for different models")
	}

	// Hash should be 16 chars (truncated SHA256)
	if len(hash1) != 16 {
		t.Errorf("configHash should be 16 chars, got %d", len(hash1))
	}
}

func TestSkillMCContents(t *testing.T) {
	vars := templateVars{
		CustomerName: "Test",
		CustomerSlug: "test",
		ProjectName:  "Test",
	}

	tmpl, err := renderAllTemplates(vars, templateOpts{})
	if err != nil {
		t.Fatalf("renderAllTemplates() error: %v", err)
	}

	// Skill should have YAML frontmatter
	if !strings.HasPrefix(tmpl.SkillMC, "---") {
		t.Error("SKILL-mc.md should start with YAML frontmatter")
	}
	if !strings.Contains(tmpl.SkillMC, "name: mission_control") {
		t.Error("skill should be named mission_control")
	}

	// Skill should use -y flag everywhere mc is referenced
	for _, line := range strings.Split(tmpl.SkillMC, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "./mc ") && !strings.HasPrefix(trimmed, "./mc -y") {
			t.Errorf("skill mc command missing -y flag: %q", trimmed)
		}
	}
}

// catalogFixture writes a minimal catalog tree to a temp dir and returns
// the dir path. Mirrors the agents/catalog/<slug>/SOUL.md.tmpl shape.
func catalogFixture(t *testing.T, slug, soulBody string) string {
	t.Helper()
	dir := t.TempDir()
	personaDir := filepath.Join(dir, slug)
	if err := os.MkdirAll(personaDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(personaDir, "SOUL.md.tmpl"), []byte(soulBody), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	return dir
}

func TestRenderAllTemplatesUsesCatalogSoulWhenAppRefSet(t *testing.T) {
	t.Parallel()
	catalogDir := catalogFixture(t, "writing-coach", "# {{WORKSPACE_NAME}} — Writing Coach\n\nHi {{USER_NAME}}, app={{APP_NAME}}.")

	vars := templateVars{CustomerName: "alice@example.org", CustomerSlug: "alice", ProjectName: "Workspace"}
	tmpl, err := renderAllTemplates(vars, templateOpts{CatalogDir: catalogDir, AppRef: "writing-coach"})
	if err != nil {
		t.Fatalf("renderAllTemplates: %v", err)
	}
	if !strings.Contains(tmpl.SoulMD, "Writing Coach") {
		t.Errorf("SOUL.md should be from catalog, got: %q", tmpl.SoulMD)
	}
	// Catalog placeholders resolved.
	if !strings.Contains(tmpl.SoulMD, "alice@example.org") {
		t.Errorf("WORKSPACE_NAME should be substituted, got: %q", tmpl.SoulMD)
	}
	if !strings.Contains(tmpl.SoulMD, "Hi alice,") {
		t.Errorf("USER_NAME should be email local part, got: %q", tmpl.SoulMD)
	}
	if !strings.Contains(tmpl.SoulMD, "app=alice") {
		t.Errorf("APP_NAME placeholder should be substituted, got: %q", tmpl.SoulMD)
	}
	// AGENTS.md and friends still come from the embedded set — only SOUL is
	// per-persona.
	if !strings.Contains(tmpl.AgentsMD, "alice") && !strings.Contains(tmpl.AgentsMD, "Alice") {
		// AgentsMD uses CustomerName/CustomerSlug too, just confirm it rendered.
		t.Logf("AGENTS.md content (first 100 chars): %.100s", tmpl.AgentsMD)
	}
}

func TestRenderAllTemplatesFallsBackWhenAppRefMissing(t *testing.T) {
	t.Parallel()
	// CatalogDir set but the appRef doesn't exist on disk → must fall back to
	// the embedded customer-template, NOT error. This protects against the
	// catalog ConfigMap drifting behind a freshly-curated persona slug.
	catalogDir := t.TempDir()
	vars := templateVars{CustomerName: "Acme GmbH", CustomerSlug: "acme", ProjectName: "Robotik Pilot"}
	tmpl, err := renderAllTemplates(vars, templateOpts{CatalogDir: catalogDir, AppRef: "does-not-exist"})
	if err != nil {
		t.Fatalf("renderAllTemplates: %v", err)
	}
	// Embedded template uses {{CUSTOMER_NAME}} → expanded to "Acme GmbH".
	if !strings.Contains(tmpl.SoulMD, "Acme GmbH") {
		t.Errorf("expected fallback to embedded template (with CUSTOMER_NAME=Acme GmbH), got: %.200s", tmpl.SoulMD)
	}
}

func TestRenderAllTemplatesIgnoresCatalogWhenAppRefEmpty(t *testing.T) {
	t.Parallel()
	// AppRef empty (legacy tenant) → embedded template, even if catalogDir
	// points at a real catalog.
	catalogDir := catalogFixture(t, "writing-coach", "# Catalog content")
	vars := templateVars{CustomerName: "Acme GmbH", CustomerSlug: "acme", ProjectName: "X"}
	tmpl, err := renderAllTemplates(vars, templateOpts{CatalogDir: catalogDir, AppRef: ""})
	if err != nil {
		t.Fatalf("renderAllTemplates: %v", err)
	}
	if strings.Contains(tmpl.SoulMD, "Catalog content") {
		t.Errorf("legacy tenant (no AppRef) must NOT pick catalog SOUL, got: %s", tmpl.SoulMD)
	}
	if !strings.Contains(tmpl.SoulMD, "Acme GmbH") {
		t.Errorf("expected embedded template with CUSTOMER_NAME, got: %.200s", tmpl.SoulMD)
	}
}

func TestRenderCatalogPlaceholdersUsesEmailLocalPart(t *testing.T) {
	t.Parallel()
	body := "Hello {{USER_NAME}}!"
	got := renderCatalogPlaceholders(body, templateVars{CustomerName: "alice.smith@example.de"})
	if got != "Hello alice.smith!" {
		t.Errorf("renderCatalogPlaceholders = %q, want 'Hello alice.smith!'", got)
	}
}

func TestRenderCatalogPlaceholdersHandlesNonEmailName(t *testing.T) {
	t.Parallel()
	// Non-email name (e.g. internal tenant where CustomerName is "Acme GmbH"):
	// USER_NAME falls through to the full string.
	body := "Hello {{USER_NAME}}!"
	got := renderCatalogPlaceholders(body, templateVars{CustomerName: "Acme GmbH"})
	if got != "Hello Acme GmbH!" {
		t.Errorf("renderCatalogPlaceholders = %q, want 'Hello Acme GmbH!'", got)
	}
}
