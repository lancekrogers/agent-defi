#!/usr/bin/env just
# agent-defi — DeFi trading agent

set dotenv-load := true

binary_name := "agent-defi"
bin_dir     := "bin"
cmd_path    := "./cmd/agent-defi"

mod test '.justfiles/test.just'

@default:
    just --list --justfile {{source_file()}}

# Build binary to bin/
build:
    go build -o {{bin_dir}}/{{binary_name}} {{cmd_path}}

# Run the agent
run *ARGS:
    go run {{cmd_path}} {{ARGS}}

# Install binary to GOPATH/bin
install:
    go install {{cmd_path}}

# Uninstall binary from GOPATH/bin
uninstall:
    rm -f $(go env GOPATH)/bin/{{binary_name}}

# Run linter
lint:
    golangci-lint run ./...

# Format code
fmt:
    gofmt -w .

# Run go vet
vet:
    go vet ./...

# Tidy module dependencies
tidy:
    go mod tidy

# Show dependency graph
deps:
    go mod graph

# Generate agent_log.json from on-chain events + ritual artifacts
loggen *ARGS:
    go run ./cmd/loggen {{ARGS}}

# Remove build artifacts
clean:
    rm -rf {{bin_dir}}/ coverage.out coverage.html
