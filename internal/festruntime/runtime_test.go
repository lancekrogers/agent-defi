package festruntime

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/lancekrogers/agent-defi/internal/base/trading"
)

type fakeSessionRunner struct {
	preflightErr error
	meta         SessionMeta
	lastReq      SessionRequest
	lastPrompt   string
	runCalls     int
	writeFn      func(req SessionRequest) error
}

func (f *fakeSessionRunner) Preflight(ctx context.Context) error {
	return f.preflightErr
}

func (f *fakeSessionRunner) RunPrompt(ctx context.Context, req SessionRequest, prompt string) (SessionMeta, string, error) {
	f.runCalls++
	f.lastReq = req
	f.lastPrompt = prompt
	if f.writeFn != nil {
		if err := f.writeFn(req); err != nil {
			return SessionMeta{}, "", err
		}
	}
	return f.meta, "ok", nil
}

func TestRuntimeEvaluateReturnsHoldForNoGoDecision(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	runDir := filepath.Join(root, "festivals", "active", "agent-market-research-RI-AM0001-0001")
	fest := writeFakeFest(t, root, runDir, 0, 100)

	session := &fakeSessionRunner{
		meta: SessionMeta{
			SessionID: "session-123",
			Campaign:  "Obey-Agent-Economy",
			Provider:  "test-provider",
			Model:     "test-model",
			Festival:  "agent-market-research-RI-AM0001-0001",
			Workdir:   runDir,
		},
		writeFn: func(req SessionRequest) error {
			resultsDir := filepath.Join(req.Workdir, "003_DECIDE", "01_synthesize_decision", "results")
			if err := os.MkdirAll(resultsDir, 0o755); err != nil {
				return err
			}
			decision := `{
  "ritual_id": "RI-AM0001",
  "ritual_run_id": "agent-market-research-RI-AM0001-0001",
  "timestamp": "2026-03-19T07:00:00Z",
  "decision": "NO_GO",
  "confidence": 0.0,
  "blocking_factors": ["no_signal"],
  "rationale": {
    "summary": "NO_GO because the ritual found no mean-reversion signal."
  },
  "guardrails": {
    "trade_allowed": false,
    "min_confidence_required": 0.5,
    "min_net_profit_usd": 1.0,
    "min_cre_gates_passed": 6,
    "max_slippage_bps": 100
  },
  "artifact_paths": {
    "decision": "003_DECIDE/01_synthesize_decision/results/decision.json",
    "agent_log_entry": "003_DECIDE/01_synthesize_decision/results/agent_log_entry.json"
  }
}`
			if err := os.WriteFile(filepath.Join(resultsDir, "decision.json"), []byte(decision), 0o644); err != nil {
				return err
			}
			return os.WriteFile(filepath.Join(resultsDir, "agent_log_entry.json"), []byte(`{"ok":true}`), 0o644)
		},
	}

	runtime, err := New(Config{
		CampaignRoot: root,
		RitualID:     "agent-market-research-RI-AM0001",
		FestBinary:   fest,
		TokenIn:      "0xusdc",
		TokenOut:     "0xweth",
		PollInterval: time.Millisecond,
		Timeout:      50 * time.Millisecond,
	}, session)
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
	if signal.Ritual == nil || signal.Ritual.SessionID != "session-123" {
		t.Fatalf("ritual metadata missing session: %#v", signal.Ritual)
	}
	if session.lastReq.Festival != "agent-market-research-RI-AM0001-0001" {
		t.Fatalf("festival = %q, want run id", session.lastReq.Festival)
	}
	if session.lastReq.Workdir != runDir {
		t.Fatalf("workdir = %q, want %q", session.lastReq.Workdir, runDir)
	}
	if !strings.Contains(session.lastPrompt, "fest commands") {
		t.Fatalf("prompt = %q, want ritual completion instructions", session.lastPrompt)
	}
}

