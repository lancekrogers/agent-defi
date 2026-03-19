package strategy

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"

	"github.com/lancekrogers/agent-defi/internal/festruntime"
)

// ObeyClient implements LLMClient using the obey daemon session system.
// It creates a persistent session and sends prompts through the daemon
// rather than calling AI provider APIs directly.
type ObeyClient struct {
	Socket    string // daemon gRPC socket path (default: /tmp/obey.sock)
	Campaign  string // campaign name
	Provider  string // AI provider (e.g., "claude-code")
	Model     string // model name (e.g., "claude-sonnet-4-6")
	Agent     string // obey agent name
	Festival  string // festival ID (optional)
	Workdir   string // working directory for session execution (optional)
	Config    string // provider session config JSON (optional)
	SessionID string // reused across calls once created
}

// Complete sends a prompt to an obey session and returns the response.
func (c *ObeyClient) Complete(ctx context.Context, prompt string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", fmt.Errorf("obey: context cancelled: %w", err)
	}

	// Create session on first call
	if c.SessionID == "" {
		meta, err := c.CreateSession(ctx, festruntime.SessionRequest{
			Festival: c.Festival,
			Workdir:  c.Workdir,
		})
		if err != nil {
			return "", fmt.Errorf("obey: create session: %w", err)
		}
		c.SessionID = meta.SessionID
	}

	return c.sendMessage(ctx, prompt)
}

// Preflight fails closed when the obey binary or daemon path is unavailable.
func (c *ObeyClient) Preflight(ctx context.Context) error {
	if _, err := exec.LookPath("obey"); err != nil {
		return fmt.Errorf("obey binary not found: %w", err)
	}
	cmd := exec.CommandContext(ctx, "obey", "ping", "--socket", c.socket())
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("obey ping failed: %w: %s", err, stderr.String())
	}
	return nil
}

// CreateSession creates one obey session with the given ritual context.
func (c *ObeyClient) CreateSession(ctx context.Context, req festruntime.SessionRequest) (festruntime.SessionMeta, error) {
	args := []string{
		"session", "create",
		"--socket", c.socket(),
		"--campaign", c.Campaign,
		"--provider", c.provider(),
		"--model", c.model(),
		"--agent", c.agent(),
	}
	festival := c.Festival
	if req.Festival != "" {
		festival = req.Festival
	}
	if festival != "" {
		args = append(args, "--festival", festival)
	}
	workdir := c.Workdir
	if req.Workdir != "" {
		workdir = req.Workdir
	}
	if workdir != "" {
		args = append(args, "--workdir", workdir)
	}
	if config := c.configJSON(); config != "" {
		args = append(args, "--config", config)
	}

	cmd := exec.CommandContext(ctx, "obey", args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return festruntime.SessionMeta{}, fmt.Errorf("obey session create failed: %w: %s", err, stderr.String())
	}

	// Parse session ID from output like "Session: <uuid>"
	for _, line := range strings.Split(stdout.String(), "\n") {
		if strings.HasPrefix(line, "Session: ") {
			return festruntime.SessionMeta{
				SessionID: strings.TrimPrefix(line, "Session: "),
				Campaign:  c.Campaign,
				Provider:  c.provider(),
				Model:     c.model(),
				Festival:  festival,
				Workdir:   workdir,
			}, nil
		}
	}

	return festruntime.SessionMeta{}, fmt.Errorf("obey: could not parse session ID from: %s", stdout.String())
}

// RunPrompt creates a fresh session for a ritual run and sends the execution prompt.
func (c *ObeyClient) RunPrompt(ctx context.Context, req festruntime.SessionRequest, prompt string) (festruntime.SessionMeta, string, error) {
	if err := c.Preflight(ctx); err != nil {
		return festruntime.SessionMeta{}, "", err
	}
	meta, err := c.CreateSession(ctx, req)
	if err != nil {
		return festruntime.SessionMeta{}, "", err
	}
	resp, err := c.sendMessageWithSession(ctx, meta.SessionID, prompt)
	if err != nil {
		return festruntime.SessionMeta{}, "", err
	}
	return meta, resp, nil
}

func (c *ObeyClient) sendMessage(ctx context.Context, message string) (string, error) {
	return c.sendMessageWithSession(ctx, c.SessionID, message)
}

func (c *ObeyClient) sendMessageWithSession(ctx context.Context, sessionID, message string) (string, error) {
	cmd := exec.CommandContext(ctx, "obey",
		"session", "send",
		"--socket", c.socket(),
		"--campaign", c.Campaign,
		"--mode", "autonomous",
		sessionID,
		message,
	)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("obey session send failed: %w: %s", err, stderr.String())
	}

	return strings.TrimSpace(stdout.String()), nil
}

func (c *ObeyClient) socket() string {
	if c.Socket != "" {
		return c.Socket
	}
	return "/tmp/obey.sock"
}

func (c *ObeyClient) provider() string {
	if c.Provider != "" {
		return c.Provider
	}
	return "claude-code"
}

func (c *ObeyClient) model() string {
	if c.Model != "" {
		return c.Model
	}
	return "claude-sonnet-4-6"
}

func (c *ObeyClient) agent() string {
	if c.Agent != "" {
		return c.Agent
	}
	return "vault-trader"
}

func (c *ObeyClient) configJSON() string {
	if c.Config != "" {
		return c.Config
	}
	if c.provider() == "claude-code" {
		return `{"permission_mode":"bypassPermissions"}`
	}
	return ""
}
