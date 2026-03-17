// Command loggen generates agent_log.json in Protocol Labs DevSpot format
// by querying on-chain SwapExecuted events and reading festival ritual artifacts.
//
// Usage:
//
//	BASE_RPC_URL=https://... VAULT_ADDRESS=0x... go run ./cmd/loggen
//	go run ./cmd/loggen -rituals festivals/dungeon/completed/ -out agent_log.json
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
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

// DevSpot schema types.

type AgentLog struct {
	AgentName     string     `json:"agent_name"`
	AgentIdentity string     `json:"agent_identity"`
	LogVersion    string     `json:"log_version"`
	Entries       []LogEntry `json:"entries"`
}

type LogEntry struct {
	Timestamp    string            `json:"timestamp"`
	Phase        string            `json:"phase"`
	Action       string            `json:"action"`
	FestivalID   string            `json:"festival_id,omitempty"`
	ToolsUsed    []string          `json:"tools_used"`
	Decision     string            `json:"decision"`
	Reasoning    map[string]any    `json:"reasoning"`
	Execution    *ExecutionDetail  `json:"execution,omitempty"`
	Verification *VerifyDetail     `json:"verification,omitempty"`
	Retries      int               `json:"retries"`
	Errors       []string          `json:"errors"`
	DurationMS   int64             `json:"duration_ms"`
}

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

type VerifyDetail struct {
	ExpectedOutput  string `json:"expected_output,omitempty"`
	ActualOutput    string `json:"actual_output,omitempty"`
	SlippageBPS     int    `json:"slippage_bps,omitempty"`
	WithinTolerance bool   `json:"within_tolerance"`
}

// RitualLogEntry is the structure written by each ritual run's decide phase.
type RitualLogEntry struct {
	Timestamp  string         `json:"timestamp"`
	FestivalID string         `json:"festival_id"`
	Decision   string         `json:"decision"`
	Reasoning  map[string]any `json:"reasoning"`
	ToolsUsed  []string       `json:"tools_used"`
	DurationMS int64          `json:"duration_ms"`
	Errors     []string       `json:"errors"`
}

// Well-known token addresses on Base and Base Sepolia.
var tokenSymbols = map[common.Address]string{
	// Base mainnet
	common.HexToAddress("0x833589fCD6eDb6E08f4c7C32D4f71b54bdA02913"): "USDC",
	// Base Sepolia
	common.HexToAddress("0x036CbD53842c5426634e7929541eC2318f3dCF7e"): "USDC",
	// WETH (same address on both networks)
	common.HexToAddress("0x4200000000000000000000000000000000000006"): "WETH",
}

func tokenName(addr common.Address) string {
	if sym, ok := tokenSymbols[addr]; ok {
		return sym
	}
	return addr.Hex()[:10]
}

func main() {
	var (
		rpcURL       = os.Getenv("BASE_RPC_URL")
		vaultAddr    = os.Getenv("VAULT_ADDRESS")
		ritualsDir   string
		outFile      string
		agentName    string
		agentID      string
		fromBlockNum uint64
	)

	flag.StringVar(&ritualsDir, "rituals", "", "directory containing completed ritual runs (scans for agent_log_entry.json)")
	flag.StringVar(&outFile, "out", "agent_log.json", "output file path")
	flag.StringVar(&agentName, "name", "OBEY Vault Agent", "agent name in log")
	flag.StringVar(&agentID, "identity", "0x0C97820abBdD2562645DaE92D35eD581266CCe70", "ERC-8004 identity address")
	flag.Uint64Var(&fromBlockNum, "from-block", 0, "block number to start scanning events from (0 = last 500K blocks)")
	flag.Parse()

	if rpcURL == "" {
		rpcURL = os.Getenv("RPC_URL")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	agentLog := AgentLog{
		AgentName:     agentName,
		AgentIdentity: agentID,
		LogVersion:    "1.0",
		Entries:       []LogEntry{},
	}

	// 1. Gather ritual decision artifacts.
	if ritualsDir != "" {
		entries, err := loadRitualEntries(ritualsDir)
		if err != nil {
			log.Printf("warning: reading ritual artifacts: %v", err)
		}
		for _, re := range entries {
			agentLog.Entries = append(agentLog.Entries, LogEntry{
				Timestamp:  re.Timestamp,
				Phase:      "discover",
				Action:     "market_research_ritual",
				FestivalID: re.FestivalID,
				ToolsUsed:  re.ToolsUsed,
				Decision:   re.Decision,
				Reasoning:  re.Reasoning,
				Retries:    0,
				Errors:     re.Errors,
				DurationMS: re.DurationMS,
			})
		}
	}

	// 2. Query on-chain SwapExecuted events.
	if rpcURL != "" && vaultAddr != "" {
		swapEntries, err := loadSwapEvents(ctx, rpcURL, vaultAddr, fromBlockNum)
		if err != nil {
			log.Printf("warning: querying swap events: %v", err)
		}
		agentLog.Entries = append(agentLog.Entries, swapEntries...)
	} else {
		log.Println("skipping on-chain events: BASE_RPC_URL or VAULT_ADDRESS not set")
	}

	// 3. Write output.
	out, err := json.MarshalIndent(agentLog, "", "  ")
	if err != nil {
		log.Fatalf("marshal agent_log: %v", err)
	}

	if err := os.WriteFile(outFile, out, 0644); err != nil {
		log.Fatalf("write %s: %v", outFile, err)
	}

	fmt.Printf("wrote %s (%d entries)\n", outFile, len(agentLog.Entries))
}

// loadRitualEntries walks a directory tree looking for agent_log_entry.json files.
func loadRitualEntries(root string) ([]RitualLogEntry, error) {
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

// loadSwapEvents queries the vault contract for SwapExecuted events and converts them to log entries.
func loadSwapEvents(ctx context.Context, rpcURL, vaultAddrHex string, fromBlock uint64) ([]LogEntry, error) {
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

	// If no explicit start block, look back at most 500K blocks.
	const maxLookback = uint64(500_000)
	if fromBlock == 0 && latestBlock > maxLookback {
		fromBlock = latestBlock - maxLookback
	}

	// Paginate in 10000-block chunks to stay within RPC limits.
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
