# Tools — Central Agent

## MissionControl (mc)

**Binary:** mc (on PATH via /shared-bin)
**Mode:** CLI tool (run via shell exec, like swarm-ctl)
**Working directory:** /home/node/.openclaw/workspace/headquarter
**Usage:** `cd /home/node/.openclaw/workspace/headquarter && mc -y <command>`

HQ knowledge base with customers, projects, tasks, meetings, research, sprints, contacts.

**Note:** Always use `-y` flag to skip interactive confirmations (required in non-TTY environments like agent shells).

### Common commands

```bash
# Overview
mc -y status                              # Dashboard with entity counts
mc -y list customers                      # List all customers
mc -y list tasks --status todo            # List tasks by status
mc -y list tasks --customer CUST-005      # List tasks for a customer
mc -y list tasks --project PROJ-001       # List tasks for a project

# Details
mc -y show TASK-001                       # Show entity details
mc -y show CUST-005                       # Show customer details

# Create
mc -y new task "Task title"               # Create task
mc -y new meeting "Meeting title"         # Create meeting
mc -y new research "Topic"                # Create research topic

# Update
mc -y move TASK-001 --status done         # Move task to done

# Maintenance
mc -y index                               # Rebuild JSON indexes after manual edits
mc -y validate                            # Check repo structure
```

### Data sync

Kai instances run MC in standalone mode on their own workspace. Data is synced with HQ via `swarm-sync`:
- **Upstream:** Kai tasks/meetings/contacts → HQ `customers/CUST-XXX/`
- **Downstream:** HQ customer data → Kai workspace
- Sync runs on demand via `swarm-sync.sh` (in swarm-config/scripts/)

**Important:** Kira has the complete view of all customers. Never share one customer's data with another.

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

### After provisioning

When a new instance is provisioned, it becomes externally reachable automatically. The **chat URL** for users is:

```
https://kai.emai.dev/chat/<slug>?token=kai-<slug>-dev&host=wss://kai.emai.dev/ws/<slug>
```

Example for customer "Test Firma" (slug: `test-firma`):
```
https://kai.emai.dev/chat/test-firma?token=kai-test-firma-dev&host=wss://kai.emai.dev/ws/test-firma
```

Always share this full URL with the user after provisioning. The gateway token follows the pattern `kai-<slug>-dev`.

### Device pairing

When a user opens the chat URL for the first time, their browser creates a device identity (Ed25519 keypair). This device must be approved before the user can chat. Pending requests expire after 5 minutes.

To approve devices, run these commands inside the Kai pod:

```bash
# List pending and paired devices
kubectl -n emai-swarm exec deployment/kai-<slug> -c agent -- openclaw devices list

# Approve the most recent pending device
kubectl -n emai-swarm exec deployment/kai-<slug> -c agent -- openclaw devices approve

# Approve a specific device by request ID
kubectl -n emai-swarm exec deployment/kai-<slug> -c agent -- openclaw devices approve <requestId>

# Reject a pending request
kubectl -n emai-swarm exec deployment/kai-<slug> -c agent -- openclaw devices reject <requestId>

# Remove a paired device
kubectl -n emai-swarm exec deployment/kai-<slug> -c agent -- openclaw devices remove <deviceId>
```

**Workflow when a user reports they can't connect:**
1. Ask the user to open the chat URL and wait on the pairing screen
2. Run `kubectl -n emai-swarm exec deployment/kai-<slug> -c agent -- openclaw devices list` to see pending requests
3. Run `kubectl -n emai-swarm exec deployment/kai-<slug> -c agent -- openclaw devices approve` to approve
4. Tell the user to refresh the page

### Notes
- The slug is auto-derived from the customer name (lowercase, hyphens)
- Provisioning creates: Deployment, Service, ConfigMap, PVC, NetworkPolicy, and Ingress (external access)
- The external URL is shown in `swarm-ctl status <slug>` under the EXTERNAL column
- Suspended instances keep their PVC data intact
- Deletion cascades to all child resources (including Ingress) via Kubernetes ownerReferences

## Web Search

For research topic exploration and market intelligence.

## File Operations

Read and write files in the HQ repository at /home/node/.openclaw/workspace/headquarter.
