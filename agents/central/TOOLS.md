# Tools — Central Agent

## MissionControl (mc)

**Binary:** /usr/local/bin/mc
**Mode:** MCP server (stdio transport)
**Working directory:** /workspace/hq
**Launch:** `mc mcp`

Provides tools for entity CRUD:

### Querying
- `get_status` — Dashboard with entity counts and recent activity
- `get_entity` — Full details for any entity by ID
- `read_entity_file` — Raw markdown content of an entity
- `list_entities` — List by kind (customers, projects, meetings, research, tasks, contacts)
- `list_tasks` — Rich filtering: project, customer, priority, sprint, owner, status

### Creating
- `create_customer`, `create_project`, `create_task`, `create_meeting`
- `create_research`, `create_sprint`, `create_proposal`, `create_contact`

### Updating
- `move_task` — Change task status and sprint

### Maintenance
- `build_index` — Rebuild JSON indexes after manual edits
- `validate_repo` — Check structure and frontmatter integrity

### Exporting
- `print_meeting`, `print_research`, `print_file` — Generate branded PDFs

## Swarm Management (swarm-ctl)

**Binary:** /shared-bin/swarm-ctl
**Mode:** Standalone shell script (NOT an mc tool — run directly via exec/shell, never via mc)
**Important:** This is a separate CLI tool. Do NOT run it through MissionControl. Run it directly as a shell command.

Manages customer agent instances on Kubernetes.

### How to use
Run these commands directly as shell/exec commands (not via mc):

```bash
swarm-ctl provision --customer "Customer Name" --project "Project Name"
swarm-ctl list
swarm-ctl status <slug>
swarm-ctl suspend <slug>
swarm-ctl resume <slug>
swarm-ctl delete <slug>
```

### Notes
- The slug is auto-derived from the customer name (lowercase, hyphens)
- Provisioning creates: Deployment, Service, ConfigMap, PVC, NetworkPolicy
- Suspended instances keep their PVC data intact
- Deletion cascades to all child resources via Kubernetes ownerReferences

## Web Search

For daily news gathering, research topic exploration, and market intelligence.

## File Operations

Read and write files in the HQ repository at /workspace/hq.
