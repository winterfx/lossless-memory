# lossless-memory

Claude Code session memory that never forgets. Incrementally ingests conversation transcripts and builds a hierarchical summary DAG — raw messages are preserved, summaries compress upward, and every level stays reachable.

## How it works

```
Transcript (JSONL)
      │
      ▼
  ┌────────┐     messages table
  │ ingest │────▶ + FTS index (Latin + CJK)
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

## Project structure

```
lossless-memory/
├── cmd/lcm/main.go              # CLI entry point
├── internal/
│   ├── config/config.go         # LLM configuration (env vars)
│   ├── db/
│   │   ├── schema.go            # SQLite schema + FTS5 virtual tables
│   │   ├── store.go             # DB connection & lifecycle
│   │   ├── messages.go          # Message CRUD & token estimation
│   │   ├── workspaces.go        # Workspace management
│   │   ├── summaries.go         # Summary nodes & DAG links
│   │   ├── fts.go               # FTS5 search engine (CJK + Latin)
│   │   └── store_test.go        # Tests
│   ├── digest/
│   │   ├── leaf.go              # Leaf summary creation (depth 0)
│   │   ├── condense.go          # Multi-depth condensation
│   │   ├── openai.go            # OpenAI-compatible LLM client
│   │   └── prompts.go           # Depth-specific summarization prompts
│   ├── ingest/
│   │   ├── ingest.go            # Incremental JSONL parsing
│   │   └── transcript.go        # Claude Code transcript structure
│   ├── recall/
│   │   ├── search.go            # FTS search with JSON output
│   │   ├── describe.go          # Summary lineage inspection
│   │   ├── expand.go            # DAG traversal to source messages
│   │   └── recall_test.go       # Tests
│   ├── overview/overview.go     # SessionStart context generation
│   └── hookio/hookio.go         # Claude Code hook I/O (JSON stdin/stdout)
├── Makefile
├── go.mod                       # Go 1.25, sole dep: go-sqlite3
└── go.sum
```

## CLI usage

```
lcm <command> [args]
```

| Command | Description |
|---------|-------------|
| `ingest` | Parse Claude Code JSONL transcript, store messages incrementally (reads hook JSON from stdin) |
| `digest` | Ingest + create leaf summaries + run condensation (reads hook JSON from stdin) |
| `overview` | Generate SessionStart context from highest-depth summaries (reads hook JSON from stdin) |
| `recall search` | Full-text search across messages and summaries |
| `recall describe` | Show a summary with its parent/child lineage |
| `recall expand` | Walk the DAG from any summary down to source messages |
| `status` | Show workspace statistics (message/session/summary counts) |

### recall search

```sh
lcm recall search --cwd <path> --query <text> \
    [--mode full_text|regex] \
    [--scope messages|summaries|both] \
    [--sort relevance|recency|hybrid] \
    [--since <datetime>] [--before <datetime>] \
    [--all] [--limit N]
```

### recall describe / expand

```sh
lcm recall describe --id <sum_xxx>
lcm recall expand  --id <sum_xxx> [--max-depth N] [--include-messages]
```

### status

```sh
lcm status --cwd <path>
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

## Search features

The search engine supports multiple modes and backends:

| Mode | Backend | Best for |
|------|---------|----------|
| `full_text` (default) | FTS5 | Latin text, fast ranked search |
| `full_text` | FTS5 CJK trigram | Chinese/Japanese/Korean (auto-detected) |
| `full_text` | LIKE fallback | Short CJK queries (<3 runes) or trigram miss |
| Wildcard (`*`, `%`) | LIKE | Partial matches |
| `regex` | Go regexp | Complex patterns (scans up to 10K rows) |

Sort modes: `relevance` (FTS5 rank), `recency` (newest first), `hybrid` (rank decayed by time).

CJK detection covers Unicode ranges for CJK Unified Ideographs, Hiragana, Katakana, and Hangul. Token estimation uses 1 rune ≈ 1 token for CJK vs 4 chars/token for ASCII.

## Key thresholds

| Constant | Value | Role |
|----------|-------|------|
| `LeafChunkTokens` | 20,000 | Max tokens per leaf's source |
| `LeafMinMessages` | 10 | Min messages before leaf creation |
| `LeafMinFanout` | 4 | Leaves needed to trigger d0→d1 |
| `CondensedMinFanout` | 4 | Summaries needed for d(n)→d(n+1) |
| `maxOverviewTokens` | 2,000 | Token budget for SessionStart injection |

## Claude Code integration

lossless-memory 通过两种方式与 Claude Code 集成：

- **Hooks（自动）** — 会话生命周期事件自动触发 ingest/digest/overview
- **Skill（用户调用）** — 通过 `/recall` 斜杠命令搜索和浏览历史记忆

```
┌─────────────────────────────────────────────────────┐
│                   Claude Code                       │
│                                                     │
│  SessionStart ──hook──▶ lcm overview                │
│       │                    │                        │
│       │              additionalContext               │
│       ◀────────────────────┘                        │
│                                                     │
│  会话进行中 ──/recall──▶ lcm recall search/describe/expand │
│                                                     │
│  SessionEnd ──hook──▶ lcm digest                    │
│  Stop       ──hook──▶ lcm digest (可选)              │
└─────────────────────────────────────────────────────┘
```

