/*
Copyright 2026.
*/

package usage

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestFormatMetrics_GaugesPerWorkspace(t *testing.T) {
	t.Parallel()
	results := []Result{
		{Slug: "anna", Tier: "starter", UsageDaily: 2.50, CapDollars: 3.00, Action: "ok"},
		{Slug: "bob", Tier: "free", UsageDaily: 1.20, CapDollars: 1.00, Action: "suspended"},
	}
	out := FormatMetrics(results, time.Date(2026, 5, 10, 12, 30, 0, 0, time.UTC))
	for _, want := range []string{
		`kai_workspace_usage_dollars{slug="anna",tier="starter"} 2.5`,
		`kai_workspace_usage_dollars{slug="bob",tier="free"} 1.2`,
		`kai_workspace_cap_dollars{slug="anna",tier="starter"} 3`,
		`kai_workspace_cap_dollars{slug="bob",tier="free"} 1`,
		"kai_usage_monitor_suspended_total 1",
		"kai_usage_monitor_errors_total 0",
		"kai_usage_monitor_pass_total 1",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing line %q\nfull output:\n%s", want, out)
		}
	}
}

func TestFormatMetrics_SkipsEmptySlug(t *testing.T) {
	t.Parallel()
	out := FormatMetrics([]Result{{Slug: "", Tier: "free", UsageDaily: 0.5}}, time.Now())
	if strings.Contains(out, "kai_workspace_usage_dollars") && strings.Contains(out, "slug=\"\"") {
		t.Errorf("must skip workspace with empty slug, got:\n%s", out)
	}
}

func TestFormatMetrics_CountsErrorsAndSuspends(t *testing.T) {
	t.Parallel()
	results := []Result{
		{Slug: "a", Action: "ok"},
		{Slug: "b", Action: "suspended"},
		{Slug: "c", Action: "suspended"},
		{Slug: "d", Action: "error"},
		{Slug: "e", Action: "skipped"},
	}
	out := FormatMetrics(results, time.Now())
	if !strings.Contains(out, "kai_usage_monitor_suspended_total 2") {
		t.Errorf("expected 2 suspends, got:\n%s", out)
	}
	if !strings.Contains(out, "kai_usage_monitor_errors_total 1") {
		t.Errorf("expected 1 error, got:\n%s", out)
	}
}

func TestPush_HappyPath(t *testing.T) {
	t.Parallel()
	var seen string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut {
			t.Fatalf("expected PUT, got %s", r.Method)
		}
		want := "/metrics/job/usage-monitor"
		if r.URL.Path != want {
			t.Errorf("path = %q, want %q", r.URL.Path, want)
		}
		if ct := r.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/plain") {
			t.Errorf("Content-Type = %q", ct)
		}
		body, _ := io.ReadAll(r.Body)
		seen = string(body)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	p := NewMetricsPusher(srv.URL)
	if p == nil {
		t.Fatal("expected non-nil pusher for non-empty URL")
	}
	results := []Result{{Slug: "anna", Tier: "free", UsageDaily: 0.7, CapDollars: 1.0, Action: "ok"}}
	if err := p.Push(context.Background(), results); err != nil {
		t.Fatalf("Push: %v", err)
	}
	if !strings.Contains(seen, "kai_workspace_usage_dollars") {
		t.Errorf("body missing metric:\n%s", seen)
	}
}

func TestPush_NilPusherIsNoOp(t *testing.T) {
	t.Parallel()
	var p *MetricsPusher
	if err := p.Push(context.Background(), []Result{{Slug: "anna"}}); err != nil {
		t.Errorf("nil pusher should be no-op, got %v", err)
	}
}

func TestNewMetricsPusher_EmptyURLReturnsNil(t *testing.T) {
	t.Parallel()
	if p := NewMetricsPusher(""); p != nil {
		t.Errorf("empty URL should return nil pusher, got %+v", p)
	}
}

func TestPush_Non2xxSurfacesStatusAndBody(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte("malformed metric line"))
	}))
	defer srv.Close()
	p := NewMetricsPusher(srv.URL)
	err := p.Push(context.Background(), nil)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "400") || !strings.Contains(err.Error(), "malformed") {
		t.Errorf("error should include status + body, got %q", err.Error())
	}
}
