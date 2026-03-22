#!/usr/bin/env bash
set -euo pipefail

# Tear down a customer OpenClaw instance.
# Archives the HQ repo before removal.
# Usage: ./teardown-customer.sh <customer-slug>

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SWARM_DIR="$(dirname "$SCRIPT_DIR")"
DOCKER_DIR="$SWARM_DIR/docker"

CUSTOMER_SLUG="${1:?Usage: $0 <customer-slug>}"
CUSTOMER_DIR="$SWARM_DIR/customers/$CUSTOMER_SLUG"
OVERRIDE_FILE="$DOCKER_DIR/docker-compose.kai-${CUSTOMER_SLUG}.yml"
ARCHIVE_DIR="$SWARM_DIR/archive"

if [ ! -d "$CUSTOMER_DIR" ]; then
    echo "ERROR: Customer not found: $CUSTOMER_DIR"
    exit 1
fi

echo "=== Tearing down customer: $CUSTOMER_SLUG ==="

# --- Stop the container ---
echo "Stopping container..."
if [ -f "$OVERRIDE_FILE" ]; then
    (cd "$DOCKER_DIR" && docker compose -f docker-compose.yml -f "$OVERRIDE_FILE" stop "kai-${CUSTOMER_SLUG}" 2>/dev/null || true)
    (cd "$DOCKER_DIR" && docker compose -f docker-compose.yml -f "$OVERRIDE_FILE" rm -f "kai-${CUSTOMER_SLUG}" 2>/dev/null || true)
fi

# --- Archive HQ repo ---
echo "Archiving HQ repository..."
mkdir -p "$ARCHIVE_DIR"
ARCHIVE_NAME="${CUSTOMER_SLUG}-$(date +%Y%m%d-%H%M%S)"

if [ -d "$CUSTOMER_DIR/hq/.git" ]; then
    (cd "$CUSTOMER_DIR/hq" && git bundle create "$ARCHIVE_DIR/${ARCHIVE_NAME}.bundle" --all)
    echo "    Git bundle: $ARCHIVE_DIR/${ARCHIVE_NAME}.bundle"
fi

# Archive the full customer directory
tar czf "$ARCHIVE_DIR/${ARCHIVE_NAME}.tar.gz" -C "$SWARM_DIR/customers" "$CUSTOMER_SLUG"
echo "    Full archive: $ARCHIVE_DIR/${ARCHIVE_NAME}.tar.gz"

# --- Remove customer directory ---
echo "Removing customer directory..."
rm -rf "$CUSTOMER_DIR"

# --- Remove Docker Compose override ---
if [ -f "$OVERRIDE_FILE" ]; then
    rm "$OVERRIDE_FILE"
    echo "    Removed: $OVERRIDE_FILE"
fi

echo ""
echo "=== Teardown complete ==="
echo "    Archive: $ARCHIVE_DIR/${ARCHIVE_NAME}.tar.gz"
echo "    Git bundle: $ARCHIVE_DIR/${ARCHIVE_NAME}.bundle (if git was initialized)"
