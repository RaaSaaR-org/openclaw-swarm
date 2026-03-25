#!/usr/bin/env bash
set -euo pipefail

# Backup all customer HQ repos and Docker volumes.
# Usage: ./backup.sh [backup-dir]

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SWARM_DIR="$(dirname "$SCRIPT_DIR")"
CUSTOMERS_DIR="$SWARM_DIR/customers"
BACKUP_DIR="${1:-$SWARM_DIR/backups/$(date +%Y%m%d-%H%M%S)}"

mkdir -p "$BACKUP_DIR"

echo "=== Backup started: $(date) ==="
echo "    Target: $BACKUP_DIR"

# --- Backup customer HQ repos ---
if [ -d "$CUSTOMERS_DIR" ]; then
    for customer_dir in "$CUSTOMERS_DIR"/*/; do
        slug=$(basename "$customer_dir")
        echo "Backing up: $slug"

        # Git bundle (captures full history)
        if [ -d "$customer_dir/headquarter/.git" ]; then
            (cd "$customer_dir/headquarter" && git bundle create "$BACKUP_DIR/${slug}-headquarter.bundle" --all)
            echo "  Git bundle: ${slug}-headquarter.bundle"
        fi

        # Agent config backup
        if [ -d "$customer_dir/agent" ]; then
            tar czf "$BACKUP_DIR/${slug}-agent.tar.gz" -C "$customer_dir" agent
            echo "  Agent config: ${slug}-agent.tar.gz"
        fi
    done
else
    echo "No customers to backup."
fi

# --- Backup central agent config ---
echo "Backing up central agent config..."
tar czf "$BACKUP_DIR/central-agent.tar.gz" -C "$SWARM_DIR/agents" central
echo "  Central config: central-agent.tar.gz"

# --- Summary ---
echo ""
echo "=== Backup complete ==="
echo "    Location: $BACKUP_DIR"
echo "    Files:"
ls -lh "$BACKUP_DIR/"
