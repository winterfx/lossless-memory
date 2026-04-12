---
name: recall
description: Search and explore past session memory from the lossless-memory database
user_invocable: true
---

# Recall — Search Past Sessions

Use `lcm recall` to search, describe, and expand summaries from past sessions stored in the lossless-memory database.

## Commands

### Search
Find relevant messages and summaries by keyword:
```bash
lcm recall search --cwd "$(pwd)" --query "<search terms>"
```

Options:
- `--all` — search across all workspaces, not just the current one
- `--limit N` — max results (default 20)

### Describe
Get full details of a specific summary, including its lineage (parent/child relationships):
```bash
lcm recall describe --id <sum_xxx>
```

### Expand
Walk the DAG from a summary down to its source messages:
```bash
lcm recall expand --id <sum_xxx>
```

Options:
- `--max-depth N` — how deep to recurse (default 3)
- `--include-messages` — include source messages for leaf summaries

## Workflow

1. User asks `/recall <query>` → run `lcm recall search` with the query
2. Present the search results (ID, type, snippet, rank)
3. If the user wants more detail on a specific summary → run `lcm recall describe --id <sum_xxx>`
4. If the user wants to drill down further → run `lcm recall expand --id <sum_xxx> --include-messages`

## Status
To check database statistics:
```bash
lcm status --cwd "$(pwd)"
```
