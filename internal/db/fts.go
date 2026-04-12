package db

import (
	"fmt"
	"regexp"
	"strings"
)

// SearchResult represents a single search match.
type SearchResult struct {
	ID      string  // message ID (as string) or summary ID
	Type    string  // "message" or "summary"
	Content string
	Rank    float64
}

// ---------------------------------------------------------------------------
// FTS5 query sanitization (ported from picoclaw/seahorse)
// ---------------------------------------------------------------------------

var phraseRegex = regexp.MustCompile(`"([^"]+)"`)

// SanitizeFTS5Query escapes user input for safe use in an FTS5 MATCH expression.
// Each token is wrapped in double quotes so FTS5 treats it as a literal phrase.
// User-quoted phrases ("...") are preserved. Returns "" for blank input.
func SanitizeFTS5Query(raw string) string {
	if strings.TrimSpace(raw) == "" {
		return ""
	}

	var parts []string
	lastIndex := 0

	for _, loc := range phraseRegex.FindAllStringIndex(raw, -1) {
		before := raw[lastIndex:loc[0]]
		for _, t := range strings.Fields(before) {
			t = strings.ReplaceAll(t, `"`, "")
			if t != "" {
				parts = append(parts, `"`+t+`"`)
			}
		}
		phrase := strings.TrimSpace(strings.ReplaceAll(raw[loc[0]+1:loc[1]-1], `"`, ""))
		if phrase != "" {
			parts = append(parts, `"`+phrase+`"`)
		}
		lastIndex = loc[1]
	}

	for _, t := range strings.Fields(raw[lastIndex:]) {
		t = strings.ReplaceAll(t, `"`, "")
		if t != "" {
			parts = append(parts, `"`+t+`"`)
		}
	}

	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, " ")
}

// isLikeMode returns true when the query should use LIKE instead of FTS5.
// A query uses LIKE mode when it contains '%' (explicit wildcard), or when
// it is empty / a bare '*' (list recent records).
func isLikeMode(query string) bool {
	trimmed := strings.TrimSpace(query)
	return trimmed == "" || trimmed == "*" || strings.Contains(trimmed, "%")
}

// ---------------------------------------------------------------------------
// LIKE helpers
// ---------------------------------------------------------------------------

// likePattern converts a user query to a LIKE pattern.
// Empty / "*" returns "" (meaning: no filter, list all).
func likePattern(query string) string {
	trimmed := strings.TrimSpace(query)
	if trimmed == "" || trimmed == "*" {
		return ""
	}
	// If user already supplied %, use as-is; otherwise wrap.
	if strings.Contains(trimmed, "%") {
		return trimmed
	}
	return "%" + trimmed + "%"
}

// ---------------------------------------------------------------------------
// SearchMessages
// ---------------------------------------------------------------------------

func (s *Store) SearchMessages(query string, workspaceID int64, limit int) ([]SearchResult, error) {
	if isLikeMode(query) {
		return s.searchMessagesLike(query, workspaceID, limit)
	}
	sanitized := SanitizeFTS5Query(query)
	if sanitized == "" {
		return nil, nil
	}
	rows, err := s.db.Query(
		`SELECT m.id, m.content, rank
		 FROM messages_fts f
		 JOIN messages m ON m.id = f.rowid
		 WHERE messages_fts MATCH ? AND m.workspace_id = ?
		 ORDER BY rank
		 LIMIT ?`, sanitized, workspaceID, limit,
	)
	if err != nil {
		return nil, fmt.Errorf("searching messages: %w", err)
	}
	defer rows.Close()
	return scanMessages(rows)
}

