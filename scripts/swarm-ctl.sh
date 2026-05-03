#!/usr/bin/env bash
set -euo pipefail

# swarm-ctl — CLI wrapper for Kira to manage KaiInstance custom resources.
# Mounted into the Kira pod; uses the pod's ServiceAccount for auth.

NAMESPACE="${SWARM_NAMESPACE:-emai-swarm}"
API_GROUP="swarm.emai.io"
API_VERSION="v1alpha1"
RESOURCE="kaiinstances"

usage() {
    cat <<EOF
Usage: swarm-ctl <command> [options]

Commands:
  provision  --customer <name> --project <name> [--output text|json]
                                                  Create a new instance and print its access info
  info       <slug> [--output text|json]          Print URLs / token / channels for an existing instance
  list                                            List all customer instances
  status     <slug>                               Show instance details (raw YAML)
  suspend    <slug>                               Suspend an instance (scale to 0)
  resume     <slug>                               Resume a suspended instance
  delete     <slug>                               Delete an instance and all its resources

Environment:
  SWARM_NAMESPACE             Namespace to use (default: emai-swarm)
  SWARM_PROVISION_TIMEOUT     kubectl wait timeout for provision (default: 300s)

Examples:
  swarm-ctl provision --customer "Acme GmbH" --project "Robot Pilot"
  swarm-ctl provision --customer "Acme GmbH" --project "Pilot" --output json | jq .chatURL
  swarm-ctl info acme-gmbh
  swarm-ctl info acme-gmbh --output json
  swarm-ctl list
  swarm-ctl suspend acme-gmbh
  swarm-ctl delete acme-gmbh
EOF
    exit 1
}

slugify() {
    echo "$1" | tr '[:upper:]' '[:lower:]' | sed 's/[^a-z0-9]/-/g' | sed 's/--*/-/g' | sed 's/^-//;s/-$//'
}

cmd_provision() {
    local customer="" project="" output_format="text"
    while [[ $# -gt 0 ]]; do
        case "$1" in
            --customer) customer="$2"; shift 2 ;;
            --project)  project="$2"; shift 2 ;;
            --output)   output_format="$2"; shift 2 ;;
            *) echo "Unknown option: $1"; usage ;;
        esac
    done

    if [[ -z "$customer" || -z "$project" ]]; then
        echo "Error: --customer and --project are required"
        usage
    fi
    if [[ "$output_format" != "text" && "$output_format" != "json" ]]; then
        echo "Error: --output must be 'text' or 'json'"
        usage
    fi

    local slug
    slug=$(slugify "$customer")
    local name="kai-${slug}"
    local timeout="${SWARM_PROVISION_TIMEOUT:-300s}"

    # Apply the CR. Send the manifest noise to stderr so JSON-mode stdout stays clean.
    kubectl apply -f - >&2 <<EOF
apiVersion: ${API_GROUP}/${API_VERSION}
kind: KaiInstance
metadata:
  name: ${name}
  namespace: ${NAMESPACE}
spec:
  customerName: "${customer}"
  projectName: "${project}"
  customerSlug: "${slug}"
EOF

    if [[ "$output_format" == "text" ]]; then
        echo "Provisioned: ${name}"
        echo "Waiting up to ${timeout} for instance to be ready..."
    fi

    if ! kubectl wait --for=jsonpath='{.status.phase}'=Running \
        "kaiinstance/${name}" -n "${NAMESPACE}" --timeout="${timeout}" >&2 2>&1; then
        if [[ "$output_format" == "text" ]]; then
            echo "" >&2
            echo "Instance created but not yet running. Re-run 'swarm-ctl info ${slug}' once it is." >&2
        fi
        # Still emit info — the caller may want the partial state (phase, partial URLs).
    fi

    if [[ "$output_format" == "text" ]]; then
        echo ""
    fi
    cmd_info "$slug" --output "$output_format"
}

