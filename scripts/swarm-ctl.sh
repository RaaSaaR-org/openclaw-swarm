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
  provision  --customer <name> --project <name>   Create a new customer instance
  list                                            List all customer instances
  status     <slug>                               Show instance details
  suspend    <slug>                               Suspend an instance (scale to 0)
  resume     <slug>                               Resume a suspended instance
  delete     <slug>                               Delete an instance and all its resources

Examples:
  swarm-ctl provision --customer "Acme GmbH" --project "Robot Pilot"
  swarm-ctl list
  swarm-ctl status acme-gmbh
  swarm-ctl suspend acme-gmbh
  swarm-ctl delete acme-gmbh
EOF
    exit 1
}

slugify() {
    echo "$1" | tr '[:upper:]' '[:lower:]' | sed 's/[^a-z0-9]/-/g' | sed 's/--*/-/g' | sed 's/^-//;s/-$//'
}

cmd_provision() {
    local customer="" project=""
    while [[ $# -gt 0 ]]; do
        case "$1" in
            --customer) customer="$2"; shift 2 ;;
            --project)  project="$2"; shift 2 ;;
            *) echo "Unknown option: $1"; usage ;;
        esac
    done

    if [[ -z "$customer" || -z "$project" ]]; then
        echo "Error: --customer and --project are required"
        usage
    fi

    local slug
    slug=$(slugify "$customer")
    local name="kai-${slug}"

    kubectl apply -f - <<EOF
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

    echo "Provisioned: ${name}"
    echo "Waiting for instance to be ready..."
    kubectl wait --for=jsonpath='{.status.phase}'=Running \
        "kaiinstance/${name}" -n "${NAMESPACE}" --timeout=120s 2>/dev/null || \
        echo "Instance created but not yet running. Check: swarm-ctl status ${slug}"
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
    list)      cmd_list ;;
    status)    shift; cmd_status "$@" ;;
    suspend)   shift; cmd_suspend "$@" ;;
    resume)    shift; cmd_resume "$@" ;;
    delete)    shift; cmd_delete "$@" ;;
    *)         usage ;;
esac
