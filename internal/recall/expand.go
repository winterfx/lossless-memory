package recall

import (
	"encoding/json"
	"fmt"

	"github.com/winter-wang/lossless-memory/internal/db"
)

// ExpandResult is the JSON output of expand.
type ExpandResult struct {
	SummaryID string          `json:"summary_id"`
	Depth     int             `json:"depth"`
	Content   string          `json:"content"`
	Children  []ExpandResult  `json:"children,omitempty"`
	Messages  []ExpandMessage `json:"messages,omitempty"`
}

// ExpandMessage is a message in the expansion tree.
type ExpandMessage struct {
	ID      int64  `json:"id"`
	Role    string `json:"role"`
	Content string `json:"content"`
	Seq     int    `json:"seq"`
}

// Expand walks the DAG from a summary down to its source messages.
func Expand(store *db.Store, summaryID string, maxDepth int, includeMessages bool) (string, error) {
	result, err := expandRecursive(store, summaryID, 0, maxDepth, includeMessages)
	if err != nil {
		return "", err
	}
	b, _ := json.MarshalIndent(result, "", "  ")
	return string(b), nil
}

func expandRecursive(store *db.Store, summaryID string, currentDepth, maxDepth int, includeMessages bool) (*ExpandResult, error) {
	sum, err := store.GetSummary(summaryID)
	if err != nil {
		return nil, fmt.Errorf("getting summary %s: %w", summaryID, err)
	}
	if sum == nil {
		return nil, fmt.Errorf("summary %s not found", summaryID)
	}

	result := &ExpandResult{
		SummaryID: sum.ID,
		Depth:     sum.Depth,
		Content:   sum.Content,
	}

	if currentDepth >= maxDepth {
		return result, nil
	}

	if sum.Kind == "condensed" {
		parentIDs, err := store.GetSummaryParentIDs(summaryID)
		if err != nil {
			return nil, err
		}
		for _, pid := range parentIDs {
			child, err := expandRecursive(store, pid, currentDepth+1, maxDepth, includeMessages)
			if err != nil {
				return nil, err
			}
			result.Children = append(result.Children, *child)
		}
	} else if sum.Kind == "leaf" && includeMessages {
		msgIDs, err := store.GetSummaryMessageIDs(summaryID)
		if err != nil {
			return nil, err
		}
		msgs, err := store.GetMessagesByIDs(msgIDs)
		if err != nil {
			return nil, err
		}
		for _, m := range msgs {
			result.Messages = append(result.Messages, ExpandMessage{
				ID:      m.ID,
				Role:    m.Role,
				Content: m.Content,
				Seq:     m.Seq,
			})
		}
	}

	return result, nil
}
