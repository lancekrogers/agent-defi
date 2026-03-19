package festruntime

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/lancekrogers/agent-defi/internal/base/trading"
)

const defaultPrompt = `Execute the active ritual run in the bound workdir.

Use fest commands from inside that workdir and follow the ritual to completion.
Write the canonical machine-readable artifacts in the ritual results directory.
Treat NO_GO as successful ritual completion when the artifacts are valid.
Do not use synthetic shortcuts or skip the fest workflow.`

// SessionRequest describes the dynamic runtime context for one obey session.
type SessionRequest struct {
	Festival string
	Workdir  string
}

// SessionMeta captures the non-secret runtime metadata for a created session.
type SessionMeta struct {
	SessionID string
	Campaign  string
	Provider  string
	Model     string
	Festival  string
	Workdir   string
}

// SessionRunner creates a live obey session and sends the ritual prompt.
type SessionRunner interface {
	Preflight(ctx context.Context) error
	RunPrompt(ctx context.Context, req SessionRequest, prompt string) (SessionMeta, string, error)
}

// Config controls the fest-backed ritual runtime.
type Config struct {
	CampaignRoot    string
	RitualID        string
	FestBinary      string
	TokenIn         string
	TokenOut        string
	Timeout         time.Duration
	PollInterval    time.Duration
	MaxPositionSize float64
	Prompt          string
	Logger          *slog.Logger
}

// Runtime shells out to fest, binds an obey session to the created run, and
// converts decision.json into the trading signal consumed by the runner.
type Runtime struct {
	cfg     Config
	session SessionRunner
	now     func() time.Time
}

type ritualRunOutput struct {
	Action     string `json:"action"`
	DestPath   string `json:"dest_path"`
	HexCounter string `json:"hex_counter"`
	Ritual     string `json:"ritual"`
	RunDir     string `json:"run_dir"`
	RunNumber  int    `json:"run_number"`
	Success    bool   `json:"success"`
	SourceID   string `json:"source_id"`
	SourceName string `json:"source_name"`
}

type showOutput struct {
	Festival struct {
		Stats struct {
			Tasks struct {
				Pending int `json:"pending"`
			} `json:"tasks"`
			Progress float64 `json:"progress"`
		} `json:"stats"`
	} `json:"festival"`
}

type runInfo struct {
	ID         string
	Path       string
	TemplateID string
}

type artifactFiles struct {
	DecisionPath  string
	AgentLogPath  string
	DecisionFile  string
	AgentLogFile  string
	RunResultPath string
}

// Decision mirrors the canonical ritual decision artifact.
type Decision struct {
	RitualID         string      `json:"ritual_id"`
	RitualRunID      string      `json:"ritual_run_id"`
	Timestamp        string      `json:"timestamp"`
	Decision         string      `json:"decision"`
	Confidence       float64     `json:"confidence"`
	BlockingFactors  []string    `json:"blocking_factors"`
	Rationale        Rationale   `json:"rationale"`
	Recommendation   *DecisionRe `json:"recommendation"`
	VaultConstraints Constraints `json:"vault_constraints_checked"`
	Guardrails       Guardrails  `json:"guardrails"`
	ArtifactPaths    Paths       `json:"artifact_paths"`
}

// Rationale is the auditable numeric explanation inside decision.json.
type Rationale struct {
	PriceDeviationPct     float64  `json:"price_deviation_pct"`
	DeviationDirection    string   `json:"deviation_direction"`
	MAPeriod              int      `json:"ma_period"`
	CREGatesPassed        int      `json:"cre_gates_passed"`
	CREGatesTotal         int      `json:"cre_gates_total"`
	FailedGates           []string `json:"failed_gates"`
	EstimatedNetProfitUSD float64  `json:"estimated_net_profit_usd"`
	RiskScore             float64  `json:"risk_score"`
	Summary               string   `json:"summary"`
}

// DecisionRe is the optional trade recommendation for GO decisions.
type DecisionRe struct {
	Direction        string  `json:"direction"`
	TokenIn          string  `json:"token_in"`
	TokenOut         string  `json:"token_out"`
	SuggestedSizeUSD float64 `json:"suggested_size_usd"`
	MaxSlippageBps   int     `json:"max_slippage_bps"`
	Urgency          string  `json:"urgency"`
}

// Constraints captures the vault checks recorded by the ritual.
type Constraints struct {
	WithinMaxSwap     bool `json:"within_max_swap"`
	WithinDailyVolume bool `json:"within_daily_volume"`
	TokenWhitelisted  bool `json:"token_whitelisted"`
}

