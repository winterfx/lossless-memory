// Package recall implements retrieval operations on the DAG.
package recall

import (
	"encoding/json"
	"fmt"

	"github.com/winter-wang/lossless-memory/internal/db"
)

// SearchResult is the JSON output of a search.
type SearchResult struct {
	ID        string  `json:"id"`
	Type      string  `json:"type"` // "message" or "summary"
	Snippet   string  `json:"snippet"`
	Rank      float64 `json:"rank,omitempty"`
	CreatedAt string  `json:"created_at,omitempty"`
	Role      string  `json:"role,omitempty"`
}

// SearchOptions defines all parameters for a search.
type SearchOptions struct {
	Query  string // search text
	Mode   string // "full_text" (default) | "regex"
	Scope  string // "both" (default) | "messages" | "summaries"
	Sort   string // "relevance" (default) | "recency" | "hybrid"
	Since  string // ISO datetime filter (optional)
	Before string // ISO datetime filter (optional)
	Cwd    string // workspace path
	All    bool   // search all workspaces
	Limit  int    // max results (default 20)
}

// Search performs search and returns JSON output.
func Search(store *db.Store, opts SearchOptions) (string, error) {
	if opts.Limit <= 0 {
		opts.Limit = 20
	}
	if opts.Mode == "" {
		opts.Mode = "full_text"
	}
	if opts.Scope == "" {
		opts.Scope = "both"
	}
	if opts.Sort == "" {
		opts.Sort = "relevance"
	}

	var workspaceID int64
	if !opts.All {
		wid, err := store.GetWorkspaceID(opts.Cwd)
		if err != nil {
			return "[]", nil // workspace not found
		}
		workspaceID = wid
	}

	p := db.SearchParams{
		Query:       opts.Query,
		WorkspaceID: workspaceID,
		Sort:        opts.Sort,
		Since:       opts.Since,
		Before:      opts.Before,
		Limit:       opts.Limit,
	}

	var msgs, sums []db.FTSSearchResult
	var err error

	// Dispatch by mode and scope
	if opts.Mode == "regex" {
		if opts.Scope != "summaries" {
			msgs, err = store.SearchMessagesRegex(p)
			if err != nil {
				return "", fmt.Errorf("regex search messages: %w", err)
			}
		}
		if opts.Scope != "messages" {
			sums, err = store.SearchSummariesRegex(p)
			if err != nil {
				return "", fmt.Errorf("regex search summaries: %w", err)
			}
		}
	} else {
		// full_text mode (FTS5, CJK, or LIKE)
		if opts.Scope != "summaries" {
			msgs, err = store.SearchMessagesFTS(p)
			if err != nil {
				return "", fmt.Errorf("FTS search messages: %w", err)
			}
		}
		if opts.Scope != "messages" {
			sums, err = store.SearchSummariesFTS(p)
			if err != nil {
				return "", fmt.Errorf("FTS search summaries: %w", err)
			}
		}
	}

	// Merge and sort
	combined := append(msgs, sums...)
	combined = db.MergeResults(combined, opts.Sort, opts.Limit)

	// Convert to output format with snippet truncation
	out := make([]SearchResult, len(combined))
	for i, r := range combined {
		snippet := r.Snippet
		if len(snippet) > 200 {
			snippet = snippet[:197] + "..."
		}
		out[i] = SearchResult{
			ID:        r.ID,
			Type:      r.Type,
			Snippet:   snippet,
			Rank:      r.Rank,
			CreatedAt: r.CreatedAt.Format("2006-01-02 15:04:05"),
			Role:      r.Role,
		}
	}

	b, _ := json.MarshalIndent(out, "", "  ")
	return string(b), nil
}