func TestRuntimeEvaluateTimesOutWhenArtifactsNeverAppear(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	runDir := filepath.Join(root, "festivals", "active", "agent-market-research-RI-AM0001-0001")
	fest := writeFakeFest(t, root, runDir, 1, 50)

	runtime, err := New(Config{
		CampaignRoot: root,
		RitualID:     "agent-market-research-RI-AM0001",
		FestBinary:   fest,
		TokenIn:      "0xusdc",
		TokenOut:     "0xweth",
		PollInterval: time.Millisecond,
		Timeout:      10 * time.Millisecond,
	}, &fakeSessionRunner{
		meta: SessionMeta{
			SessionID: "session-timeout",
			Campaign:  "Obey-Agent-Economy",
			Provider:  "test-provider",
			Model:     "test-model",
			Festival:  "agent-market-research-RI-AM0001-0001",
			Workdir:   runDir,
		},
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	_, err = runtime.Evaluate(context.Background(), trading.MarketState{Price: 500})
	if err == nil {
		t.Fatal("Evaluate() error = nil, want timeout")
	}
	if !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("error = %v, want timeout", err)
	}
}

func TestRuntimeEvaluateTimeoutIncludesLastStatusError(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	runDir := filepath.Join(root, "festivals", "active", "agent-market-research-RI-AM0001-0001")
	fest := writeBrokenShowFest(t, root, runDir)

	runtime, err := New(Config{
		CampaignRoot: root,
		RitualID:     "agent-market-research-RI-AM0001",
		FestBinary:   fest,
		TokenIn:      "0xusdc",
		TokenOut:     "0xweth",
		PollInterval: time.Millisecond,
		Timeout:      10 * time.Millisecond,
	}, &fakeSessionRunner{
		meta: SessionMeta{
			SessionID: "session-timeout",
			Campaign:  "Obey-Agent-Economy",
			Provider:  "test-provider",
			Model:     "test-model",
			Festival:  "agent-market-research-RI-AM0001-0001",
			Workdir:   runDir,
		},
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	_, err = runtime.Evaluate(context.Background(), trading.MarketState{Price: 500})
	if err == nil {
		t.Fatal("Evaluate() error = nil, want timeout")
	}
	if !strings.Contains(err.Error(), "last_status_error") {
		t.Fatalf("error = %v, want last status error context", err)
	}
}

func TestRuntimeEvaluateFailsOnMalformedDecisionArtifact(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	runDir := filepath.Join(root, "festivals", "active", "agent-market-research-RI-AM0001-0001")
	fest := writeFakeFest(t, root, runDir, 0, 100)

	session := &fakeSessionRunner{
		meta: SessionMeta{
			SessionID: "session-bad-decision",
			Campaign:  "Obey-Agent-Economy",
			Provider:  "test-provider",
			Model:     "test-model",
			Festival:  "agent-market-research-RI-AM0001-0001",
			Workdir:   runDir,
		},
		writeFn: func(req SessionRequest) error {
			resultsDir := filepath.Join(req.Workdir, "003_DECIDE", "01_synthesize_decision", "results")
			if err := os.MkdirAll(resultsDir, 0o755); err != nil {
				return err
			}
			if err := os.WriteFile(filepath.Join(resultsDir, "decision.json"), []byte(`{"decision":"MAYBE"}`), 0o644); err != nil {
				return err
			}
			return os.WriteFile(filepath.Join(resultsDir, "agent_log_entry.json"), []byte(`{"ok":true}`), 0o644)
		},
	}

	runtime, err := New(Config{
		CampaignRoot: root,
		RitualID:     "agent-market-research-RI-AM0001",
		FestBinary:   fest,
		TokenIn:      "0xusdc",
		TokenOut:     "0xweth",
		PollInterval: time.Millisecond,
		Timeout:      50 * time.Millisecond,
	}, session)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	_, err = runtime.Evaluate(context.Background(), trading.MarketState{Price: 500})
	if err == nil {
		t.Fatal("Evaluate() error = nil, want malformed artifact failure")
	}
	if !strings.Contains(err.Error(), "invalid decision value") {
		t.Fatalf("error = %v, want invalid decision value", err)
	}
}

func TestRuntimeEvaluateFailsClosedWhenPreflightFails(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	session := &fakeSessionRunner{
		preflightErr: errors.New("daemon unavailable"),
	}

	runtime, err := New(Config{
		CampaignRoot: root,
		RitualID:     "agent-market-research-RI-AM0001",
		FestBinary:   filepath.Join(root, "missing-fest"),
		TokenIn:      "0xusdc",
		TokenOut:     "0xweth",
	}, session)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	_, err = runtime.Evaluate(context.Background(), trading.MarketState{Price: 500})
	if err == nil {
		t.Fatal("Evaluate() error = nil, want preflight failure")
	}
	if !strings.Contains(err.Error(), "obey preflight") {
		t.Fatalf("error = %v, want obey preflight context", err)
	}
	if session.runCalls != 0 {
		t.Fatalf("runCalls = %d, want 0", session.runCalls)
	}
}

func TestRuntimeEvaluateLogsSessionMetadata(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	runDir := filepath.Join(root, "festivals", "active", "agent-market-research-RI-AM0001-0001")
	fest := writeFakeFest(t, root, runDir, 0, 100)

	var logBuf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logBuf, nil))
	session := &fakeSessionRunner{
		meta: SessionMeta{
			SessionID: "session-123",
			Campaign:  "Obey-Agent-Economy",
			Provider:  "test-provider",
			Model:     "test-model",
			Festival:  "agent-market-research-RI-AM0001-0001",
			Workdir:   runDir,
		},
		writeFn: func(req SessionRequest) error {
			resultsDir := filepath.Join(req.Workdir, "003_DECIDE", "01_synthesize_decision", "results")
			if err := os.MkdirAll(resultsDir, 0o755); err != nil {
				return err
			}
			decision := `{
  "ritual_id": "RI-AM0001",
  "ritual_run_id": "agent-market-research-RI-AM0001-0001",
  "timestamp": "2026-03-19T07:00:00Z",
  "decision": "NO_GO",
  "confidence": 0.0,
  "blocking_factors": ["no_signal"],
  "rationale": {
    "summary": "NO_GO because the ritual found no mean-reversion signal."
  },
  "guardrails": {
    "trade_allowed": false,
    "min_confidence_required": 0.5,
    "min_net_profit_usd": 1.0,
    "min_cre_gates_passed": 6,
    "max_slippage_bps": 100
  },
  "artifact_paths": {
    "decision": "003_DECIDE/01_synthesize_decision/results/decision.json",
    "agent_log_entry": "003_DECIDE/01_synthesize_decision/results/agent_log_entry.json"
  }
}`
			if err := os.WriteFile(filepath.Join(resultsDir, "decision.json"), []byte(decision), 0o644); err != nil {
				return err
			}
			return os.WriteFile(filepath.Join(resultsDir, "agent_log_entry.json"), []byte(`{"ok":true}`), 0o644)
		},
	}

	runtime, err := New(Config{
		CampaignRoot: root,
		RitualID:     "agent-market-research-RI-AM0001",
		FestBinary:   fest,
		TokenIn:      "0xusdc",
		TokenOut:     "0xweth",
		PollInterval: time.Millisecond,
		Timeout:      50 * time.Millisecond,
		Logger:       logger,
	}, session)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	signal, err := runtime.Evaluate(context.Background(), trading.MarketState{Price: 500})
	if err != nil {
		t.Fatalf("Evaluate() error = %v", err)
	}
	if signal.Ritual == nil || signal.Ritual.Campaign != "Obey-Agent-Economy" {
		t.Fatalf("ritual campaign = %#v, want Obey-Agent-Economy", signal.Ritual)
	}

	logged := logBuf.String()
	for _, want := range []string{
		`"msg":"ritual runtime completed"`,
		`"campaign":"Obey-Agent-Economy"`,
		`"ritual_run_id":"agent-market-research-RI-AM0001-0001"`,
		`"session_id":"session-123"`,
		`"provider":"test-provider"`,
		`"model":"test-model"`,
		`"workdir":"` + runDir + `"`,
	} {
		if !strings.Contains(logged, want) {
			t.Fatalf("logged output missing %q:\n%s", want, logged)
		}
	}
}

func TestParseRunOutputAcceptsRealFestPayload(t *testing.T) {
	t.Parallel()

	data := mustReadTestdata(t, "ritual_run_output.json")
	run, err := parseRunOutput(data, "/unused")
	if err != nil {
		t.Fatalf("parseRunOutput() error = %v", err)
	}
	if run.ID != "agent-market-research-RI-AM0001-0003" {
		t.Fatalf("run.ID = %q", run.ID)
	}
	if run.Path != "/Users/lancerogers/Dev/Crypto/ETHDENVER/Obey-Agent-Economy/festivals/active/agent-market-research-RI-AM0001-0003" {
		t.Fatalf("run.Path = %q", run.Path)
	}
	if run.TemplateID != "agent-market-research-RI-AM0001" {
		t.Fatalf("run.TemplateID = %q", run.TemplateID)
	}
}

func TestParseShowRoadmapAcceptsRealFestPayload(t *testing.T) {
	t.Parallel()

	data := mustReadTestdata(t, "show_roadmap_output.json")
	status, err := parseShowRoadmap(data)
	if err != nil {
		t.Fatalf("parseShowRoadmap() error = %v", err)
	}
	if status.Festival.Stats.Tasks.Pending != 0 {
		t.Fatalf("pending = %d, want 0", status.Festival.Stats.Tasks.Pending)
	}
	if status.Festival.Stats.Progress != 100 {
		t.Fatalf("progress = %.1f, want 100", status.Festival.Stats.Progress)
	}
}

func TestRuntimeConfigAccessors(t *testing.T) {
	t.Parallel()

	runtime, err := New(Config{
		CampaignRoot:    "/tmp/campaign",
		RitualID:        "agent-market-research-RI-AM0001",
		FestBinary:      "fest",
		TokenIn:         "0xusdc",
		TokenOut:        "0xweth",
		MaxPositionSize: 42,
	}, &fakeSessionRunner{})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	if runtime.Name() != "fest_ritual_runtime" {
		t.Fatalf("Name() = %q", runtime.Name())
	}
	if runtime.CampaignRoot() != "/tmp/campaign" {
		t.Fatalf("CampaignRoot() = %q", runtime.CampaignRoot())
	}
	if runtime.MaxPosition() != 42 {
		t.Fatalf("MaxPosition() = %v", runtime.MaxPosition())
	}
}

func TestRuntimeSignalFromDecisionBuildsBuySignal(t *testing.T) {
	t.Parallel()

	runtime := &Runtime{
		cfg: Config{
			TokenIn:         "0xusdc",
			TokenOut:        "0xweth",
			MaxPositionSize: 10,
		},
		now: func() time.Time { return time.Unix(123, 0).UTC() },
	}

	signal, err := runtime.signalFromDecision(Decision{
		RitualID:    "RI-AM0001",
		RitualRunID: "agent-market-research-RI-AM0001-0003",
		Decision:    "GO",
		Confidence:  0.8,
		Rationale: Rationale{
			Summary: "buy because price is below the moving average",
		},
		Recommendation: &DecisionRe{
			Direction:        "BUY",
			SuggestedSizeUSD: 2500,
		},
		Guardrails: Guardrails{
			TradeAllowed: true,
		},
	}, trading.MarketState{Price: 500}, SessionMeta{
		SessionID: "session-123",
		Campaign:  "Obey-Agent-Economy",
		Provider:  "test-provider",
		Model:     "test-model",
		Workdir:   "/tmp/run",
	}, artifactFiles{
		DecisionPath: "003_DECIDE/01_synthesize_decision/results/decision.json",
		AgentLogPath: "003_DECIDE/01_synthesize_decision/results/agent_log_entry.json",
	})
	if err != nil {
		t.Fatalf("signalFromDecision() error = %v", err)
	}

	if signal.Type != trading.SignalBuy {
		t.Fatalf("signal.Type = %q", signal.Type)
	}
	if signal.SuggestedSize != 5 {
		t.Fatalf("signal.SuggestedSize = %v, want 5", signal.SuggestedSize)
	}
	if signal.TokenIn != "0xusdc" || signal.TokenOut != "0xweth" {
		t.Fatalf("tokens = %s -> %s", signal.TokenIn, signal.TokenOut)
	}
	if signal.Ritual == nil || signal.Ritual.DecisionPath == "" || signal.Ritual.AgentLogPath == "" {
		t.Fatalf("ritual metadata missing artifact paths: %#v", signal.Ritual)
	}
	if signal.Ritual.Campaign != "Obey-Agent-Economy" {
		t.Fatalf("ritual campaign = %q, want Obey-Agent-Economy", signal.Ritual.Campaign)
	}
}

func TestParseRunOutputRejectsMissingDestPath(t *testing.T) {
	t.Parallel()

	_, err := parseRunOutput([]byte(`{"run_dir":"agent-market-research-RI-AM0001-0003","success":true}`), "/tmp")
	if err == nil {
		t.Fatal("parseRunOutput() error = nil, want missing dest_path")
	}
	if !strings.Contains(err.Error(), "missing dest_path") {
		t.Fatalf("error = %v", err)
	}
}

func TestLoadDecisionRejectsNoGoWithoutBlockingFactors(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "decision.json")
	if err := os.WriteFile(path, []byte(`{"decision":"NO_GO","confidence":0.5}`), 0o644); err != nil {
		t.Fatalf("write decision: %v", err)
	}

	runtime := &Runtime{}
	_, err := runtime.loadDecision(path)
	if err == nil {
		t.Fatal("loadDecision() error = nil, want blocking_factors failure")
	}
	if !strings.Contains(err.Error(), "blocking_factors") {
		t.Fatalf("error = %v", err)
	}
}

