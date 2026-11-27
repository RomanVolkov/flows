package localapi

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/romanvolkov/flows/internal/library"
	"github.com/romanvolkov/flows/internal/paths"
)

type importRequest struct {
	Directory         string `json:"directory"`
	AllowOverwrite    bool   `json:"allow_overwrite,omitempty"`
	CheckDependencies bool   `json:"check_dependencies,omitempty"`
}

func handleLibrary(w http.ResponseWriter, r *http.Request, home paths.Home, worker *libraryReconciler) {
	switch r.URL.Path {
	case "/v1/library":
		if !method(w, r, http.MethodGet) {
			return
		}
		worker.trigger()
		writeJSON(w, http.StatusOK, worker.snapshot())
	case "/v1/library/preview":
		if !method(w, r, http.MethodPost) {
			return
		}
		var req importRequest
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, MaxBodyBytes)).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid_body", "invalid preview request")
			return
		}
		prev, err := library.Preview(home, req.Directory)
		if err != nil {
			writeError(w, http.StatusBadRequest, "preview_failed", err.Error())
			return
		}
		writeJSON(w, http.StatusOK, prev)
	case "/v1/library/validate":
		if !method(w, r, http.MethodPost) {
			return
		}
		var req importRequest
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, MaxBodyBytes)).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid_body", "invalid validation request")
			return
		}
		report := library.Validate(home, req.Directory)
		importable := canImportPackage(report)
		if req.CheckDependencies && report.Valid {
			ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
			defer cancel()
			report = library.ValidateWithDependencies(ctx, home, req.Directory)
		}
		writeJSON(w, http.StatusOK, struct {
			library.ValidationReport
			Importable bool `json:"importable"`
		}{report, importable})
	case "/v1/library/import":
		if !method(w, r, http.MethodPost) {
			return
		}
		var req importRequest
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, MaxBodyBytes)).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid_body", "invalid import request")
			return
		}
		if report := library.Validate(home, req.Directory); !canImportPackage(report) {
			writeJSON(w, http.StatusUnprocessableEntity, report)
			return
		}
		rec, err := library.Import(home, req.Directory, library.ImportOptions{AllowOverwrite: req.AllowOverwrite})
		if err != nil {
			writeError(w, http.StatusConflict, "import_failed", err.Error())
			return
		}
		worker.trigger()
		writeJSON(w, http.StatusOK, rec)
	default:
		writeError(w, http.StatusNotFound, "not_found", "unknown library endpoint")
	}
}

// An unresolved dependency can be prepared after the package is imported.
func canImportPackage(report library.ValidationReport) bool {
	if report.Valid {
		return true
	}
	if report.Kind != library.KindWorkflow || len(report.Diagnostics) == 0 {
		return false
	}
	for _, diagnostic := range report.Diagnostics {
		if diagnostic.Code != "unknown_operation" && diagnostic.Code != "unknown_workflow" {
			return false
		}
	}
	return true
}
