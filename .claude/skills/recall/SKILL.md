---
name: recall
description: Search and explore past session memory from the lossless-memory database
user_invocable: true
---

# Recall — Search Past Sessions

Use `lcm recall` to search, describe, and expand summaries from past sessions stored in the lossless-memory database.

## Tools

### lcm_grep (search)

Find relevant messages and summaries by keyword, regex, or CJK text:

```bash
lcm recall search --cwd "$(pwd)" --query "<search terms>" \
    [--mode full_text|regex] \
    [--scope messages|summaries|both] \
    [--sort relevance|recency|hybrid] \
    [--since <datetime>] [--before <datetime>] \
    [--all] [--limit N]
```

**Parameters:**
- `--query` — search text (required)
- `--mode` — `full_text` (default, FTS5 + CJK trigram) or `regex` (Go regex matching)
- `--scope` — `both` (default), `messages`, or `summaries`
  - Always start with `both` (default). Do not narrow scope preemptively.
  - Use `messages` when searching for verbatim content: exact commands, code snippets, error messages, URLs.
  - Use `summaries` when searching for decisions, conclusions, or high-level context.
  - Only switch from `both` when results are too noisy and you need to filter.
- `--sort` — `relevance` (default, BM25 rank), `recency` (newest first), or `hybrid` (time-decayed relevance)
- `--since` / `--before` — ISO datetime range filter
- `--all` — search across all workspaces
- `--limit` — max results (default 20)

**CJK support:** Chinese/Japanese/Korean queries are automatically routed to trigram FTS5 tables or LIKE fallback. No special flags needed.

### lcm_describe (describe)

Get full details of a specific summary, including its lineage (parent/child relationships):

```bash
lcm recall describe --id <sum_xxx>
```

Returns: summary content, kind (leaf/condensed), depth, time range, parent IDs, child IDs, linked message IDs.

### lcm_expand (expand)

Walk the DAG from a summary down to its source messages:

```bash
lcm recall expand --id <sum_xxx> [--max-depth N] [--include-messages]
```

**Parameters:**
- `--max-depth` — how deep to recurse (default 3)
- `--include-messages` — include source messages for leaf summaries

## When to Use Each Tool

- **lcm_grep**: First step for any recall query. Use `full_text` for keyword search, `regex` for patterns. Use `--sort hybrid` when both relevance and recency matter.
- **lcm_describe**: When you need to understand a summary's context, time range, or position in the hierarchy before deciding whether to expand.
- **lcm_expand_query** (see below): When search results are insufficient — summaries hint at relevant content but you need the underlying details. Delegate to a sub-agent to avoid flooding your context.

**Escalation flow:** grep → describe → expand_query (only if needed)

## Deep Recall: expand_query

When search results reference summaries but you need deeper context to answer the user's question, **delegate to a sub-agent** using the Agent tool. This prevents large expansion results from overwhelming your context window.

### How to Use

1. Identify seed summary IDs from search results
2. Spawn a sub-agent using the Agent tool with this prompt template:

```
You are an LCM retrieval navigator. Use the following bash commands to retrieve evidence from the lossless-memory database:

- `lcm recall describe --id <sum_xxx>` — inspect summary metadata, subtree structure, and linked IDs
- `lcm recall expand --id <sum_xxx> --max-depth 2 --include-messages` — walk DAG to source messages
- `lcm recall search --cwd "<cwd>" --query "<text>" --scope summaries --sort relevance` — find additional related summaries

Seed summary IDs: {comma-separated summary IDs from search results}
User question: {the user's original question}

Strategy:
1. Start with `lcm recall describe` on seed summaries to inspect subtree structure and linked IDs
2. If additional candidates are needed, use `lcm recall search` scoped to summaries
3. Select branches that seem most relevant; prefer high-signal paths first
4. Call `lcm recall expand` selectively — do not expand everything blindly
5. Use --include-messages only for leaf summaries with relevant evidence
6. Keep total expansion calls reasonable (aim for 3-5 calls maximum)

Synthesize an answer from retrieved evidence, not assumptions.

Return JSON:
{
  "answer": "your synthesized answer based on evidence",
  "citedIds": ["sum_xxx", "sum_yyy"],
  "expandedSummaryCount": N,
  "truncated": false
}
```

3. Parse the sub-agent's JSON reply and present the answer to the user

### Example

```
User: "What authentication changes were made last week?"

1. Run: lcm recall search --cwd "$(pwd)" --query "authentication" --sort hybrid --limit 10
2. Results show sum_leaf_001 and sum_cond_003 are relevant
3. Spawn Agent tool with seed IDs and user question
4. Sub-agent describes, expands, and synthesizes
5. Present the synthesized answer with citations
```

## Status

To check database statistics:

```bash
lcm status --cwd "$(pwd)"
```
