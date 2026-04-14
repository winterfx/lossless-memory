package db

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

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

// scanFunc is the signature of sql.Rows.Scan, used by searchTarget scan helpers.
type scanFunc = func(dest ...interface{}) error

// searchTarget captures the schema differences between the messages and summaries
// tables so that search methods can be written once and applied to both.
//
// hasRole distinguishes the two targets:
//   - messages (hasRole=true):  integer ID, has role column
//   - summaries (hasRole=false): text ID, no role column
type searchTarget struct {
	table      string // "messages" | "summaries"
	alias      string // "m" | "s"
	ftsTable   string // "messages_fts" | "summaries_fts"
	cjkTable   string // "messages_fts_cjk" | "summaries_fts_cjk"
	joinOn     string // FTS join condition
	resultType string // "message" | "summary"
	hasRole    bool
}

var msgTarget = searchTarget{
	table: "messages", alias: "m",
	ftsTable: "messages_fts", cjkTable: "messages_fts_cjk",
	joinOn: "m.id = f.rowid", resultType: "message", hasRole: true,
}

var sumTarget = searchTarget{
	table: "summaries", alias: "s",
	ftsTable: "summaries_fts", cjkTable: "summaries_fts_cjk",
	joinOn: "s.rowid = f.rowid", resultType: "summary", hasRole: false,
}

// selectFTS returns the SELECT column list for FTS queries (snippet + rank).
//   messages:  m.id, snippet(...), m.created_at, m.role, rank
//   summaries: s.id, snippet(...), s.created_at, rank
func (t searchTarget) selectFTS(ftsTable string) string {
	a := t.alias
	if t.hasRole {
		return fmt.Sprintf("%s.id, snippet(%s, 0, '', '', '...', 32), %s.created_at, %s.role, rank", a, ftsTable, a, a)
	}
	return fmt.Sprintf("%s.id, snippet(%s, 0, '', '', '...', 32), %s.created_at, rank", a, ftsTable, a)
}

// selectContent returns the SELECT column list for LIKE/regex queries (raw content).
//   messages:  m.id, m.content, m.created_at, m.role
//   summaries: s.id, s.content, s.created_at
func (t searchTarget) selectContent() string {
	a := t.alias
	if t.hasRole {
		return fmt.Sprintf("%s.id, %s.content, %s.created_at, %s.role", a, a, a, a)
	}
	return fmt.Sprintf("%s.id, %s.content, %s.created_at", a, a, a)
}

// scanFTSRow scans one FTS result row (snippet + rank) via the given rows.Scan function.
func (t searchTarget) scanFTSRow(scan scanFunc) (FTSSearchResult, error) {
	var r FTSSearchResult
	r.Type = t.resultType
	if t.hasRole {
		var id int64
		if err := scan(&id, &r.Snippet, &r.CreatedAt, &r.Role, &r.Rank); err != nil {
			return r, err
		}
		r.ID = fmt.Sprintf("%d", id)
	} else {
		if err := scan(&r.ID, &r.Snippet, &r.CreatedAt, &r.Rank); err != nil {
			return r, err
		}
	}
	return r, nil
}

// scanContentRow scans one content row (raw text) via the given rows.Scan function.
// Returns the result and the raw content (for snippet generation by the caller).
func (t searchTarget) scanContentRow(scan scanFunc) (FTSSearchResult, string, error) {
	var r FTSSearchResult
	var content string
	r.Type = t.resultType
	if t.hasRole {
		var id int64
		if err := scan(&id, &content, &r.CreatedAt, &r.Role); err != nil {
			return r, "", err
		}
		r.ID = fmt.Sprintf("%d", id)
	} else {
		if err := scan(&r.ID, &content, &r.CreatedAt); err != nil {
			return r, "", err
		}
	}
	return r, content, nil
}

