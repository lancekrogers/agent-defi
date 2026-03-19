package loggen

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestLoadRitualEntriesWalksArchivedAndActiveRuns(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	active := filepath.Join(root, "active", "agent-market-research-RI-AM0001-0001", "003_DECIDE", "01_synthesize_decision", "results", "agent_log_entry.json")
	archived := filepath.Join(root, "dungeon", "completed", "2026-03-19", "agent-market-research-RI-AM0001-0002", "003_DECIDE", "01_synthesize_decision", "results", "agent_log_entry.json")

	writeEntry := func(path, decision, runID string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", path, err)
		}
		entry := RitualLogEntry{
			Action:     "market_research_ritual",
			Timestamp:  "2026-03-19T07:28:51.442Z",
			Phase:      "discover",
			FestivalID: "RI-AM0001",
			RunID:      runID,
			Decision:   decision,
			ToolsUsed:  []string{"fest", "obey"},
			Reasoning: map[string]any{
				"signal": decision,
			},
			DurationMS: 1234,
		}
		data, err := json.Marshal(entry)
		if err != nil {
			t.Fatalf("marshal %s: %v", path, err)
		}
		if err := os.WriteFile(path, data, 0o644); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}

	writeEntry(active, "NO_GO", "agent-market-research-RI-AM0001-0001")
	writeEntry(archived, "GO", "agent-market-research-RI-AM0001-0002")

	entries, err := LoadRitualEntries(root)
	if err != nil {
		t.Fatalf("LoadRitualEntries() error = %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("len(entries) = %d, want 2", len(entries))
	}
}

func TestRefresherRefreshWritesAggregateLog(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	entryPath := filepath.Join(root, "active", "agent-market-research-RI-AM0001-0001", "003_DECIDE", "01_synthesize_decision", "results", "agent_log_entry.json")
	if err := os.MkdirAll(filepath.Dir(entryPath), 0o755); err != nil {
		t.Fatalf("mkdir entry dir: %v", err)
	}

	entry := RitualLogEntry{
		Action:     "market_research_ritual",
		Timestamp:  "2026-03-19T07:28:51.442Z",
		Phase:      "discover",
		FestivalID: "RI-AM0001",
		RunID:      "agent-market-research-RI-AM0001-0001",
		Decision:   "NO_GO",
		ToolsUsed:  []string{"fest", "obey"},
		Reasoning: map[string]any{
			"signal": "NO_SIGNAL",
		},
		DurationMS: 1234,
	}
	data, err := json.Marshal(entry)
	if err != nil {
		t.Fatalf("marshal entry: %v", err)
	}
	if err := os.WriteFile(entryPath, data, 0o644); err != nil {
		t.Fatalf("write entry: %v", err)
	}

	outFile := filepath.Join(root, "agent_log.json")
	refresher := Refresher{
		Config: Config{
			RitualsDir: root,
			AgentName:  "OBEY Vault Agent",
			AgentID:    "0x0C97820abBdD2562645DaE92D35eD581266CCe70",
		},
		OutFile: outFile,
	}

	count, err := refresher.Refresh(context.Background())
	if err != nil {
		t.Fatalf("Refresh() error = %v", err)
	}
	if count != 1 {
		t.Fatalf("count = %d, want 1", count)
	}

	written, err := os.ReadFile(outFile)
	if err != nil {
		t.Fatalf("read outFile: %v", err)
	}

	var log AgentLog
	if err := json.Unmarshal(written, &log); err != nil {
		t.Fatalf("unmarshal outFile: %v", err)
	}
	if len(log.Entries) != 1 {
		t.Fatalf("len(log.Entries) = %d, want 1", len(log.Entries))
	}
	if log.Entries[0].Action != "market_research_ritual" {
		t.Fatalf("entry action = %q, want market_research_ritual", log.Entries[0].Action)
	}
	if log.Entries[0].FestivalID != "RI-AM0001" {
		t.Fatalf("entry festival = %q, want RI-AM0001", log.Entries[0].FestivalID)
	}
}
