package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/winter-wang/lossless-memory/internal/config"
	"github.com/winter-wang/lossless-memory/internal/db"
	"github.com/winter-wang/lossless-memory/internal/digest"
	"github.com/winter-wang/lossless-memory/internal/hookio"
	"github.com/winter-wang/lossless-memory/internal/ingest"
	"github.com/winter-wang/lossless-memory/internal/overview"
	"github.com/winter-wang/lossless-memory/internal/recall"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(1)
	}

	switch os.Args[1] {
	case "ingest":
		runIngest()
	case "digest":
		runDigest()
	case "overview":
		runOverview()
	case "recall":
		if len(os.Args) < 3 {
			fmt.Fprintln(os.Stderr, "usage: lcm recall <search|describe|expand> [args]")
			os.Exit(1)
		}
		switch os.Args[2] {
		case "search":
			runRecallSearch()
		case "describe":
			runRecallDescribe()
		case "expand":
			runRecallExpand()
		default:
			fmt.Fprintf(os.Stderr, "unknown recall subcommand: %s\n", os.Args[2])
			os.Exit(1)
		}
	case "status":
		runStatus()
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n", os.Args[1])
		usage()
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, `usage: lcm <command> [args]

Commands:
  ingest    Ingest messages from transcript (reads hook input from stdin)
  digest    Create summaries for a session (reads hook input from stdin)
  overview  Generate session overview (reads hook input from stdin)
  recall    Search/describe/expand memory
    search  --cwd <path> --query <text> [--mode full_text|regex] [--scope messages|summaries|both] [--sort relevance|recency|hybrid] [--since <datetime>] [--before <datetime>] [--all] [--limit N]
    describe --id <sum_xxx>
    expand  --id <sum_xxx> [--max-depth N] [--include-messages]
  status    Show database statistics --cwd <path>`)
}

func openStore() *db.Store {
	store, err := db.Open(db.DefaultDBPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error opening database: %v\n", err)
		os.Exit(1)
	}
	return store
}

// readHookInput reads hook input from stdin when called as a hook.
func readHookInput() *hookio.HookInput {
	h, err := hookio.Read()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error reading hook input: %v\n", err)
		os.Exit(1)
	}
	return h
}

func runIngest() {
	h := readHookInput()
	store := openStore()
	defer store.Close()

	if err := ingest.Run(store, h.SessionID, h.TranscriptPath, h.Cwd); err != nil {
		fmt.Fprintf(os.Stderr, "[lcm] ingest error: %v\n", err)
		os.Exit(1)
	}
}

func runDigest() {
	h := readHookInput()
	store := openStore()
	defer store.Close()

	ctx := context.Background()

	// Ensure all messages are ingested first
	if err := ingest.Run(store, h.SessionID, h.TranscriptPath, h.Cwd); err != nil {
		fmt.Fprintf(os.Stderr, "[lcm] ingest before digest error: %v\n", err)
		// Continue anyway, we might have partial data
	}

	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "[lcm] config error: %v\n", err)
		os.Exit(1)
	}
	summarizer := digest.NewOpenAISummarizer(cfg)

	// Create leaf summaries; flush all remaining messages at session end
	forceFlush := h.HookEventName == "SessionEnd"
	leafIDs, err := digest.CreateLeafSummaries(ctx, store, h.SessionID, summarizer, forceFlush)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[lcm] leaf summary error: %v\n", err)
		os.Exit(1)
	}
	if len(leafIDs) > 0 {
		fmt.Fprintf(os.Stderr, "[lcm] created %d leaf summaries\n", len(leafIDs))
	}

	// Run condensation
	wid, err := store.EnsureWorkspace(h.Cwd)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[lcm] workspace error: %v\n", err)
		os.Exit(1)
	}
	condensedIDs, err := digest.RunCondensation(ctx, store, wid, summarizer)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[lcm] condensation error: %v\n", err)
		// Non-fatal, we still created leaf summaries
	}
	if len(condensedIDs) > 0 {
		fmt.Fprintf(os.Stderr, "[lcm] created %d condensed summaries\n", len(condensedIDs))
	}
}

func runOverview() {
	h := readHookInput()
	store := openStore()
	defer store.Close()

	text, err := overview.Generate(store, h.Cwd)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[lcm] overview error: %v\n", err)
		os.Exit(1)
	}

	if text == "" {
		return // no output, hook will have no effect
	}

	// Output hook response JSON with additionalContext
	if err := hookio.WriteResponse("SessionStart", text); err != nil {
		fmt.Fprintf(os.Stderr, "[lcm] writing response: %v\n", err)
		os.Exit(1)
	}
}