func (s *Store) searchMessagesLike(query string, workspaceID int64, limit int) ([]SearchResult, error) {
	pat := likePattern(query)
	var q string
	var args []interface{}
	if pat == "" {
		q = `SELECT id, content FROM messages WHERE workspace_id = ? ORDER BY id DESC LIMIT ?`
		args = []interface{}{workspaceID, limit}
	} else {
		q = `SELECT id, content FROM messages WHERE workspace_id = ? AND content LIKE ? ORDER BY id DESC LIMIT ?`
		args = []interface{}{workspaceID, pat, limit}
	}
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, fmt.Errorf("searching messages (like): %w", err)
	}
	defer rows.Close()
	return scanMessagesNoRank(rows)
}

// ---------------------------------------------------------------------------
// SearchSummaries
// ---------------------------------------------------------------------------

func (s *Store) SearchSummaries(query string, workspaceID int64, limit int) ([]SearchResult, error) {
	if isLikeMode(query) {
		return s.searchSummariesLike(query, workspaceID, limit)
	}
	sanitized := SanitizeFTS5Query(query)
	if sanitized == "" {
		return nil, nil
	}
	rows, err := s.db.Query(
		`SELECT s.id, s.content, rank
		 FROM summaries_fts f
		 JOIN summaries s ON s.rowid = f.rowid
		 WHERE summaries_fts MATCH ? AND s.workspace_id = ?
		 ORDER BY rank
		 LIMIT ?`, sanitized, workspaceID, limit,
	)
	if err != nil {
		return nil, fmt.Errorf("searching summaries: %w", err)
	}
	defer rows.Close()
	return scanSummaries(rows)
}

func (s *Store) searchSummariesLike(query string, workspaceID int64, limit int) ([]SearchResult, error) {
	pat := likePattern(query)
	var q string
	var args []interface{}
	if pat == "" {
		q = `SELECT id, content FROM summaries WHERE workspace_id = ? ORDER BY rowid DESC LIMIT ?`
		args = []interface{}{workspaceID, limit}
	} else {
		q = `SELECT id, content FROM summaries WHERE workspace_id = ? AND content LIKE ? ORDER BY rowid DESC LIMIT ?`
		args = []interface{}{workspaceID, pat, limit}
	}
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, fmt.Errorf("searching summaries (like): %w", err)
	}
	defer rows.Close()
	return scanSummariesNoRank(rows)
}

// ---------------------------------------------------------------------------
// SearchAll (single workspace)
// ---------------------------------------------------------------------------

func (s *Store) SearchAll(query string, workspaceID int64, limit int) ([]SearchResult, error) {
	msgs, err := s.SearchMessages(query, workspaceID, limit)
	if err != nil {
		return nil, err
	}
	sums, err := s.SearchSummaries(query, workspaceID, limit)
	if err != nil {
		return nil, err
	}
	return mergeAndSort(append(msgs, sums...), limit), nil
}

// ---------------------------------------------------------------------------
// SearchAllWorkspaces
// ---------------------------------------------------------------------------

func (s *Store) SearchAllWorkspaces(query string, limit int) ([]SearchResult, error) {
	if isLikeMode(query) {
		return s.searchAllWorkspacesLike(query, limit)
	}
	sanitized := SanitizeFTS5Query(query)
	if sanitized == "" {
		return nil, nil
	}

	msgs, err := s.db.Query(
		`SELECT m.id, m.content, rank
		 FROM messages_fts f
		 JOIN messages m ON m.id = f.rowid
		 WHERE messages_fts MATCH ?
		 ORDER BY rank LIMIT ?`, sanitized, limit,
	)
	if err != nil {
		return nil, err
	}
	defer msgs.Close()
	msgResults, err := scanMessages(msgs)
	if err != nil {
		return nil, err
	}

	sums, err := s.db.Query(
		`SELECT s.id, s.content, rank
		 FROM summaries_fts f
		 JOIN summaries s ON s.rowid = f.rowid
		 WHERE summaries_fts MATCH ?
		 ORDER BY rank LIMIT ?`, sanitized, limit,
	)
	if err != nil {
		return nil, err
	}
	defer sums.Close()
	sumResults, err := scanSummaries(sums)
	if err != nil {
		return nil, err
	}

	return mergeAndSort(append(msgResults, sumResults...), limit), nil
}

