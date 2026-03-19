package strategy

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lancekrogers/agent-defi/internal/base/trading"
	"github.com/lancekrogers/agent-defi/internal/festruntime"
)

func TestRuntimeEvaluateIntegratesWithObeyClient(t *testing.T) {
	root := t.TempDir()
	runDir := filepath.Join(root, "festivals", "active", "agent-market-research-RI-AM0001-0001")
	resultsDir := filepath.Join(runDir, "003_DECIDE", "01_synthesize_decision", "results")
	argsFile := filepath.Join(root, "obey-args.log")
	promptFile := filepath.Join(root, "obey-prompt.log")
	binDir := filepath.Join(root, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatalf("mkdir bin: %v", err)
	}

	fest := filepath.Join(binDir, "fest")
	festScript := strings.Join([]string{
		"#!/bin/sh",
		"if [ \"$1\" = \"ritual\" ] && [ \"$2\" = \"run\" ]; then",
		"  mkdir -p \"" + resultsDir + "\"",
		"  cat <<'JSON'",
		"{",
		"  \"dest_path\": \"" + runDir + "\",",
		"  \"run_dir\": \"" + filepath.Base(runDir) + "\",",
		"  \"run_number\": 1,",
		"  \"source_id\": \"RI-AM0001\",",
		"  \"source_name\": \"agent-market-research\"",
		"}",
		"JSON",
		"  exit 0",
		"fi",
		"if [ \"$1\" = \"show\" ]; then",
		"  cat <<'JSON'",
		"{",
		"  \"festival\": {",
		"    \"stats\": {",
		"      \"tasks\": {\"pending\": 0},",
		"      \"progress\": 100",
		"    }",
		"  }",
		"}",
		"JSON",
		"  exit 0",
		"fi",
		"echo \"unexpected fest args: $@\" >&2",
		"exit 1",
	}, "\n")
	if err := os.WriteFile(fest, []byte(festScript), 0o755); err != nil {
		t.Fatalf("write fest script: %v", err)
	}

	obey := filepath.Join(binDir, "obey")
	obeyScript := strings.Join([]string{
		"#!/bin/sh",
		"printf '%s\\n' \"$@\" >> \"" + argsFile + "\"",
		"if [ \"$1\" = \"ping\" ]; then",
		"  exit 0",
		"fi",
		"if [ \"$1\" = \"session\" ] && [ \"$2\" = \"create\" ]; then",
		"  echo \"Session: session-123\"",
		"  exit 0",
		"fi",
		"if [ \"$1\" = \"session\" ] && [ \"$2\" = \"send\" ]; then",
		"  printf '%s' \"${10}\" > \"" + promptFile + "\"",
		"  mkdir -p \"" + resultsDir + "\"",
		"  cat <<'JSON' > \"" + filepath.Join(resultsDir, "decision.json") + "\"",
		"{",
		"  \"ritual_id\": \"RI-AM0001\",",
		"  \"ritual_run_id\": \"agent-market-research-RI-AM0001-0001\",",
		"  \"timestamp\": \"2026-03-19T07:00:00Z\",",
		"  \"decision\": \"NO_GO\",",
		"  \"confidence\": 0.0,",
		"  \"blocking_factors\": [\"no_signal\"],",
		"  \"rationale\": {",
		"    \"summary\": \"NO_GO because the ritual found no mean-reversion signal.\"",
		"  },",
		"  \"guardrails\": {",
		"    \"trade_allowed\": false,",
		"    \"min_confidence_required\": 0.5,",
		"    \"min_net_profit_usd\": 1.0,",
		"    \"min_cre_gates_passed\": 6,",
		"    \"max_slippage_bps\": 100",
		"  },",
		"  \"artifact_paths\": {",
		"    \"decision\": \"003_DECIDE/01_synthesize_decision/results/decision.json\",",
		"    \"agent_log_entry\": \"003_DECIDE/01_synthesize_decision/results/agent_log_entry.json\"",
		"  }",
		"}",
		"JSON",
		"  cat <<'JSON' > \"" + filepath.Join(resultsDir, "agent_log_entry.json") + "\"",
		"{\"ok\":true}",
		"JSON",
		"  echo '{\"ok\":true}'",
		"  exit 0",
		"fi",
		"echo \"unexpected obey args: $@\" >&2",
		"exit 1",
	}, "\n")
	if err := os.WriteFile(obey, []byte(obeyScript), 0o755); err != nil {
		t.Fatalf("write obey script: %v", err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	runtime, err := festruntime.New(festruntime.Config{
		CampaignRoot: root,
		RitualID:     "agent-market-research-RI-AM0001",
		FestBinary:   fest,
		TokenIn:      "0xusdc",
		TokenOut:     "0xweth",
		PollInterval: time.Millisecond,
		Timeout:      50 * time.Millisecond,
	}, &ObeyClient{
		Socket:   "/tmp/obey.sock",
		Campaign: "Obey-Agent-Economy",
		Provider: "claude-code",
		Model:    "test-model",
		Agent:    "vault-trader",
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	signal, err := runtime.Evaluate(context.Background(), trading.MarketState{Price: 500})
	if err != nil {
		t.Fatalf("Evaluate() error = %v", err)
	}
	if signal.Type != trading.SignalHold {
		t.Fatalf("signal type = %s, want hold", signal.Type)
	}
	if signal.Ritual == nil {
		t.Fatal("ritual metadata = nil, want metadata")
	}
	if signal.Ritual.SessionID != "session-123" {
		t.Fatalf("session id = %q, want session-123", signal.Ritual.SessionID)
	}
	if signal.Ritual.Workdir != runDir {
		t.Fatalf("workdir = %q, want %q", signal.Ritual.Workdir, runDir)
	}

	argsData, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatalf("read args log: %v", err)
	}
	logged := string(argsData)
	for _, want := range []string{
		"ping",
		"session",
		"create",
		"--festival",
		"agent-market-research-RI-AM0001-0001",
		"--workdir",
		runDir,
		"--mode",
		"autonomous",
		`--config`,
		`{"permission_mode":"bypassPermissions"}`,
	} {
		if !strings.Contains(logged, want) {
			t.Fatalf("logged args missing %q:\n%s", want, logged)
		}
	}

	promptData, err := os.ReadFile(promptFile)
	if err != nil {
		t.Fatalf("read prompt log: %v", err)
	}
	prompt := string(promptData)
	if !strings.Contains(prompt, "cd "+runDir+" && fest next") {
		t.Fatalf("prompt = %q, want self-contained fest next command", prompt)
	}
	if !strings.Contains(prompt, "do not rely on a prior standalone `cd`") {
		t.Fatalf("prompt = %q, want shell-isolation guidance", prompt)
	}
	if !strings.Contains(prompt, "fest context") {
		t.Fatalf("prompt = %q, want explicit fest context verification", prompt)
	}
}
