package inspector

import (
	"github.com/romanvolkov/flows/internal/usage"
)

// UsageInspector displays node, group, and session token breakdowns and context fill.
type UsageInspector struct {
	Scope              string            `json:"scope"` // "node" | "group" | "session"
	Summary            usage.Summary     `json:"summary"`
	ContextFillPercent float64           `json:"context_fill_percent"`
	State              string            `json:"state"` // "zero" | "partial" | "unavailable" | "live" | "final"
}

// BuildUsageInspector computes the inspector usage breakdown and context gauge.
func BuildUsageInspector(scope string, sum usage.Summary) UsageInspector {
	state := sum.Coverage
	if sum.Requests == 0 {
		state = "zero"
	} else if sum.UnavailableCount > 0 && sum.KnownSubtotal > 0 {
		state = "partial"
	}
	var fill float64
	if sum.LatestContextWindow > 0 {
		fill = float64(sum.LatestContextTokens) / float64(sum.LatestContextWindow) * 100.0
	}
	return UsageInspector{
		Scope:              scope,
		Summary:            sum,
		ContextFillPercent: fill,
		State:              state,
	}
}
