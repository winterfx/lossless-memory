package db

import (
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

// ---------------------------------------------------------------------------
// Structs
// ---------------------------------------------------------------------------

// SearchParams defines parameters for FTS, CJK, regex, and LIKE searches.
type SearchParams struct {
	Query       string
	WorkspaceID int64  // 0 = all workspaces
	Sort        string // "relevance" | "recency" | "hybrid"
	Since       string // optional ISO datetime
	Before      string // optional ISO datetime
	Limit       int
}

// FTSSearchResult represents a single search match with snippet.
type FTSSearchResult struct {
	ID        string
	Type      string // "message" | "summary"
	Snippet   string // from FTS5 snippet() or fallback
	Rank      float64
	CreatedAt time.Time
	Role      string // message role (user/assistant), empty for summaries
}

// ---------------------------------------------------------------------------
// FTS5 query sanitization
// ---------------------------------------------------------------------------

var phraseRegex = regexp.MustCompile(`"([^"]+)"`)

// SanitizeFTS5Query escapes user input for safe use in an FTS5 MATCH expression.
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

// ---------------------------------------------------------------------------
// CJK detection
// ---------------------------------------------------------------------------

// ContainsCJK returns true if the string contains CJK, Hiragana, Katakana, or Hangul characters.
func ContainsCJK(s string) bool {
	for _, r := range s {
		if (r >= 0x2E80 && r <= 0x9FFF) || // CJK Unified Ideographs + Radicals
			(r >= 0x3400 && r <= 0x4DBF) || // CJK Extension A
			(r >= 0xF900 && r <= 0xFAFF) || // CJK Compatibility Ideographs
			(r >= 0x3040 && r <= 0x309F) || // Hiragana
			(r >= 0x30A0 && r <= 0x30FF) || // Katakana
			(r >= 0xAC00 && r <= 0xD7AF) { // Hangul
			return true
		}
	}
	return false
}

// cjkSegmentRegex matches contiguous CJK character runs.
var cjkSegmentRegex = regexp.MustCompile(`[\x{2E80}-\x{9FFF}\x{3400}-\x{4DBF}\x{F900}-\x{FAFF}\x{AC00}-\x{D7AF}\x{3040}-\x{309F}\x{30A0}-\x{30FF}]+`)

// latinTokenRegex matches Latin/ASCII word tokens.
var latinTokenRegex = regexp.MustCompile(`[a-zA-Z0-9][\w./-]*`)

// cjkRuneCount counts CJK runes in a string.
func cjkRuneCount(s string) int {
	n := 0
	for _, r := range s {
		if (r >= 0x2E80 && r <= 0x9FFF) || (r >= 0x3400 && r <= 0x4DBF) ||
			(r >= 0xF900 && r <= 0xFAFF) || (r >= 0x3040 && r <= 0x309F) ||
			(r >= 0x30A0 && r <= 0x30FF) || (r >= 0xAC00 && r <= 0xD7AF) {
			n++
		}
	}
	return n
}

// splitCJKChunks splits a CJK string into overlapping chunks of the given rune size.
func splitCJKChunks(s string, size int) []string {
	runes := []rune(s)
	if len(runes) <= size {
		return []string{string(runes)}
	}
	seen := map[string]bool{}
	var chunks []string
	for i := 0; i <= len(runes)-size; i++ {
		chunk := string(runes[i : i+size])
		if !seen[chunk] {
			seen[chunk] = true
			chunks = append(chunks, chunk)
		}
	}
	return chunks
}

// buildCJKMatchExpr builds an FTS5 MATCH expression for CJK queries using trigram chunking.
// Returns the MATCH expr, any extra LIKE clauses for Latin tokens, and the LIKE args.
func buildCJKMatchExpr(query string) (matchExpr string, likeClauses []string, likeArgs []interface{}) {
	cjkSegments := cjkSegmentRegex.FindAllString(query, -1)
	latinTokens := latinTokenRegex.FindAllString(query, -1)

	var cjkGroups []string
	for _, seg := range cjkSegments {
		runeLen := utf8.RuneCountInString(seg)
		var terms []string
		if runeLen <= 4 {
			terms = []string{seg}
		} else {
			terms = splitCJKChunks(seg, 4)
		}
		var quoted []string
		for _, t := range terms {
			quoted = append(quoted, `"`+strings.ReplaceAll(t, `"`, `""`)+`"`)
		}
		cjkGroups = append(cjkGroups, "("+strings.Join(quoted, " OR ")+")")
	}

	if len(cjkGroups) > 0 {
		matchExpr = strings.Join(cjkGroups, " AND ")
	}

	// Latin tokens need separate LIKE filtering
	for _, tok := range latinTokens {
		likeClauses = append(likeClauses, "LOWER(content) LIKE ? ESCAPE '\\'")
		likeArgs = append(likeArgs, "%"+escapeLikeTerm(strings.ToLower(tok))+"%")
	}

	return
}

// escapeLikeTerm escapes special LIKE characters.
func escapeLikeTerm(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `%`, `\%`)
	s = strings.ReplaceAll(s, `_`, `\_`)
	return s
}