// collectFTSRows scans all rows from an FTS query into a result slice.
func (t searchTarget) collectFTSRows(rows interface {
	Next() bool
	Scan(...interface{}) error
	Err() error
}) ([]FTSSearchResult, error) {
	var results []FTSSearchResult
	for rows.Next() {
		r, err := t.scanFTSRow(rows.Scan)
		if err != nil {
			return nil, err
		}
		results = append(results, r)
	}
	return results, rows.Err()
}

// --- FTS5 query sanitization ---

var phraseRegex = regexp.MustCompile(`"([^"]+)"`)

// SanitizeFTS5Query escapes user input for safe use in an FTS5 MATCH expression.
// Each unquoted token is wrapped in double-quotes; user-quoted phrases are preserved.
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

// --- CJK detection and helpers ---

func isCJKRune(r rune) bool {
	return (r >= 0x2E80 && r <= 0x9FFF) || // CJK Unified Ideographs + Radicals
		(r >= 0x3400 && r <= 0x4DBF) || // CJK Extension A
		(r >= 0xF900 && r <= 0xFAFF) || // CJK Compatibility Ideographs
		(r >= 0x3040 && r <= 0x309F) || // Hiragana
		(r >= 0x30A0 && r <= 0x30FF) || // Katakana
		(r >= 0xAC00 && r <= 0xD7AF) // Hangul
}

// ContainsCJK returns true if s contains any CJK/Hiragana/Katakana/Hangul character.
func ContainsCJK(s string) bool {
	for _, r := range s {
		if isCJKRune(r) {
			return true
		}
	}
	return false
}

var cjkSegmentRegex = regexp.MustCompile(`[\x{2E80}-\x{9FFF}\x{3400}-\x{4DBF}\x{F900}-\x{FAFF}\x{AC00}-\x{D7AF}\x{3040}-\x{309F}\x{30A0}-\x{30FF}]+`)
var latinTokenRegex = regexp.MustCompile(`[a-zA-Z0-9][\w./-]*`)

func cjkRuneCount(s string) int {
	n := 0
	for _, r := range s {
		if isCJKRune(r) {
			n++
		}
	}
	return n
}

// hasCJKShortSegment reports whether query has any CJK segment shorter than 3 runes
// (too short for trigram FTS, needs LIKE fallback).
func hasCJKShortSegment(query string) bool {
	for _, seg := range cjkSegmentRegex.FindAllString(query, -1) {
		if cjkRuneCount(seg) < 3 {
			return true
		}
	}
	return false
}

// splitCJKChunks splits a CJK string into de-duplicated overlapping chunks of the given rune size.
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

// buildCJKMatchExpr builds an FTS5 MATCH expression for CJK trigram queries.
// Latin tokens in the query are returned as separate LIKE clauses (already qualified
// with the given table alias, e.g. "m" or "s").
func buildCJKMatchExpr(query, alias string) (matchExpr string, likeClauses []string, likeArgs []interface{}) {
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

	for _, tok := range latinTokens {
		likeClauses = append(likeClauses, fmt.Sprintf("LOWER(%s.content) LIKE ? ESCAPE '\\'", alias))
		likeArgs = append(likeArgs, "%"+escapeLikeTerm(strings.ToLower(tok))+"%")
	}

	return
}

func escapeLikeTerm(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `%`, `\%`)
	s = strings.ReplaceAll(s, `_`, `\_`)
	return s
}

// --- SQL builder helpers ---

func buildOrderBy(sortMode, alias string) string {
	switch sortMode {
	case "recency":
		return fmt.Sprintf("%s.created_at DESC", alias)
	case "hybrid":
		return fmt.Sprintf("(rank / (1.0 + ((julianday('now') - julianday(%s.created_at)) * 24 * 0.001))) ASC", alias)
	default: // "relevance"
		return fmt.Sprintf("rank ASC, %s.created_at DESC", alias)
	}
}