// Guardrails captures the minimum requirements emitted by the ritual.
type Guardrails struct {
	TradeAllowed          bool    `json:"trade_allowed"`
	MinConfidenceRequired float64 `json:"min_confidence_required"`
	MinNetProfitUSD       float64 `json:"min_net_profit_usd"`
	MinCREGatesPassed     int     `json:"min_cre_gates_passed"`
	MaxSlippageBps        int     `json:"max_slippage_bps"`
}

// Paths records the canonical output locations referenced by the ritual.
type Paths struct {
	MarketSnapshot     string `json:"market_snapshot"`
	ResearchOutput     string `json:"research_output"`
	AggregatedFindings string `json:"aggregated_findings"`
	Decision           string `json:"decision"`
	AgentLogEntry      string `json:"agent_log_entry"`
}

// New returns a fest-backed trading strategy.
func New(cfg Config, session SessionRunner) (*Runtime, error) {
	if session == nil {
		return nil, errors.New("festruntime: session runner is required")
	}
	root, err := resolveCampaignRoot(cfg.CampaignRoot)
	if err != nil {
		return nil, err
	}
	if cfg.RitualID == "" {
		return nil, errors.New("festruntime: ritual id is required")
	}
	if cfg.FestBinary == "" {
		cfg.FestBinary = "fest"
	}
	if cfg.Timeout == 0 {
		cfg.Timeout = 2 * time.Minute
	}
	if cfg.PollInterval == 0 {
		cfg.PollInterval = time.Second
	}
	if cfg.Prompt == "" {
		cfg.Prompt = defaultPrompt
	}
	cfg.CampaignRoot = root
	return &Runtime{cfg: cfg, session: session, now: time.Now}, nil
}

// Name returns the strategy identifier used in logs.
func (r *Runtime) Name() string {
	return "fest_ritual_runtime"
}

// CampaignRoot returns the resolved campaign root used for fest invocations.
func (r *Runtime) CampaignRoot() string {
	return r.cfg.CampaignRoot
}

// MaxPosition returns the configured max position size in token units when the
// ritual does not provide a tighter recommendation.
func (r *Runtime) MaxPosition() float64 {
	return r.cfg.MaxPositionSize
}

// Evaluate runs a real ritual cycle and converts decision.json into a signal.
func (r *Runtime) Evaluate(ctx context.Context, market trading.MarketState) (*trading.Signal, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("festruntime: context cancelled: %w", err)
	}
	if err := r.session.Preflight(ctx); err != nil {
		return nil, fmt.Errorf("festruntime: obey preflight: %w", err)
	}

	run, err := r.startRun(ctx)
	if err != nil {
		return nil, err
	}
	if r.cfg.Logger != nil {
		r.cfg.Logger.Info("ritual run created",
			"ritual_template_id", run.TemplateID,
			"ritual_run_id", run.ID,
			"run_path", run.Path,
		)
	}

	sessionMeta, _, err := r.session.RunPrompt(ctx, SessionRequest{
		Festival: run.ID,
		Workdir:  run.Path,
	}, r.cfg.Prompt)
	if err != nil {
		return nil, fmt.Errorf("festruntime: execute ritual run %s: %w", run.ID, err)
	}

	status, artifacts, err := r.waitForArtifacts(ctx, run)
	if err != nil {
		return nil, err
	}

	decision, err := r.loadDecision(artifacts.DecisionFile)
	if err != nil {
		return nil, err
	}

	signal, err := r.signalFromDecision(decision, market, sessionMeta, artifacts)
	if err != nil {
		return nil, err
	}

	if r.cfg.Logger != nil {
		r.cfg.Logger.Info("ritual runtime completed",
			"campaign", sessionMeta.Campaign,
			"ritual_id", decision.RitualID,
			"ritual_run_id", decision.RitualRunID,
			"session_id", sessionMeta.SessionID,
			"provider", sessionMeta.Provider,
			"model", sessionMeta.Model,
			"workdir", sessionMeta.Workdir,
			"decision", decision.Decision,
			"progress", status.Festival.Stats.Progress,
		)
	}

	return signal, nil
}

func (r *Runtime) startRun(ctx context.Context) (runInfo, error) {
	out, err := r.execFest(ctx, "ritual", "run", r.cfg.RitualID, "--json")
	if err != nil {
		return runInfo{}, fmt.Errorf("festruntime: create ritual run: %w", err)
	}
	return parseRunOutput(out, r.cfg.CampaignRoot)
}

