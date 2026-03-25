#!/usr/bin/env bash
set -euo pipefail

# Sync sanitized status summaries from customer instances to central HQ.
# Usage: ./sync-summaries.sh [customer-slug]
# Without args, syncs all customers.

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SWARM_DIR="$(dirname "$SCRIPT_DIR")"
CUSTOMERS_DIR="$SWARM_DIR/customers"
SUMMARY_DIR="$SWARM_DIR/data/customer-summaries"

mkdir -p "$SUMMARY_DIR"

sync_customer() {
    local slug="$1"
    local hq_dir="$CUSTOMERS_DIR/$slug/headquarter"
    local container="swarm-kai-${slug}"

    if [ ! -d "$hq_dir" ]; then
        echo "SKIP: No HQ directory for $slug"
        return
    fi

    echo "Syncing: $slug"

    # Try docker exec first (for running containers)
    if docker ps --format '{{.Names}}' | grep -q "^${container}$"; then
        docker exec "$container" mc status --json 2>/dev/null > "$SUMMARY_DIR/${slug}.json" \
            && echo "  OK (via container)" \
            && return
    fi

    # Fallback: run mc directly against the HQ directory
    if command -v mc &>/dev/null; then
        (cd "$hq_dir" && mc status --json 2>/dev/null > "$SUMMARY_DIR/${slug}.json") \
            && echo "  OK (via local mc)" \
            && return
    fi

    echo "  WARN: Could not sync $slug (container not running, mc not available)"
}

if [ $# -gt 0 ]; then
    sync_customer "$1"
else
    if [ ! -d "$CUSTOMERS_DIR" ]; then
        echo "No customers directory found."
        exit 0
    fi
    for customer_dir in "$CUSTOMERS_DIR"/*/; do
        slug=$(basename "$customer_dir")
        sync_customer "$slug"
    done
fi

echo ""
echo "Summaries written to: $SUMMARY_DIR/"
