package loggen

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math/big"
	"os"
	"path/filepath"
	"time"

	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	"github.com/lancekrogers/agent-defi/internal/base/ethutil"
	"github.com/lancekrogers/agent-defi/internal/vault"
)

// AgentLog is the top-level Protocol Labs DevSpot artifact.
type AgentLog struct {
	AgentName     string     `json:"agent_name"`
	AgentIdentity string     `json:"agent_identity"`
	LogVersion    string     `json:"log_version"`
	Entries       []LogEntry `json:"entries"`
}

// LogEntry is one discover/execute/verify event in the aggregate log.
type LogEntry struct {
	Timestamp    string           `json:"timestamp"`
	Phase        string           `json:"phase"`
	Action       string           `json:"action"`
	FestivalID   string           `json:"festival_id,omitempty"`
	ToolsUsed    []string         `json:"tools_used"`
	Decision     string           `json:"decision"`
	Reasoning    map[string]any   `json:"reasoning"`
	Execution    *ExecutionDetail `json:"execution,omitempty"`
	Verification *VerifyDetail    `json:"verification,omitempty"`
	Retries      int              `json:"retries"`
	Errors       []string         `json:"errors"`
	DurationMS   int64            `json:"duration_ms"`
}

// ExecutionDetail describes one on-chain execution.
type ExecutionDetail struct {
	TxHash     string `json:"tx_hash"`
	Chain      string `json:"chain"`
	ChainID    int64  `json:"chain_id"`
	TokenIn    string `json:"token_in"`
	TokenOut   string `json:"token_out"`
	AmountIn   string `json:"amount_in"`
	AmountOut  string `json:"amount_out"`
	GasUsed    uint64 `json:"gas_used,omitempty"`
	GasCostUSD string `json:"gas_cost_usd,omitempty"`
}

// VerifyDetail describes one verification step for a log entry.
type VerifyDetail struct {
	ExpectedOutput  string `json:"expected_output,omitempty"`
	ActualOutput    string `json:"actual_output,omitempty"`
	SlippageBPS     int    `json:"slippage_bps,omitempty"`
	WithinTolerance bool   `json:"within_tolerance"`
}

// RitualLogEntry is the single JSON object written by each ritual run.
type RitualLogEntry struct {
	Action       string         `json:"action"`
	Timestamp    string         `json:"timestamp"`
	Phase        string         `json:"phase"`
	FestivalID   string         `json:"festival_id"`
	RunID        string         `json:"run_id"`
	Decision     string         `json:"decision"`
	Reasoning    map[string]any `json:"reasoning"`
	ToolsUsed    []string       `json:"tools_used"`
	Retries      int            `json:"retries"`
	DurationMS   int64          `json:"duration_ms"`
	Errors       []string       `json:"errors"`
	ArtifactPaths map[string]any `json:"artifact_paths,omitempty"`
}

// Config controls aggregate log generation.
type Config struct {
	RPCURL       string
	VaultAddress string
	RitualsDir   string
	AgentName    string
	AgentID      string
	FromBlockNum uint64
}

// Refresher rebuilds agent_log.json from ritual outputs and on-chain events.
type Refresher struct {
	Config Config
	OutFile string
}

// Refresh rebuilds and writes the aggregate log.
func (r Refresher) Refresh(ctx context.Context) (int, error) {
	agentLog, err := Generate(ctx, r.Config)
	if err != nil {
		return 0, err
	}
	if err := Write(r.OutFile, agentLog); err != nil {
		return 0, err
	}
	return len(agentLog.Entries), nil
}

// Generate builds the aggregate agent log in memory.
func Generate(ctx context.Context, cfg Config) (AgentLog, error) {
	agentLog := AgentLog{
		AgentName:     cfg.AgentName,
		AgentIdentity: cfg.AgentID,
		LogVersion:    "1.0",
		Entries:       []LogEntry{},
	}

	if cfg.RitualsDir != "" {
		entries, err := LoadRitualEntries(cfg.RitualsDir)
		if err != nil {
			return AgentLog{}, err
		}
		for _, re := range entries {
			agentLog.Entries = append(agentLog.Entries, LogEntry{
				Timestamp:  re.Timestamp,
				Phase:      valueOr(re.Phase, "discover"),
				Action:     valueOr(re.Action, "market_research_ritual"),
				FestivalID: re.FestivalID,
				ToolsUsed:  append([]string(nil), re.ToolsUsed...),
				Decision:   re.Decision,
				Reasoning:  re.Reasoning,
				Retries:    re.Retries,
				Errors:     append([]string(nil), re.Errors...),
				DurationMS: re.DurationMS,
			})
		}
	}

	if cfg.RPCURL != "" && cfg.VaultAddress != "" {
		swapEntries, err := LoadSwapEvents(ctx, cfg.RPCURL, cfg.VaultAddress, cfg.FromBlockNum)
		if err != nil {
			return AgentLog{}, err
		}
		agentLog.Entries = append(agentLog.Entries, swapEntries...)
	}

	return agentLog, nil
}

// Write writes the aggregate log to disk.
func Write(outFile string, agentLog AgentLog) error {
	out, err := json.MarshalIndent(agentLog, "", "  ")
	if err != nil {
		return fmt.Errorf("loggen: marshal agent_log: %w", err)
	}
	if err := os.WriteFile(outFile, out, 0o644); err != nil {
		return fmt.Errorf("loggen: write %s: %w", outFile, err)
	}
	return nil
}