// ---------------------------------------------------------------------------
// Sort helpers
// ---------------------------------------------------------------------------

// buildOrderBy returns the ORDER BY clause for the given sort mode.
func buildOrderBy(sort, alias string) string {
	switch sort {
	case "recency":
		return fmt.Sprintf("%s.created_at DESC", alias)
	case "hybrid":
		return fmt.Sprintf("(rank / (1.0 + ((julianday('now') - julianday(%s.created_at)) * 24 * 0.001))) ASC", alias)
	default: // "relevance"
		return fmt.Sprintf("rank ASC, %s.created_at DESC", alias)
	}
}

// buildOrderByNoRank returns ORDER BY for LIKE/regex queries (no rank column).
func buildOrderByNoRank(sort, alias string) string {
	// Without rank, all modes fall back to created_at DESC
	return fmt.Sprintf("%s.created_at DESC", alias)
}

// ---------------------------------------------------------------------------
// Time filter helpers
// ---------------------------------------------------------------------------

// appendTimeFilters appends WHERE clauses and args for since/before.
func appendTimeFilters(where *[]string, args *[]interface{}, alias string, since, before string) {
	if since != "" {
		*where = append(*where, fmt.Sprintf("%s.created_at >= ?", alias))
		*args = append(*args, since)
	}
	if before != "" {
		*where = append(*where, fmt.Sprintf("%s.created_at <= ?", alias))
		*args = append(*args, before)
	}
}

// ---------------------------------------------------------------------------
// Fallback snippet extraction
// ---------------------------------------------------------------------------

// CreateFallbackSnippet generates a snippet by centering on the earliest matching term.
func CreateFallbackSnippet(content string, terms []string) string {
	haystack := strings.ToLower(content)
	matchIndex := -1
	matchLength := 0

	for _, term := range terms {
		idx := strings.Index(haystack, strings.ToLower(term))
		if idx != -1 && (matchIndex == -1 || idx < matchIndex) {
			matchIndex = idx
			matchLength = len(term)
		}
	}

	if matchIndex == -1 {
		head := strings.TrimSpace(content)
		if len(head) <= 80 {
			return head
		}
		return strings.TrimRight(head[:77], " ") + "..."
	}

	start := matchIndex - 24
	if start < 0 {
		start = 0
	}
	end := matchIndex + matchLength + 40
	if end > len(content) {
		end = len(content)
	}

	prefix := ""
	if start > 0 {
		prefix = "..."
	}
	suffix := ""
	if end < len(content) {
		suffix = "..."
	}
	return prefix + strings.TrimSpace(content[start:end]) + suffix
}

// ---------------------------------------------------------------------------
// LIKE helpers
// ---------------------------------------------------------------------------

func isLikeMode(query string) bool {
	trimmed := strings.TrimSpace(query)
	return trimmed == "" || trimmed == "*" || strings.Contains(trimmed, "%")
}

func likePattern(query string) string {
	trimmed := strings.TrimSpace(query)
	if trimmed == "" || trimmed == "*" {
		return ""
	}
	if strings.Contains(trimmed, "%") {
		return trimmed
	}
	return "%" + trimmed + "%"
}

// ---------------------------------------------------------------------------
// SearchMessagesFTS — standard FTS5 search with snippet() and sort
// ---------------------------------------------------------------------------

