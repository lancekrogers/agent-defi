package guard

import (
	"context"
	"fmt"
	"log/slog"
)

// CREGuard enforces position constraints from CRE Risk Router decisions.
type CREGuard struct {
	logger *slog.Logger
}

// NewCREGuard creates a new CRE constraint enforcement guard.
func NewCREGuard(logger *slog.Logger) *CREGuard {
	return &CREGuard{logger: logger}
}

// EnforceConstraint checks that the proposed position does not exceed the CRE-approved limit.
// Returns the effective position (clamped if necessary) and any error.
func (g *CREGuard) EnforceConstraint(ctx context.Context, taskID string, requestedUSD, constrainedUSD float64) (float64, error) {
	if err := ctx.Err(); err != nil {
		return 0, fmt.Errorf("CRE guard: context cancelled: %w", err)
	}

	if constrainedUSD <= 0 {
		g.logger.Info("no CRE constraint, using requested position",
			"task_id", taskID,
			"requested_usd", requestedUSD,
		)
		return requestedUSD, nil
	}

	if requestedUSD > constrainedUSD {
		g.logger.Warn("CRE constraint exceeded, clamping position",
			"task_id", taskID,
			"requested_usd", requestedUSD,
			"constrained_usd", constrainedUSD,
		)
		return constrainedUSD, nil
	}

	g.logger.Info("CRE constraint respected",
		"task_id", taskID,
		"position_usd", requestedUSD,
		"constrained_usd", constrainedUSD,
	)
	return requestedUSD, nil
}
