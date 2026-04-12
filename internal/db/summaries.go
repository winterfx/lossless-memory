package db

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"time"
)

// Summary represents a summary node in the DAG.
type Summary struct {
	ID          string
	WorkspaceID int64
	Kind        string // "leaf" or "condensed"
	Depth       int
	Content     string
	TokenCount  int
	EarliestAt  time.Time
	LatestAt    time.Time
	Model       string
	CreatedAt   time.Time
}

// GenerateSummaryID creates a new sum_ prefixed ID.
func GenerateSummaryID() string {
	b := make([]byte, 8)
	rand.Read(b)
	return "sum_" + hex.EncodeToString(b)
}

// InsertSummary inserts a summary and updates the FTS index.
func (s *Store) InsertSummary(sum *Summary) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	_, err = tx.Exec(
		`INSERT INTO summaries (id, workspace_id, kind, depth, content, token_count,
		 earliest_at, latest_at, model, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		sum.ID, sum.WorkspaceID, sum.Kind, sum.Depth, sum.Content, sum.TokenCount,
		sum.EarliestAt, sum.LatestAt, sum.Model, sum.CreatedAt,
	)
	if err != nil {
		return fmt.Errorf("inserting summary: %w", err)
	}

	// Update FTS index - use the rowid assigned by sqlite
	var rowid int64
	if err := tx.QueryRow("SELECT rowid FROM summaries WHERE id = ?", sum.ID).Scan(&rowid); err != nil {
		return fmt.Errorf("getting summary rowid: %w", err)
	}
	if _, err := tx.Exec(
		"INSERT INTO summaries_fts(rowid, content) VALUES (?, ?)", rowid, sum.Content,
	); err != nil {
		return fmt.Errorf("updating summaries_fts: %w", err)
	}

	return tx.Commit()
}

// LinkSummaryMessages creates edges from a leaf summary to its source messages.
func (s *Store) LinkSummaryMessages(summaryID string, messageIDs []int64) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	stmt, err := tx.Prepare("INSERT INTO summary_messages (summary_id, message_id, ordinal) VALUES (?, ?, ?)")
	if err != nil {
		return err
	}
	defer stmt.Close()

	for i, msgID := range messageIDs {
		if _, err := stmt.Exec(summaryID, msgID, i); err != nil {
			return fmt.Errorf("linking summary %s to message %d: %w", summaryID, msgID, err)
		}
	}
	return tx.Commit()
}

// LinkSummaryParents creates edges from a condensed summary to its parent summaries.
func (s *Store) LinkSummaryParents(summaryID string, parentIDs []string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	stmt, err := tx.Prepare("INSERT INTO summary_parents (summary_id, parent_summary_id, ordinal) VALUES (?, ?, ?)")
	if err != nil {
		return err
	}
	defer stmt.Close()

	for i, pid := range parentIDs {
		if _, err := stmt.Exec(summaryID, pid, i); err != nil {
			return fmt.Errorf("linking summary %s to parent %s: %w", summaryID, pid, err)
		}
	}
	return tx.Commit()
}

// GetUnconsumedSummaries returns summaries at a given depth that have NOT been
// consumed as parents by any higher-depth summary.
func (s *Store) GetUnconsumedSummaries(workspaceID int64, depth int) ([]Summary, error) {
	rows, err := s.db.Query(
		`SELECT id, workspace_id, kind, depth, content, token_count,
		 earliest_at, latest_at, model, created_at
		 FROM summaries
		 WHERE workspace_id = ? AND depth = ?
		   AND id NOT IN (SELECT parent_summary_id FROM summary_parents)
		 ORDER BY earliest_at`, workspaceID, depth,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var sums []Summary
	for rows.Next() {
		var sum Summary
		if err := rows.Scan(&sum.ID, &sum.WorkspaceID, &sum.Kind, &sum.Depth, &sum.Content,
			&sum.TokenCount, &sum.EarliestAt, &sum.LatestAt, &sum.Model, &sum.CreatedAt); err != nil {
			return nil, err
		}
		sums = append(sums, sum)
	}
	return sums, rows.Err()
}

// GetSummary returns a summary by ID.
func (s *Store) GetSummary(id string) (*Summary, error) {
	var sum Summary
	err := s.db.QueryRow(
		`SELECT id, workspace_id, kind, depth, content, token_count,
		 earliest_at, latest_at, model, created_at
		 FROM summaries WHERE id = ?`, id,
	).Scan(&sum.ID, &sum.WorkspaceID, &sum.Kind, &sum.Depth, &sum.Content,
		&sum.TokenCount, &sum.EarliestAt, &sum.LatestAt, &sum.Model, &sum.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &sum, nil
}

// GetSummariesByWorkspaceAndDepth returns summaries at a given depth for a workspace,
// ordered by created_at.
func (s *Store) GetSummariesByWorkspaceAndDepth(workspaceID int64, depth int) ([]Summary, error) {
	rows, err := s.db.Query(
		`SELECT id, workspace_id, kind, depth, content, token_count,
		 earliest_at, latest_at, model, created_at
		 FROM summaries WHERE workspace_id = ? AND depth = ?
		 ORDER BY created_at`, workspaceID, depth,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var sums []Summary
	for rows.Next() {
		var sum Summary
		if err := rows.Scan(&sum.ID, &sum.WorkspaceID, &sum.Kind, &sum.Depth, &sum.Content,
			&sum.TokenCount, &sum.EarliestAt, &sum.LatestAt, &sum.Model, &sum.CreatedAt); err != nil {
			return nil, err
		}
		sums = append(sums, sum)
	}
	return sums, rows.Err()
}

// GetHighestDepthSummaries returns summaries at or above the given minimum depth.
func (s *Store) GetHighestDepthSummaries(workspaceID int64, minDepth int, limit int) ([]Summary, error) {
	rows, err := s.db.Query(
		`SELECT id, workspace_id, kind, depth, content, token_count,
		 earliest_at, latest_at, model, created_at
		 FROM summaries WHERE workspace_id = ? AND depth >= ?
		 ORDER BY depth DESC, created_at DESC LIMIT ?`,
		workspaceID, minDepth, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var sums []Summary
	for rows.Next() {
		var sum Summary
		if err := rows.Scan(&sum.ID, &sum.WorkspaceID, &sum.Kind, &sum.Depth, &sum.Content,
			&sum.TokenCount, &sum.EarliestAt, &sum.LatestAt, &sum.Model, &sum.CreatedAt); err != nil {
			return nil, err
		}
		sums = append(sums, sum)
	}
	return sums, rows.Err()
}

// GetSummaryMessageIDs returns the source message IDs for a leaf summary.
func (s *Store) GetSummaryMessageIDs(summaryID string) ([]int64, error) {
	rows, err := s.db.Query(
		"SELECT message_id FROM summary_messages WHERE summary_id = ? ORDER BY ordinal", summaryID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// GetSummaryParentIDs returns the parent summary IDs for a condensed summary.
func (s *Store) GetSummaryParentIDs(summaryID string) ([]string, error) {
	rows, err := s.db.Query(
		"SELECT parent_summary_id FROM summary_parents WHERE summary_id = ? ORDER BY ordinal", summaryID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// GetChildSummaryIDs returns summaries that have this summary as a parent.
func (s *Store) GetChildSummaryIDs(parentSummaryID string) ([]string, error) {
	rows, err := s.db.Query(
		"SELECT summary_id FROM summary_parents WHERE parent_summary_id = ?", parentSummaryID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// GetUncoveredMessages returns messages in a session that are not covered by any leaf summary.
func (s *Store) GetUncoveredMessages(sessionID string) ([]Message, error) {
	rows, err := s.db.Query(
		`SELECT m.id, m.workspace_id, m.session_id, m.seq, m.role, m.content, m.token_count, m.created_at
		 FROM messages m
		 WHERE m.session_id = ?
		   AND m.id NOT IN (SELECT message_id FROM summary_messages)
		 ORDER BY m.seq`, sessionID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var msgs []Message
	for rows.Next() {
		var m Message
		if err := rows.Scan(&m.ID, &m.WorkspaceID, &m.SessionID, &m.Seq,
			&m.Role, &m.Content, &m.TokenCount, &m.CreatedAt); err != nil {
			return nil, err
		}
		msgs = append(msgs, m)
	}
	return msgs, rows.Err()
}

// CountSummariesByDepth returns the count of summaries at each depth for a workspace.
func (s *Store) CountSummariesByDepth(workspaceID int64) (map[int]int, error) {
	rows, err := s.db.Query(
		"SELECT depth, COUNT(*) FROM summaries WHERE workspace_id = ? GROUP BY depth",
		workspaceID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	counts := make(map[int]int)
	for rows.Next() {
		var depth, count int
		if err := rows.Scan(&depth, &count); err != nil {
			return nil, err
		}
		counts[depth] = count
	}
	return counts, rows.Err()
}