func (s *Store) SearchMessagesFTS(p SearchParams) ([]FTSSearchResult, error) {
	if isLikeMode(p.Query) {
		return s.searchMessagesLike(p)
	}

	// CJK routing
	if ContainsCJK(p.Query) {
		segs := cjkSegmentRegex.FindAllString(p.Query, -1)
		hasShort := false
		for _, seg := range segs {
			if cjkRuneCount(seg) < 3 {
				hasShort = true
				break
			}
		}
		if !hasShort {
			results, err := s.searchMessagesCJKTrigram(p)
			if err == nil && len(results) > 0 {
				return results, nil
			}
			// Fall through to LIKE on error or no results
		}
		return s.searchMessagesCJKLike(p)
	}

	sanitized := SanitizeFTS5Query(p.Query)
	if sanitized == "" {
		return nil, nil
	}

	var where []string
	var args []interface{}
	where = append(where, "messages_fts MATCH ?")
	args = append(args, sanitized)
	if p.WorkspaceID > 0 {
		where = append(where, "m.workspace_id = ?")
		args = append(args, p.WorkspaceID)
	}
	appendTimeFilters(&where, &args, "m", p.Since, p.Before)
	args = append(args, p.Limit)

	query := fmt.Sprintf(
		`SELECT m.id, snippet(messages_fts, 0, '', '', '...', 32), m.created_at, m.role, rank
		 FROM messages_fts f
		 JOIN messages m ON m.id = f.rowid
		 WHERE %s
		 ORDER BY %s
		 LIMIT ?`,
		strings.Join(where, " AND "),
		buildOrderBy(p.Sort, "m"),
	)

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("searching messages FTS: %w", err)
	}
	defer rows.Close()

	return scanMessagesFTS(rows)
}

// ---------------------------------------------------------------------------
// SearchSummariesFTS — standard FTS5 search with snippet() and sort
// ---------------------------------------------------------------------------

func (s *Store) SearchSummariesFTS(p SearchParams) ([]FTSSearchResult, error) {
	if isLikeMode(p.Query) {
		return s.searchSummariesLike(p)
	}

	// CJK routing
	if ContainsCJK(p.Query) {
		segs := cjkSegmentRegex.FindAllString(p.Query, -1)
		hasShort := false
		for _, seg := range segs {
			if cjkRuneCount(seg) < 3 {
				hasShort = true
				break
			}
		}
		if !hasShort {
			results, err := s.searchSummariesCJKTrigram(p)
			if err == nil && len(results) > 0 {
				return results, nil
			}
		}
		return s.searchSummariesCJKLike(p)
	}

	sanitized := SanitizeFTS5Query(p.Query)
	if sanitized == "" {
		return nil, nil
	}

	var where []string
	var args []interface{}
	where = append(where, "summaries_fts MATCH ?")
	args = append(args, sanitized)
	if p.WorkspaceID > 0 {
		where = append(where, "s.workspace_id = ?")
		args = append(args, p.WorkspaceID)
	}
	appendTimeFilters(&where, &args, "s", p.Since, p.Before)
	args = append(args, p.Limit)

	query := fmt.Sprintf(
		`SELECT s.id, snippet(summaries_fts, 0, '', '', '...', 32), s.created_at, rank
		 FROM summaries_fts f
		 JOIN summaries s ON s.rowid = f.rowid
		 WHERE %s
		 ORDER BY %s
		 LIMIT ?`,
		strings.Join(where, " AND "),
		buildOrderBy(p.Sort, "s"),
	)

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("searching summaries FTS: %w", err)
	}
	defer rows.Close()

	return scanSummariesFTS(rows)
}

// ---------------------------------------------------------------------------
// CJK trigram FTS search
// ---------------------------------------------------------------------------

