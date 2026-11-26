// Package activity records direct plugin invocations, node-mode execution
// provenance, and timestamped terminal excerpts for audit and workflow improvement.
package activity

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Provenance identifies the exact package/version/digest that executed an operation.
type Provenance struct {
	PackageID string `json:"package_id"`
	Version   string `json:"version"`
	Digest    string `json:"digest,omitempty"`
}

// Record captures one direct or node-mode operation invocation.
type Record struct {
	ID          string          `json:"id"`
	AttemptID   string          `json:"attempt_id"`
	RunID       string          `json:"run_id"`
	OperationID string          `json:"operation_id"`
	Mode        string          `json:"mode"` // "direct" | "node"
	Arguments   json.RawMessage `json:"arguments,omitempty"`
	Provenance  Provenance      `json:"provenance"`
	DurationMs  int64           `json:"duration_ms"`
	Status      string          `json:"status"` // "succeeded" | "failed"
	Result      json.RawMessage `json:"result,omitempty"`
	Error       string          `json:"error,omitempty"`
	CreatedAt   int64           `json:"created_at"`
}

// TerminalExcerpt is a user-selected timestamped excerpt captured when
// structured conversation turns are unavailable.
type TerminalExcerpt struct {
	AttemptID   string `json:"attempt_id"`
	StartSeq    uint64 `json:"start_seq"`
	EndSeq      uint64 `json:"end_seq"`
	Text        string `json:"text"`
	CoverageGap string `json:"coverage_gap,omitempty"`
	Timestamp   int64  `json:"timestamp"`
}

// Log persists activity records and terminal excerpts per attempt.
type Log struct {
	mu  sync.Mutex
	dir string
}

// NewLog opens durable activity storage under dir.
func NewLog(dir string) (*Log, error) {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	return &Log{dir: dir}, nil
}

// Record appends rec to the attempt's activity log.
func (l *Log) Record(rec Record) error {
	if rec.CreatedAt == 0 {
		rec.CreatedAt = time.Now().UnixNano()
	}
	return l.appendJSONL(rec.AttemptID+".activities.jsonl", rec)
}

// List returns all activity records for attemptID.
func (l *Log) List(attemptID string) ([]Record, error) {
	return readJSONL[Record](filepath.Join(l.dir, attemptID+".activities.jsonl"))
}

// RecordExcerpt appends a user-selected timestamped terminal excerpt.
func (l *Log) RecordExcerpt(ex TerminalExcerpt) error {
	if ex.Timestamp == 0 {
		ex.Timestamp = time.Now().UnixNano()
	}
	return l.appendJSONL(ex.AttemptID+".excerpts.jsonl", ex)
}

// ListExcerpts returns all terminal excerpts for attemptID.
func (l *Log) ListExcerpts(attemptID string) ([]TerminalExcerpt, error) {
	return readJSONL[TerminalExcerpt](filepath.Join(l.dir, attemptID+".excerpts.jsonl"))
}

func (l *Log) appendJSONL(filename string, v any) error {
	line, err := json.Marshal(v)
	if err != nil {
		return err
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	f, err := os.OpenFile(filepath.Join(l.dir, filename), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := f.Write(append(line, '\n')); err != nil {
		return err
	}
	return f.Sync()
}

func readJSONL[T any](path string) ([]T, error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()
	var out []T
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 16*1024), 4*1024*1024)
	for sc.Scan() {
		var item T
		if json.Unmarshal(sc.Bytes(), &item) == nil {
			out = append(out, item)
		}
	}
	return out, sc.Err()
}