func (r *Runtime) waitForArtifacts(ctx context.Context, run runInfo) (showOutput, artifactFiles, error) {
	ritualCtx, cancel := context.WithTimeout(ctx, r.cfg.Timeout)
	defer cancel()

	artifacts := r.resolveArtifacts(run)
	ticker := time.NewTicker(r.cfg.PollInterval)
	defer ticker.Stop()
	var lastStatusErr error

	for {
		status, statusErr := r.showRoadmap(ritualCtx, run.ID)
		if statusErr != nil {
			lastStatusErr = statusErr
		}
		decisionOK := fileExists(artifacts.DecisionFile)
		logOK := fileExists(artifacts.AgentLogFile)
		if statusErr == nil && status.Festival.Stats.Tasks.Pending == 0 && decisionOK && logOK {
			return status, artifacts, nil
		}
		if statusErr == nil && status.Festival.Stats.Tasks.Pending == 0 && (!decisionOK || !logOK) {
			return showOutput{}, artifactFiles{}, fmt.Errorf(
				"festruntime: ritual %s completed without required artifacts (decision=%t agent_log=%t)",
				run.ID, decisionOK, logOK,
			)
		}

		select {
		case <-ritualCtx.Done():
			progress := 0.0
			pending := -1
			if statusErr == nil {
				progress = status.Festival.Stats.Progress
				pending = status.Festival.Stats.Tasks.Pending
			}
			if lastStatusErr != nil {
				return showOutput{}, artifactFiles{}, fmt.Errorf(
					"festruntime: ritual %s timed out waiting for artifacts (pending=%d progress=%.1f%% last_status_error=%v): %w",
					run.ID, pending, progress, lastStatusErr, ritualCtx.Err(),
				)
			}
			return showOutput{}, artifactFiles{}, fmt.Errorf(
				"festruntime: ritual %s timed out waiting for artifacts (pending=%d progress=%.1f%%): %w",
				run.ID, pending, progress, ritualCtx.Err(),
			)
		case <-ticker.C:
		}
	}
}

func (r *Runtime) showRoadmap(ctx context.Context, festivalID string) (showOutput, error) {
	out, err := r.execFest(ctx, "show", "--festival", festivalID, "--json", "--roadmap")
	if err != nil {
		return showOutput{}, fmt.Errorf("festruntime: show roadmap: %w", err)
	}
	return parseShowRoadmap(out)
}

func (r *Runtime) loadDecision(path string) (Decision, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Decision{}, fmt.Errorf("festruntime: read decision artifact: %w", err)
	}
	var decision Decision
	if err := json.Unmarshal(data, &decision); err != nil {
		return Decision{}, fmt.Errorf("festruntime: parse decision artifact: %w", err)
	}
	if decision.Decision != "GO" && decision.Decision != "NO_GO" {
		return Decision{}, fmt.Errorf("festruntime: invalid decision value %q", decision.Decision)
	}
	if decision.Confidence < 0 || decision.Confidence > 1 {
		return Decision{}, fmt.Errorf("festruntime: invalid confidence %.4f", decision.Confidence)
	}
	if decision.Decision == "NO_GO" && len(decision.BlockingFactors) == 0 {
		return Decision{}, errors.New("festruntime: NO_GO decision missing blocking_factors")
	}
	return decision, nil
}

func (r *Runtime) signalFromDecision(decision Decision, market trading.MarketState, sessionMeta SessionMeta, artifacts artifactFiles) (*trading.Signal, error) {
	signal := &trading.Signal{
		GeneratedAt: r.now(),
		Confidence:  decision.Confidence,
		Reason:      decision.Rationale.Summary,
		TokenIn:     r.cfg.TokenIn,
		TokenOut:    r.cfg.TokenOut,
		Ritual: &trading.RitualMetadata{
			Campaign:        sessionMeta.Campaign,
			RitualID:        decision.RitualID,
			RitualRunID:     decision.RitualRunID,
			Workdir:         sessionMeta.Workdir,
			SessionID:       sessionMeta.SessionID,
			Provider:        sessionMeta.Provider,
			Model:           sessionMeta.Model,
			Summary:         decision.Rationale.Summary,
			DecisionPath:    decision.ArtifactPaths.Decision,
			AgentLogPath:    decision.ArtifactPaths.AgentLogEntry,
			BlockingFactors: append([]string(nil), decision.BlockingFactors...),
			Guardrails: trading.RitualGuardrails{
				TradeAllowed:          decision.Guardrails.TradeAllowed,
				MinConfidenceRequired: decision.Guardrails.MinConfidenceRequired,
				MinNetProfitUSD:       decision.Guardrails.MinNetProfitUSD,
				MinCREGatesPassed:     decision.Guardrails.MinCREGatesPassed,
				MaxSlippageBps:        decision.Guardrails.MaxSlippageBps,
			},
		},
	}

	if decision.Decision == "NO_GO" {
		signal.Type = trading.SignalHold
		return signal, nil
	}
	if decision.Recommendation == nil {
		return nil, errors.New("festruntime: GO decision missing recommendation")
	}
	if market.Price <= 0 {
		return nil, fmt.Errorf("festruntime: invalid market price %.4f", market.Price)
	}

	size := decision.Recommendation.SuggestedSizeUSD / market.Price
	if r.cfg.MaxPositionSize > 0 && size > r.cfg.MaxPositionSize {
		size = r.cfg.MaxPositionSize
	}

	switch strings.ToUpper(decision.Recommendation.Direction) {
	case "BUY":
		signal.Type = trading.SignalBuy
		signal.SuggestedSize = size
		signal.TokenIn = r.cfg.TokenIn
		signal.TokenOut = r.cfg.TokenOut
	case "SELL":
		signal.Type = trading.SignalSell
		signal.SuggestedSize = size
		signal.TokenIn = r.cfg.TokenOut
		signal.TokenOut = r.cfg.TokenIn
	default:
		return nil, fmt.Errorf("festruntime: unsupported recommendation direction %q", decision.Recommendation.Direction)
	}

	if signal.Ritual != nil && signal.Ritual.DecisionPath == "" {
		signal.Ritual.DecisionPath = artifacts.DecisionPath
	}
	if signal.Ritual != nil && signal.Ritual.AgentLogPath == "" {
		signal.Ritual.AgentLogPath = artifacts.AgentLogPath
	}

	return signal, nil
}

