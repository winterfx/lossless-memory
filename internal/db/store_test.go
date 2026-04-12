package db

import (
	"testing"
	"time"
)

func mustOpenInMemory(t *testing.T) *Store {
	t.Helper()
	store, err := OpenInMemory()
	if err != nil {
		t.Fatalf("OpenInMemory: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	return store
}

func TestWorkspace(t *testing.T) {
	store := mustOpenInMemory(t)

	wid, err := store.EnsureWorkspace("/tmp/project")
	if err != nil {
		t.Fatalf("EnsureWorkspace: %v", err)
	}
	if wid == 0 {
		t.Fatal("expected non-zero workspace ID")
	}

	// Idempotent
	wid2, err := store.EnsureWorkspace("/tmp/project")
	if err != nil {
		t.Fatalf("EnsureWorkspace again: %v", err)
	}
	if wid2 != wid {
		t.Fatalf("expected same workspace ID, got %d vs %d", wid, wid2)
	}

	// GetWorkspaceID
	got, err := store.GetWorkspaceID("/tmp/project")
	if err != nil {
		t.Fatalf("GetWorkspaceID: %v", err)
	}
	if got != wid {
		t.Fatalf("GetWorkspaceID: expected %d, got %d", wid, got)
	}

	// Not found
	_, err = store.GetWorkspaceID("/nonexistent")
	if err == nil {
		t.Fatal("expected error for nonexistent workspace")
	}
}

func TestInsertAndQueryMessages(t *testing.T) {
	store := mustOpenInMemory(t)
	wid, _ := store.EnsureWorkspace("/tmp/project")
	now := time.Now()

	msg := &Message{
		WorkspaceID: wid,
		SessionID:   "sess-1",
		Seq:         0,
		Role:        "user",
		Content:     "hello world",
		TokenCount:  3,
		CreatedAt:   now,
	}
	id, err := store.InsertMessage(msg)
	if err != nil {
		t.Fatalf("InsertMessage: %v", err)
	}
	if id == 0 {
		t.Fatal("expected non-zero message ID")
	}

	msg2 := &Message{
		WorkspaceID: wid,
		SessionID:   "sess-1",
		Seq:         1,
		Role:        "assistant",
		Content:     "hi there",
		TokenCount:  2,
		CreatedAt:   now.Add(time.Second),
	}
	_, err = store.InsertMessage(msg2)
	if err != nil {
		t.Fatalf("InsertMessage msg2: %v", err)
	}

	// GetMaxSeq
	maxSeq, err := store.GetMaxSeq("sess-1")
	if err != nil {
		t.Fatalf("GetMaxSeq: %v", err)
	}
	if maxSeq != 1 {
		t.Fatalf("expected maxSeq=1, got %d", maxSeq)
	}

	// Empty session
	maxSeq, err = store.GetMaxSeq("nonexistent")
	if err != nil {
		t.Fatalf("GetMaxSeq empty: %v", err)
	}
	if maxSeq != -1 {
		t.Fatalf("expected maxSeq=-1, got %d", maxSeq)
	}

	// GetMessagesBySession
	msgs, err := store.GetMessagesBySession("sess-1")
	if err != nil {
		t.Fatalf("GetMessagesBySession: %v", err)
	}
	if len(msgs) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(msgs))
	}
	if msgs[0].Role != "user" || msgs[1].Role != "assistant" {
		t.Fatalf("unexpected roles: %s, %s", msgs[0].Role, msgs[1].Role)
	}

	// GetMessagesByIDs
	byID, err := store.GetMessagesByIDs([]int64{msgs[0].ID, msgs[1].ID})
	if err != nil {
		t.Fatalf("GetMessagesByIDs: %v", err)
	}
	if len(byID) != 2 {
		t.Fatalf("expected 2, got %d", len(byID))
	}

	// Empty IDs
	empty, err := store.GetMessagesByIDs(nil)
	if err != nil {
		t.Fatalf("GetMessagesByIDs nil: %v", err)
	}
	if len(empty) != 0 {
		t.Fatalf("expected 0, got %d", len(empty))
	}
}

func TestIngestState(t *testing.T) {
	store := mustOpenInMemory(t)

	// First time: returns zero
	offset, path, err := store.GetIngestState("sess-1")
	if err != nil {
		t.Fatalf("GetIngestState: %v", err)
	}
	if offset != 0 || path != "" {
		t.Fatalf("expected 0/'', got %d/%q", offset, path)
	}

	// Set
	if err := store.SetIngestState("sess-1", "/tmp/transcript.jsonl", 1024); err != nil {
		t.Fatalf("SetIngestState: %v", err)
	}

	offset, path, err = store.GetIngestState("sess-1")
	if err != nil {
		t.Fatalf("GetIngestState after set: %v", err)
	}
	if offset != 1024 || path != "/tmp/transcript.jsonl" {
		t.Fatalf("expected 1024/'/tmp/transcript.jsonl', got %d/%q", offset, path)
	}

	// Upsert
	if err := store.SetIngestState("sess-1", "/tmp/transcript.jsonl", 2048); err != nil {
		t.Fatalf("SetIngestState upsert: %v", err)
	}
	offset, _, err = store.GetIngestState("sess-1")
	if err != nil {
		t.Fatalf("GetIngestState upsert: %v", err)
	}
	if offset != 2048 {
		t.Fatalf("expected 2048, got %d", offset)
	}
}

