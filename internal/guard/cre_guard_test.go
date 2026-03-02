package guard

import (
	"context"
	"log/slog"
	"testing"
)

func TestEnforceConstraint_NoConstraint(t *testing.T) {
	g := NewCREGuard(slog.Default())
	pos, err := g.EnforceConstraint(context.Background(), "task-1", 1000, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if pos != 1000 {
		t.Errorf("expected 1000, got %f", pos)
	}
}

func TestEnforceConstraint_WithinLimit(t *testing.T) {
	g := NewCREGuard(slog.Default())
	pos, err := g.EnforceConstraint(context.Background(), "task-1", 500, 810)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if pos != 500 {
		t.Errorf("expected 500, got %f", pos)
	}
}

func TestEnforceConstraint_ExceedsLimit(t *testing.T) {
	g := NewCREGuard(slog.Default())
	pos, err := g.EnforceConstraint(context.Background(), "task-1", 1000, 810)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if pos != 810 {
		t.Errorf("expected 810 (clamped), got %f", pos)
	}
}

func TestEnforceConstraint_ContextCancelled(t *testing.T) {
	g := NewCREGuard(slog.Default())
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := g.EnforceConstraint(ctx, "task-1", 1000, 810)
	if err == nil {
		t.Fatal("expected error for cancelled context")
	}
}
