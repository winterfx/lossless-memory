// Package overview generates high-level summaries for SessionStart injection.
package overview

import (
	"fmt"
	"strings"

	"github.com/winter-wang/lossless-memory/internal/db"
)

const maxOverviewTokens = 2000

// Generate returns a text overview of the highest-level summaries for a workspace.
// This is injected as additionalContext in the SessionStart hook.
func Generate(store *db.Store, cwd string) (string, error) {
	wid, err := store.GetWorkspaceID(cwd)
	if err != nil {
		return "", nil // workspace not found, no overview
	}

	// Get the highest depth summaries first
	sums, err := store.GetHighestDepthSummaries(wid, 0, 20)
	if err != nil {
		return "", fmt.Errorf("getting summaries: %w", err)
	}
	if len(sums) == 0 {
		return "", nil
	}

	// Find the max depth available
	maxDepth := 0
	for _, s := range sums {
		if s.Depth > maxDepth {
			maxDepth = s.Depth
		}
	}

	// Use only the highest depth summaries for the overview
	// If there's only depth 0, use those
	minDepth := maxDepth
	if maxDepth >= 2 {
		minDepth = maxDepth - 1 // include one level below max for more detail
	}

	var sb strings.Builder
	sb.WriteString("## Prior Session Context\n\n")

	totalTokens := 0
	for _, s := range sums {
		if s.Depth < minDepth {
			continue
		}
		tokens := s.TokenCount
		if totalTokens+tokens > maxOverviewTokens {
			break
		}
		sb.WriteString(fmt.Sprintf("### [d%d] %s to %s\n",
			s.Depth,
			s.EarliestAt.Format("2006-01-02"),
			s.LatestAt.Format("2006-01-02"),
		))
		sb.WriteString(s.Content)
		sb.WriteString("\n\n")
		totalTokens += tokens
	}

	if totalTokens == 0 {
		return "", nil
	}

	sb.WriteString("---\nUse /recall <query> to search for specific details from past sessions.\n")
	return sb.String(), nil
}
