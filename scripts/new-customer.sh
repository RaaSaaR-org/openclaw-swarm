#!/usr/bin/env bash
set -euo pipefail

# new-customer.sh — Create a new customer from an agent template
#
# Usage:
#   new-customer.sh <template> <slug> <customer-name> <project-name> [options]
#   new-customer.sh --list
#
# Examples:
#   new-customer.sh project-assistant acme-gmbh "Acme GmbH" "Robotik Pilot"
#   new-customer.sh support-agent test-corp "Test Corp" "Support" --hq-id CUST-010 --telegram
#   new-customer.sh project-assistant demo "Demo GmbH" "Demo" --dry-run
#   new-customer.sh --list
#
# Options:
#   --hq-id ID          HQ entity ID (default: CUST-XXX)
#   --env ENV           Environment for KaiInstance YAML (default: cloud)
#   --output DIR        Output base dir for customer files (default: ../swarm-config/customers)
#   --var KEY=VALUE     Set a template variable (repeatable)
#   --telegram          Enable Telegram channel
#   --dry-run           Preview generated files without writing
#   --list              List available templates

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
TEMPLATES_DIR="$SCRIPT_DIR/../templates"
DEFAULT_OUTPUT="$SCRIPT_DIR/../../swarm-config/customers"
DEFAULT_KAI_OUTPUT="$SCRIPT_DIR/../../swarm-config/environments"

# Colors
RED='\033[0;31m'
GREEN='\033[0;32m'
BLUE='\033[0;34m'
BOLD='\033[1m'
DIM='\033[2m'
NC='\033[0m'

