package cli

import (
	"context"
	"encoding/json"

	"github.com/romanvolkov/flows/internal/catalog"
)

// DoctorTools runs readiness checks across all specs using the same checker
// logic exposed over the daemon API.
func DoctorTools(ctx context.Context, checker *catalog.ReadinessChecker, specs []catalog.ToolCheckSpec) []catalog.ReadinessReport {
	var all []catalog.ReadinessReport
	for _, spec := range specs {
		all = append(all, checker.Check(ctx, spec)...)
	}
	if all == nil {
		all = []catalog.ReadinessReport{}
	}
	return all
}

// FormatDoctorJSON formats readiness reports as indented JSON.
func FormatDoctorJSON(reports []catalog.ReadinessReport) ([]byte, error) {
	return json.MarshalIndent(reports, "", "  ")
}
