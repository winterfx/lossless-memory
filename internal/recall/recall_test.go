package recall

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/winter-wang/lossless-memory/internal/db"
)

// setupTestStore creates an in-memory store with test data:
// - 1 workspace at /tmp/project
// - 3 messages in sess-1
// - 1 leaf summary (sum_leaf_001) linked to all 3 messages
// - 1 leaf summary (sum_leaf_002)
// - 1 condensed summary (sum_cond_001) at depth 1, parent of both leaves
func setupTestStore(t *testing.T) (*db.Store, int64) {
	t.Helper()
	store, err := db.OpenInMemory()
	if err != nil {
		t.Fatalf("OpenInMemory: %v", err)
	}
	t.Cleanup(func() { store.Close() })

	wid, _ := store.EnsureWorkspace("/tmp/project")
	now := time.Now()

	// Messages
	var msgIDs []int64
	contents := []string{
		"fix the authentication bug in login flow",
		"refactored database migration to use transactions",
		"added unit tests for the user service",
	}
	for i, c := range contents {
		id, err := store.InsertMessage(&db.Message{
			WorkspaceID: wid, SessionID: "sess-1", Seq: i,
			Role: "user", Content: c, TokenCount: 8,
			CreatedAt: now.Add(time.Duration(i) * time.Minute),
		})
		if err != nil {
			t.Fatalf("InsertMessage: %v", err)
		}
		msgIDs = append(msgIDs, id)
	}

	// Leaf 1
	store.InsertSummary(&db.Summary{
		ID: "sum_leaf_001", WorkspaceID: wid, Kind: "leaf", Depth: 0,
		Content:    "worked on authentication bug fix and database migration",
		TokenCount: 10, EarliestAt: now, LatestAt: now.Add(2 * time.Minute),
		Model: "test", CreatedAt: now,
	})
	store.LinkSummaryMessages("sum_leaf_001", msgIDs)

	// Leaf 2
	store.InsertSummary(&db.Summary{
		ID: "sum_leaf_002", WorkspaceID: wid, Kind: "leaf", Depth: 0,
		Content:    "added comprehensive testing for user service layer",
		TokenCount: 8, EarliestAt: now.Add(3 * time.Minute), LatestAt: now.Add(5 * time.Minute),
		Model: "test", CreatedAt: now.Add(time.Minute),
	})

	// Condensed
	store.InsertSummary(&db.Summary{
		ID: "sum_cond_001", WorkspaceID: wid, Kind: "condensed", Depth: 1,
		Content:    "full session covering auth fix, db migration, and testing",
		TokenCount: 12, EarliestAt: now, LatestAt: now.Add(5 * time.Minute),
		Model: "test", CreatedAt: now.Add(2 * time.Minute),
	})
	store.LinkSummaryParents("sum_cond_001", []string{"sum_leaf_001", "sum_leaf_002"})

	return store, wid
}

func TestSearch(t *testing.T) {
	store, _ := setupTestStore(t)

	result, err := Search(store, "authentication", "/tmp/project", false, 10)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}

	var items []SearchResult
	if err := json.Unmarshal([]byte(result), &items); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(items) == 0 {
		t.Fatal("expected at least 1 result")
	}

	// Should find both message and summary
	hasMessage := false
	hasSummary := false
	for _, item := range items {
		if item.Type == "message" {
			hasMessage = true
		}
		if item.Type == "summary" {
			hasSummary = true
		}
	}
	if !hasMessage {
		t.Error("expected a message result")
	}
	if !hasSummary {
		t.Error("expected a summary result")
	}
}

func TestSearchSnippetTruncation(t *testing.T) {
	store, err := db.OpenInMemory()
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	wid, _ := store.EnsureWorkspace("/tmp/project")
	now := time.Now()

	// Insert a message with very long content
	longContent := strings.Repeat("authentication bug fix details ", 20) // ~620 chars
	store.InsertMessage(&db.Message{
		WorkspaceID: wid, SessionID: "sess-long", Seq: 0,
		Role: "user", Content: longContent, TokenCount: 100,
		CreatedAt: now,
	})

	result, err := Search(store, "authentication", "/tmp/project", false, 10)
	if err != nil {
		t.Fatal(err)
	}

	var items []SearchResult
	json.Unmarshal([]byte(result), &items)
	if len(items) == 0 {
		t.Fatal("expected results")
	}
	if len(items[0].Snippet) > 310 {
		t.Fatalf("snippet too long: %d chars", len(items[0].Snippet))
	}
	if !strings.HasSuffix(items[0].Snippet, "...") {
		t.Fatal("expected snippet to end with ...")
	}
}

func TestSearchNoWorkspace(t *testing.T) {
	store, err := db.OpenInMemory()
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	result, err := Search(store, "anything", "/nonexistent", false, 10)
	if err != nil {
		t.Fatalf("Search nonexistent workspace: %v", err)
	}
	if result != "[]" {
		t.Fatalf("expected empty array, got %s", result)
	}
}

