// Command loggen generates agent_log.json in Protocol Labs DevSpot format
// by querying on-chain SwapExecuted events and reading festival ritual artifacts.
//
// Usage:
//
//	BASE_RPC_URL=https://... VAULT_ADDRESS=0x... go run ./cmd/loggen
//	go run ./cmd/loggen -rituals festivals/ -out agent_log.json
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/lancekrogers/agent-defi/internal/loggen"
)

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

	flag.StringVar(&ritualsDir, "rituals", "", "directory containing ritual runs (scans recursively for agent_log_entry.json)")
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

	agentLog, err := loggen.Generate(ctx, loggen.Config{
		RPCURL:       rpcURL,
		VaultAddress: vaultAddr,
		RitualsDir:   ritualsDir,
		AgentName:    agentName,
		AgentID:      agentID,
		FromBlockNum: fromBlockNum,
	})
	if err != nil {
		log.Fatalf("generate agent_log: %v", err)
	}
	if err := loggen.Write(outFile, agentLog); err != nil {
		log.Fatalf("write agent_log: %v", err)
	}

	fmt.Printf("wrote %s (%d entries)\n", outFile, len(agentLog.Entries))
}
