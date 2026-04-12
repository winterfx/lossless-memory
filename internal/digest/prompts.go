package digest

// Depth-aware summarization prompts, adapted from lossless-claw.

const systemPrompt = "You are a context-compaction summarization engine. Follow user instructions exactly and return plain text summary content only."

const leafPrompt = `You summarize a SEGMENT of a coding conversation for future model turns.
Treat this as incremental memory compaction input, not a full-conversation summary.

Normal summary policy:
- Preserve key decisions, rationale, constraints, and active tasks.
- Keep essential technical details needed to continue work safely.
- Remove obvious repetition and conversational filler.

Output requirements:
- Plain text only.
- No preamble, headings, or markdown formatting.
- Keep it concise while preserving required details.
- Track file operations (created, modified, deleted, renamed) with file paths and current status.
- If no file operations appear, include exactly: "Files: none".
- End with exactly: "Expand for details about: <comma-separated list of what was dropped or compressed>".
- Target length: about 2400 tokens or less.

`

const condensedD1Prompt = `You are compacting leaf-level conversation summaries into a single condensed memory node.
You are preparing context for a fresh model instance that will continue this conversation.

Preserve:
- Decisions made and their rationale when rationale matters going forward.
- Earlier decisions that were superseded, and what replaced them.
- Completed tasks/topics with outcomes.
- In-progress items with current state and what remains.
- Blockers, open questions, and unresolved tensions.
- Specific references (names, paths, URLs, identifiers) needed for continuation.

Drop low-value detail:
- Intermediate dead ends where the conclusion is already known.
- Transient states that are already resolved.
- Tool-internal mechanics and process scaffolding.

Use plain text. No mandatory structure.
Include a timeline with timestamps (hour or half-hour) for significant events.
Present information chronologically and mark superseded decisions.
End with exactly: "Expand for details about: <comma-separated list of what was dropped or compressed>".
Target length: about 2000 tokens.

`

const condensedD2Prompt = `You are condensing multiple session-level summaries into a higher-level memory node.
A future model should understand trajectory, not per-session minutiae.

Preserve:
- Decisions still in effect and their rationale.
- Decisions that evolved: what changed and why.
- Completed work with outcomes.
- Active constraints, limitations, and known issues.
- Current state of in-progress work.

Drop:
- Session-local operational detail and process mechanics.
- Identifiers that are no longer relevant.
- Intermediate states superseded by later outcomes.

Use plain text. Brief headers are fine if useful.
Include a timeline with dates and approximate time of day for key milestones.
End with exactly: "Expand for details about: <comma-separated list of what was dropped or compressed>".
Target length: about 2000 tokens.

`

const condensedD3PlusPrompt = `You are creating a high-level memory node from multiple phase-level summaries.
This may persist for the rest of the conversation. Keep only durable context.

Preserve:
- Key decisions and rationale.
- What was accomplished and current state.
- Active constraints and hard limitations.
- Important relationships between people, systems, or concepts.
- Durable lessons learned.

Drop:
- Operational and process detail.
- Method details unless the method itself was the decision.
- Specific references unless essential for continuation.

Use plain text. Be concise.
Include a brief timeline with dates (or date ranges) for major milestones.
End with exactly: "Expand for details about: <comma-separated list of what was dropped or compressed>".
Target length: about 1500 tokens.

`

// SystemPrompt returns the system prompt for the summarizer LLM.
func SystemPrompt() string {
	return systemPrompt
}

// GetPrompt returns the appropriate user prompt for the given depth.
func GetPrompt(depth int) string {
	switch depth {
	case 0:
		return leafPrompt
	case 1:
		return condensedD1Prompt
	case 2:
		return condensedD2Prompt
	default:
		return condensedD3PlusPrompt
	}
}
