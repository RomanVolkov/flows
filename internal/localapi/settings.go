package localapi

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/romanvolkov/flows/internal/config"
	"github.com/romanvolkov/flows/internal/paths"
)

type patchSettingsRequest struct {
	ExpectedRevision int64           `json:"expected_revision"`
	Settings         config.Settings `json:"settings"`
}

// SettingsHandler serves GET/PATCH /v1/settings and POST /v1/settings/validate.
func SettingsHandler(home paths.Home) http.HandlerFunc {
	path := home.Settings()
	return func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/settings":
			switch r.Method {
			case http.MethodGet:
				s, err := config.LoadSettings(path)
				if err != nil {
					writeError(w, http.StatusUnprocessableEntity, "settings_invalid", err.Error())
					return
				}
				writeJSON(w, http.StatusOK, s)
			case http.MethodPatch:
				var req patchSettingsRequest
				if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, MaxBodyBytes)).Decode(&req); err != nil {
					writeError(w, http.StatusBadRequest, "invalid_body", "invalid settings JSON")
					return
				}
				saved, err := config.SaveSettings(path, req.ExpectedRevision, req.Settings)
				if err != nil {
					if errors.Is(err, config.ErrRevisionConflict) {
						writeError(w, http.StatusConflict, "revision_conflict", err.Error())
						return
					}
					writeError(w, http.StatusBadRequest, "invalid_settings", err.Error())
					return
				}
				writeJSON(w, http.StatusOK, saved)
			default:
				w.Header().Set("Allow", "GET, PATCH")
				writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "unsupported request method")
			}
		case "/v1/settings/validate":
			if !method(w, r, http.MethodPost) {
				return
			}
			var s config.Settings
			if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, MaxBodyBytes)).Decode(&s); err != nil {
				writeError(w, http.StatusBadRequest, "invalid_body", "invalid settings JSON")
				return
			}
			if err := config.ValidateSettings(s); err != nil {
				writeError(w, http.StatusBadRequest, "invalid_settings", err.Error())
				return
			}
			writeJSON(w, http.StatusOK, map[string]bool{"valid": true})
		default:
			writeError(w, http.StatusNotFound, "not_found", "unknown settings endpoint")
		}
	}
}
