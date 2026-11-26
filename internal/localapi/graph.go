package localapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/romanvolkov/flows/internal/engine"
)

// GraphEditsHandler serves POST /v1/runs/{id}/edits.
func GraphEditsHandler(gm *engine.GraphManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !method(w, r, http.MethodPost) {
			return
		}
		trimmed := strings.TrimPrefix(r.URL.Path, "/v1/runs/")
		runID, ok := strings.CutSuffix(trimmed, "/edits")
		if !ok || runID == "" || strings.Contains(runID, "/") {
			writeError(w, http.StatusNotFound, "not_found", "unknown run edits endpoint")
			return
		}
		var cmd engine.GraphEditCommand
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, MaxBodyBytes)).Decode(&cmd); err != nil {
			writeError(w, http.StatusBadRequest, "invalid_body", "invalid graph edit JSON")
			return
		}
		cmd.RunID = runID
		res, err := gm.ApplyEdit(cmd)
		if err != nil {
			var conflict *engine.EditConflictError
			if errors.As(err, &conflict) {
				writeJSON(w, http.StatusConflict, conflict)
				return
			}
			writeError(w, http.StatusBadRequest, "edit_rejected", err.Error())
			return
		}
		writeJSON(w, http.StatusOK, res)
	}
}
