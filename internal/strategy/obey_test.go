package strategy

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lancekrogers/agent-defi/internal/festruntime"
)

func TestObeyClientRunPromptPassesFestivalAndWorkdir(t *testing.T) {
	tmp := t.TempDir()
	argsFile := filepath.Join(tmp, "obey-args.log")
	binDir := filepath.Join(tmp, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatalf("mkdir bin: %v", err)
	}
	script := filepath.Join(binDir, "obey")
	content := strings.Join([]string{
		"#!/bin/sh",
		"echo \"$@\" >> \"" + argsFile + "\"",
		"if [ \"$1\" = \"ping\" ]; then",
		"  exit 0",
		"fi",
		"if [ \"$1\" = \"session\" ] && [ \"$2\" = \"create\" ]; then",
		"  echo \"Session: session-123\"",
		"  exit 0",
		"fi",
		"if [ \"$1\" = \"session\" ] && [ \"$2\" = \"send\" ]; then",
		"  echo '{\"ok\":true}'",
		"  exit 0",
		"fi",
		"exit 1",
	}, "\n")
	if err := os.WriteFile(script, []byte(content), 0o755); err != nil {
		t.Fatalf("write obey script: %v", err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	client := &ObeyClient{
		Socket:   "/tmp/obey.sock",
		Campaign: "Obey-Agent-Economy",
		Provider: "claude-code",
		Model:    "test-model",
		Agent:    "vault-trader",
	}

	meta, resp, err := client.RunPrompt(context.Background(), festruntime.SessionRequest{
		Festival: "agent-market-research-RI-AM0001-0002",
		Workdir:  "/tmp/ritual-run",
	}, "complete the ritual")
	if err != nil {
		t.Fatalf("RunPrompt() error = %v", err)
	}
	if meta.SessionID != "session-123" {
		t.Fatalf("session id = %q, want session-123", meta.SessionID)
	}
	if resp != "{\"ok\":true}" {
		t.Fatalf("response = %q", resp)
	}

	data, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatalf("read args: %v", err)
	}
	logged := string(data)
	for _, want := range []string{
		"session create",
		"--campaign Obey-Agent-Economy",
		"--festival agent-market-research-RI-AM0001-0002",
		"--workdir /tmp/ritual-run",
		"--provider claude-code",
		"--model test-model",
		"--agent vault-trader",
		`--config {"permission_mode":"bypassPermissions"}`,
		"session send --socket /tmp/obey.sock --campaign Obey-Agent-Economy --mode autonomous session-123 complete the ritual",
	} {
		if !strings.Contains(logged, want) {
			t.Fatalf("logged args missing %q:\n%s", want, logged)
		}
	}
}

func TestObeyClientPreflightFailsWhenPingFails(t *testing.T) {
	tmp := t.TempDir()
	binDir := filepath.Join(tmp, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatalf("mkdir bin: %v", err)
	}
	script := filepath.Join(binDir, "obey")
	content := strings.Join([]string{
		"#!/bin/sh",
		"if [ \"$1\" = \"ping\" ]; then",
		"  echo \"daemon unavailable\" >&2",
		"  exit 1",
		"fi",
		"exit 0",
	}, "\n")
	if err := os.WriteFile(script, []byte(content), 0o755); err != nil {
		t.Fatalf("write obey script: %v", err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	client := &ObeyClient{Socket: "/tmp/obey.sock"}
	err := client.Preflight(context.Background())
	if err == nil {
		t.Fatal("Preflight() error = nil, want ping failure")
	}
	if !strings.Contains(err.Error(), "obey ping failed") {
		t.Fatalf("error = %v, want ping failure context", err)
	}
}

func TestObeyClientRunPromptFailsWhenSessionIDMissing(t *testing.T) {
	tmp := t.TempDir()
	binDir := filepath.Join(tmp, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatalf("mkdir bin: %v", err)
	}
	script := filepath.Join(binDir, "obey")
	content := strings.Join([]string{
		"#!/bin/sh",
		"if [ \"$1\" = \"ping\" ]; then",
		"  exit 0",
		"fi",
		"if [ \"$1\" = \"session\" ] && [ \"$2\" = \"create\" ]; then",
		"  echo \"created but no session id\"",
		"  exit 0",
		"fi",
		"exit 1",
	}, "\n")
	if err := os.WriteFile(script, []byte(content), 0o755); err != nil {
		t.Fatalf("write obey script: %v", err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	client := &ObeyClient{
		Socket:   "/tmp/obey.sock",
		Campaign: "Obey-Agent-Economy",
	}

	_, _, err := client.RunPrompt(context.Background(), festruntime.SessionRequest{
		Festival: "agent-market-research-RI-AM0001-0002",
		Workdir:  "/tmp/ritual-run",
	}, "complete the ritual")
	if err == nil {
		t.Fatal("RunPrompt() error = nil, want session parse failure")
	}
	if !strings.Contains(err.Error(), "could not parse session ID") {
		t.Fatalf("error = %v, want session id parse failure", err)
	}
}

func TestObeyClientRunPromptPropagatesSessionSendFailure(t *testing.T) {
	tmp := t.TempDir()
	binDir := filepath.Join(tmp, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatalf("mkdir bin: %v", err)
	}
	script := filepath.Join(binDir, "obey")
	content := strings.Join([]string{
		"#!/bin/sh",
		"if [ \"$1\" = \"ping\" ]; then",
		"  exit 0",
		"fi",
		"if [ \"$1\" = \"session\" ] && [ \"$2\" = \"create\" ]; then",
		"  echo \"Session: session-123\"",
		"  exit 0",
		"fi",
		"if [ \"$1\" = \"session\" ] && [ \"$2\" = \"send\" ]; then",
		"  echo \"send failed\" >&2",
		"  exit 1",
		"fi",
		"exit 1",
	}, "\n")
	if err := os.WriteFile(script, []byte(content), 0o755); err != nil {
		t.Fatalf("write obey script: %v", err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	client := &ObeyClient{
		Socket:   "/tmp/obey.sock",
		Campaign: "Obey-Agent-Economy",
	}

	_, _, err := client.RunPrompt(context.Background(), festruntime.SessionRequest{
		Festival: "agent-market-research-RI-AM0001-0002",
		Workdir:  "/tmp/ritual-run",
	}, "complete the ritual")
	if err == nil {
		t.Fatal("RunPrompt() error = nil, want send failure")
	}
	if !strings.Contains(err.Error(), "obey session send failed") {
		t.Fatalf("error = %v, want send failure context", err)
	}
}