func (r *Runtime) resolveArtifacts(run runInfo) artifactFiles {
	const resultsDir = "003_DECIDE/01_synthesize_decision/results"
	return artifactFiles{
		DecisionPath:  filepath.ToSlash(filepath.Join(resultsDir, "decision.json")),
		AgentLogPath:  filepath.ToSlash(filepath.Join(resultsDir, "agent_log_entry.json")),
		DecisionFile:  filepath.Join(run.Path, resultsDir, "decision.json"),
		AgentLogFile:  filepath.Join(run.Path, resultsDir, "agent_log_entry.json"),
		RunResultPath: filepath.Join(run.Path, resultsDir),
	}
}

func (r *Runtime) execFest(ctx context.Context, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, r.cfg.FestBinary, args...)
	cmd.Dir = r.cfg.CampaignRoot
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("%s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return stdout.Bytes(), nil
}

func resolveCampaignRoot(configured string) (string, error) {
	if configured != "" {
		return configured, nil
	}
	wd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("festruntime: getwd: %w", err)
	}
	for dir := wd; dir != filepath.Dir(dir); dir = filepath.Dir(dir) {
		if fileExists(filepath.Join(dir, ".campaign")) {
			return dir, nil
		}
	}
	return "", errors.New("festruntime: could not locate campaign root")
}

// parseRunOutput captures the machine-readable contract from:
//
//	fest ritual run <ritual-id> --json
//
// The current CLI returns dest_path/run_dir/run_number plus ritual/success
// metadata, while older builds exposed source_id/source_name. The bridge
// accepts either shape and fails closed when the run path or run id is absent.
func parseRunOutput(out []byte, campaignRoot string) (runInfo, error) {
	var parsed ritualRunOutput
	if err := json.Unmarshal(out, &parsed); err != nil {
		return runInfo{}, fmt.Errorf("festruntime: parse ritual run json: %w", err)
	}
	if parsed.Success == false && parsed.Action == "ritual_run" {
		return runInfo{}, errors.New("festruntime: ritual run reported success=false")
	}
	if parsed.DestPath == "" {
		return runInfo{}, errors.New("festruntime: ritual run json missing dest_path")
	}
	runPath := parsed.DestPath
	if !filepath.IsAbs(runPath) {
		runPath = filepath.Join(campaignRoot, runPath)
	}
	runID := parsed.RunDir
	if runID == "" {
		runID = filepath.Base(runPath)
	}
	if runID == "" || runID == "." {
		return runInfo{}, errors.New("festruntime: ritual run json missing run_dir")
	}
	templateID := parsed.SourceID
	if templateID == "" {
		templateID = parsed.Ritual
	}
	return runInfo{ID: runID, Path: runPath, TemplateID: templateID}, nil
}

// parseShowRoadmap captures the machine-readable contract from:
//
//	fest show --festival <run-id> --json --roadmap
//
// The runtime needs only festival.stats.tasks.pending and progress, but it
// intentionally parses the real JSON payload instead of scraping text output.
func parseShowRoadmap(out []byte) (showOutput, error) {
	var parsed showOutput
	if err := json.Unmarshal(out, &parsed); err != nil {
		return showOutput{}, fmt.Errorf("festruntime: parse show json: %w", err)
	}
	return parsed, nil
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