func (s *Store) searchMessagesCJKTrigram(p SearchParams) ([]FTSSearchResult, error) {
	matchExpr, likeClauses, likeArgs := buildCJKMatchExpr(p.Query)
	if matchExpr == "" {
		return nil, nil
	}

	var where []string
	var args []interface{}
	where = append(where, "messages_fts_cjk MATCH ?")
	args = append(args, matchExpr)
	if p.WorkspaceID > 0 {
		where = append(where, "m.workspace_id = ?")
		args = append(args, p.WorkspaceID)
	}
	for _, lc := range likeClauses {
		// Rewrite to reference m.content
		where = append(where, strings.Replace(lc, "content", "m.content", 1))
	}
	args = append(args, likeArgs...)
	appendTimeFilters(&where, &args, "m", p.Since, p.Before)
	args = append(args, p.Limit)

	query := fmt.Sprintf(
		`SELECT m.id, snippet(messages_fts_cjk, 0, '', '', '...', 32), m.created_at, m.role, rank
		 FROM messages_fts_cjk f
		 JOIN messages m ON m.id = f.rowid
		 WHERE %s
		 ORDER BY %s
		 LIMIT ?`,
		strings.Join(where, " AND "),
		buildOrderBy(p.Sort, "m"),
	)

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	return scanMessagesFTS(rows)
}

func (s *Store) searchSummariesCJKTrigram(p SearchParams) ([]FTSSearchResult, error) {
	matchExpr, likeClauses, likeArgs := buildCJKMatchExpr(p.Query)
	if matchExpr == "" {
		return nil, nil
	}

	var where []string
	var args []interface{}
	where = append(where, "summaries_fts_cjk MATCH ?")
	args = append(args, matchExpr)
	if p.WorkspaceID > 0 {
		where = append(where, "s.workspace_id = ?")
		args = append(args, p.WorkspaceID)
	}
	for _, lc := range likeClauses {
		where = append(where, strings.Replace(lc, "content", "s.content", 1))
	}
	args = append(args, likeArgs...)
	appendTimeFilters(&where, &args, "s", p.Since, p.Before)
	args = append(args, p.Limit)

	query := fmt.Sprintf(
		`SELECT s.id, snippet(summaries_fts_cjk, 0, '', '', '...', 32), s.created_at, rank
		 FROM summaries_fts_cjk f
		 JOIN summaries s ON s.rowid = f.rowid
		 WHERE %s
		 ORDER BY %s
		 LIMIT ?`,
		strings.Join(where, " AND "),
		buildOrderBy(p.Sort, "s"),
	)

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	return scanSummariesFTS(rows)
}

// ---------------------------------------------------------------------------
// CJK LIKE fallback (for < 3 CJK chars or trigram table errors)
// ---------------------------------------------------------------------------

func (s *Store) searchMessagesCJKLike(p SearchParams) ([]FTSSearchResult, error) {
	cjkSegs := cjkSegmentRegex.FindAllString(p.Query, -1)
	latinTokens := latinTokenRegex.FindAllString(p.Query, -1)

	var where []string
	var args []interface{}

	if p.WorkspaceID > 0 {
		where = append(where, "m.workspace_id = ?")
		args = append(args, p.WorkspaceID)
	}

	var snippetTerms []string
	for _, seg := range cjkSegs {
		runes := []rune(seg)
		var terms []string
		if len(runes) <= 2 {
			terms = []string{seg}
		} else {
			terms = splitCJKChunks(seg, 2)
		}
		snippetTerms = append(snippetTerms, terms...)
		var clauses []string
		for _, t := range terms {
			clauses = append(clauses, "m.content LIKE ? ESCAPE '\\'")
			args = append(args, "%"+escapeLikeTerm(t)+"%")
		}
		where = append(where, "("+strings.Join(clauses, " OR ")+")")
	}

	for _, tok := range latinTokens {
		snippetTerms = append(snippetTerms, tok)
		where = append(where, "LOWER(m.content) LIKE ? ESCAPE '\\'")
		args = append(args, "%"+escapeLikeTerm(strings.ToLower(tok))+"%")
	}

	appendTimeFilters(&where, &args, "m", p.Since, p.Before)

	if len(where) == 0 {
		return nil, nil
	}

	args = append(args, p.Limit)

	query := fmt.Sprintf(
		`SELECT m.id, m.content, m.created_at, m.role
		 FROM messages m
		 WHERE %s
		 ORDER BY %s
		 LIMIT ?`,
		strings.Join(where, " AND "),
		buildOrderByNoRank(p.Sort, "m"),
	)

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("searching messages CJK LIKE: %w", err)
	}
	defer rows.Close()

	var results []FTSSearchResult
	for rows.Next() {
		var r FTSSearchResult
		var id int64
		var content string
		if err := rows.Scan(&id, &content, &r.CreatedAt, &r.Role); err != nil {
			return nil, err
		}
		r.ID = fmt.Sprintf("%d", id)
		r.Type = "message"
		r.Snippet = CreateFallbackSnippet(content, snippetTerms)
		results = append(results, r)
	}
	return results, rows.Err()
}