func TestResolveCampaignRootFindsCampaignAncestor(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".campaign"), 0o755); err != nil {
		t.Fatalf("mkdir .campaign: %v", err)
	}
	nested := filepath.Join(root, "projects", "agent-defi")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatalf("mkdir nested: %v", err)
	}

	prev, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(nested); err != nil {
		t.Fatalf("chdir nested: %v", err)
	}
	defer func() {
		_ = os.Chdir(prev)
	}()

	resolved, err := resolveCampaignRoot("")
	if err != nil {
		t.Fatalf("resolveCampaignRoot() error = %v", err)
	}
	resolvedInfo, err := os.Stat(resolved)
	if err != nil {
		t.Fatalf("stat resolved root: %v", err)
	}
	rootInfo, err := os.Stat(root)
	if err != nil {
		t.Fatalf("stat expected root: %v", err)
	}
	if !os.SameFile(resolvedInfo, rootInfo) {
		t.Fatalf("resolveCampaignRoot() = %q, want same directory as %q", resolved, root)
	}
}

func writeFakeFest(t *testing.T, root, runDir string, pending int, progress int) string {
	t.Helper()
	binDir := filepath.Join(root, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatalf("mkdir bin: %v", err)
	}
	script := filepath.Join(binDir, "fest")
	content := strings.Join([]string{
		"#!/bin/sh",
		"if [ \"$1\" = \"ritual\" ] && [ \"$2\" = \"run\" ]; then",
		"  mkdir -p \"" + filepath.Join(runDir, "003_DECIDE", "01_synthesize_decision", "results") + "\"",
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
		"      \"tasks\": {\"pending\": " + strconv.Itoa(pending) + "},",
		"      \"progress\": " + strconv.Itoa(progress),
		"    }",
		"  }",
		"}",
		"JSON",
		"  exit 0",
		"fi",
		"echo \"unexpected args: $@\" >&2",
		"exit 1",
	}, "\n")
	if err := os.WriteFile(script, []byte(content), 0o755); err != nil {
		t.Fatalf("write fake fest: %v", err)
	}
	return script
}

