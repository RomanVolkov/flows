package localapi

import (
	"net/http"
	"strconv"

	"github.com/romanvolkov/flows/internal/store"
	"github.com/romanvolkov/flows/internal/usage"
)

// UsageHandler serves GET /v1/usage and GET /v1/usage/daily.
func UsageHandler(st *store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !method(w, r, http.MethodGet) {
			return
		}
		q := r.URL.Query()
		f := usage.QueryFilter{
			Timezone:  q.Get("tz"),
			Harness:   q.Get("harness"),
			Model:     q.Get("model"),
			RunID:     q.Get("run_id"),
			NodeIDs:   q["node_id"],
			SessionID: q.Get("session_id"),
		}
		if v, err := strconv.ParseInt(q.Get("from"), 10, 64); err == nil {
			f.FromUnixNano = v
		}
		if v, err := strconv.ParseInt(q.Get("to"), 10, 64); err == nil {
			f.ToUnixNano = v
		}

		switch r.URL.Path {
		case "/v1/usage":
			sum, err := usage.Aggregate(r.Context(), st.DB(), f)
			if err != nil {
				writeError(w, http.StatusInternalServerError, "usage_query_failed", err.Error())
				return
			}
			writeJSON(w, http.StatusOK, sum)
		case "/v1/usage/daily":
			days, err := usage.AggregateDaily(r.Context(), st.DB(), f)
			if err != nil {
				writeError(w, http.StatusInternalServerError, "usage_daily_failed", err.Error())
				return
			}
			if days == nil {
				days = []usage.DailyBucket{}
			}
			writeJSON(w, http.StatusOK, days)
		default:
			writeError(w, http.StatusNotFound, "not_found", "unknown usage endpoint")
		}
	}
}