# ── List templates ───────────────────────────────────────────────────
list_templates() {
  echo ""
  echo -e "${BOLD}Available templates:${NC}"
  echo ""
  printf "  %-22s %s\n" "NAME" "DESCRIPTION"
  printf "  %-22s %s\n" "----" "-----------"

  for dir in "$TEMPLATES_DIR"/*/; do
    [ -f "$dir/template.yml" ] || continue
    local name desc
    name=$(basename "$dir")
    desc=$(grep '^description:' "$dir/template.yml" | sed 's/^description: *"//;s/"$//')
    printf "  %-22s %s\n" "$name" "$desc"
  done
  echo ""
}

# ── Parse args ───────────────────────────────────────────────────────
ACTION="create"
TEMPLATE=""
SLUG=""
CUSTOMER_NAME=""
PROJECT_NAME=""
HQ_ID="CUST-XXX"
ENV="cloud"
OUTPUT=""
KAI_OUTPUT=""
DRY_RUN=false
TELEGRAM=false
declare -a EXTRA_VARS=()

# ── Help ─────────────────────────────────────────────────────────────
show_help() {
  cat <<'HELP'
new-customer.sh — Create a new customer from an agent template

Usage:
  new-customer.sh <template> <slug> <customer-name> <project-name> [options]
  new-customer.sh --list
  new-customer.sh --help

Arguments:
  template          Template name (see --list)
  slug              DNS-safe identifier (lowercase, hyphens)
  customer-name     Display name (e.g. "Acme GmbH")
  project-name      Project title (e.g. "Robotik Pilot")

Options:
  --hq-id ID        HQ entity ID (default: CUST-XXX)
  --env ENV         Environment for KaiInstance YAML (default: cloud)
  --output DIR      Output directory for customer files
  --kai-output FILE Output path for KaiInstance YAML
  --var KEY=VALUE   Set a template variable (repeatable)
  --telegram        Enable Telegram channel
  --dry-run         Preview generated files without writing
  --list            List available templates
  --help            Show this help

Examples:
  new-customer.sh project-assistant acme "Acme GmbH" "Robotik Pilot"
  new-customer.sh support-agent demo "Demo Corp" "Support" --telegram
  new-customer.sh project-assistant test "Test" "Test" --dry-run
HELP
}

# Handle --list and --help first
if [[ "${1:-}" == "--list" ]]; then
  list_templates
  exit 0
fi

if [[ "${1:-}" == "--help" ]] || [[ "${1:-}" == "-h" ]]; then
  show_help
  exit 0
fi

# Positional args
if [[ $# -lt 4 ]]; then
  show_help
  exit 1
fi

TEMPLATE="$1"; shift
SLUG="$1"; shift
CUSTOMER_NAME="$1"; shift
PROJECT_NAME="$1"; shift

# Optional flags
while [[ $# -gt 0 ]]; do
  case "$1" in
    --hq-id) HQ_ID="$2"; shift 2 ;;
    --env) ENV="$2"; shift 2 ;;
    --output) OUTPUT="$2"; shift 2 ;;
    --kai-output) KAI_OUTPUT="$2"; shift 2 ;;
    --var) EXTRA_VARS+=("$2"); shift 2 ;;
    --telegram) TELEGRAM=true; shift ;;
    --dry-run) DRY_RUN=true; shift ;;
    *) echo "Unknown option: $1"; exit 1 ;;
  esac
done

# Defaults
OUTPUT="${OUTPUT:-$DEFAULT_OUTPUT/$SLUG}"
KAI_OUTPUT="${KAI_OUTPUT:-$DEFAULT_KAI_OUTPUT/$ENV/kai-$SLUG.yaml}"

# ── Validate ─────────────────────────────────────────────────────────
TEMPLATE_DIR="$TEMPLATES_DIR/$TEMPLATE"

if [ ! -d "$TEMPLATE_DIR" ] || [ ! -f "$TEMPLATE_DIR/template.yml" ]; then
  echo -e "${RED}Error: Template '$TEMPLATE' not found.${NC}"
  echo ""
  list_templates
  exit 1
fi

# Validate slug is DNS-safe
if ! echo "$SLUG" | grep -qE '^[a-z0-9]([a-z0-9-]*[a-z0-9])?$'; then
  echo -e "${RED}Error: Slug '$SLUG' is not DNS-safe.${NC}"
  echo "  Use lowercase letters, numbers, and hyphens only."
  echo "  Must start and end with a letter or number."
  exit 1
fi

if [ "$DRY_RUN" = false ] && [ -d "$OUTPUT" ]; then
  echo -e "${RED}Error: Output directory already exists: $OUTPUT${NC}"
  echo "  Remove it first or choose a different slug."
  exit 1
fi

# ── Generate gateway token ──────────────────────────────────────────
GATEWAY_TOKEN="kai-${SLUG}-$(openssl rand -hex 8)"

# ── Build variable map ──────────────────────────────────────────────
# Read defaults from template.yml
LANGUAGE="de"
TONE=""
DOMAIN=""

# Read template defaults for optional variables
if command -v yq &>/dev/null; then
  while IFS='=' read -r key val; do
    case "$key" in
      LANGUAGE) LANGUAGE="$val" ;;
      TONE) TONE="$val" ;;
      DOMAIN) DOMAIN="$val" ;;
    esac
  done < <(yq e '.variables[] | .name + "=" + (.default // "")' "$TEMPLATE_DIR/template.yml" 2>/dev/null)
else
  # Fallback: grep for defaults
  TONE=$(grep -A1 'name: TONE' "$TEMPLATE_DIR/template.yml" 2>/dev/null | grep 'default:' | sed 's/.*default: *"//;s/"$//' || echo "")
  LANGUAGE=$(grep -A1 'name: LANGUAGE' "$TEMPLATE_DIR/template.yml" 2>/dev/null | grep 'default:' | sed 's/.*default: *"//;s/"$//' || echo "de")
fi

# Apply --var overrides
for var in "${EXTRA_VARS[@]+"${EXTRA_VARS[@]}"}"; do
  key="${var%%=*}"
  val="${var#*=}"
  case "$key" in
    DOMAIN) DOMAIN="$val" ;;
    LANGUAGE) LANGUAGE="$val" ;;
    TONE) TONE="$val" ;;
  esac
done

# ── Render function ─────────────────────────────────────────────────
render() {
  local content
  content=$(cat "$1")
  content="${content//\{\{CUSTOMER_NAME\}\}/$CUSTOMER_NAME}"
  content="${content//\{\{CUSTOMER_SLUG\}\}/$SLUG}"
  content="${content//\{\{PROJECT_NAME\}\}/$PROJECT_NAME}"
  content="${content//\{\{HQ_ID\}\}/$HQ_ID}"
  content="${content//\{\{GATEWAY_TOKEN\}\}/$GATEWAY_TOKEN}"
  content="${content//\{\{LANGUAGE\}\}/$LANGUAGE}"
  content="${content//\{\{TONE\}\}/$TONE}"
  content="${content//\{\{DOMAIN\}\}/$DOMAIN}"
  echo "$content"
}

# ── Enable telegram in config if requested ──────────────────────────
render_config() {
  local content
  content=$(render "$1")

  if [ "$TELEGRAM" = true ]; then
    # Uncomment telegram section
    content=$(echo "$content" | sed 's/^  # telegram:/  telegram:/;s/^  #   enabled: false/    enabled: true/')
  fi

  echo "$content"
}

# ── Dry run ──────────────────────────────────────────────────────────
if [ "$DRY_RUN" = true ]; then
  echo ""
  echo -e "${BOLD}Dry run: ${BLUE}$TEMPLATE${NC} → ${BLUE}$SLUG${NC}"
  echo -e "${DIM}────────────────────────────────────────${NC}"
  echo ""

  for tmpl in "$TEMPLATE_DIR"/*.tmpl; do
    fname=$(basename "$tmpl" .tmpl)
    echo -e "${GREEN}── $fname ──${NC}"
    if [[ "$fname" == "config.yml" ]]; then
      render_config "$tmpl"
    else
      render "$tmpl"
    fi
    echo ""
  done

  echo -e "${GREEN}── .env ──${NC}"
  echo "GATEWAY_TOKEN=$GATEWAY_TOKEN"
  if [ "$TELEGRAM" = true ]; then
    echo "TELEGRAM_BOT_TOKEN="
  fi
  echo "DASHBOARD_PASSWORD="
  echo ""
  exit 0
fi

# ── Write files ──────────────────────────────────────────────────────
mkdir -p "$OUTPUT"

for tmpl in "$TEMPLATE_DIR"/*.tmpl; do
  fname=$(basename "$tmpl" .tmpl)

  # Skip template.yml (metadata, not output)
  [[ "$fname" == "template" ]] && continue

  # kai.yaml goes to a separate location
  if [[ "$fname" == "kai.yaml" ]]; then
    mkdir -p "$(dirname "$KAI_OUTPUT")"
    render "$tmpl" > "$KAI_OUTPUT"
    continue
  fi

  if [[ "$fname" == "config.yml" ]]; then
    render_config "$tmpl" > "$OUTPUT/$fname"
  else
    render "$tmpl" > "$OUTPUT/$fname"
  fi
done

# Generate .env
{
  echo "# ${CUSTOMER_NAME} — Customer Secrets"
  echo "# Copy to .env and fill in real values"
  echo ""
  echo "# Required: Gateway auth token for webchat"
  echo "GATEWAY_TOKEN=$GATEWAY_TOKEN"
  echo ""
  if [ "$TELEGRAM" = true ]; then
    echo "# Required: Telegram bot token (from @BotFather)"
    echo "TELEGRAM_BOT_TOKEN="
    echo ""
  fi
  echo "# Required if dashboard enabled (basic auth password)"
  echo "DASHBOARD_PASSWORD="
} > "$OUTPUT/.env"

# Generate .env.example
{
  echo "# ${CUSTOMER_NAME} — Customer Secrets"
  echo "# Copy to .env and fill in real values"
  echo ""
  echo "# Required: Gateway auth token for webchat"
  echo "GATEWAY_TOKEN=kai-${SLUG}-dev"
  echo ""
  if [ "$TELEGRAM" = true ]; then
    echo "# Required if telegram channel enabled"
    echo "TELEGRAM_BOT_TOKEN="
    echo ""
  fi
  echo "# Required if dashboard enabled (basic auth password)"
  echo "DASHBOARD_PASSWORD="
} > "$OUTPUT/.env.example"

# ── Summary ──────────────────────────────────────────────────────────
echo ""
echo -e "${GREEN}Created customer \"$CUSTOMER_NAME\" from template \"$TEMPLATE\"${NC}"
echo ""

# Show relative paths if possible
REL_OUTPUT="${OUTPUT#$SCRIPT_DIR/../../swarm-config/}"
REL_KAI="${KAI_OUTPUT#$SCRIPT_DIR/../../swarm-config/}"

echo "  $REL_OUTPUT/"
for fname in config.yml SOUL.md USER.md TOOLS.md HEARTBEAT.md .env .env.example; do
  [ -f "$OUTPUT/$fname" ] || continue
  case "$fname" in
    config.yml)    desc="features & channels" ;;
    SOUL.md)       desc="agent identity" ;;
    USER.md)       desc="customer contacts (fill in!)" ;;
    TOOLS.md)      desc="environment notes" ;;
    HEARTBEAT.md)  desc="scheduled tasks" ;;
    .env)          desc="secrets (GATEWAY_TOKEN generated)" ;;
    .env.example)  desc="secrets template" ;;
    *)             desc="" ;;
  esac
  printf "    %-20s %s\n" "$fname" "$desc"
done
echo ""
echo -e "  $REL_KAI   ${DIM}KaiInstance CRD${NC}"
echo ""
echo -e "${BOLD}Next steps:${NC}"
echo "  1. Review & customize files in $REL_OUTPUT/"
echo "  2. Fill in contacts in USER.md"
echo "  3. Add secrets to .env (Telegram token, dashboard password if needed)"
echo "  4. ./scripts/onboard.sh $SLUG --env $ENV"
echo ""
