package digest

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/winter-wang/lossless-memory/internal/db"
)

// Fanout thresholds for condensation at each depth.
const (
	LeafMinFanout      = 4 // depth 0 → 1: need at least 4 leaf summaries
	CondensedMinFanout = 4 // depth 1+ → N+1: need at least 4 summaries
)

// RunCondensation checks each depth level and merges summaries where fanout is met.
// Runs from the shallowest depth upward, no depth limit.
func RunCondensation(ctx context.Context, store *db.Store, workspaceID int64, summarizer Summarizer) ([]string, error) {
	var createdIDs []string

	for depth := 0; ; depth++ {
		sums, err := store.GetUnconsumedSummaries(workspaceID, depth)
		if err != nil {
			return createdIDs, err
		}

		fanout := CondensedMinFanout
		if depth == 0 {
			fanout = LeafMinFanout
		}

		if len(sums) < fanout {
			break // not enough summaries at this depth
		}

		// Condense all summaries at this depth into one at depth+1
		chunks := chunkSummaries(sums, fanout)
		for _, chunk := range chunks {
			id, err := condenseSummaries(ctx, store, chunk, depth+1, summarizer)
			if err != nil {
				return createdIDs, fmt.Errorf("condensing depth %d: %w", depth, err)
			}
			createdIDs = append(createdIDs, id)
		}
	}

	return createdIDs, nil
}

func condenseSummaries(ctx context.Context, store *db.Store, sums []db.Summary, targetDepth int, summarizer Summarizer) (string, error) {
	// Build prompt
	var sb strings.Builder
	sb.WriteString(GetPrompt(targetDepth))
	for _, s := range sums {
		sb.WriteString(fmt.Sprintf("\n--- Summary %s (depth %d, %s to %s) ---\n%s\n",
			s.ID, s.Depth,
			s.EarliestAt.Format("2006-01-02"),
			s.LatestAt.Format("2006-01-02"),
			s.Content,
		))
	}

	summary, err := summarizer.Summarize(ctx, sb.String())
	if err != nil {
		return "", fmt.Errorf("calling LLM: %w", err)
	}

	parentIDs := make([]string, len(sums))
	for i, s := range sums {
		parentIDs[i] = s.ID
	}

	sumID := db.GenerateSummaryID()
	sum := &db.Summary{
		ID:          sumID,
		WorkspaceID: sums[0].WorkspaceID,
		Kind:        "condensed",
		Depth:       targetDepth,
		Content:     summary,
		TokenCount:  db.EstimateTokens(summary),
		EarliestAt:  sums[0].EarliestAt,
		LatestAt:    sums[len(sums)-1].LatestAt,
		Model:       summarizer.Model(),
		CreatedAt:   time.Now(),
	}

	if err := store.InsertSummary(sum); err != nil {
		return "", fmt.Errorf("inserting condensed summary: %w", err)
	}
	if err := store.LinkSummaryParents(sumID, parentIDs); err != nil {
		return "", fmt.Errorf("linking parents: %w", err)
	}

	return sumID, nil
}

func chunkSummaries(sums []db.Summary, fanout int) [][]db.Summary {
	var chunks [][]db.Summary
	for i := 0; i+fanout <= len(sums); i += fanout {
		chunks = append(chunks, sums[i:i+fanout])
	}
	return chunks
}
