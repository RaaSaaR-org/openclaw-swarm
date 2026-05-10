#!/usr/bin/env bash
# mc-client — thin wrapper that turns a familiar mc CLI shape into HTTPS calls
# against the mc-gateway. Used by Kai pods after the swarm-sync → REST
# migration so the SKILL-mc.md surface stays close to the old `./mc -y …` UX.
#
# Required env (set by the operator when the mc.swarm.emai.io/api-enabled
# annotation is "true"):
#   MC_API_BASE       e.g. http://mc-gateway.emai-swarm.svc:8080
#   MC_API_TOKEN      tenant bearer token (per-Kai)
#   MC_CUSTOMER_ID    e.g. CUST-001 — auto-injected into POST bodies so the
#                     gateway accepts them
#
# Usage:
#   mc-client list tasks [--status …]            → GET  /v1/tasks
#   mc-client list research                      → GET  /v1/entities/research
#   mc-client show TASK-001                      → GET  /v1/entities/task/TASK-001
#   mc-client new task "Title" [--priority N]    → POST /v1/tasks
#   mc-client move TASK-001 --status done        → POST /v1/tasks/TASK-001/move
#   mc-client status                             → GET  /v1/status
#   mc-client validate                           → POST /v1/validate (admin)
#
# Exit codes: curl's exit code on transport errors, otherwise 0 (we print the
# upstream body unchanged so the agent sees both 2xx success and 4xx problem
# JSON; non-2xx prints a short prefix to stderr for visibility).

set -euo pipefail

if [[ -z "${MC_API_BASE:-}" || -z "${MC_API_TOKEN:-}" ]]; then
  echo "mc-client: MC_API_BASE and MC_API_TOKEN must be set in the environment" >&2
  exit 64
fi

api() {
  local method="$1" path="$2" body="${3-}"
  local args=(-sS -H "Authorization: Bearer ${MC_API_TOKEN}" -H "Accept: application/json")
  if [[ -n "$body" ]]; then
    args+=(-H "Content-Type: application/json" -d "$body")
  fi
  args+=(-X "$method" -w '\n%{http_code}\n' "${MC_API_BASE}${path}")
  local out status
  out="$(curl "${args[@]}")" || return $?
  status="${out##*$'\n'}"
  out="${out%$'\n'*}"
  out="${out%$'\n'}"
  printf '%s\n' "$out"
  if [[ "$status" -ge 400 ]]; then
    echo "mc-client: HTTP $status" >&2
    return 0
  fi
}

# json_pair: emit a JSON key/value (string) only if value is non-empty.
json_pair() {
  local key="$1" val="$2"
  if [[ -n "$val" ]]; then
    printf ',"%s":"%s"' "$key" "${val//\"/\\\"}"
  fi
}

cmd_list() {
  local kind="${1:-tasks}"; shift || true
  local status="" tag=""
  while [[ $# -gt 0 ]]; do
    case "$1" in
      --status) status="$2"; shift 2 ;;
      --tag) tag="$2"; shift 2 ;;
      *) echo "mc-client list: unknown flag $1" >&2; return 64 ;;
    esac
  done
  local path
  case "$kind" in
    tasks|task) path="/v1/tasks" ;;
    *) path="/v1/entities/${kind%s}" ;;  # singular
  esac
  local q=""
  [[ -n "$status" ]] && q+="${q:+&}status=${status}"
  [[ -n "$tag" ]] && q+="${q:+&}tag=${tag}"
  [[ -n "$q" ]] && path+="?${q}"
  api GET "$path"
}

cmd_show() {
  local id="${1:?mc-client show <ID>}"
  local kind
  case "$id" in
    TASK-*) kind=task ;;
    CUST-*) kind=customer ;;
    PROJ-*) kind=project ;;
    MTG-*) kind=meeting ;;
    RES-*) kind=research ;;
    SPR-*) kind=sprint ;;
    PROP-*) kind=proposal ;;
    CONT-*) kind=contact ;;
    *) echo "mc-client show: unrecognised ID prefix in $id" >&2; return 64 ;;
  esac
  api GET "/v1/entities/${kind}/${id}"
}

cmd_new() {
  local kind="${1:?mc-client new <kind> \"Title\"}"; shift
  local title="${1:?mc-client new $kind \"Title\"}"; shift || true
  local priority="" project=""
  while [[ $# -gt 0 ]]; do
    case "$1" in
      --priority) priority="$2"; shift 2 ;;
      --project) project="$2"; shift 2 ;;
      *) echo "mc-client new: unknown flag $1" >&2; return 64 ;;
    esac
  done
  case "$kind" in
    task)
      # POST /v1/tasks. customer is auto-injected by the wrapper so the
      # gateway accepts the request; mc upstream doesn't care if both
      # priority and project are present in the body.
      local body
      body=$(printf '{"title":"%s","customer":"%s"' \
        "${title//\"/\\\"}" "${MC_CUSTOMER_ID:-}")
      if [[ -n "$priority" ]]; then
        body+=$(printf ',"priority":%s' "$priority")
      fi
      body+=$(json_pair project "$project")
      body+="}"
      api POST "/v1/tasks" "$body"
      ;;
    *) echo "mc-client new: unsupported kind '$kind' (only 'task' supported on tenant tokens)" >&2; return 64 ;;
  esac
}

cmd_move() {
  local id="${1:?mc-client move TASK-NNN --status …}"; shift
  local status=""
  while [[ $# -gt 0 ]]; do
    case "$1" in
      --status) status="$2"; shift 2 ;;
      *) echo "mc-client move: unknown flag $1" >&2; return 64 ;;
    esac
  done
  if [[ -z "$status" ]]; then
    echo "mc-client move: --status is required" >&2
    return 64
  fi
  api POST "/v1/tasks/${id}/move" "$(printf '{"status":"%s"}' "$status")"
}

cmd_status() {
  api GET "/v1/status"
}

cmd_validate() {
  api POST "/v1/validate"
}

main() {
  if [[ $# -eq 0 ]]; then
    echo "mc-client: usage: mc-client <list|show|new|move|status|validate> [args…]" >&2
    return 64
  fi
  local cmd="$1"; shift
  case "$cmd" in
    list) cmd_list "$@" ;;
    show) cmd_show "$@" ;;
    new) cmd_new "$@" ;;
    move) cmd_move "$@" ;;
    status) cmd_status "$@" ;;
    validate) cmd_validate "$@" ;;
    -y|--yes) "$@" ;;  # back-compat: old SKILL-mc said './mc -y …'; accept
                        # but ignore the flag — the API doesn't have prompts.
    *) echo "mc-client: unknown command '$cmd'" >&2; return 64 ;;
  esac
}

main "$@"
