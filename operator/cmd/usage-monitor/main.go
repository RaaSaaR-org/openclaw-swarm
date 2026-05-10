/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

// usage-monitor is the once-a-day workload that suspends SaaS-enrolled
// KaiInstances whose per-workspace OpenRouter usage exceeds the tier's
// DailyDollars cap (TASK-019 Phase 3). Bundled with the operator's go
// module so it shares clients + types.
//
// Run as a Kubernetes CronJob (see config/cronjob/usage-monitor.yaml).
// Schedule should be just after OpenRouter's daily reset at 00:00 UTC —
// 00:30 UTC is the default — so the previous day's usage is still
// readable on the keys we're about to suspend.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"time"

	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/emai-ai/swarm-operator/internal/usage"
	"github.com/emai-ai/swarm/pkg/email"
)

func main() {
	var (
		namespace string
		timeout   time.Duration
	)
	flag.StringVar(&namespace, "namespace", envDefault("SWARM_NAMESPACE", "swarm-system"), "namespace SaaS workspaces live in")
	flag.DurationVar(&timeout, "timeout", 5*time.Minute, "abort the pass after this much wall time")
	flag.Parse()

	cfg, err := loadConfig()
	if err != nil {
		log.Fatalf("kubeconfig: %v", err)
	}
	dyn, err := dynamic.NewForConfig(cfg)
	if err != nil {
		log.Fatalf("dynamic client: %v", err)
	}
	core, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		log.Fatalf("core client: %v", err)
	}

	r := &usage.Runner{
		Dyn:       dyn,
		Core:      core,
		Namespace: namespace,
		Reader:    usage.NewRealUsageReader(),
	}

	// 80%-of-cap warning email branch (TASK-019 Phase 5). All three of
	// RESEND_API_KEY + KAI_UPGRADE_URL + a User-lookup wiring must be
	// present for the branch to fire. The User-lookup adapter lives in the
	// swarm-cloud overlay (it depends on `pkg/userspg`); the public swarm
	// repo ships only the contract here. Until the overlay wires it, the
	// cron only suspends — Phase 3 behavior, no emails.
	if apiKey := os.Getenv("RESEND_API_KEY"); apiKey != "" {
		if sender, err := email.NewResendSender(apiKey); err != nil {
			log.Printf("usage-monitor: RESEND_API_KEY rejected (%v); skipping email branch", err)
		} else {
			r.Email = sender
			r.UpgradeURL = envDefault("KAI_UPGRADE_URL", "")
			r.EmailFrom = os.Getenv("EMAIL_FROM")
			if r.UpgradeURL == "" {
				log.Printf("usage-monitor: RESEND_API_KEY set but KAI_UPGRADE_URL missing — email branch stays disabled")
				r.Email = nil
			} else {
				log.Printf("usage-monitor: warning-email branch enabled (UpgradeURL=%s, From=%q)", r.UpgradeURL, r.EmailFrom)
			}
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	results, err := r.Run(ctx)
	if err != nil {
		log.Fatalf("usage-monitor pass aborted: %v", err)
	}

	// One log line per workspace so kubectl logs is greppable.
	suspended, errs := 0, 0
	for _, r := range results {
		log.Printf("usage-monitor slug=%s tier=%s usage=$%.4f cap=$%.2f action=%s reason=%q",
			r.Slug, r.Tier, r.UsageDaily, r.CapDollars, r.Action, r.Reason)
		switch r.Action {
		case "suspended":
			suspended++
		case "error":
			errs++
		}
	}
	log.Printf("usage-monitor pass complete: total=%d suspended=%d errors=%d", len(results), suspended, errs)

	// Phase 4: emit metrics to Prometheus pushgateway (TASK-019). Opt-in via
	// KAI_PUSHGATEWAY_URL — when unset, NewMetricsPusher returns nil and Push
	// silently no-ops. Metric push failures are non-fatal — the suspend work
	// already landed; a pushgateway hiccup shouldn't fail the whole pass.
	if pusher := usage.NewMetricsPusher(os.Getenv("KAI_PUSHGATEWAY_URL")); pusher != nil {
		log.Printf("usage-monitor: pushing metrics to %s", pusher.URL)
		if err := pusher.Push(context.Background(), results); err != nil {
			log.Printf("usage-monitor: metric push failed (continuing): %v", err)
		}
	}

	if errs > 0 {
		// Non-zero exit so the CronJob's status surfaces the partial failure.
		// Runner already kept going past per-workspace errors; we just want
		// the alerting trail.
		os.Exit(1)
	}
}

func loadConfig() (*rest.Config, error) {
	if cfg, err := rest.InClusterConfig(); err == nil {
		return cfg, nil
	}
	kubeconfig := os.Getenv("KUBECONFIG")
	if kubeconfig == "" {
		if home, _ := os.UserHomeDir(); home != "" {
			kubeconfig = home + "/.kube/config"
		}
	}
	if kubeconfig == "" {
		return nil, fmt.Errorf("no in-cluster config and KUBECONFIG/HOME both unset")
	}
	return clientcmd.BuildConfigFromFlags("", kubeconfig)
}

func envDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