func writeBrokenShowFest(t *testing.T, root, runDir string) string {
	t.Helper()
	binDir := filepath.Join(root, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatalf("mkdir bin: %v", err)
	}
	script := filepath.Join(binDir, "fest")
	content := strings.Join([]string{
		"#!/bin/sh",
		"if [ \"$1\" = \"ritual\" ] && [ \"$2\" = \"run\" ]; then",
		"  mkdir -p \"" + filepath.Join(runDir, "003_DECIDE", "01_synthesize_decision", "results") + "\"",
		"  cat <<'JSON'",
		"{",
		"  \"dest_path\": \"" + runDir + "\",",
		"  \"run_dir\": \"" + filepath.Base(runDir) + "\",",
		"  \"ritual\": \"agent-market-research-RI-AM0001\",",
		"  \"run_number\": 1,",
		"  \"success\": true",
		"}",
		"JSON",
		"  exit 0",
		"fi",
		"if [ \"$1\" = \"show\" ]; then",
		"  echo '{not-json}'",
		"  exit 0",
		"fi",
		"echo \"unexpected args: $@\" >&2",
		"exit 1",
	}, "\n")
	if err := os.WriteFile(script, []byte(content), 0o755); err != nil {
		t.Fatalf("write fake fest: %v", err)
	}
	return script
}

func mustReadTestdata(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read testdata %s: %v", name, err)
	}
	return data
}
