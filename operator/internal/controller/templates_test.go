package controller

import (
	"encoding/json"
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

	tmpl, err := renderAllTemplates(vars)
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
		"SoulMD":   tmpl.SoulMD,
		"AgentsMD": tmpl.AgentsMD,
		"ToolsMD":  tmpl.ToolsMD,
		"HeartbeatMD": tmpl.HeartbeatMD,
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

	tmpl, err := renderAllTemplates(vars)
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

	tmpl, err := renderAllTemplates(vars)
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

	tmpl, err := renderAllTemplates(vars)
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
