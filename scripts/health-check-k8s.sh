#!/usr/bin/env bash
set -euo pipefail

# Platform-operator-facing health check: walks every KaiInstance in a namespace
# and reports OK / DEGRADED / SUSPENDED / PROVISIONING / FAILED / DOWN, plus a
# spec-drift flag from observedGeneration.
#
# This reports the OPERATOR'S view (CRD status + pod state). The agent gateway
# itself can be unhappy in ways the operator doesn't see — to probe that, port-
# forward the kai-<slug> service and curl /healthz directly. Both views can lie
# independently; status-page covers the per-tenant gateway-side view.

CONTEXT=""
NAMESPACE="swarm-system"
OUTPUT="text"

usage() {
    cat <<EOF
Usage: health-check-k8s.sh [--context <ctx>] [--namespace <ns>] [--output text|json]

  --context     kubectl context (default: whatever kubectl is currently bound to)
  --namespace   K8s namespace to scan (default: swarm-system)
  --output      'text' (human) or 'json' (CI / Prometheus / pipeline)

Exit codes:
  0   all instances OK / Suspended / Provisioning (expected states)
  1   at least one instance DEGRADED, FAILED, or DOWN
  2   environment error (no cluster, no kubectl, RBAC, missing jq, …)

Examples:
  health-check-k8s.sh
  health-check-k8s.sh --context emai-cloud --namespace swarm-system
  health-check-k8s.sh --output json | jq '.instances[] | select(.health != "OK")'
EOF
    exit 1
}

while [[ $# -gt 0 ]]; do
    case "$1" in
        --context)   CONTEXT="$2"; shift 2 ;;
        --namespace) NAMESPACE="$2"; shift 2 ;;
        --output)    OUTPUT="$2"; shift 2 ;;
        -h|--help)   usage ;;
        *) echo "Unknown option: $1" >&2; usage ;;
    esac
done

if [[ "$OUTPUT" != "text" && "$OUTPUT" != "json" ]]; then
    echo "Error: --output must be 'text' or 'json'" >&2
    exit 2
fi

if ! command -v jq >/dev/null 2>&1; then
    echo "Error: jq is required" >&2
    exit 2
fi

KCTL=(kubectl)
if [[ -n "$CONTEXT" ]]; then
    KCTL=(kubectl --context "$CONTEXT")
fi

CURRENT_CTX=$("${KCTL[@]}" config current-context 2>/dev/null || echo "(unknown)")

if ! SWARM_JSON=$("${KCTL[@]}" -n "$NAMESPACE" get kaiinstances -o json 2>/dev/null); then
    echo "Error: cannot list KaiInstances in $CURRENT_CTX/$NAMESPACE — check --context, --namespace, and RBAC" >&2
    exit 2
fi

declare -a RESULTS=()
TOTAL=0
UNHEALTHY=0

# jq -c prints one compact JSON object per item; collect them into KAIS via
# read-loop (portable to bash 3.2 on macOS — no mapfile).
KAIS=()
while IFS= read -r kai_line; do
    [[ -z "$kai_line" ]] && continue
    KAIS+=("$kai_line")
