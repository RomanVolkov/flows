package localapi

import (
	"context"
	"net/http"

	"github.com/romanvolkov/flows/internal/catalog"
)

// ToolReadinessHandler serves GET /v1/tools/readiness and POST /v1/tools/validate.
func ToolReadinessHandler(checker *catalog.ReadinessChecker, specs func(context.Context) []catalog.ToolCheckSpec) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/tools/readiness":
			if !method(w, r, http.MethodGet) {
				return
			}
		case "/v1/tools/validate":
			if !method(w, r, http.MethodPost) {
				return
			}
			checker.Invalidate("")
		default:
			writeError(w, http.StatusNotFound, "not_found", "unknown tools endpoint")
			return
		}
		var all []catalog.ReadinessReport
		for _, spec := range specs(r.Context()) {
			all = append(all, checker.Check(r.Context(), spec)...)
		}
		if all == nil {
			all = []catalog.ReadinessReport{}
		}
		writeJSON(w, http.StatusOK, all)
	}
}