func TestSummaryAndDAG(t *testing.T) {
	store := mustOpenInMemory(t)
	wid, _ := store.EnsureWorkspace("/tmp/project")
	now := time.Now()

	// Insert messages
	var msgIDs []int64
	for i := 0; i < 3; i++ {
		id, err := store.InsertMessage(&Message{
			WorkspaceID: wid,
			SessionID:   "sess-1",
			Seq:         i,
			Role:        "user",
			Content:     "message content " + string(rune('A'+i)),
			TokenCount:  5,
			CreatedAt:   now.Add(time.Duration(i) * time.Minute),
		})
		if err != nil {
			t.Fatalf("InsertMessage %d: %v", i, err)
		}
		msgIDs = append(msgIDs, id)
	}

	// GetUncoveredMessages — all uncovered initially
	uncovered, err := store.GetUncoveredMessages("sess-1")
	if err != nil {
		t.Fatalf("GetUncoveredMessages: %v", err)
	}
	if len(uncovered) != 3 {
		t.Fatalf("expected 3 uncovered, got %d", len(uncovered))
	}

	// Create leaf summary
	leaf := &Summary{
		ID:          "sum_leaf_001",
		WorkspaceID: wid,
		Kind:        "leaf",
		Depth:       0,
		Content:     "leaf summary of messages",
		TokenCount:  10,
		EarliestAt:  now,
		LatestAt:    now.Add(2 * time.Minute),
		Model:       "test-model",
		CreatedAt:   now,
	}
	if err := store.InsertSummary(leaf); err != nil {
		t.Fatalf("InsertSummary: %v", err)
	}

	// Link to messages
	if err := store.LinkSummaryMessages("sum_leaf_001", msgIDs); err != nil {
		t.Fatalf("LinkSummaryMessages: %v", err)
	}

	// GetUncoveredMessages — now 0
	uncovered, err = store.GetUncoveredMessages("sess-1")
	if err != nil {
		t.Fatalf("GetUncoveredMessages after link: %v", err)
	}
	if len(uncovered) != 0 {
		t.Fatalf("expected 0 uncovered, got %d", len(uncovered))
	}

	// GetSummary
	got, err := store.GetSummary("sum_leaf_001")
	if err != nil {
		t.Fatalf("GetSummary: %v", err)
	}
	if got == nil || got.Kind != "leaf" {
		t.Fatal("expected leaf summary")
	}

	// Not found
	notFound, err := store.GetSummary("sum_nonexistent")
	if err != nil {
		t.Fatalf("GetSummary not found: %v", err)
	}
	if notFound != nil {
		t.Fatal("expected nil for nonexistent summary")
	}

	// GetSummaryMessageIDs
	linkedMsgIDs, err := store.GetSummaryMessageIDs("sum_leaf_001")
	if err != nil {
		t.Fatalf("GetSummaryMessageIDs: %v", err)
	}
	if len(linkedMsgIDs) != 3 {
		t.Fatalf("expected 3 linked messages, got %d", len(linkedMsgIDs))
	}

	// Create a second leaf and a condensed summary
	leaf2 := &Summary{
		ID:          "sum_leaf_002",
		WorkspaceID: wid,
		Kind:        "leaf",
		Depth:       0,
		Content:     "second leaf summary",
		TokenCount:  8,
		EarliestAt:  now.Add(3 * time.Minute),
		LatestAt:    now.Add(5 * time.Minute),
		Model:       "test-model",
		CreatedAt:   now.Add(time.Minute),
	}
	if err := store.InsertSummary(leaf2); err != nil {
		t.Fatalf("InsertSummary leaf2: %v", err)
	}

	condensed := &Summary{
		ID:          "sum_condensed_001",
		WorkspaceID: wid,
		Kind:        "condensed",
		Depth:       1,
		Content:     "condensed summary of two leaves",
		TokenCount:  15,
		EarliestAt:  now,
		LatestAt:    now.Add(5 * time.Minute),
		Model:       "test-model",
		CreatedAt:   now.Add(2 * time.Minute),
	}
	if err := store.InsertSummary(condensed); err != nil {
		t.Fatalf("InsertSummary condensed: %v", err)
	}

	if err := store.LinkSummaryParents("sum_condensed_001", []string{"sum_leaf_001", "sum_leaf_002"}); err != nil {
		t.Fatalf("LinkSummaryParents: %v", err)
	}

	// GetSummariesByWorkspaceAndDepth
	depth0, err := store.GetSummariesByWorkspaceAndDepth(wid, 0)
	if err != nil {
		t.Fatalf("GetSummariesByWorkspaceAndDepth 0: %v", err)
	}
	if len(depth0) != 2 {
		t.Fatalf("expected 2 at depth 0, got %d", len(depth0))
	}

	depth1, err := store.GetSummariesByWorkspaceAndDepth(wid, 1)
	if err != nil {
		t.Fatalf("GetSummariesByWorkspaceAndDepth 1: %v", err)
	}
	if len(depth1) != 1 {
		t.Fatalf("expected 1 at depth 1, got %d", len(depth1))
	}

	// GetSummaryParentIDs
	parentIDs, err := store.GetSummaryParentIDs("sum_condensed_001")
	if err != nil {
		t.Fatalf("GetSummaryParentIDs: %v", err)
	}
	if len(parentIDs) != 2 {
		t.Fatalf("expected 2 parents, got %d", len(parentIDs))
	}

	// GetChildSummaryIDs
	childIDs, err := store.GetChildSummaryIDs("sum_leaf_001")
	if err != nil {
		t.Fatalf("GetChildSummaryIDs: %v", err)
	}
	if len(childIDs) != 1 || childIDs[0] != "sum_condensed_001" {
		t.Fatalf("expected [sum_condensed_001], got %v", childIDs)
	}

	// GetHighestDepthSummaries
	highest, err := store.GetHighestDepthSummaries(wid, 0, 10)
	if err != nil {
		t.Fatalf("GetHighestDepthSummaries: %v", err)
	}
	if len(highest) != 3 {
		t.Fatalf("expected 3 summaries, got %d", len(highest))
	}
	if highest[0].Depth != 1 {
		t.Fatalf("expected depth 1 first, got %d", highest[0].Depth)
	}

	// CountSummariesByDepth
	counts, err := store.CountSummariesByDepth(wid)
	if err != nil {
		t.Fatalf("CountSummariesByDepth: %v", err)
	}
	if counts[0] != 2 || counts[1] != 1 {
		t.Fatalf("expected {0:2, 1:1}, got %v", counts)
	}
}