func TestSearchAll(t *testing.T) {
	store, _ := setupTestStore(t)

	result, err := Search(store, "authentication", "/tmp/project", true, 10)
	if err != nil {
		t.Fatalf("Search --all: %v", err)
	}

	var items []SearchResult
	json.Unmarshal([]byte(result), &items)
	if len(items) == 0 {
		t.Fatal("expected results with --all")
	}
}

func TestDescribeLeaf(t *testing.T) {
	store, _ := setupTestStore(t)

	result, err := Describe(store, "sum_leaf_001")
	if err != nil {
		t.Fatalf("Describe: %v", err)
	}

	var desc DescribeResult
	if err := json.Unmarshal([]byte(result), &desc); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if desc.Kind != "leaf" {
		t.Fatalf("expected kind=leaf, got %s", desc.Kind)
	}
	if desc.Depth != 0 {
		t.Fatalf("expected depth=0, got %d", desc.Depth)
	}
	if len(desc.MessageIDs) != 3 {
		t.Fatalf("expected 3 message_ids, got %d", len(desc.MessageIDs))
	}
	if len(desc.ParentIDs) != 0 {
		t.Fatalf("expected 0 parent_ids for leaf, got %d", len(desc.ParentIDs))
	}
	// sum_leaf_001 is a parent of sum_cond_001
	if len(desc.ChildIDs) != 1 || desc.ChildIDs[0] != "sum_cond_001" {
		t.Fatalf("expected child_ids=[sum_cond_001], got %v", desc.ChildIDs)
	}
}

func TestDescribeCondensed(t *testing.T) {
	store, _ := setupTestStore(t)

	result, err := Describe(store, "sum_cond_001")
	if err != nil {
		t.Fatalf("Describe: %v", err)
	}

	var desc DescribeResult
	json.Unmarshal([]byte(result), &desc)

	if desc.Kind != "condensed" {
		t.Fatalf("expected kind=condensed, got %s", desc.Kind)
	}
	if desc.Depth != 1 {
		t.Fatalf("expected depth=1, got %d", desc.Depth)
	}
	if len(desc.ParentIDs) != 2 {
		t.Fatalf("expected 2 parent_ids, got %d", len(desc.ParentIDs))
	}
	if len(desc.MessageIDs) != 0 {
		t.Fatalf("expected 0 message_ids for condensed, got %d", len(desc.MessageIDs))
	}
}

func TestDescribeNotFound(t *testing.T) {
	store, err := db.OpenInMemory()
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	_, err = Describe(store, "sum_nonexistent")
	if err == nil {
		t.Fatal("expected error for nonexistent summary")
	}
}

func TestExpandCondensed(t *testing.T) {
	store, _ := setupTestStore(t)

	result, err := Expand(store, "sum_cond_001", 3, false)
	if err != nil {
		t.Fatalf("Expand: %v", err)
	}

	var exp ExpandResult
	if err := json.Unmarshal([]byte(result), &exp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if exp.SummaryID != "sum_cond_001" {
		t.Fatalf("expected sum_cond_001, got %s", exp.SummaryID)
	}
	if exp.Depth != 1 {
		t.Fatalf("expected depth=1, got %d", exp.Depth)
	}
	if len(exp.Children) != 2 {
		t.Fatalf("expected 2 children, got %d", len(exp.Children))
	}
	// Children should be the two leaves
	for _, child := range exp.Children {
		if child.Depth != 0 {
			t.Fatalf("expected child depth=0, got %d", child.Depth)
		}
	}
}

func TestExpandWithMessages(t *testing.T) {
	store, _ := setupTestStore(t)

	result, err := Expand(store, "sum_cond_001", 3, true)
	if err != nil {
		t.Fatalf("Expand with messages: %v", err)
	}

	var exp ExpandResult
	json.Unmarshal([]byte(result), &exp)

	// The first child (sum_leaf_001) should have messages
	found := false
	for _, child := range exp.Children {
		if child.SummaryID == "sum_leaf_001" && len(child.Messages) == 3 {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("expected sum_leaf_001 to have 3 messages")
	}
}

func TestExpandMaxDepth(t *testing.T) {
	store, _ := setupTestStore(t)

	// maxDepth=0 should only return the top-level summary, no children
	result, err := Expand(store, "sum_cond_001", 0, false)
	if err != nil {
		t.Fatalf("Expand maxDepth=0: %v", err)
	}

	var exp ExpandResult
	json.Unmarshal([]byte(result), &exp)

	if len(exp.Children) != 0 {
		t.Fatalf("expected 0 children with maxDepth=0, got %d", len(exp.Children))
	}
}

func TestExpandLeaf(t *testing.T) {
	store, _ := setupTestStore(t)

	result, err := Expand(store, "sum_leaf_001", 3, true)
	if err != nil {
		t.Fatalf("Expand leaf: %v", err)
	}

	var exp ExpandResult
	json.Unmarshal([]byte(result), &exp)

	if exp.SummaryID != "sum_leaf_001" {
		t.Fatalf("expected sum_leaf_001, got %s", exp.SummaryID)
	}
	if len(exp.Messages) != 3 {
		t.Fatalf("expected 3 messages, got %d", len(exp.Messages))
	}
	if len(exp.Children) != 0 {
		t.Fatalf("expected 0 children for leaf, got %d", len(exp.Children))
	}
}