// LoadRitualEntries walks a directory tree looking for agent_log_entry.json files.
func LoadRitualEntries(root string) ([]RitualLogEntry, error) {
	var entries []RitualLogEntry

	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil // skip unreadable dirs
		}
		if d.Name() != "agent_log_entry.json" {
			return nil
		}

		data, err := os.ReadFile(path)
		if err != nil {
			log.Printf("warning: read %s: %v", path, err)
			return nil
		}

		var entry RitualLogEntry
		if err := json.Unmarshal(data, &entry); err != nil {
			log.Printf("warning: parse %s: %v", path, err)
			return nil
		}

		entries = append(entries, entry)
		return nil
	})

	return entries, err
}

// LoadSwapEvents queries the vault contract for SwapExecuted events and converts them to log entries.
func LoadSwapEvents(ctx context.Context, rpcURL, vaultAddrHex string, fromBlock uint64) ([]LogEntry, error) {
	if !common.IsHexAddress(vaultAddrHex) {
		return nil, errors.New("loggen: invalid vault address: " + vaultAddrHex)
	}

	ethClient, err := ethutil.DialClient(ctx, rpcURL)
	if err != nil {
		return nil, errors.New("loggen: dial rpc: " + err.Error())
	}
	defer ethClient.Close()

	chainID, err := ethClient.ChainID(ctx)
	if err != nil {
		return nil, errors.New("loggen: query chain id: " + err.Error())
	}

	chainName := chainNameFromID(chainID.Int64())

	vaultAddress := common.HexToAddress(vaultAddrHex)
	filterer, err := vault.NewObeyVaultFilterer(vaultAddress, ethClient)
	if err != nil {
		return nil, errors.New("loggen: bind filterer: " + err.Error())
	}

	latestBlock, err := ethClient.BlockNumber(ctx)
	if err != nil {
		return nil, errors.New("loggen: query latest block: " + err.Error())
	}

	const maxLookback = uint64(500_000)
	if fromBlock == 0 && latestBlock > maxLookback {
		fromBlock = latestBlock - maxLookback
	}

	const chunkSize = uint64(10_000)
	var entries []LogEntry

	for start := fromBlock; start <= latestBlock; start += chunkSize {
		end := start + chunkSize - 1
		if end > latestBlock {
			end = latestBlock
		}

		opts := &bind.FilterOpts{
			Start:   start,
			End:     &end,
			Context: ctx,
		}

		iter, err := filterer.FilterSwapExecuted(opts, nil, nil)
		if err != nil {
			return entries, errors.New("loggen: filter swap events: " + err.Error())
		}

		for iter.Next() {
			evt := iter.Event
			header, err := ethClient.HeaderByNumber(ctx, new(big.Int).SetUint64(evt.Raw.BlockNumber))
			if err != nil {
				log.Printf("warning: get block %d header: %v", evt.Raw.BlockNumber, err)
				iter.Close()
				continue
			}

			ts := time.Unix(int64(header.Time), 0).UTC().Format(time.RFC3339)
			entries = append(entries, LogEntry{
				Timestamp: ts,
				Phase:     "execute",
				Action:    "vault_swap",
				ToolsUsed: []string{"obey_vault_execute_swap", "uniswap_v3_pool_query"},
				Decision:  "GO",
				Reasoning: map[string]any{
					"reason_bytes": fmt.Sprintf("0x%x", evt.Reason),
				},
				Execution: &ExecutionDetail{
					TxHash:    evt.Raw.TxHash.Hex(),
					Chain:     chainName,
					ChainID:   chainID.Int64(),
					TokenIn:   tokenName(evt.TokenIn),
					TokenOut:  tokenName(evt.TokenOut),
					AmountIn:  evt.AmountIn.String(),
					AmountOut: evt.AmountOut.String(),
				},
				Verification: &VerifyDetail{
					ActualOutput:    evt.AmountOut.String(),
					WithinTolerance: true,
				},
				Retries: 0,
				Errors:  []string{},
			})
		}

		if iterErr := iter.Error(); iterErr != nil {
			iter.Close()
			return entries, errors.New("loggen: event iteration failed: " + iterErr.Error())
		}
		iter.Close()
	}

	return entries, nil
}

// Well-known token addresses on Base and Base Sepolia.
var tokenSymbols = map[common.Address]string{
	common.HexToAddress("0x833589fCD6eDb6E08f4c7C32D4f71b54bdA02913"): "USDC",
	common.HexToAddress("0x036CbD53842c5426634e7929541eC2318f3dCF7e"): "USDC",
	common.HexToAddress("0x4200000000000000000000000000000000000006"): "WETH",
}

func tokenName(addr common.Address) string {
	if sym, ok := tokenSymbols[addr]; ok {
		return sym
	}
	return addr.Hex()[:10]
}

func chainNameFromID(id int64) string {
	switch id {
	case 8453:
		return "Base"
	case 84532:
		return "Base Sepolia"
	default:
		return fmt.Sprintf("Chain %d", id)
	}
}

func valueOr(value, fallback string) string {
	if value != "" {
		return value
	}
	return fallback
}
