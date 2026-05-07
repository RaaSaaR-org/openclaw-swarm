package main

import (
	"encoding/json"
	"net/http"
	"sort"
	"strings"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

type agent struct {
	ID      string   `json:"id"`
	Name    string   `json:"name"`
	Emoji   string   `json:"emoji,omitempty"`
	Model   string   `json:"model"`
	Persona string   `json:"persona"` // raw markdown body of SOUL.md
	Skills  []string `json:"skills"`
	Source  string   `json:"source"` // catalog slug or "default-template"
	Default bool     `json:"default,omitempty"`
}

type agentsResponse struct {
	Agents []agent `json:"agents"`
}

// openClawConfig is the subset of openclaw.json the workspace cares about.
// Other keys are intentionally ignored — schema can drift without breaking us.
type openClawConfig struct {
	Agents struct {
		Defaults struct {
			Model struct {
				Primary string `json:"primary"`
			} `json:"model"`
		} `json:"defaults"`
		List []openClawAgent `json:"list"`
	} `json:"agents"`
}

type openClawAgent struct {
	ID       string `json:"id"`
	Default  bool   `json:"default"`
	Identity struct {
		Name  string `json:"name"`
		Emoji string `json:"emoji"`
	} `json:"identity"`
	Model struct {
		Primary string `json:"primary"`
	} `json:"model"`
}

func (s *server) listAgents(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	if !slugRegex.MatchString(slug) || len(slug) > 63 {
		writeUnauthorized(w)
		return
	}
	if s.demoMode {
		writeJSON(w, http.StatusOK, demoAgents(slug))
		return
	}
	if !s.requireCenterAuth(w, r, slug) {
		return
	}
	if s.core == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "agents backend unavailable"})
		return
	}

	cm, err := s.core.CoreV1().ConfigMaps(s.namespace).Get(r.Context(), "kai-"+slug+"-identity", metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			// Workspace exists (auth passed) but identity ConfigMap not yet rendered — fresh provision.
			writeJSON(w, http.StatusOK, agentsResponse{Agents: []agent{}})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "lookup failed"})
		return
	}

	var cfg openClawConfig
	if raw, ok := cm.Data["openclaw.json"]; ok {
		// Tolerate empty / malformed config — return an empty list rather than 500.
		// Operator renders this from a template, so malformed should not happen,
		// but a stricter parse would require coupling to the operator's schema.
		_ = json.Unmarshal([]byte(raw), &cfg)
	}

	persona := cm.Data["SOUL.md"]
	skills := extractSkills(cm.Data)

	source := s.agentSource(r, slug)

	agents := make([]agent, 0, len(cfg.Agents.List))
	for _, a := range cfg.Agents.List {
		model := a.Model.Primary
		if model == "" {
			model = cfg.Agents.Defaults.Model.Primary
		}
		agents = append(agents, agent{
			ID:      a.ID,
			Name:    a.Identity.Name,
			Emoji:   a.Identity.Emoji,
			Model:   model,
			Persona: persona,
			Skills:  skills,
			Source:  source,
			Default: a.Default,
		})
	}

	writeJSON(w, http.StatusOK, agentsResponse{Agents: agents})
}

// extractSkills returns the skill IDs present in the identity ConfigMap.
// Keys of the form "SKILL-<id>.md" are mapped to "<id>".
func extractSkills(data map[string]string) []string {
	var out []string
	for k := range data {
		if !strings.HasPrefix(k, "SKILL-") || !strings.HasSuffix(k, ".md") {
			continue
		}
		id := strings.TrimSuffix(strings.TrimPrefix(k, "SKILL-"), ".md")
		if id != "" {
			out = append(out, id)
		}
	}
	sort.Strings(out)
	return out
}

// agentSource looks up the KaiInstance.spec.appRef. Returns "default-template"
// when unset (the legacy customer-template path) or the catalog slug otherwise.
// Errors are swallowed to "default-template" — Phase A is read-only and a
// missing/unreachable CR shouldn't block the agents list from rendering.
func (s *server) agentSource(r *http.Request, slug string) string {
	if s.dyn == nil {
		return "default-template"
	}
	obj, err := s.dyn.Resource(kaiInstanceGVR).Namespace(s.namespace).Get(r.Context(), "kai-"+slug, metav1.GetOptions{})
	if err != nil {
		return "default-template"
	}
	if appRef, found, _ := unstructured.NestedString(obj.Object, "spec", "appRef"); found && appRef != "" {
		return appRef
	}
	return "default-template"
}

func demoAgents(slug string) agentsResponse {
	persona := "## Wer ich bin\n\nIch bin **Kai**, der Projekt-Assistent für " + slug + ". Ich helfe dabei, Aufgaben zu strukturieren, Termine vorzubereiten und den Überblick zu behalten.\n\n## Was ich übernehme\n\n- Projekt-Status verfolgen\n- Briefings vorbereiten\n- Meeting-Notizen festhalten\n\n## Was ich nicht mache\n\n- Verträge oder Rechnungen\n- Personalentscheidungen"
	return agentsResponse{Agents: []agent{
		{
			ID:      "kai-" + slug,
			Name:    "Kai",
			Emoji:   "🤖",
			Model:   "openrouter/anthropic/claude-sonnet-4-6",
			Persona: persona,
			Skills:  []string{"mc"},
			Source:  "project-assistant",
			Default: true,
		},
	}}
}
