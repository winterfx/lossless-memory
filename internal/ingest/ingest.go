// Package ingest handles incremental ingestion of Claude Code transcript JSONL files.
package ingest

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/winter-wang/lossless-memory/internal/db"
)

// Run performs incremental ingest from a transcript JSONL file.
// It reads from the last processed offset and writes new messages to the store.
func Run(store *db.Store, sessionID, transcriptPath, cwd string) error {
	workspaceID, err := store.EnsureWorkspace(cwd)
	if err != nil {
		return fmt.Errorf("ensuring workspace: %w", err)
	}

	lastOffset, _, err := store.GetIngestState(sessionID)
	if err != nil {
		return fmt.Errorf("getting ingest state: %w", err)
	}

	f, err := os.Open(transcriptPath)
	if err != nil {
		return fmt.Errorf("opening transcript: %w", err)
	}
	defer f.Close()

	// Seek to last processed position
	if lastOffset > 0 {
		if _, err := f.Seek(lastOffset, io.SeekStart); err != nil {
			return fmt.Errorf("seeking to offset %d: %w", lastOffset, err)
		}
	}

	maxSeq, err := store.GetMaxSeq(sessionID)
	if err != nil {
		return fmt.Errorf("getting max seq: %w", err)
	}
	seq := maxSeq + 1

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 1024*1024), 10*1024*1024) // 10MB max line

	var bytesRead int64
	var ingested int

	for scanner.Scan() {
		line := scanner.Bytes()
		bytesRead += int64(len(line)) + 1 // +1 for newline

		entry, err := ParseEntry(line)
		if err != nil {
			continue // skip malformed lines
		}

		role := entry.ExtractRole()
		if role == "" {
			continue // skip non-message entries (file-history-snapshot, etc.)
		}

		content := entry.ExtractContent()
		if content == "" {
			continue
		}

		msg := &db.Message{
			WorkspaceID: workspaceID,
			SessionID:   sessionID,
			Seq:         seq,
			Role:        role,
			Content:     content,
			TokenCount:  db.EstimateTokens(content),
			CreatedAt:   parseTimestamp(entry.Timestamp),
		}

		if _, err := store.InsertMessage(msg); err != nil {
			fmt.Fprintf(os.Stderr, "[lcm] insert error seq=%d role=%s: %v\n", seq, role, err)
			// UNIQUE constraint violation means we already have this message
			continue
		}

		seq++
		ingested++
	}

	if err := scanner.Err(); err != nil {
		return fmt.Errorf("scanning transcript: %w", err)
	}

	// Update ingest state
	newOffset := lastOffset + bytesRead
	if err := store.SetIngestState(sessionID, transcriptPath, newOffset); err != nil {
		return fmt.Errorf("updating ingest state: %w", err)
	}

	if ingested > 0 {
		fmt.Fprintf(os.Stderr, "[lcm] ingested %d messages from session %s\n", ingested, sessionID)
	}

	return nil
}

func parseTimestamp(ts string) time.Time {
	t, err := time.Parse(time.RFC3339Nano, ts)
	if err != nil {
		return time.Now()
	}
	return t
}