func TestFTSSearch(t *testing.T) {
	store := mustOpenInMemory(t)
	wid, _ := store.EnsureWorkspace("/tmp/project")
	now := time.Now()

	// Insert messages with distinct content
	store.InsertMessage(&Message{
		WorkspaceID: wid, SessionID: "sess-1", Seq: 0,
		Role: "user", Content: "fix the authentication bug in login",
		TokenCount: 7, CreatedAt: now,
	})
	store.InsertMessage(&Message{
		WorkspaceID: wid, SessionID: "sess-1", Seq: 1,
		Role: "assistant", Content: "refactored the database migration script",
		TokenCount: 6, CreatedAt: now.Add(time.Second),
	})

	// Insert a summary
	store.InsertSummary(&Summary{
		ID: "sum_fts_001", WorkspaceID: wid, Kind: "leaf", Depth: 0,
		Content: "session about authentication improvements and login flow",
		TokenCount: 10, EarliestAt: now, LatestAt: now.Add(time.Second),
		Model: "test", CreatedAt: now,
	})

	// SearchMessages
	results, err := store.SearchMessages("authentication", wid, 10)
	if err != nil {
		t.Fatalf("SearchMessages: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 message result, got %d", len(results))
	}
	if results[0].Type != "message" {
		t.Fatalf("expected type=message, got %s", results[0].Type)
	}

	// SearchSummaries
	results, err = store.SearchSummaries("authentication", wid, 10)
	if err != nil {
		t.Fatalf("SearchSummaries: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 summary result, got %d", len(results))
	}
	if results[0].Type != "summary" {
		t.Fatalf("expected type=summary, got %s", results[0].Type)
	}

	// SearchAll
	results, err = store.SearchAll("authentication", wid, 10)
	if err != nil {
		t.Fatalf("SearchAll: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}

	// No results
	results, err = store.SearchMessages("kubernetes", wid, 10)
	if err != nil {
		t.Fatalf("SearchMessages no match: %v", err)
	}
	if len(results) != 0 {
		t.Fatalf("expected 0 results, got %d", len(results))
	}

	// SearchAllWorkspaces
	results, err = store.SearchAllWorkspaces("database", 10)
	if err != nil {
		t.Fatalf("SearchAllWorkspaces: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
}