func appendTimeFilters(where *[]string, args *[]interface{}, alias, since, before string) {
	if since != "" {
		*where = append(*where, fmt.Sprintf("%s.created_at >= ?", alias))
		*args = append(*args, since)
	}
	if before != "" {
		*where = append(*where, fmt.Sprintf("%s.created_at <= ?", alias))
		*args = append(*args, before)
	}
}

// --- Snippet helpers (rune-safe) ---

// safeSnippet extracts a snippet around byte offsets [matchStart, matchEnd),
// converting to rune indices first to avoid splitting multi-byte characters.
// ctxBefore/ctxAfter are the number of runes to include before/after the match.
func safeSnippet(content string, matchStart, matchEnd, ctxBefore, ctxAfter int) string {
	runes := []rune(content)
	runeStart := utf8.RuneCountInString(content[:matchStart])
	runeEnd := utf8.RuneCountInString(content[:matchEnd])

	start := max(runeStart-ctxBefore, 0)
	end := min(runeEnd+ctxAfter, len(runes))

	prefix, suffix := "", ""
	if start > 0 {
		prefix = "..."
	}
	if end < len(runes) {
		suffix = "..."
	}
	return prefix + strings.TrimSpace(string(runes[start:end])) + suffix
}

// truncateRunes returns s truncated to maxRunes runes with "..." appended if needed.
func truncateRunes(s string, maxRunes int) string {
	runes := []rune(s)
	if len(runes) <= maxRunes {
		return s
	}
	return strings.TrimRight(string(runes[:maxRunes-3]), " ") + "..."
}

// CreateFallbackSnippet generates a snippet centered on the first matching term.
// Used when FTS5 snippet() is unavailable (LIKE / regex paths).
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
		return truncateRunes(strings.TrimSpace(content), 80)
	}
	return safeSnippet(content, matchIndex, matchIndex+matchLength, 16, 30)
}

// --- LIKE helpers ---

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

// === Public API =============================================================
//
// Each public method delegates to a unified internal method with the
// appropriate searchTarget (msgTarget or sumTarget).

func (s *Store) SearchMessagesFTS(p SearchParams) ([]FTSSearchResult, error) {
	return s.searchFTS(msgTarget, p)
}

func (s *Store) SearchSummariesFTS(p SearchParams) ([]FTSSearchResult, error) {
	return s.searchFTS(sumTarget, p)
}

func (s *Store) SearchMessagesRegex(p SearchParams) ([]FTSSearchResult, error) {
	return s.searchRegex(msgTarget, p)
}

func (s *Store) SearchSummariesRegex(p SearchParams) ([]FTSSearchResult, error) {
	return s.searchRegex(sumTarget, p)
}

// === Internal search methods ================================================

// searchFTS routes the query to the appropriate search strategy:
//   - LIKE mode (wildcards)   → searchLike
//   - CJK text (>= 3 runes)  → searchCJKTrigram, fallback to searchCJKLike
//   - CJK text (< 3 runes)   → searchCJKLike
//   - Latin text              → standard FTS5 MATCH
func (s *Store) searchFTS(t searchTarget, p SearchParams) ([]FTSSearchResult, error) {
	if isLikeMode(p.Query) {
		return s.searchLike(t, p)
	}

	if ContainsCJK(p.Query) {
		if !hasCJKShortSegment(p.Query) {
			if results, err := s.searchCJKTrigram(t, p); err == nil && len(results) > 0 {
				return results, nil
			}
		}
		return s.searchCJKLike(t, p)
	}

	sanitized := SanitizeFTS5Query(p.Query)
	if sanitized == "" {
		return nil, nil
	}

	var where []string
	var args []interface{}
	where = append(where, fmt.Sprintf("%s MATCH ?", t.ftsTable))
	args = append(args, sanitized)
	if p.WorkspaceID > 0 {
		where = append(where, fmt.Sprintf("%s.workspace_id = ?", t.alias))
		args = append(args, p.WorkspaceID)
	}
	appendTimeFilters(&where, &args, t.alias, p.Since, p.Before)
	args = append(args, p.Limit)

	q := fmt.Sprintf(
		`SELECT %s FROM %s f JOIN %s %s ON %s WHERE %s ORDER BY %s LIMIT ?`,
		t.selectFTS(t.ftsTable), t.ftsTable, t.table, t.alias, t.joinOn,
		strings.Join(where, " AND "), buildOrderBy(p.Sort, t.alias),
	)

	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, fmt.Errorf("searching %s FTS: %w", t.table, err)
	}
	defer rows.Close()
	return t.collectFTSRows(rows)
}

