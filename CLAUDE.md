# agent-defi

DeFi agent — executes trading strategies on Base via go-ethereum, registers identity via ERC-8004, pays for compute via x402, attributes transactions via ERC-8021. Reports P&L to coordinator via HCS.

## Build

```bash
just build   # Build binary to bin/
just run     # Run the agent
just test    # Run tests
```

## Structure

- `cmd/` — Entry point
- `internal/` — Private packages
- `justfile` — Build recipes

## Development

- Follow Go conventions from root CLAUDE.md
- Always pass context.Context as first parameter for I/O
- Use the project's error framework, not fmt.Errorf
- Keep files under 500 lines, functions under 50 lines
