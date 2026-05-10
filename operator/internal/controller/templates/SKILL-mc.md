---
name: mission_control
description: MissionControl (mc) project-management skill — list, create, show, and update tasks, research, and sprints via the central MC API.
metadata: {"openclaw": {"always": true}}
---

# MissionControl (mc) skill

mc is a remote service. Use the `mc-client` wrapper, which translates familiar
CLI shapes into HTTPS calls against the central MC API. The wrapper reads
`MC_API_BASE`, `MC_API_TOKEN`, and `MC_CUSTOMER_ID` from your environment —
they're already set by the operator. **Always run from the workspace
directory.**

## How to run

```bash
cd /home/node/.openclaw/workspace && ./mc-client <command> [args…]
```

## Common commands

### Project overview
```bash
./mc-client status                              # Counts + recent activity
```

### Tasks
```bash
./mc-client list tasks                          # All your tasks (auto-scoped)
./mc-client list tasks --status todo            # Filter by status
./mc-client show TASK-001                       # Show task details

./mc-client new task "Task title"               # Create a new task
./mc-client new task "Title" --priority 2       # With priority
./mc-client move TASK-001 --status in-progress  # Update status
./mc-client move TASK-001 --status done         # Mark as done
```

### Research
```bash
./mc-client list research
./mc-client show RES-001
```

### Sprints
```bash
./mc-client list sprints
./mc-client show SPR-001
```

## Rules

1. **Tasks you create are auto-scoped to your customer.** Do not pass any
   `--customer` flag — the wrapper injects `MC_CUSTOMER_ID` for you, and the
   gateway rejects mismatching values.
2. **Cross-tenant access returns `404`.** That's expected, not a bug — if you
   try to read another customer's TASK-001, you'll get "no such entity".
3. **Use entity IDs in your responses.** Always reference `TASK-001`,
   `RES-001`, etc. in messages back to the user.
4. **`-y` is accepted for back-compat but is a no-op.** The API has no
   interactive prompts; the wrapper still accepts the flag so old habits
   work.
5. **Failures print to stderr and exit 0.** Look for `mc-client: HTTP 4xx`
   in the output and react accordingly (most often a 400 from a body the
   gateway rejected).
6. **No web search needed.** All project data is local to the central MC API
   instance.