func (s *Store) searchSummariesCJKLike(p SearchParams) ([]FTSSearchResult, error) {
	cjkSegs := cjkSegmentRegex.FindAllString(p.Query, -1)
	latinTokens := latinTokenRegex.FindAllString(p.Query, -1)

	var where []string
	var args []interface{}

	if p.WorkspaceID > 0 {
		where = append(where, "s.workspace_id = ?")
		args = append(args, p.WorkspaceID)
	}

	var snippetTerms []string
	for _, seg := range cjkSegs {
		runes := []rune(seg)
		var terms []string
		if len(runes) <= 2 {
			terms = []string{seg}
		} else {
			terms = splitCJKChunks(seg, 2)
		}
		snippetTerms = append(snippetTerms, terms...)
		var clauses []string
		for _, t := range terms {
			clauses = append(clauses, "s.content LIKE ? ESCAPE '\\'")
			args = append(args, "%"+escapeLikeTerm(t)+"%")
		}
		where = append(where, "("+strings.Join(clauses, " OR ")+")")
	}

	for _, tok := range latinTokens {
		snippetTerms = append(snippetTerms, tok)
		where = append(where, "LOWER(s.content) LIKE ? ESCAPE '\\'")
		args = append(args, "%"+escapeLikeTerm(strings.ToLower(tok))+"%")
	}

	appendTimeFilters(&where, &args, "s", p.Since, p.Before)

	if len(where) == 0 {
		return nil, nil
	}

	args = append(args, p.Limit)

	query := fmt.Sprintf(
		`SELECT s.id, s.content, s.created_at
		 FROM summaries s
		 WHERE %s
		 ORDER BY %s
		 LIMIT ?`,
		strings.Join(where, " AND "),
		buildOrderByNoRank(p.Sort, "s"),
	)

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("searching summaries CJK LIKE: %w", err)
	}
	defer rows.Close()

	var results []FTSSearchResult
	for rows.Next() {
		var r FTSSearchResult
		var content string
		if err := rows.Scan(&r.ID, &content, &r.CreatedAt); err != nil {
			return nil, err
		}
		r.Type = "summary"
		r.Snippet = CreateFallbackSnippet(content, snippetTerms)
		results = append(results, r)
	}
	return results, rows.Err()
}

// ---------------------------------------------------------------------------
// LIKE search (for wildcard queries like % or *)
// ---------------------------------------------------------------------------

func (s *Store) searchMessagesLike(p SearchParams) ([]FTSSearchResult, error) {
	pat := likePattern(p.Query)
	var where []string
	var args []interface{}

	if p.WorkspaceID > 0 {
		where = append(where, "m.workspace_id = ?")
		args = append(args, p.WorkspaceID)
	}
	if pat != "" {
		where = append(where, "m.content LIKE ?")
		args = append(args, pat)
	}
	appendTimeFilters(&where, &args, "m", p.Since, p.Before)

	whereClause := ""
	if len(where) > 0 {
		whereClause = "WHERE " + strings.Join(where, " AND ")
	}
	args = append(args, p.Limit)

	query := fmt.Sprintf(
		`SELECT m.id, m.content, m.created_at, m.role
		 FROM messages m %s
		 ORDER BY m.created_at DESC
		 LIMIT ?`, whereClause,
	)

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("searching messages LIKE: %w", err)
	}
	defer rows.Close()

	terms := strings.Fields(strings.TrimSpace(p.Query))
	var results []FTSSearchResult
	for rows.Next() {
		var r FTSSearchResult
		var id int64
		var content string
		if err := rows.Scan(&id, &content, &r.CreatedAt, &r.Role); err != nil {
			return nil, err
		}
		r.ID = fmt.Sprintf("%d", id)
		r.Type = "message"
		if pat != "" {
			r.Snippet = CreateFallbackSnippet(content, terms)
		} else {
			if len(content) > 80 {
				r.Snippet = content[:77] + "..."
			} else {
				r.Snippet = content
			}
		}
		results = append(results, r)
	}
	return results, rows.Err()
}