done < <(echo "$SWARM_JSON" | jq -c '.items[]')
for kai in "${KAIS[@]:-}"; do
    [[ -z "$kai" ]] && continue
    TOTAL=$((TOTAL + 1))
    name=$(echo "$kai" | jq -r '.metadata.name')
    slug=$(echo "$kai" | jq -r '.status.customerSlug // .spec.customerSlug // ""')
    phase=$(echo "$kai" | jq -r '.status.phase // "Unknown"')
    ready=$(echo "$kai" | jq -r '.status.ready // false')
    observed=$(echo "$kai" | jq -r '.status.observedGeneration // 0')
    generation=$(echo "$kai" | jq -r '.metadata.generation // 0')

    pod_status="absent"
    pod_restarts=0
    if [[ -n "$slug" ]]; then
        pod_json=$("${KCTL[@]}" -n "$NAMESPACE" get pod -l "emai.io/customer=$slug" -o json 2>/dev/null || echo '{"items":[]}')
        pod_count=$(echo "$pod_json" | jq '.items | length')
        if (( pod_count > 0 )); then
            pod_status=$(echo "$pod_json" | jq -r '.items[0].status.phase // "Unknown"')
            pod_restarts=$(echo "$pod_json" | jq '[.items[0].status.containerStatuses[]?.restartCount // 0] | add // 0')
        fi
    fi

    # Verdict mapping. Provisioning + Suspended are *expected* not-OK states and
    # don't trip the unhealthy counter. DEGRADED, FAILED, DOWN do.
    case "$phase" in
        Running)
            if [[ "$ready" == "true" && "$pod_status" == "Running" && $pod_restarts -le 3 ]]; then
                health="OK"
            else
                health="DEGRADED"
            fi
            ;;
        Suspended)    health="SUSPENDED" ;;
        Provisioning) health="PROVISIONING" ;;
        Failed)       health="FAILED" ;;
        *)
            if [[ "$pod_status" == "absent" ]]; then
                health="DOWN"
            else
                health="UNKNOWN"
            fi
            ;;
    esac

    drift="false"
    if (( observed < generation )); then
        drift="true"
    fi

    case "$health" in
        OK|SUSPENDED|PROVISIONING) ;;
        *) UNHEALTHY=$((UNHEALTHY + 1)) ;;
    esac

    RESULTS+=("$(jq -n \
        --arg name "$name" \
        --arg slug "$slug" \
        --arg phase "$phase" \
        --argjson ready "$ready" \
        --arg health "$health" \
        --arg podStatus "$pod_status" \
        --argjson podRestarts "$pod_restarts" \
        --argjson observedGeneration "$observed" \
        --argjson generation "$generation" \
        --argjson drift "$drift" \
        '{name:$name, slug:$slug, phase:$phase, ready:$ready, health:$health, pod:{status:$podStatus, restarts:$podRestarts}, observedGeneration:$observedGeneration, generation:$generation, drift:$drift}')")
done

if [[ "$OUTPUT" == "json" ]]; then
    if (( ${#RESULTS[@]} == 0 )); then
        instances_json="[]"
    else
        instances_json=$(printf '%s\n' "${RESULTS[@]}" | jq -s .)
    fi
    jq -n \
        --argjson total "$TOTAL" \
        --argjson unhealthy "$UNHEALTHY" \
        --arg context "$CURRENT_CTX" \
        --arg namespace "$NAMESPACE" \
        --argjson instances "$instances_json" \
        '{context:$context, namespace:$namespace, total:$total, unhealthy:$unhealthy, instances:$instances}'
else
    echo "=== KaiInstance Health Check ==="
    echo "Context:   $CURRENT_CTX"
    echo "Namespace: $NAMESPACE"
    echo "Time:      $(date)"
    echo ""
    if (( TOTAL == 0 )); then
        echo "(no KaiInstances)"
    else
        printf "%-28s %-13s %-13s %-10s %-9s %s\n" "NAME" "PHASE" "HEALTH" "POD" "RESTARTS" "DRIFT"
        for r in "${RESULTS[@]}"; do
            name=$(echo "$r" | jq -r '.name')
            phase=$(echo "$r" | jq -r '.phase')
            health=$(echo "$r" | jq -r '.health')
            pod_status=$(echo "$r" | jq -r '.pod.status')
            pod_restarts=$(echo "$r" | jq -r '.pod.restarts')
            drift=$(echo "$r" | jq -r '.drift')
            printf "%-28s %-13s %-13s %-10s %-9s %s\n" "$name" "$phase" "$health" "$pod_status" "$pod_restarts" "$drift"
        done
    fi
    echo ""
    echo "=== Summary: $((TOTAL - UNHEALTHY))/$TOTAL in expected state, $UNHEALTHY need attention ==="
fi

if (( UNHEALTHY > 0 )); then
    exit 1
fi
exit 0