// searchCJKTrigram searches using the CJK trigram FTS5 table.
func (s *Store) searchCJKTrigram(t searchTarget, p SearchParams) ([]FTSSearchResult, error) {
	matchExpr, likeClauses, likeArgs := buildCJKMatchExpr(p.Query, t.alias)
	if matchExpr == "" {
		return nil, nil
	}

	var where []string
	var args []interface{}
	where = append(where, fmt.Sprintf("%s MATCH ?", t.cjkTable))
	args = append(args, matchExpr)
	if p.WorkspaceID > 0 {
		where = append(where, fmt.Sprintf("%s.workspace_id = ?", t.alias))
		args = append(args, p.WorkspaceID)
	}
	where = append(where, likeClauses...)
	args = append(args, likeArgs...)
	appendTimeFilters(&where, &args, t.alias, p.Since, p.Before)
	args = append(args, p.Limit)

	q := fmt.Sprintf(
		`SELECT %s FROM %s f JOIN %s %s ON %s WHERE %s ORDER BY %s LIMIT ?`,
		t.selectFTS(t.cjkTable), t.cjkTable, t.table, t.alias, t.joinOn,
		strings.Join(where, " AND "), buildOrderBy(p.Sort, t.alias),
	)

	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return t.collectFTSRows(rows)
}

// searchCJKLike is the LIKE fallback for CJK queries with short segments (< 3 runes)
// or when the trigram table returns no results.
func (s *Store) searchCJKLike(t searchTarget, p SearchParams) ([]FTSSearchResult, error) {
	cjkSegs := cjkSegmentRegex.FindAllString(p.Query, -1)
	latinTokens := latinTokenRegex.FindAllString(p.Query, -1)

	var where []string
	var args []interface{}
	if p.WorkspaceID > 0 {
		where = append(where, fmt.Sprintf("%s.workspace_id = ?", t.alias))
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
		for _, term := range terms {
			clauses = append(clauses, fmt.Sprintf("%s.content LIKE ? ESCAPE '\\'", t.alias))
			args = append(args, "%"+escapeLikeTerm(term)+"%")
		}
		where = append(where, "("+strings.Join(clauses, " OR ")+")")
	}

	for _, tok := range latinTokens {
		snippetTerms = append(snippetTerms, tok)
		where = append(where, fmt.Sprintf("LOWER(%s.content) LIKE ? ESCAPE '\\'", t.alias))
		args = append(args, "%"+escapeLikeTerm(strings.ToLower(tok))+"%")
	}

	appendTimeFilters(&where, &args, t.alias, p.Since, p.Before)
	if len(where) == 0 {
		return nil, nil
	}
	args = append(args, p.Limit)

	q := fmt.Sprintf(
		`SELECT %s FROM %s %s WHERE %s ORDER BY %s.created_at DESC LIMIT ?`,
		t.selectContent(), t.table, t.alias,
		strings.Join(where, " AND "), t.alias,
	)

	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, fmt.Errorf("searching %s CJK LIKE: %w", t.table, err)
	}
	defer rows.Close()

	var results []FTSSearchResult
	for rows.Next() {
		r, content, err := t.scanContentRow(rows.Scan)
		if err != nil {
			return nil, err
		}
		r.Snippet = CreateFallbackSnippet(content, snippetTerms)
		results = append(results, r)
	}
	return results, rows.Err()
}