func (s *Store) searchSummariesLike(p SearchParams) ([]FTSSearchResult, error) {
	pat := likePattern(p.Query)
	var where []string
	var args []interface{}

	if p.WorkspaceID > 0 {
		where = append(where, "s.workspace_id = ?")
		args = append(args, p.WorkspaceID)
	}
	if pat != "" {
		where = append(where, "s.content LIKE ?")
		args = append(args, pat)
	}
	appendTimeFilters(&where, &args, "s", p.Since, p.Before)

	whereClause := ""
	if len(where) > 0 {
		whereClause = "WHERE " + strings.Join(where, " AND ")
	}
	args = append(args, p.Limit)

	query := fmt.Sprintf(
		`SELECT s.id, s.content, s.created_at
		 FROM summaries s %s
		 ORDER BY s.created_at DESC
		 LIMIT ?`, whereClause,
	)

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("searching summaries LIKE: %w", err)
	}
	defer rows.Close()

	terms := strings.Fields(strings.TrimSpace(p.Query))
	var results []FTSSearchResult
	for rows.Next() {
		var r FTSSearchResult
		var content string
		if err := rows.Scan(&r.ID, &content, &r.CreatedAt); err != nil {
			return nil, err
		}
		r.Type = "summary"
		if pat != "" {
			r.Snippet = CreateFallbackSnippet(content, terms)
		} else {
			if len(content) > 80 {
				r.Snippet = content[:77] + "..."
			} else {
				r.Snippet = content
			}
		}
		results = append(results, r)
	}
	return results, rows.Err()
}

// ---------------------------------------------------------------------------
// Regex search
// ---------------------------------------------------------------------------

const regexScanLimit = 10000

// SearchMessagesRegex performs regex matching in Go after fetching rows.
func (s *Store) SearchMessagesRegex(p SearchParams) ([]FTSSearchResult, error) {
	re, err := regexp.Compile(p.Query)
	if err != nil {
		return nil, fmt.Errorf("invalid regex: %w", err)
	}

	var where []string
	var args []interface{}
	if p.WorkspaceID > 0 {
		where = append(where, "m.workspace_id = ?")
		args = append(args, p.WorkspaceID)
	}
	appendTimeFilters(&where, &args, "m", p.Since, p.Before)
	args = append(args, regexScanLimit)

	whereClause := ""
	if len(where) > 0 {
		whereClause = "WHERE " + strings.Join(where, " AND ")
	}

	query := fmt.Sprintf(
		`SELECT m.id, m.content, m.created_at, m.role
		 FROM messages m %s
		 ORDER BY m.created_at DESC
		 LIMIT ?`, whereClause,
	)

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("regex scan messages: %w", err)
	}
	defer rows.Close()

	var results []FTSSearchResult
	for rows.Next() && len(results) < p.Limit {
		var id int64
		var content, role string
		var createdAt time.Time
		if err := rows.Scan(&id, &content, &createdAt, &role); err != nil {
			return nil, err
		}
		loc := re.FindStringIndex(content)
		if loc == nil {
			continue
		}
		// Build snippet around the match
		start := loc[0] - 24
		if start < 0 {
			start = 0
		}
		end := loc[1] + 40
		if end > len(content) {
			end = len(content)
		}
		prefix := ""
		if start > 0 {
			prefix = "..."
		}
		suffix := ""
		if end < len(content) {
			suffix = "..."
		}
		results = append(results, FTSSearchResult{
			ID:        fmt.Sprintf("%d", id),
			Type:      "message",
			Snippet:   prefix + strings.TrimSpace(content[start:end]) + suffix,
			CreatedAt: createdAt,
			Role:      role,
		})
	}
	return results, rows.Err()
}

