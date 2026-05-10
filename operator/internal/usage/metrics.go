/*
Copyright 2026.
*/

package usage

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// MetricsPusher emits the per-workspace usage gauges + the per-pass counters
// to a Prometheus Pushgateway after each Runner pass (TASK-019 Phase 4).
// Opt-in via cmd/usage-monitor's SWARM_PUSHGATEWAY_URL env var; nil pushers
// silently no-op so deploys without a pushgateway aren't forced into one.
//
// We don't pull in `prometheus/client_golang` for this — the surface is one
// HTTP POST in text format, same reasoning as `pkg/email`'s ResendSender.
type MetricsPusher struct {
	URL    string       // e.g. http://prometheus-pushgateway.monitoring.svc:9091
	Job    string       // pushgateway "job" label; defaults to "usage-monitor"
	Client *http.Client // optional; defaults to a 10s-timeout client
}

// NewMetricsPusher returns a pusher targeted at the given pushgateway URL.
// Empty URL returns nil — caller treats nil as "not configured" and skips
// metric emission entirely.
func NewMetricsPusher(url string) *MetricsPusher {
	if url == "" {
		return nil
	}
	return &MetricsPusher{
		URL:    strings.TrimRight(url, "/"),
		Job:    "usage-monitor",
	}
}

// Push emits the metrics derived from one Run() pass. The body is
// Prometheus text format (text/plain; version=0.0.4). We use PUT so the
// pushgateway replaces the whole job's metrics on each push — that means
// a workspace that disappeared between passes also drops out of the
// scraped metric set, instead of going stale.
func (p *MetricsPusher) Push(ctx context.Context, results []Result) error {
	if p == nil {
		return nil // not configured
	}
	body := FormatMetrics(results, time.Now())
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, p.URL+"/metrics/job/"+p.Job, bytes.NewReader([]byte(body)))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "text/plain; version=0.0.4")
	resp, err := p.client().Do(req)
	if err != nil {
		return fmt.Errorf("pushgateway: %w", err)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("pushgateway %d: %s", resp.StatusCode, strings.TrimSpace(string(respBody)))
	}
	return nil
}

func (p *MetricsPusher) client() *http.Client {
	if p.Client != nil {
		return p.Client
	}
	return &http.Client{Timeout: 10 * time.Second}
}

// FormatMetrics renders one pass's results as Prometheus text format.
// Metrics emitted:
//
//   - kai_workspace_usage_dollars{slug,tier}  — gauge, per-workspace daily $
//   - kai_workspace_cap_dollars{slug,tier}    — gauge, per-workspace tier cap
//   - kai_usage_monitor_pass_total            — counter increment per pass
//   - kai_usage_monitor_suspended_total       — counter, suspends in this pass
//   - kai_usage_monitor_errors_total          — counter, per-workspace errors
//   - kai_usage_monitor_pass_timestamp_seconds — gauge, unix time of the pass
//
// The two _total counters are emitted as gauges that REFLECT this pass's
// outcome — the pushgateway aggregates with `delta()` / `increase()` over
// pushes, so we don't track a long-lived counter ourselves. (For long-lived
// counters we'd need a sidecar instead of a daily cron.)
func FormatMetrics(results []Result, now time.Time) string {
	var b strings.Builder
	b.WriteString("# HELP kai_workspace_usage_dollars Daily OpenRouter usage in USD per workspace.\n")
	b.WriteString("# TYPE kai_workspace_usage_dollars gauge\n")
	b.WriteString("# HELP kai_workspace_cap_dollars Daily-dollar cap from pkg/quotas for the workspace's tier.\n")
	b.WriteString("# TYPE kai_workspace_cap_dollars gauge\n")
	for _, r := range results {
		if r.Slug == "" {
			continue // can't label without a slug
		}
		fmt.Fprintf(&b, "kai_workspace_usage_dollars{slug=%q,tier=%q} %g\n", r.Slug, r.Tier, r.UsageDaily)
		fmt.Fprintf(&b, "kai_workspace_cap_dollars{slug=%q,tier=%q} %g\n", r.Slug, r.Tier, r.CapDollars)
	}

	suspended, errs := 0, 0
	for _, r := range results {
		switch r.Action {
		case "suspended":
			suspended++
		case "error":
			errs++
		}
	}

	b.WriteString("# HELP kai_usage_monitor_pass_total One per pass.\n")
	b.WriteString("# TYPE kai_usage_monitor_pass_total gauge\n")
	fmt.Fprintf(&b, "kai_usage_monitor_pass_total 1\n")

	b.WriteString("# HELP kai_usage_monitor_suspended_total Workspaces suspended in this pass.\n")
	b.WriteString("# TYPE kai_usage_monitor_suspended_total gauge\n")
	fmt.Fprintf(&b, "kai_usage_monitor_suspended_total %d\n", suspended)

	b.WriteString("# HELP kai_usage_monitor_errors_total Per-workspace errors in this pass.\n")
	b.WriteString("# TYPE kai_usage_monitor_errors_total gauge\n")
	fmt.Fprintf(&b, "kai_usage_monitor_errors_total %d\n", errs)

	b.WriteString("# HELP kai_usage_monitor_pass_timestamp_seconds Unix timestamp of the pass.\n")
	b.WriteString("# TYPE kai_usage_monitor_pass_timestamp_seconds gauge\n")
	fmt.Fprintf(&b, "kai_usage_monitor_pass_timestamp_seconds %d\n", now.Unix())

	return b.String()
}
