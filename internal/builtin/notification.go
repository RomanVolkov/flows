package builtin

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

var ErrAlreadyAnswered = errors.New("question already answered")

// Notification is a user-facing alert with a deep link to the originating node.
type Notification struct {
	ID         string `json:"id"`
	RunID      string `json:"run_id"`
	NodeID     string `json:"node_id"`
	AttemptID  string `json:"attempt_id,omitempty"`
	Title      string `json:"title"`
	Body       string `json:"body"`
	DeepLink   string `json:"deep_link"`
	QuestionID string `json:"question_id,omitempty"`
	CreatedAt  int64  `json:"created_at"`
}

// Question is a persisted human-input prompt linked to a specific attempt.
type Question struct {
	ID         string   `json:"id"`
	RunID      string   `json:"run_id"`
	NodeID     string   `json:"node_id"`
	AttemptID  string   `json:"attempt_id"`
	Prompt     string   `json:"prompt"`
	Options    []string `json:"options,omitempty"`
	Answer     string   `json:"answer,omitempty"`
	Answered   bool     `json:"answered"`
	CreatedAt  int64    `json:"created_at"`
	AnsweredAt int64    `json:"answered_at,omitempty"`
}

// NotificationCenter persists questions before emitting notifications and
// enforces exactly-once answer delivery.
type NotificationCenter struct {
	mu       sync.Mutex
	notiDir  string
	questDir string
}

// NewNotificationCenter initializes durable storage under stateDir.
func NewNotificationCenter(stateDir string) (*NotificationCenter, error) {
	notiDir := filepath.Join(stateDir, "notifications")
	questDir := filepath.Join(stateDir, "questions")
	if err := os.MkdirAll(notiDir, 0700); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(questDir, 0700); err != nil {
		return nil, err
	}
	return &NotificationCenter{notiDir: notiDir, questDir: questDir}, nil
}

// Notify persists and returns a notification with a deep link.
func (c *NotificationCenter) Notify(runID, nodeID, attemptID, title, body, questionID string) (Notification, error) {
	n := Notification{
		ID:         "noti-" + randHex(8),
		RunID:      runID,
		NodeID:     nodeID,
		AttemptID:  attemptID,
		Title:      title,
		Body:       body,
		DeepLink:   BuildDeepLink(runID, nodeID),
		QuestionID: questionID,
		CreatedAt:  time.Now().UnixNano(),
	}
	data, _ := json.MarshalIndent(n, "", "  ")
	if err := os.WriteFile(filepath.Join(c.notiDir, n.ID+".json"), data, 0600); err != nil {
		return Notification{}, err
	}
	return n, nil
}

// AskAndNotify persists the Question durably BEFORE creating the Notification.
func (c *NotificationCenter) AskAndNotify(runID, nodeID, attemptID, prompt string, options []string) (Question, Notification, error) {
	q := Question{
		ID:        "q-" + randHex(8),
		RunID:     runID,
		NodeID:    nodeID,
		AttemptID: attemptID,
		Prompt:    prompt,
		Options:   append([]string(nil), options...),
		CreatedAt: time.Now().UnixNano(),
	}
	data, _ := json.MarshalIndent(q, "", "  ")
	if err := os.WriteFile(filepath.Join(c.questDir, q.ID+".json"), data, 0600); err != nil {
		return Question{}, Notification{}, err
	}
	n, err := c.Notify(runID, nodeID, attemptID, "Input requested", prompt, q.ID)
	if err != nil {
		return Question{}, Notification{}, err
	}
	return q, n, nil
}

// AnswerOnce delivers an answer to questionID exactly once.
func (c *NotificationCenter) AnswerOnce(questionID, answer string) (Question, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	path := filepath.Join(c.questDir, questionID+".json")
	data, err := os.ReadFile(path)
	if err != nil {
		return Question{}, err
	}
	var q Question
	if err := json.Unmarshal(data, &q); err != nil {
		return Question{}, err
	}
	if q.Answered {
		return Question{}, fmt.Errorf("%w: %s", ErrAlreadyAnswered, questionID)
	}
	q.Answered = true
	q.Answer = answer
	q.AnsweredAt = time.Now().UnixNano()
	updated, _ := json.MarshalIndent(q, "", "  ")
	if err := os.WriteFile(path, updated, 0600); err != nil {
		return Question{}, err
	}
	return q, nil
}

// ListNotifications returns all persisted notifications.
func (c *NotificationCenter) ListNotifications() ([]Notification, error) {
	entries, err := os.ReadDir(c.notiDir)
	if err != nil {
		return nil, err
	}
	var out []Notification
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(c.notiDir, e.Name()))
		if err != nil {
			continue
		}
		var n Notification
		if json.Unmarshal(data, &n) == nil {
			out = append(out, n)
		}
	}
	return out, nil
}

// BuildDeepLink constructs a flows:// deep link for a run and node.
func BuildDeepLink(runID, nodeID string) string {
	return fmt.Sprintf("flows://runs/%s/nodes/%s", url.PathEscape(runID), url.PathEscape(nodeID))
}

// ResolveDeepLink extracts (runID, nodeID) from a flows:// deep link.
func ResolveDeepLink(link string) (runID, nodeID string, err error) {
	u, err := url.Parse(link)
	if err != nil || u.Scheme != "flows" || u.Host != "runs" {
		return "", "", fmt.Errorf("invalid flows deep link %q", link)
	}
	parts := strings.Split(strings.TrimPrefix(u.Path, "/"), "/")
	if len(parts) != 3 || parts[1] != "nodes" || parts[0] == "" || parts[2] == "" {
		return "", "", fmt.Errorf("invalid flows deep link path %q", link)
	}
	return parts[0], parts[2], nil
}

func randHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
