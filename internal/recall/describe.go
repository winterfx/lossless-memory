package recall

import (
	"encoding/json"
	"fmt"

	"github.com/winter-wang/lossless-memory/internal/db"
)

// DescribeResult is the JSON output of describe.
type DescribeResult struct {
	ID        string   `json:"id"`
	Kind      string   `json:"kind"`
	Depth     int      `json:"depth"`
	Content   string   `json:"content"`
	Model     string   `json:"model,omitempty"`
	Earliest  string   `json:"earliest_at"`
	Latest    string   `json:"latest_at"`
	CreatedAt string   `json:"created_at"`
	// Lineage
	ParentIDs  []string `json:"parent_ids,omitempty"`  // for condensed: source summaries
	MessageIDs []int64  `json:"message_ids,omitempty"` // for leaf: source messages
	ChildIDs   []string `json:"child_ids,omitempty"`   // summaries that built on this one
}

// Describe returns detailed information about a summary, including its lineage.
func Describe(store *db.Store, summaryID string) (string, error) {
	sum, err := store.GetSummary(summaryID)
	if err != nil {
		return "", fmt.Errorf("getting summary: %w", err)
	}
	if sum == nil {
		return "", fmt.Errorf("summary %s not found", summaryID)
	}

	result := DescribeResult{
		ID:        sum.ID,
		Kind:      sum.Kind,
		Depth:     sum.Depth,
		Content:   sum.Content,
		Model:     sum.Model,
		Earliest:  sum.EarliestAt.Format("2006-01-02 15:04:05"),
		Latest:    sum.LatestAt.Format("2006-01-02 15:04:05"),
		CreatedAt: sum.CreatedAt.Format("2006-01-02 15:04:05"),
	}

	if sum.Kind == "leaf" {
		msgIDs, err := store.GetSummaryMessageIDs(summaryID)
		if err != nil {
			return "", err
		}
		result.MessageIDs = msgIDs
	} else {
		parentIDs, err := store.GetSummaryParentIDs(summaryID)
		if err != nil {
			return "", err
		}
		result.ParentIDs = parentIDs
	}

	childIDs, err := store.GetChildSummaryIDs(summaryID)
	if err != nil {
		return "", err
	}
	result.ChildIDs = childIDs

	b, _ := json.MarshalIndent(result, "", "  ")
	return string(b), nil
}
