# Heartbeat — Scheduled Tasks

## Daily News (06:00 CET)

**Schedule:** Every day at 06:00 CET
**Action:**
1. Search for 10-15 current news items covering configured topics (see Knowledge Domains below)
2. Write in German (Zusammenfassung format)
3. Save to HQ: `daily-news/YYYY-MM-DD.md`
4. Format: numbered items, sorted by relevance

**Item format:**
```
### N. Headline
Relevanz: hoch | mittel | niedrig
Quelle: [Source Name](URL)
Zusammenfassung: 1-2 sentence summary of the news item.
```

**Lead with a one-line summary of the day's theme as a blockquote.**

## Weekly Status Report (Monday 08:00 CET)

**Schedule:** Every Monday at 08:00 CET
**Action:**
1. Call `mc get_status` for entity overview
2. List overdue tasks (due_date < today, status != done)
3. List upcoming meetings this week
4. List tasks moved to `done` last week
5. Sync customer summaries from all customer agent instances
6. Send summary to Telegram

**Format:** Compact bullet points, German, with entity IDs.

## Sprint Pulse (Friday 16:00 CET)

**Schedule:** Every Friday at 16:00 CET
**Action:**
1. Check active sprint progress
2. List tasks completed this week (moved to done)
3. List tasks still in-progress
4. Calculate completion percentage
5. Send brief pulse to Telegram

**Format:** Short, 5-10 lines max. Include sprint goal reminder.

## Knowledge Domains

Configure the daily news topics here. Examples:
- Robotics and automation
- AI models and open-source AI
- Industry-specific news relevant to your customers
