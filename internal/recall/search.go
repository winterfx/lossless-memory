// Package recall implements retrieval operations on the DAG.
package recall

import (
	"encoding/json"
	"fmt"

	"github.com/winter-wang/lossless-memory/internal/db"
)

// SearchResult is the JSON output of a search.
type SearchResult struct {
	ID      string `json:"id"`
	Type    string `json:"type"` // "message" or "summary"
	Snippet string `json:"snippet"`
	Rank    float64 `json:"rank"`
}

// Search performs FTS5 search and returns JSON output.
func Search(store *db.Store, query, cwd string, searchAll bool, limit int) (string, error) {
	var results []db.SearchResult
	var err error

	if searchAll {
		results, err = store.SearchAllWorkspaces(query, limit)
	} else {
		wid, werr := store.GetWorkspaceID(cwd)
		if werr != nil {
			return "[]", nil // workspace not found, no results
		}
		results, err = store.SearchAll(query, wid, limit)
	}
	if err != nil {
		return "", fmt.Errorf("searching: %w", err)
	}

	out := make([]SearchResult, len(results))
	for i, r := range results {
		snippet := r.Content
		if len(snippet) > 300 {
			snippet = snippet[:300] + "..."
		}
		out[i] = SearchResult{
			ID:      r.ID,
			Type:    r.Type,
			Snippet: snippet,
			Rank:    r.Rank,
		}
	}

	b, _ := json.MarshalIndent(out, "", "  ")
	return string(b), nil
}
