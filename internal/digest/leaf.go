package digest

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/winter-wang/lossless-memory/internal/db"
)

const (
	LeafChunkTokens  = 20000
	LeafTargetTokens = 2000
	LeafMinMessages  = 10 // minimum uncovered messages to trigger leaf creation
)

// Summarizer calls an LLM to generate summaries.
type Summarizer interface {
	Summarize(ctx context.Context, prompt string) (string, error)
	Model() string
}

// CreateLeafSummaries finds uncovered messages in a session and creates leaf summaries.
// When forceFlush is true (e.g. session end), the minimum message threshold is skipped.
func CreateLeafSummaries(ctx context.Context, store *db.Store, sessionID string, summarizer Summarizer, forceFlush bool) ([]string, error) {
	msgs, err := store.GetUncoveredMessages(sessionID)
	if err != nil {
		return nil, fmt.Errorf("getting uncovered messages: %w", err)
	}
	if len(msgs) == 0 {
		return nil, nil
	}

	if !forceFlush && len(msgs) < LeafMinMessages {
		return nil, nil
	}

	// Chunk messages by token count
	chunks := chunkMessages(msgs, LeafChunkTokens)
	var summaryIDs []string

	for _, chunk := range chunks {
		id, err := createLeafFromChunk(ctx, store, chunk, summarizer)
		if err != nil {
			return summaryIDs, fmt.Errorf("creating leaf summary: %w", err)
		}
		summaryIDs = append(summaryIDs, id)
	}

	return summaryIDs, nil
}

func createLeafFromChunk(ctx context.Context, store *db.Store, msgs []db.Message, summarizer Summarizer) (string, error) {
	// Build prompt
	var sb strings.Builder
	sb.WriteString(GetPrompt(0))
	for _, m := range msgs {
		ts := m.CreatedAt.Format("2006-01-02 15:04")
		sb.WriteString(fmt.Sprintf("\n[%s] %s: %s\n", ts, m.Role, m.Content))
	}

	summary, err := summarizer.Summarize(ctx, sb.String())
	if err != nil {
		return "", fmt.Errorf("calling LLM: %w", err)
	}

	// Collect message IDs
	msgIDs := make([]int64, len(msgs))
	for i, m := range msgs {
		msgIDs[i] = m.ID
	}

	sumID := db.GenerateSummaryID()
	sum := &db.Summary{
		ID:          sumID,
		WorkspaceID: msgs[0].WorkspaceID,
		Kind:        "leaf",
		Depth:       0,
		Content:     summary,
		TokenCount:  db.EstimateTokens(summary),
		EarliestAt:  msgs[0].CreatedAt,
		LatestAt:    msgs[len(msgs)-1].CreatedAt,
		Model:       summarizer.Model(),
		CreatedAt:   time.Now(),
	}

	if err := store.InsertSummary(sum); err != nil {
		return "", fmt.Errorf("inserting summary: %w", err)
	}
	if err := store.LinkSummaryMessages(sumID, msgIDs); err != nil {
		return "", fmt.Errorf("linking messages: %w", err)
	}

	return sumID, nil
}

func chunkMessages(msgs []db.Message, maxTokens int) [][]db.Message {
	var chunks [][]db.Message
	var current []db.Message
	var currentTokens int

	for _, m := range msgs {
		if currentTokens+m.TokenCount > maxTokens && len(current) > 0 {
			chunks = append(chunks, current)
			current = nil
			currentTokens = 0
		}
		current = append(current, m)
		currentTokens += m.TokenCount
	}
	if len(current) > 0 {
		chunks = append(chunks, current)
	}
	return chunks
}
