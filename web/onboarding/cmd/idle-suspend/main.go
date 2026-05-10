// idle-suspend is the once-a-day workload that flips
// `spec.suspended=true` on free-tier KaiInstances whose owning User
// hasn't logged in for longer than `pkg/quotas.For(tier).IdleSuspendAfter`
// (TASK-015 Phase 3). Paid tiers keep IdleSuspendAfter=0 and are never
// touched.
//
// Run as a Kubernetes CronJob (see kubernetes/onboarding/idle-suspend-cronjob.yaml).
// Schedule: daily at 04:00 UTC — well clear of the GDPR purge cron at 03:00
// UTC and the operator's usage-monitor cron at 00:30 UTC so the three
// pods don't compete for cluster resources.
//
// Env vars:
//
//	KAI_USERS_DSN      Postgres URL — same DSN onboarding + gdpr-purge use.
//	SWARM_NAMESPACE    Namespace to walk for KaiInstances. Defaults to
//	                   `swarm-system` (matches the operator's default).
//	KAI_IDLE_TIMEOUT   Wall-clock ceiling (Go duration). Defaults to 5m.
//
// In-cluster service account needs:
//   - `kaiinstances.swarm.emai.io` list + patch (the suspend write)
//   - reads from the Postgres `users` table
//
// The cron does NOT run `userspg.Migrate(...)`; the schema is owned by the
// onboarding server's startup path. Same deploy ordering applies as for
// gdpr-purge: brand-new clusters need the server (or a separate migration
// tool) to run before the first cron fire.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/emai-ai/swarm-onboarding/cmd/idle-suspend/internal/idle"
	"github.com/emai-ai/swarm/pkg/userspg"
)

var kaiInstanceGVR = schema.GroupVersionResource{
	Group:    "swarm.emai.io",
	Version:  "v1alpha2",
	Resource: "kaiinstances",
}

func main() {
	var (
		dsn       string
		namespace string
		timeout   time.Duration
	)
	flag.StringVar(&dsn, "dsn", os.Getenv("KAI_USERS_DSN"), "Postgres DSN (KAI_USERS_DSN)")
	flag.StringVar(&namespace, "namespace", envDefault("SWARM_NAMESPACE", "swarm-system"), "namespace to walk for KaiInstances")
	flag.DurationVar(&timeout, "timeout", parseDurationDefault(os.Getenv("KAI_IDLE_TIMEOUT"), 5*time.Minute), "wall-clock ceiling")
	flag.Parse()

	if dsn == "" {
		log.Fatal("KAI_USERS_DSN must be set")
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		log.Fatalf("idle-suspend: pgxpool.New: %v", err)
	}
	defer pool.Close()
	if err := pool.Ping(ctx); err != nil {
		log.Fatalf("idle-suspend: ping postgres: %v", err)
	}
	store, err := userspg.New(pool)
	if err != nil {
		log.Fatalf("idle-suspend: userspg.New: %v", err)
	}

	cfg, err := loadKubeConfig()
	if err != nil {
		log.Fatalf("idle-suspend: kubeconfig: %v", err)
	}
	dyn, err := dynamic.NewForConfig(cfg)
	if err != nil {
		log.Fatalf("idle-suspend: dynamic client: %v", err)
	}

	r := &idle.Runner{
		Lister:  &dynamicLister{dyn: dyn, namespace: namespace},
		Patcher: &dynamicPatcher{dyn: dyn, namespace: namespace},
		Users:   store,
	}
	res, err := r.Run(ctx)
	if err != nil {
		log.Fatalf("idle-suspend: run: %v", err)
	}
	log.Printf("idle-suspend: inspected=%d suspended=%d (namespace=%s)", res.Inspected, res.Suspended, namespace)
}

// dynamicLister adapts the dynamic K8s client to idle.Lister. Lists every
// SaaS-managed KaiInstance in the namespace via label selector — keeping
// internal-managed (`swarm.io/managed=internal`) tenants invisible to the
// suspend pass even if their tier label happens to be `free` for some
// historical reason.
type dynamicLister struct {
	dyn       dynamic.Interface
	namespace string
}

func (l *dynamicLister) ListSaaSInstances(ctx context.Context) ([]*unstructured.Unstructured, error) {
	list, err := l.dyn.Resource(kaiInstanceGVR).Namespace(l.namespace).List(ctx, metav1.ListOptions{
		LabelSelector: "swarm.io/managed=saas",
	})
	if err != nil {
		return nil, err
	}
	out := make([]*unstructured.Unstructured, 0, len(list.Items))
	for i := range list.Items {
		out = append(out, &list.Items[i])
	}
	return out, nil
}

// dynamicPatcher adapts the dynamic K8s client to idle.Patcher. Uses a
// strategic-merge-style JSON patch on `spec.suspended` so we don't touch
// any other field — important because `spec.resources` may have been
// clamped by the operator and the cron's namespaced ServiceAccount only
// has list+patch permissions, not full update.
type dynamicPatcher struct {
	dyn       dynamic.Interface
	namespace string
}

func (p *dynamicPatcher) SetSuspended(ctx context.Context, name string) error {
	patch := []byte(`{"spec":{"suspended":true}}`)
	_, err := p.dyn.Resource(kaiInstanceGVR).Namespace(p.namespace).Patch(ctx, name, types.MergePatchType, patch, metav1.PatchOptions{})
	return err
}

func loadKubeConfig() (*rest.Config, error) {
	if cfg, err := rest.InClusterConfig(); err == nil {
		return cfg, nil
	}
	kubeconfig := os.Getenv("KUBECONFIG")
	if kubeconfig == "" {
		if home, _ := os.UserHomeDir(); home != "" {
			kubeconfig = home + "/.kube/config"
		}
	}
	return clientcmd.BuildConfigFromFlags("", kubeconfig)
}

func envDefault(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func parseDurationDefault(s string, def time.Duration) time.Duration {
	if s == "" {
		return def
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		fmt.Fprintf(os.Stderr, "idle-suspend: invalid duration %q, using default %s\n", s, def)
		return def
	}
	return d
}
