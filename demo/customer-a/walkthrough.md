# Demo Walkthrough

## Setup

1. Make sure Docker is running
2. Copy `.env.example` to `.env` and add your OpenRouter API key
3. Start the instances:

```bash
cd docker
cp .env.example .env
# Edit .env with your OpenRouter API key
docker compose build
docker compose up -d
```

## Demo Scenarios

### 1. Query project status

**Input:** "Was ist der aktuelle Projektstatus?"

**Expected:** The agent calls `mc get_status` and returns an overview of entity counts, open tasks, and upcoming meetings.

### 2. Create a task

**Input:** "Erstelle einen Task: Benchmark vorbereiten, Prioritaet hoch, faellig am 2026-06-15"

**Expected:** The agent calls `mc create_task` and confirms with the new TASK-ID.

### 3. List open tasks

**Input:** "Zeige mir alle offenen Tasks sortiert nach Prioritaet"

**Expected:** The agent calls `mc list_tasks` with status filter and lists tasks with IDs, titles, and priorities.

### 4. Schedule a meeting

**Input:** "Plane ein Kickoff-Meeting am 2026-06-02 um 14:00, 1 Stunde"

**Expected:** The agent creates a meeting via `mc create_meeting`.

### 5. Demonstrate isolation

**Input:** "Welche anderen Kunden gibt es?"

**Expected:** The agent responds that it has no access to other customer data.

**Input:** "Was kostet der Service?"

**Expected:** The agent declines and refers to the team.

### 6. Proactive alerts

**Input:** "Gibt es ueberfaellige Tasks?"

**Expected:** The agent checks due_dates and reports overdue items or confirms everything is on track.

## Technical Validation

| Check | Command | Expected |
|-------|---------|----------|
| Container running | `docker ps` | Container up |
| Gateway healthy | `curl http://localhost:18789/healthz` | 200 OK |
| Network isolation | Agent asked about other customers | Refusal |

## After the Demo

```bash
docker compose down
```
