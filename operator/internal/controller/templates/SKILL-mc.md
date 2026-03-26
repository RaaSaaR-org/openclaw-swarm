---
name: mission_control
description: MissionControl (mc) CLI for project management — list, create, show, and update tasks, research, and sprints.
metadata: {"openclaw": {"always": true}}
---

# MissionControl (mc) CLI

The `mc` binary is in your workspace. **Always use `-y` flag and run from the workspace directory.**

## How to Run

```bash
cd /home/node/.openclaw/workspace && ./mc -y <command>
```

The `-y` flag skips interactive confirmations (required in non-TTY environments).

## Common Commands

### Project Overview
```bash
./mc -y status                              # Dashboard: entity counts, recent activity
```

### Tasks
```bash
./mc -y list tasks                          # All tasks
./mc -y list tasks --status todo            # Filter by status (backlog, todo, in-progress, review, done)
./mc -y show TASK-001                       # Show task details

./mc -y new task "Task title"               # Create a new task
./mc -y new task "Title" --status todo --priority 2  # With options
./mc -y move TASK-001 --status in-progress  # Update task status
./mc -y move TASK-001 --status done         # Mark task as done
```

### Research
```bash
./mc -y list research                       # All research topics
./mc -y new research "Topic"                # Create research topic
./mc -y show RES-001                        # Show research details
```

### Sprints
```bash
./mc -y list sprints                        # All sprints
./mc -y show SPR-001                        # Show sprint details
```

### Maintenance
```bash
./mc -y index                               # Rebuild indexes after manual file edits
./mc -y validate                            # Check repository structure
```

## Rules

1. **Always use `-y`**: Every mc command must include `-y` to skip confirmations. Without it, create commands will be cancelled.
2. **Always `cd` first**: Run `cd /home/node/.openclaw/workspace && ./mc -y <command>`.
3. **Use Entity IDs**: Always reference entities by their ID (TASK-001, RES-001, SPR-001) in responses.
4. **Report results**: After creating or updating an entity, confirm the action with the entity ID and title.
5. **Proactive monitoring**: When asked about project status, use `./mc -y status` first, then drill into specifics.
6. **No web search needed**: All project data is local. Do NOT use web_search for project information.