// searchLike handles wildcard queries (containing % or *).
func (s *Store) searchLike(t searchTarget, p SearchParams) ([]FTSSearchResult, error) {
	pat := likePattern(p.Query)

	var where []string
	var args []interface{}
	if p.WorkspaceID > 0 {
		where = append(where, fmt.Sprintf("%s.workspace_id = ?", t.alias))
		args = append(args, p.WorkspaceID)
	}
	if pat != "" {
		where = append(where, fmt.Sprintf("%s.content LIKE ?", t.alias))
		args = append(args, pat)
	}
	appendTimeFilters(&where, &args, t.alias, p.Since, p.Before)

	whereClause := ""
	if len(where) > 0 {
		whereClause = "WHERE " + strings.Join(where, " AND ")
	}
	args = append(args, p.Limit)

	q := fmt.Sprintf(
		`SELECT %s FROM %s %s %s ORDER BY %s.created_at DESC LIMIT ?`,
		t.selectContent(), t.table, t.alias, whereClause, t.alias,
	)

	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, fmt.Errorf("searching %s LIKE: %w", t.table, err)
	}
	defer rows.Close()

	terms := strings.Fields(strings.TrimSpace(p.Query))
	var results []FTSSearchResult
	for rows.Next() {
		r, content, err := t.scanContentRow(rows.Scan)
		if err != nil {
			return nil, err
		}
		if pat != "" {
			r.Snippet = CreateFallbackSnippet(content, terms)
		} else {
			r.Snippet = truncateRunes(content, 80)
		}
		results = append(results, r)
	}
	return results, rows.Err()
}

// searchRegex fetches up to regexScanLimit rows and filters with Go regexp.
const regexScanLimit = 10000

func (s *Store) searchRegex(t searchTarget, p SearchParams) ([]FTSSearchResult, error) {
	re, err := regexp.Compile(p.Query)
	if err != nil {
		return nil, fmt.Errorf("invalid regex: %w", err)
	}

	var where []string
	var args []interface{}
	if p.WorkspaceID > 0 {
		where = append(where, fmt.Sprintf("%s.workspace_id = ?", t.alias))
		args = append(args, p.WorkspaceID)
	}
	appendTimeFilters(&where, &args, t.alias, p.Since, p.Before)
	args = append(args, regexScanLimit)

	whereClause := ""
	if len(where) > 0 {
		whereClause = "WHERE " + strings.Join(where, " AND ")
	}

	q := fmt.Sprintf(
		`SELECT %s FROM %s %s %s ORDER BY %s.created_at DESC LIMIT ?`,
		t.selectContent(), t.table, t.alias, whereClause, t.alias,
	)

	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, fmt.Errorf("regex scan %s: %w", t.table, err)
	}
	defer rows.Close()

	var results []FTSSearchResult
	for rows.Next() {
		if len(results) >= p.Limit {
			break
		}
		r, content, err := t.scanContentRow(rows.Scan)
		if err != nil {
			return nil, err
		}
		loc := re.FindStringIndex(content)
		if loc == nil {
			continue
		}
		r.Snippet = safeSnippet(content, loc[0], loc[1], 16, 30)
		results = append(results, r)
	}
	return results, rows.Err()
}

// === Result merging =========================================================

// MergeResults sorts and truncates combined message+summary results.
func MergeResults(results []FTSSearchResult, sortMode string, limit int) []FTSSearchResult {
	sort.Slice(results, func(i, j int) bool {
		if sortMode == "recency" {
			return results[i].CreatedAt.After(results[j].CreatedAt)
		}
		// relevance / hybrid: rank primary, recency tiebreaker
		if results[i].Rank != results[j].Rank {
			return results[i].Rank < results[j].Rank
		}
		return results[i].CreatedAt.After(results[j].CreatedAt)
	})
	if len(results) > limit {
		results = results[:limit]
	}
	return results
}
