// Package hookio reads Claude Code hook input from stdin.
package hookio

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
)

// HookInput is the common fields passed by Claude Code hooks via stdin.
type HookInput struct {
	SessionID      string `json:"session_id"`
	TranscriptPath string `json:"transcript_path"`
	Cwd            string `json:"cwd"`
	HookEventName  string `json:"hook_event_name"`
	Reason         string `json:"reason,omitempty"` // SessionEnd only
}

// Read parses hook input JSON from stdin.
func Read() (*HookInput, error) {
	data, err := io.ReadAll(os.Stdin)
	if err != nil {
		return nil, fmt.Errorf("reading stdin: %w", err)
	}
	if len(data) == 0 {
		return nil, fmt.Errorf("no input on stdin")
	}
	var h HookInput
	if err := json.Unmarshal(data, &h); err != nil {
		return nil, fmt.Errorf("parsing hook input: %w", err)
	}
	return &h, nil
}

// HookResponse is the JSON output format for hooks that inject additionalContext.
type HookResponse struct {
	HookSpecificOutput *HookSpecificOutput `json:"hookSpecificOutput,omitempty"`
}

type HookSpecificOutput struct {
	HookEventName     string `json:"hookEventName"`
	AdditionalContext string `json:"additionalContext,omitempty"`
}

// WriteResponse writes a hook response with additionalContext to stdout.
func WriteResponse(eventName, context string) error {
	resp := HookResponse{
		HookSpecificOutput: &HookSpecificOutput{
			HookEventName:     eventName,
			AdditionalContext: context,
		},
	}
	return json.NewEncoder(os.Stdout).Encode(resp)
}
