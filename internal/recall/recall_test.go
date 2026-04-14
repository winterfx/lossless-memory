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
// - 3 messages in sess-1 (English)
// - 1 Chinese message in sess-2
// - 1 leaf summary (sum_leaf_001) linked to all 3 English messages
// - 1 leaf summary (sum_leaf_002) with Chinese content
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

	// English messages
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

	// Chinese message
	store.InsertMessage(&db.Message{
		WorkspaceID: wid, SessionID: "sess-2", Seq: 0,
		Role: "user", Content: "修复了登录认证的端到端测试结果", TokenCount: 12,
		CreatedAt: now.Add(5 * time.Minute),
	})

	// Leaf 1 (English)
	store.InsertSummary(&db.Summary{
		ID: "sum_leaf_001", WorkspaceID: wid, Kind: "leaf", Depth: 0,
		Content:    "worked on authentication bug fix and database migration",
		TokenCount: 10, EarliestAt: now, LatestAt: now.Add(2 * time.Minute),
		Model: "test", CreatedAt: now,
	})
	store.LinkSummaryMessages("sum_leaf_001", msgIDs)

	// Leaf 2 (Chinese)
	store.InsertSummary(&db.Summary{
		ID: "sum_leaf_002", WorkspaceID: wid, Kind: "leaf", Depth: 0,
		Content:    "添加了用户服务层的综合测试覆盖",
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

	result, err := Search(store, SearchOptions{
		Query: "authentication", Cwd: "/tmp/project", Limit: 10,
	})
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
		// Verify snippet is not empty
		if item.Snippet == "" {
			t.Error("expected non-empty snippet")
		}
		// Verify created_at is populated
		if item.CreatedAt == "" {
			t.Error("expected non-empty created_at")
		}
	}
	if !hasMessage {
		t.Error("expected a message result")
	}
	if !hasSummary {
		t.Error("expected a summary result")
	}
}

func TestSearchWithSnippet(t *testing.T) {
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

	result, err := Search(store, SearchOptions{
		Query: "authentication", Cwd: "/tmp/project", Limit: 10,
	})
	if err != nil {
		t.Fatal(err)
	}

	var items []SearchResult
	json.Unmarshal([]byte(result), &items)
	if len(items) == 0 {
		t.Fatal("expected results")
	}
	if len(items[0].Snippet) > 210 {
		t.Fatalf("snippet too long: %d chars", len(items[0].Snippet))
	}
}

func TestSearchNoWorkspace(t *testing.T) {
	store, err := db.OpenInMemory()
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	result, err := Search(store, SearchOptions{
		Query: "anything", Cwd: "/nonexistent", Limit: 10,
	})
	if err != nil {
		t.Fatalf("Search nonexistent workspace: %v", err)
	}
	if result != "[]" {
		t.Fatalf("expected empty array, got %s", result)
	}
}

func TestSearchAll(t *testing.T) {
	store, _ := setupTestStore(t)

	result, err := Search(store, SearchOptions{
		Query: "authentication", All: true, Limit: 10,
	})
	if err != nil {
		t.Fatalf("Search --all: %v", err)
	}

	var items []SearchResult
	json.Unmarshal([]byte(result), &items)
	if len(items) == 0 {
		t.Fatal("expected results with --all")
	}
}

func TestSearchSortModes(t *testing.T) {
	store, _ := setupTestStore(t)

	for _, sort := range []string{"relevance", "recency", "hybrid"} {
		result, err := Search(store, SearchOptions{
			Query: "authentication", Cwd: "/tmp/project",
			Sort: sort, Limit: 10,
		})
		if err != nil {
			t.Fatalf("Search sort=%s: %v", sort, err)
		}

		var items []SearchResult
		json.Unmarshal([]byte(result), &items)
		if len(items) == 0 {
			t.Fatalf("expected results with sort=%s", sort)
		}
	}
}

func TestSearchScope(t *testing.T) {
	store, _ := setupTestStore(t)

	// Messages only
	result, err := Search(store, SearchOptions{
		Query: "authentication", Cwd: "/tmp/project",
		Scope: "messages", Limit: 10,
	})
	if err != nil {
		t.Fatal(err)
	}
	var items []SearchResult
	json.Unmarshal([]byte(result), &items)
	for _, item := range items {
		if item.Type != "message" {
			t.Fatalf("scope=messages returned type=%s", item.Type)
		}
	}

	// Summaries only
	result, err = Search(store, SearchOptions{
		Query: "authentication", Cwd: "/tmp/project",
		Scope: "summaries", Limit: 10,
	})
	if err != nil {
		t.Fatal(err)
	}
	json.Unmarshal([]byte(result), &items)
	for _, item := range items {
		if item.Type != "summary" {
			t.Fatalf("scope=summaries returned type=%s", item.Type)
		}
	}
}

func TestSearchTimeFilter(t *testing.T) {
	store, _ := setupTestStore(t)

	// Search with a future since — should return no results
	result, err := Search(store, SearchOptions{
		Query: "authentication", Cwd: "/tmp/project",
		Since: "2099-01-01", Limit: 10,
	})
	if err != nil {
		t.Fatal(err)
	}
	var items []SearchResult
	json.Unmarshal([]byte(result), &items)
	if len(items) != 0 {
		t.Fatalf("expected 0 results with future since, got %d", len(items))
	}
}

func TestSearchRegex(t *testing.T) {
	store, _ := setupTestStore(t)

	result, err := Search(store, SearchOptions{
		Query: "auth.*bug", Cwd: "/tmp/project",
		Mode: "regex", Limit: 10,
	})
	if err != nil {
		t.Fatal(err)
	}

	var items []SearchResult
	json.Unmarshal([]byte(result), &items)
	if len(items) == 0 {
		t.Fatal("expected regex results")
	}
	if items[0].Type != "message" {
		t.Fatalf("expected message, got %s", items[0].Type)
	}
}

func TestSearchCJK(t *testing.T) {
	store, _ := setupTestStore(t)

	// Search for Chinese content (>= 3 CJK chars → trigram path)
	result, err := Search(store, SearchOptions{
		Query: "端到端测试", Cwd: "/tmp/project", Limit: 10,
	})
	if err != nil {
		t.Fatal(err)
	}

	var items []SearchResult
	json.Unmarshal([]byte(result), &items)
	if len(items) == 0 {
		t.Fatal("expected CJK search results")
	}
}

func TestSearchCJKShort(t *testing.T) {
	store, _ := setupTestStore(t)

	// Search with short CJK query (< 3 chars → LIKE fallback)
	result, err := Search(store, SearchOptions{
		Query: "测试", Cwd: "/tmp/project", Limit: 10,
	})
	if err != nil {
		t.Fatal(err)
	}

	var items []SearchResult
	json.Unmarshal([]byte(result), &items)
	if len(items) == 0 {
		t.Fatal("expected CJK LIKE fallback results")
	}
}

func TestSearchMixed(t *testing.T) {
	store, _ := setupTestStore(t)

	// Mixed query — search for Chinese term "认证" present in Chinese message
	result, err := Search(store, SearchOptions{
		Query: "认证", Cwd: "/tmp/project", Limit: 10,
	})
	if err != nil {
		t.Fatal(err)
	}

	var items []SearchResult
	json.Unmarshal([]byte(result), &items)
	// Should find the Chinese message containing "认证"
	if len(items) == 0 {
		t.Fatal("expected mixed search results")
	}
}

// --- Describe tests ---

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

// --- Expand tests ---

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
