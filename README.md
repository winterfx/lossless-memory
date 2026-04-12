# lossless-memory

Claude Code session memory that never forgets. Incrementally ingests conversation transcripts and builds a hierarchical summary DAG — raw messages are preserved, summaries compress upward, and every level stays reachable.

## How it works

```
Transcript (JSONL)
      │
      ▼
  ┌────────┐     messages table
  │ ingest │────▶ + FTS index
  └────────┘
      │
      ▼
  ┌────────────────┐
  │ leaf summaries │   depth 0 — chunk by 20k tokens, summarize each chunk
  └────────────────┘
      │  when ≥ 4 leaves accumulate
      ▼
  ┌──────────────────┐
  │ condensed (d1)   │   session-level: merge 4 leaves → 1 condensed
  └──────────────────┘
      │  when ≥ 4 at d1
      ▼
  ┌──────────────────┐
  │ condensed (d2+)  │   phase / project level, repeat until fanout < 4
  └──────────────────┘
      │
      ▼
  ┌──────────┐
  │ overview │   top-depth summaries → injected into SessionStart hook
  └──────────┘
```

## Summary mechanism

### 1. Ingest

`lcm ingest` reads the Claude Code JSONL transcript **incrementally** (byte-offset tracking). Each message becomes a row in `messages` with an estimated token count. Tool calls are reduced to markers (`[tool_use: name]`, `[tool_result]`) — content is discarded to save space.

### 2. Leaf summaries (depth 0)

Triggered by `lcm digest`. Uncovered messages in a session are chunked into groups of up to **20,000 tokens**. Each chunk is sent to an LLM with a compaction prompt that preserves decisions, rationale, file operations, and open work while stripping filler.

- Minimum **10 messages** to trigger (avoids summarizing half-finished work).
- On `SessionEnd`, **force-flush** summarizes whatever remains.
- Each leaf links back to its source message IDs via `summary_messages`.

### 3. Condensation (depth 1+)

After leaves are created, `RunCondensation` walks upward:

```
for depth = 0, 1, 2, …:
    unconsumed = summaries at this depth not yet rolled up
    if len(unconsumed) < 4: break
    group into chunks of 4
    summarize each chunk → new summary at depth+1
    link via summary_parents
```

Each depth has a purpose-tuned prompt:

| Depth | Scope | Keeps | Drops |
|-------|-------|-------|-------|
| 0 (leaf) | Message chunk | Decisions, file ops, active tasks | Filler, repetition |
| 1 | Session | Chronological timeline, superseded decisions | Dead-end explorations |
| 2 | Multi-session phase | Trajectory, evolved decisions | Per-session operational detail |
| 3+ | Project arc | Durable lessons, key constraints | Method details, stale references |

Every summary ends with `"Expand for details about: ..."` — a compressed pointer to what was dropped, enabling targeted recall.

### 4. Overview injection

`lcm overview` selects the **highest-depth** summaries (up to 2,000 tokens) and formats them as markdown. The SessionStart hook injects this into the new conversation as `additionalContext`, giving the model a warm start.

### 5. Recall

`lcm recall` provides three operations for drilling back down:

- **search** — FTS5 full-text search across messages and summaries.
- **describe** — Show a summary with its parent/child lineage.
- **expand** — Walk the DAG from any summary down to source messages.

## Key thresholds

| Constant | Value | Role |
|----------|-------|------|
| `LeafChunkTokens` | 20,000 | Max tokens per leaf's source |
| `LeafMinMessages` | 10 | Min messages before leaf creation |
| `LeafMinFanout` | 4 | Leaves needed to trigger d0→d1 |
| `CondensedMinFanout` | 4 | Summaries needed for d(n)→d(n+1) |
| `maxOverviewTokens` | 2,000 | Token budget for SessionStart injection |

## Hook integration

Configured as Claude Code hooks in `settings.json`:

- **SessionStart** → `lcm overview` → injects prior context
- **SessionEnd** → `lcm digest` → ingest + leaf + condense
- **Notification stop hook** → `lcm ingest` (optional, for mid-session persistence)

## Configuration

```sh
LCM_API_KEY=<key>                  # LLM API auth
LCM_API_BASE_URL=<base-url>       # OpenAI-compatible endpoint
LCM_MODEL=gpt-4o-mini             # Model (default: gpt-4o-mini)
```

## Build

```sh
make build   # requires CGO for SQLite FTS5
```
