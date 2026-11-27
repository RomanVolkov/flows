package localapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/romanvolkov/flows/internal/builtin"
)

// QuestionsHandler serves notifications and exactly-once question answering.
func QuestionsHandler(nc *builtin.NotificationCenter, active ...func(context.Context, builtin.Question) bool) http.HandlerFunc {
	isActive := func(ctx context.Context, q builtin.Question) bool {
		return len(active) == 0 || active[0](ctx, q)
	}
	return func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/v1/notifications":
			if !method(w, r, http.MethodGet) {
				return
			}
			list, err := nc.ListNotifications()
			if err != nil {
				writeError(w, http.StatusInternalServerError, "list_failed", err.Error())
				return
			}
			if list == nil {
				list = []builtin.Notification{}
			}
			for i := range list {
				if list[i].QuestionID == "" {
					continue
				}
				q, err := nc.Question(list[i].QuestionID)
				if err == nil && !q.Answered && isActive(r.Context(), q) {
					list[i].Question = &q
				}
			}
			writeJSON(w, http.StatusOK, list)

		case strings.HasPrefix(r.URL.Path, "/v1/questions/") && strings.HasSuffix(r.URL.Path, "/answer"):
			if !method(w, r, http.MethodPost) {
				return
			}
			qid := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/v1/questions/"), "/answer")
			var req struct {
				Answer string `json:"answer"`
			}
			if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, MaxBodyBytes)).Decode(&req); err != nil {
				writeError(w, http.StatusBadRequest, "invalid_body", "invalid answer JSON")
				return
			}
			current, err := nc.Question(qid)
			if err != nil {
				writeError(w, http.StatusNotFound, "not_found", "question does not exist")
				return
			}
			if !current.Answered && !isActive(r.Context(), current) {
				writeError(w, http.StatusConflict, "question_closed", "the workflow is no longer waiting for this answer")
				return
			}
			q, err := nc.AnswerOnce(qid, req.Answer)
			if err != nil {
				if errors.Is(err, builtin.ErrInvalidAnswer) {
					writeError(w, http.StatusUnprocessableEntity, "invalid_answer", err.Error())
					return
				}
				if errors.Is(err, builtin.ErrAlreadyAnswered) {
					writeError(w, http.StatusConflict, "already_answered", err.Error())
					return
				}
				writeError(w, http.StatusNotFound, "not_found", err.Error())
				return
			}
			writeJSON(w, http.StatusOK, q)

		default:
			writeError(w, http.StatusNotFound, "not_found", "unknown questions endpoint")
		}
	}
}
