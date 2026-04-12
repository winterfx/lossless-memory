package db

import (
	"database/sql"
	"fmt"
	"time"
)

// EstimateTokens returns a rough token count for mixed CJK/English text.
// ASCII characters are counted at ~4 chars per token; non-ASCII (CJK etc.) at ~1 rune per token.
func EstimateTokens(s string) int {
	ascii := 0
	nonASCII := 0
	for _, r := range s {
		if r < 128 {
			ascii++
		} else {
			nonASCII++
		}
	}
	tokens := ascii/4 + nonASCII
	if tokens == 0 && len(s) > 0 {
		tokens = 1
	}
	return tokens
}

// Message represents a stored conversation message.
type Message struct {
	ID          int64
	WorkspaceID int64
	SessionID   string
	Seq         int
	Role        string
	Content     string
	TokenCount  int
	CreatedAt   time.Time
}

// InsertMessage inserts a message and updates the FTS index.
func (s *Store) InsertMessage(m *Message) (int64, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	res, err := tx.Exec(
		`INSERT INTO messages (workspace_id, session_id, seq, role, content, token_count, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		m.WorkspaceID, m.SessionID, m.Seq, m.Role, m.Content, m.TokenCount, m.CreatedAt,
	)
	if err != nil {
		return 0, fmt.Errorf("inserting message: %w", err)
	}
	id, _ := res.LastInsertId()

	// Update FTS index
	if _, err := tx.Exec(
		"INSERT INTO messages_fts(rowid, content) VALUES (?, ?)", id, m.Content,
	); err != nil {
		return 0, fmt.Errorf("updating messages_fts: %w", err)
	}

	return id, tx.Commit()
}

// GetIngestState returns the last processed offset for a session.
func (s *Store) GetIngestState(sessionID string) (offset int64, transcriptPath string, err error) {
	err = s.db.QueryRow(
		"SELECT last_processed_offset, transcript_path FROM ingest_state WHERE session_id = ?",
		sessionID,
	).Scan(&offset, &transcriptPath)
	if err == sql.ErrNoRows {
		return 0, "", nil
	}
	return
}

// SetIngestState updates the ingest progress for a session.
func (s *Store) SetIngestState(sessionID, transcriptPath string, offset int64) error {
	_, err := s.db.Exec(
		`INSERT INTO ingest_state (session_id, transcript_path, last_processed_offset, updated_at)
		 VALUES (?, ?, ?, CURRENT_TIMESTAMP)
		 ON CONFLICT(session_id) DO UPDATE SET
		   last_processed_offset = excluded.last_processed_offset,
		   transcript_path = excluded.transcript_path,
		   updated_at = CURRENT_TIMESTAMP`,
		sessionID, transcriptPath, offset,
	)
	return err
}

// GetMaxSeq returns the highest seq number for a session, or -1 if none.
func (s *Store) GetMaxSeq(sessionID string) (int, error) {
	var seq sql.NullInt64
	err := s.db.QueryRow(
		"SELECT MAX(seq) FROM messages WHERE session_id = ?", sessionID,
	).Scan(&seq)
	if err != nil {
		return -1, err
	}
	if !seq.Valid {
		return -1, nil
	}
	return int(seq.Int64), nil
}

// GetMessagesBySession returns all messages for a session ordered by seq.
func (s *Store) GetMessagesBySession(sessionID string) ([]Message, error) {
	rows, err := s.db.Query(
		`SELECT id, workspace_id, session_id, seq, role, content, token_count, created_at
		 FROM messages WHERE session_id = ? ORDER BY seq`, sessionID,
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

// GetMessagesByIDs returns messages for the given IDs.
func (s *Store) GetMessagesByIDs(ids []int64) ([]Message, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	query := "SELECT id, workspace_id, session_id, seq, role, content, token_count, created_at FROM messages WHERE id IN ("
	args := make([]interface{}, len(ids))
	for i, id := range ids {
		if i > 0 {
			query += ","
		}
		query += "?"
		args[i] = id
	}
	query += ") ORDER BY id"

	rows, err := s.db.Query(query, args...)
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