func (s *Store) searchAllWorkspacesLike(query string, limit int) ([]SearchResult, error) {
	pat := likePattern(query)
	var results []SearchResult

	// Messages
	var msgQ string
	var msgArgs []interface{}
	if pat == "" {
		msgQ = `SELECT id, content FROM messages ORDER BY id DESC LIMIT ?`
		msgArgs = []interface{}{limit}
	} else {
		msgQ = `SELECT id, content FROM messages WHERE content LIKE ? ORDER BY id DESC LIMIT ?`
		msgArgs = []interface{}{pat, limit}
	}
	rows, err := s.db.Query(msgQ, msgArgs...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	mr, err := scanMessagesNoRank(rows)
	if err != nil {
		return nil, err
	}
	results = append(results, mr...)

	// Summaries
	var sumQ string
	var sumArgs []interface{}
	if pat == "" {
		sumQ = `SELECT id, content FROM summaries ORDER BY rowid DESC LIMIT ?`
		sumArgs = []interface{}{limit}
	} else {
		sumQ = `SELECT id, content FROM summaries WHERE content LIKE ? ORDER BY rowid DESC LIMIT ?`
		sumArgs = []interface{}{pat, limit}
	}
	srows, err := s.db.Query(sumQ, sumArgs...)
	if err != nil {
		return nil, err
	}
	defer srows.Close()
	sr, err := scanSummariesNoRank(srows)
	if err != nil {
		return nil, err
	}
	results = append(results, sr...)

	if len(results) > limit {
		results = results[:limit]
	}
	return results, nil
}

// ---------------------------------------------------------------------------
// Row scanners (DRY helpers)
// ---------------------------------------------------------------------------

func scanMessages(rows interface{ Next() bool; Scan(...interface{}) error; Err() error }) ([]SearchResult, error) {
	var results []SearchResult
	for rows.Next() {
		var r SearchResult
		var id int64
		if err := rows.Scan(&id, &r.Content, &r.Rank); err != nil {
			return nil, err
		}
		r.ID = fmt.Sprintf("%d", id)
		r.Type = "message"
		results = append(results, r)
	}
	return results, rows.Err()
}

func scanMessagesNoRank(rows interface{ Next() bool; Scan(...interface{}) error; Err() error }) ([]SearchResult, error) {
	var results []SearchResult
	for rows.Next() {
		var r SearchResult
		var id int64
		if err := rows.Scan(&id, &r.Content); err != nil {
			return nil, err
		}
		r.ID = fmt.Sprintf("%d", id)
		r.Type = "message"
		results = append(results, r)
	}
	return results, rows.Err()
}

func scanSummaries(rows interface{ Next() bool; Scan(...interface{}) error; Err() error }) ([]SearchResult, error) {
	var results []SearchResult
	for rows.Next() {
		var r SearchResult
		if err := rows.Scan(&r.ID, &r.Content, &r.Rank); err != nil {
			return nil, err
		}
		r.Type = "summary"
		results = append(results, r)
	}
	return results, rows.Err()
}

func scanSummariesNoRank(rows interface{ Next() bool; Scan(...interface{}) error; Err() error }) ([]SearchResult, error) {
	var results []SearchResult
	for rows.Next() {
		var r SearchResult
		if err := rows.Scan(&r.ID, &r.Content); err != nil {
			return nil, err
		}
		r.Type = "summary"
		results = append(results, r)
	}
	return results, rows.Err()
}

// mergeAndSort sorts by rank (lower = better) and truncates to limit.
func mergeAndSort(results []SearchResult, limit int) []SearchResult {
	for i := 0; i < len(results); i++ {
		for j := i + 1; j < len(results); j++ {
			if results[j].Rank < results[i].Rank {
				results[i], results[j] = results[j], results[i]
			}
		}
	}
	if len(results) > limit {
		results = results[:limit]
	}
	return results
}
