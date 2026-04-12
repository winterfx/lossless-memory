package ingest

import (
	"encoding/json"
	"fmt"
	"strings"
)

// TranscriptEntry represents one line of a Claude Code transcript JSONL.
type TranscriptEntry struct {
	Type      string          `json:"type"`
	UUID      string          `json:"uuid"`
	SessionID string          `json:"sessionId"`
	Timestamp string          `json:"timestamp"`
	Message   json.RawMessage `json:"message"`
	Cwd       string          `json:"cwd"`
}

// TranscriptMessage is the message field inside a transcript entry.
type TranscriptMessage struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}

// ContentBlock represents a structured content block (text, tool_use, tool_result, etc.)
type ContentBlock struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
	Name string `json:"name,omitempty"`
	// tool_use
	Input json.RawMessage `json:"input,omitempty"`
	// tool_result
	Content json.RawMessage `json:"content,omitempty"`
}

// ParseEntry parses a single JSONL line into a TranscriptEntry.
func ParseEntry(line []byte) (*TranscriptEntry, error) {
	var e TranscriptEntry
	if err := json.Unmarshal(line, &e); err != nil {
		return nil, fmt.Errorf("parsing transcript entry: %w", err)
	}
	return &e, nil
}

// ExtractRole returns the message role, or empty if not a message entry.
func (e *TranscriptEntry) ExtractRole() string {
	if e.Type != "user" && e.Type != "assistant" {
		return ""
	}
	var msg TranscriptMessage
	if err := json.Unmarshal(e.Message, &msg); err != nil {
		return ""
	}
	return msg.Role
}

// ExtractContent extracts human-readable text content from the message.
// For structured content (list of blocks), it concatenates text blocks and
// summarizes tool_use/tool_result blocks.
func (e *TranscriptEntry) ExtractContent() string {
	if e.Message == nil {
		return ""
	}
	var msg TranscriptMessage
	if err := json.Unmarshal(e.Message, &msg); err != nil {
		return ""
	}

	// Try string content first
	var strContent string
	if err := json.Unmarshal(msg.Content, &strContent); err == nil {
		return strContent
	}

	// Try array of content blocks
	var blocks []ContentBlock
	if err := json.Unmarshal(msg.Content, &blocks); err != nil {
		return ""
	}

	var parts []string
	onlyToolResults := true
	for _, b := range blocks {
		switch b.Type {
		case "text":
			if b.Text != "" {
				parts = append(parts, b.Text)
				onlyToolResults = false
			}
		case "tool_use":
			parts = append(parts, fmt.Sprintf("[tool_use: %s]", b.Name))
			onlyToolResults = false
		case "tool_result":
			// skip — no content stored, just noise
		}
	}
	if onlyToolResults {
		return ""
	}
	return strings.Join(parts, "\n")
}

