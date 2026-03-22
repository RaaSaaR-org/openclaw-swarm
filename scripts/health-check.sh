#!/usr/bin/env bash
set -euo pipefail

# Health check for all running OpenClaw instances.
# Usage: ./health-check.sh

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SWARM_DIR="$(dirname "$SCRIPT_DIR")"

echo "=== OpenClaw Instance Health Check ==="
echo "    Time: $(date)"
echo ""

TOTAL=0
HEALTHY=0
UNHEALTHY=0

check_instance() {
    local name="$1"
    local container="$2"
    TOTAL=$((TOTAL + 1))

    # Check if container exists and is running
    if ! docker ps --format '{{.Names}}' | grep -q "^${container}$"; then
        echo "  [DOWN]     $name ($container) — not running"
        UNHEALTHY=$((UNHEALTHY + 1))
        return
    fi

    # Check gateway health endpoint
    local port
    port=$(docker port "$container" 18789 2>/dev/null | head -1 | cut -d: -f2)

    if [ -n "$port" ]; then
        if curl -sf "http://localhost:${port}/healthz" >/dev/null 2>&1; then
            echo "  [OK]       $name ($container) — gateway healthy on port $port"
            HEALTHY=$((HEALTHY + 1))
        else
            echo "  [DEGRADED] $name ($container) — container running but gateway unhealthy"
            UNHEALTHY=$((UNHEALTHY + 1))
        fi
    else
        # No port mapping (internal network) — check container health status
        local health
        health=$(docker inspect --format='{{.State.Health.Status}}' "$container" 2>/dev/null || echo "unknown")
        if [ "$health" = "healthy" ]; then
            echo "  [OK]       $name ($container) — healthy (internal network)"
            HEALTHY=$((HEALTHY + 1))
        else
            echo "  [CHECK]    $name ($container) — running, health: $health (internal network)"
            HEALTHY=$((HEALTHY + 1))  # Still counts as up
        fi
    fi
}

# Check central instance
check_instance "Central Agent" "swarm-central"

# Check demo instances
check_instance "Demo Customer A" "swarm-demo-a"
check_instance "Demo Customer B" "swarm-demo-b"

# Check dynamically provisioned customers
if [ -d "$SWARM_DIR/customers" ]; then
    for customer_dir in "$SWARM_DIR/customers"/*/; do
        slug=$(basename "$customer_dir")
        check_instance "Customer: $slug" "swarm-kai-${slug}"
    done
fi

echo ""
echo "=== Summary: $HEALTHY/$TOTAL healthy, $UNHEALTHY unhealthy ==="