### 1. Hook 配置

在 `~/.claude/settings.json`（全局）或项目级 `.claude/settings.json` 中添加：

```json
{
  "hooks": {
    "SessionStart": [
      {
        "matcher": "",
        "hooks": [
          {
            "type": "command",
            "command": "lcm overview"
          }
        ]
      }
    ],
    "SessionEnd": [
      {
        "matcher": "",
        "hooks": [
          {
            "type": "command",
            "command": "lcm digest"
          }
        ]
      }
    ],
    "Stop": [
      {
        "matcher": "",
        "hooks": [
          {
            "type": "command",
            "command": "lcm digest"
          }
        ]
      }
    ]
  }
}
```

| Hook 事件 | 命令 | 作用 |
|-----------|------|------|
| `SessionStart` | `lcm overview` | 从最高层摘要生成上下文，注入到新会话中 |
| `SessionEnd` | `lcm digest` | 增量解析 transcript，创建叶子摘要 + 向上凝缩 |
| `Stop` | `lcm digest` | 每次 Claude 停止响应时刷新记忆（可选，保证长会话不丢数据）|

Hook 通过 stdin/stdout 的 JSON 协议通信：

**输入**（Claude Code → lcm）：
```json
{
  "session_id": "sess_abc123",
  "transcript_path": "/Users/you/.claude/sessions/sess_abc123/transcript.jsonl",
  "cwd": "/Users/you/project",
  "hook_event_name": "SessionStart"
}
```

**输出**（lcm → Claude Code，仅 `overview` 命令）：
```json
{
  "hookSpecificOutput": {
    "hookEventName": "SessionStart",
    "additionalContext": "## Prior Session Context\n\n### [d2] 2026-04-10 to 2026-04-12\n- Implemented auth flow..."
  }
}
```

### 2. Skill 配置（/recall）

Skill 定义文件位于 `.claude/skills/recall/SKILL.md`，提供 `/recall` 斜杠命令，用户可在会话中随时调用来搜索历史记忆。

**安装方式**：将本项目的 `.claude/skills/recall/` 目录复制到你的项目或 `~/.claude/skills/recall/`。

```
.claude/skills/recall/SKILL.md    # 项目级（可 git 提交共享）
~/.claude/skills/recall/SKILL.md  # 全局（所有项目可用）
```

Skill 前置声明：

```yaml
---
name: recall
description: Search and explore past session memory from the lossless-memory database
user_invocable: true
---
```

**使用方式**：在 Claude Code 中输入 `/recall` 或让 Claude 自动识别相关意图。

Skill 为 Claude 提供三个工具：

| 工具 | 命令 | 用途 |
|------|------|------|
| `lcm_grep` | `lcm recall search` | 全文/正则搜索消息和摘要 |
| `lcm_describe` | `lcm recall describe` | 查看摘要详情及上下层关系 |
| `lcm_expand` | `lcm recall expand` | 沿 DAG 向下展开到源消息 |

**逐级升级流程**：

```
/recall "authentication changes"
    │
    ▼
  grep（搜索）── 找到相关摘要 ID
    │
    ▼
  describe ── 检查摘要的层级、时间范围
    │
    ▼
  expand（深度召回）── 委托子 agent 展开 DAG，避免上下文溢出
```

对于复杂问题，Skill 指导 Claude 将展开操作委托给子 agent（Agent tool），防止大量展开结果占满上下文窗口。子 agent 返回结构化 JSON：

```json
{
  "answer": "synthesized answer based on evidence",
  "citedIds": ["sum_xxx", "sum_yyy"],
  "expandedSummaryCount": 3,
  "truncated": false
}
```

### 3. 完整安装步骤

```sh
# 1. 构建并安装二进制
make build && make install   # → ~/.local/bin/lcm

# 2. 配置环境变量（摘要所需的 LLM）
export LCM_API_KEY=<your-key>
export LCM_API_BASE_URL=<openai-compatible-endpoint>
export LCM_MODEL=gpt-4o-mini   # 可选，默认 gpt-4o-mini

# 3. 配置 hooks（添加到 ~/.claude/settings.json）
# 参见上方 Hook 配置 JSON

# 4. 安装 recall skill
cp -r .claude/skills/recall ~/.claude/skills/recall   # 全局安装
# 或保留在项目目录中作为项目级 skill

# 5. 验证
lcm status --cwd "$(pwd)"
```

## Configuration

```sh
LCM_API_KEY=<key>                 # LLM API auth (required for digest)
LCM_API_BASE_URL=<base-url>      # OpenAI-compatible endpoint
LCM_MODEL=gpt-4o-mini            # Model (default: gpt-4o-mini)
```

Database is stored at `~/.claude/lcm.db` (SQLite with WAL mode).

## Build & install

```sh
make build     # → ./lcm (requires CGO for SQLite FTS5)
make install   # → ~/.local/bin/lcm
make test      # run tests
make clean     # remove binary and database
```

Requires `CGO_ENABLED=1` — the SQLite FTS5 extension is compiled in via `CGO_CFLAGS="-DSQLITE_ENABLE_FTS5"`.