func runRecallSearch() {
	args := parseArgs(os.Args[3:])

	opts := recall.SearchOptions{
		Query:  args["query"],
		Mode:   args["mode"],
		Scope:  args["scope"],
		Sort:   args["sort"],
		Since:  args["since"],
		Before: args["before"],
		Cwd:    args["cwd"],
		All:    args["all"] == "true",
	}
	if l, ok := args["limit"]; ok {
		if n, err := strconv.Atoi(l); err == nil {
			opts.Limit = n
		}
	}

	if opts.Query == "" {
		fmt.Fprintln(os.Stderr, `usage: lcm recall search --cwd <path> --query <text>
    [--mode full_text|regex]
    [--scope messages|summaries|both]
    [--sort relevance|recency|hybrid]
    [--since <datetime>] [--before <datetime>]
    [--all] [--limit N]`)
		os.Exit(1)
	}

	store := openStore()
	defer store.Close()

	result, err := recall.Search(store, opts)
	if err != nil {
		fmt.Fprintf(os.Stderr, "search error: %v\n", err)
		os.Exit(1)
	}
	fmt.Println(result)
}

func runRecallDescribe() {
	args := parseArgs(os.Args[3:])
	id := args["id"]
	if id == "" {
		fmt.Fprintln(os.Stderr, "usage: lcm recall describe --id <sum_xxx>")
		os.Exit(1)
	}

	store := openStore()
	defer store.Close()

	result, err := recall.Describe(store, id)
	if err != nil {
		fmt.Fprintf(os.Stderr, "describe error: %v\n", err)
		os.Exit(1)
	}
	fmt.Println(result)
}

func runRecallExpand() {
	args := parseArgs(os.Args[3:])
	id := args["id"]
	if id == "" {
		fmt.Fprintln(os.Stderr, "usage: lcm recall expand --id <sum_xxx> [--max-depth N] [--include-messages]")
		os.Exit(1)
	}

	maxDepth := 3
	if d, ok := args["max-depth"]; ok {
		if n, err := strconv.Atoi(d); err == nil {
			maxDepth = n
		}
	}
	includeMessages := args["include-messages"] == "true"

	store := openStore()
	defer store.Close()

	result, err := recall.Expand(store, id, maxDepth, includeMessages)
	if err != nil {
		fmt.Fprintf(os.Stderr, "expand error: %v\n", err)
		os.Exit(1)
	}
	fmt.Println(result)
}

func runStatus() {
	args := parseArgs(os.Args[2:])
	cwd := args["cwd"]
	if cwd == "" {
		fmt.Fprintln(os.Stderr, "usage: lcm status --cwd <path>")
		os.Exit(1)
	}

	store := openStore()
	defer store.Close()

	wid, err := store.GetWorkspaceID(cwd)
	if err != nil {
		fmt.Fprintf(os.Stderr, "workspace not found for: %s\n", cwd)
		os.Exit(1)
	}

	// Count messages
	var msgCount int
	store.DB().QueryRow("SELECT COUNT(*) FROM messages WHERE workspace_id = ?", wid).Scan(&msgCount)

	// Count summaries by depth
	depthCounts, _ := store.CountSummariesByDepth(wid)

	// Count sessions
	var sessionCount int
	store.DB().QueryRow("SELECT COUNT(DISTINCT session_id) FROM messages WHERE workspace_id = ?", wid).Scan(&sessionCount)

	status := map[string]interface{}{
		"workspace":    cwd,
		"messages":     msgCount,
		"sessions":     sessionCount,
		"summaries":    depthCounts,
	}

	b, _ := json.MarshalIndent(status, "", "  ")
	fmt.Println(string(b))
}

// parseArgs parses --key value pairs from command line args.
func parseArgs(args []string) map[string]string {
	m := make(map[string]string)
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if !strings.HasPrefix(arg, "--") {
			continue
		}
		key := strings.TrimPrefix(arg, "--")
		// Boolean flags
		if key == "all" || key == "include-messages" {
			m[key] = "true"
			continue
		}
		if i+1 < len(args) {
			m[key] = args[i+1]
			i++
		}
	}
	return m
}
