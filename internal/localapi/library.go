package localapi

import (
	"encoding/json"
	"net/http"

	"github.com/romanvolkov/flows/internal/library"
	"github.com/romanvolkov/flows/internal/paths"
)

type importRequest struct {
	Directory      string `json:"directory"`
	AllowOverwrite bool   `json:"allow_overwrite,omitempty"`
}

func handleLibrary(w http.ResponseWriter, r *http.Request, home paths.Home, extraRoots []string) {
	switch r.URL.Path {
	case "/v1/library":
		if !method(w, r, http.MethodGet) {
			return
		}
		snap, err := library.Scan(home, extraRoots)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "scan_failed", err.Error())
			return
		}
		writeJSON(w, http.StatusOK, snap)
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
	case "/v1/library/import":
		if !method(w, r, http.MethodPost) {
			return
		}
		var req importRequest
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, MaxBodyBytes)).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid_body", "invalid import request")
			return
		}
		rec, err := library.Import(home, req.Directory, library.ImportOptions{AllowOverwrite: req.AllowOverwrite})
		if err != nil {
			writeError(w, http.StatusConflict, "import_failed", err.Error())
			return
		}
		writeJSON(w, http.StatusOK, rec)
	default:
		writeError(w, http.StatusNotFound, "not_found", "unknown library endpoint")
	}
}