# cmd_info prints the access info for an existing KaiInstance. Reads only —
# never mutates cluster state. Output is text (human) or json (machine).
cmd_info() {
    local slug="${1:-}"
    if [[ -z "$slug" ]]; then
        echo "Usage: swarm-ctl info <slug> [--output text|json]"
        exit 1
    fi
    shift

    local output_format="text"
    while [[ $# -gt 0 ]]; do
        case "$1" in
            --output) output_format="$2"; shift 2 ;;
            *) echo "Unknown option: $1"; exit 1 ;;
        esac
    done
    if [[ "$output_format" != "text" && "$output_format" != "json" ]]; then
        echo "Error: --output must be 'text' or 'json'" >&2
        exit 1
    fi

    local name="kai-${slug}"
    local kai_json
    if ! kai_json=$(kubectl get kaiinstance "${name}" -n "${NAMESPACE}" -o json 2>/dev/null); then
        echo "Error: KaiInstance ${name} not found in namespace ${NAMESPACE}" >&2
        exit 1
    fi

    local customer project phase ready internal_url external_url token telegram_secret chat_url=""
    customer=$(echo "$kai_json" | jq -r '.spec.customerName // ""')
    project=$(echo "$kai_json" | jq -r '.spec.projectName // ""')
    phase=$(echo "$kai_json" | jq -r '.status.phase // "Unknown"')
    ready=$(echo "$kai_json" | jq -r '.status.ready // false')
    internal_url=$(echo "$kai_json" | jq -r '.status.gatewayURL // ""')
    external_url=$(echo "$kai_json" | jq -r '.status.externalURL // ""')
    token=$(echo "$kai_json" | jq -r '.spec.gatewayAuth.token // ""')
    telegram_secret=$(echo "$kai_json" | jq -r '.spec.telegram.botTokenSecretRef // ""')

    if [[ -n "$external_url" && -n "$token" ]]; then
        chat_url="${external_url}/chat/${slug}?token=${token}"
    fi

    if [[ "$output_format" == "json" ]]; then
        jq -n \
            --arg name "$name" \
            --arg slug "$slug" \
            --arg customer "$customer" \
            --arg project "$project" \
            --arg phase "$phase" \
            --argjson ready "$ready" \
            --arg internalURL "$internal_url" \
            --arg externalURL "$external_url" \
            --arg chatURL "$chat_url" \
            --arg gatewayToken "$token" \
            --arg telegram "$telegram_secret" \
            '{
                name: $name,
                slug: $slug,
                customer: $customer,
                project: $project,
                phase: $phase,
                ready: $ready,
                internalURL: ($internalURL | select(. != "") // null),
                externalURL: ($externalURL | select(. != "") // null),
                chatURL: ($chatURL | select(. != "") // null),
                gatewayToken: ($gatewayToken | select(. != "") // null),
                telegram: (if $telegram == "" then null else {botTokenSecretRef: $telegram} end)
            }'
        return
    fi

    cat <<EOF
Instance: ${name}
Customer: ${customer}
Project:  ${project}
Phase:    ${phase}
Ready:    ${ready}

URLs:
  Internal: ${internal_url:-(not set yet)}
EOF
    if [[ -n "$external_url" ]]; then
        echo "  External: ${external_url}"
    else
        echo "  External: (not configured — use 'kubectl port-forward svc/${name} 18789:18789')"
    fi
    if [[ -n "$chat_url" ]]; then
        echo "  Chat:     ${chat_url}"
    fi
    echo ""
    echo "Auth:"
    echo "  Gateway token: ${token:-(not set)}"
    echo ""
    if [[ -n "$telegram_secret" ]]; then
        echo "Telegram: configured (botTokenSecretRef: ${telegram_secret})"
    else
        echo "Telegram: not configured"
    fi
}

cmd_list() {
    kubectl get kaiinstances -n "${NAMESPACE}" \
        -o custom-columns='NAME:.metadata.name,CUSTOMER:.spec.customerName,PHASE:.status.phase,READY:.status.ready,GATEWAY:.status.gatewayURL'
}

cmd_status() {
    local slug="${1:?Usage: swarm-ctl status <slug>}"
    local name="kai-${slug}"
    kubectl get kaiinstance "${name}" -n "${NAMESPACE}" -o yaml
}

cmd_suspend() {
    local slug="${1:?Usage: swarm-ctl suspend <slug>}"
    local name="kai-${slug}"
    kubectl patch kaiinstance "${name}" -n "${NAMESPACE}" \
        --type merge -p '{"spec":{"suspended":true}}'
    echo "Suspended: ${name}"
}

cmd_resume() {
    local slug="${1:?Usage: swarm-ctl resume <slug>}"
    local name="kai-${slug}"
    kubectl patch kaiinstance "${name}" -n "${NAMESPACE}" \
        --type merge -p '{"spec":{"suspended":false}}'
    echo "Resumed: ${name}"
}

cmd_delete() {
    local slug="${1:?Usage: swarm-ctl delete <slug>}"
    local name="kai-${slug}"
    kubectl delete kaiinstance "${name}" -n "${NAMESPACE}"
    echo "Deleted: ${name} (all child resources will be garbage collected)"
}

# Main dispatch
case "${1:-}" in
    provision) shift; cmd_provision "$@" ;;
    info)      shift; cmd_info "$@" ;;
    list)      cmd_list ;;
    status)    shift; cmd_status "$@" ;;
    suspend)   shift; cmd_suspend "$@" ;;
    resume)    shift; cmd_resume "$@" ;;
    delete)    shift; cmd_delete "$@" ;;
    *)         usage ;;
esac