// SearchSummariesRegex performs regex matching in Go after fetching rows.
func (s *Store) SearchSummariesRegex(p SearchParams) ([]FTSSearchResult, error) {
	re, err := regexp.Compile(p.Query)
	if err != nil {
		return nil, fmt.Errorf("invalid regex: %w", err)
	}

	var where []string
	var args []interface{}
	if p.WorkspaceID > 0 {
		where = append(where, "s.workspace_id = ?")
		args = append(args, p.WorkspaceID)
	}
	appendTimeFilters(&where, &args, "s", p.Since, p.Before)
	args = append(args, regexScanLimit)

	whereClause := ""
	if len(where) > 0 {
		whereClause = "WHERE " + strings.Join(where, " AND ")
	}

	query := fmt.Sprintf(
		`SELECT s.id, s.content, s.created_at
		 FROM summaries s %s
		 ORDER BY s.created_at DESC
		 LIMIT ?`, whereClause,
	)

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("regex scan summaries: %w", err)
	}
	defer rows.Close()

	var results []FTSSearchResult
	for rows.Next() && len(results) < p.Limit {
		var id, content string
		var createdAt time.Time
		if err := rows.Scan(&id, &content, &createdAt); err != nil {
			return nil, err
		}
		loc := re.FindStringIndex(content)
		if loc == nil {
			continue
		}
		start := loc[0] - 24
		if start < 0 {
			start = 0
		}
		end := loc[1] + 40
		if end > len(content) {
			end = len(content)
		}
		prefix := ""
		if start > 0 {
			prefix = "..."
		}
		suffix := ""
		if end < len(content) {
			suffix = "..."
		}
		results = append(results, FTSSearchResult{
			ID:        id,
			Type:      "summary",
			Snippet:   prefix + strings.TrimSpace(content[start:end]) + suffix,
			CreatedAt: createdAt,
		})
	}
	return results, rows.Err()
}

// ---------------------------------------------------------------------------
// Row scanners
// ---------------------------------------------------------------------------

func scanMessagesFTS(rows interface {
	Next() bool
	Scan(...interface{}) error
	Err() error
}) ([]FTSSearchResult, error) {
	var results []FTSSearchResult
	for rows.Next() {
		var r FTSSearchResult
		var id int64
		if err := rows.Scan(&id, &r.Snippet, &r.CreatedAt, &r.Role, &r.Rank); err != nil {
			return nil, err
		}
		r.ID = fmt.Sprintf("%d", id)
		r.Type = "message"
		results = append(results, r)
	}
	return results, rows.Err()
}

func scanSummariesFTS(rows interface {
	Next() bool
	Scan(...interface{}) error
	Err() error
}) ([]FTSSearchResult, error) {
	var results []FTSSearchResult
	for rows.Next() {
		var r FTSSearchResult
		if err := rows.Scan(&r.ID, &r.Snippet, &r.CreatedAt, &r.Rank); err != nil {
			return nil, err
		}
		r.Type = "summary"
		results = append(results, r)
	}
	return results, rows.Err()
}

// ---------------------------------------------------------------------------
// MergeResults sorts and truncates combined results.
// ---------------------------------------------------------------------------

// MergeResults merges message and summary results with sort-aware ordering.
func MergeResults(results []FTSSearchResult, sort string, limit int) []FTSSearchResult {
	// Sort based on mode
	for i := 0; i < len(results); i++ {
		for j := i + 1; j < len(results); j++ {
			swap := false
			switch sort {
			case "recency":
				swap = results[j].CreatedAt.After(results[i].CreatedAt)
			case "hybrid":
				// Approximate: use rank as primary, recency as tiebreaker
				if results[j].Rank != results[i].Rank {
					swap = results[j].Rank < results[i].Rank
				} else {
					swap = results[j].CreatedAt.After(results[i].CreatedAt)
				}
			default: // relevance
				if results[j].Rank != results[i].Rank {
					swap = results[j].Rank < results[i].Rank
				} else {
					swap = results[j].CreatedAt.After(results[i].CreatedAt)
				}
			}
			if swap {
				results[i], results[j] = results[j], results[i]
			}
		}
	}
	if len(results) > limit {
		results = results[:limit]
	}
	return results
}
