// Package bridge implements the attempt-scoped callback service shared by
// the CLI bridge and MCP stdio server, including typed result validation,
// host outbox capture, and durable completion delivery.
package bridge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/romanvolkov/flows/internal/activity"
	"github.com/romanvolkov/flows/internal/builtin"
	"github.com/romanvolkov/flows/internal/catalog"
	"github.com/romanvolkov/flows/internal/engine"
	"github.com/romanvolkov/flows/internal/host"
	"github.com/romanvolkov/flows/internal/store"
)

var (
	ErrCredentialExpired = errors.New("attempt credential expired")
	ErrInvalidToken      = errors.New("invalid attempt token")
	ErrCapabilityDenied  = errors.New("capability not granted to attempt")
)

// FieldError describes a single schema validation failure on a harness result.
type FieldError struct {
	Field   string `json:"field"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

// ResultValidationError is returned before outbox capture so a live harness
// can correct an invalid result in the same turn while offline.
type ResultValidationError struct {
	Errors []FieldError `json:"errors"`
}

func (e *ResultValidationError) Error() string {
	if len(e.Errors) == 0 {
		return "result validation failed"
	}
	return fmt.Sprintf("result validation failed: %s (%s)", e.Errors[0].Field, e.Errors[0].Message)
}

// AttemptScope binds callbacks to a specific run, node, attempt, capability set,
// and pinned result schema.
type AttemptScope struct {
	RunID        string          `json:"run_id"`
	NodeID       string          `json:"node_id"`
	AttemptID    string          `json:"attempt_id"`
	Token        string          `json:"token"`
	ExpiresAt    int64           `json:"expires_at"`
	Capabilities []string        `json:"capabilities"`
	PinnedSchema json.RawMessage `json:"pinned_schema,omitempty"`
}

// CompletionStatus summarizes local outbox capture vs daemon disposition.
type CompletionStatus struct {
	State       string `json:"state"` // "saved_locally" | "accepted" | "rejected"
	Reason      string `json:"reason,omitempty"`
	AttemptID   string `json:"attempt_id"`
	EventID     string `json:"event_id"`
	LocalDigest string `json:"local_digest,omitempty"`
}

// OperationExecutor executes a catalog operation in direct or node mode.
type OperationExecutor func(ctx context.Context, op catalog.OperationDescriptor, input json.RawMessage) (json.RawMessage, error)

// Service backs both the scoped CLI bridge and the MCP stdio server.
type Service struct {
	Catalog       *catalog.Registry
	Graph         *engine.GraphManager
	Store         *store.Store
	Activities    *activity.Log
	Notifications *builtin.NotificationCenter
	ExecuteOp     OperationExecutor
	tokens        map[string]AttemptScope
}

// NewService constructs a BridgeService.
func NewService(cat *catalog.Registry, gm *engine.GraphManager, st *store.Store, act *activity.Log, nc *builtin.NotificationCenter, execOp OperationExecutor) *Service {
	return &Service{
		Catalog:       cat,
		Graph:         gm,
		Store:         st,
		Activities:    act,
		Notifications: nc,
		ExecuteOp:     execOp,
		tokens:        make(map[string]AttemptScope),
	}
}

// RegisterScope registers an attempt-scoped credential.
func (s *Service) RegisterScope(scope AttemptScope) {
	s.tokens[scope.Token] = scope
}

// RevokeScope revokes an attempt credential immediately.
func (s *Service) RevokeScope(token string) {
	delete(s.tokens, token)
}

// Authorize verifies token validity, expiration, and required capability.
func (s *Service) Authorize(token, capability string, now time.Time) (AttemptScope, error) {
	scope, ok := s.tokens[token]
	if !ok {
		return AttemptScope{}, ErrInvalidToken
	}
	if scope.ExpiresAt > 0 && now.UnixNano() > scope.ExpiresAt {
		return AttemptScope{}, ErrCredentialExpired
	}
	if capability != "" && !slices.Contains(scope.Capabilities, capability) {
		return AttemptScope{}, fmt.Errorf("%w: %s", ErrCapabilityDenied, capability)
	}
	return scope, nil
}

// CatalogSearch searches enabled catalog operations.
func (s *Service) CatalogSearch(token, query string) (catalog.SearchResult, error) {
	if _, err := s.Authorize(token, "catalog", time.Now()); err != nil {
		return catalog.SearchResult{}, err
	}
	return s.Catalog.Search(query, false), nil
}

// CatalogDescribe returns operation details and provenance.
func (s *Service) CatalogDescribe(token, opID string) (catalog.OperationDescriptor, catalog.PackageProvenance, error) {
	if _, err := s.Authorize(token, "catalog", time.Now()); err != nil {
		return catalog.OperationDescriptor{}, catalog.PackageProvenance{}, err
	}
	return s.Catalog.Describe(opID)
}

// GraphInspect returns the current run graph snapshot.
func (s *Service) GraphInspect(token, runID string) (*engine.RunGraph, error) {
	scope, err := s.Authorize(token, "graph_read", time.Now())
	if err != nil {
		return nil, err
	}
	if runID == "" {
		runID = scope.RunID
	}
	rg, ok := s.Graph.RunSnapshot(runID)
	if !ok {
		return nil, fmt.Errorf("unknown run %q", runID)
	}
	return rg, nil
}

// GraphEdit submits an atomic graph edit batch scoped to the attempt's run.
func (s *Service) GraphEdit(token string, cmd engine.GraphEditCommand) (engine.EditResult, error) {
	scope, err := s.Authorize(token, "graph_edit", time.Now())
	if err != nil {
		return engine.EditResult{}, err
	}
	cmd.RunID = scope.RunID
	return s.Graph.ApplyEdit(cmd)
}

// InvokeDirect executes opID in direct mode and records an inspectable ActivityRecord.
func (s *Service) InvokeDirect(ctx context.Context, token, opID string, input json.RawMessage) (json.RawMessage, error) {
	scope, err := s.Authorize(token, "invoke", time.Now())
	if err != nil {
		return nil, err
	}
	op, pkg, err := s.Catalog.Describe(opID)
	if err != nil {
		return nil, err
	}
	start := time.Now()
	var out json.RawMessage
	var execErr error
	if s.ExecuteOp != nil {
		out, execErr = s.ExecuteOp(ctx, op, input)
	}
	dur := time.Since(start).Milliseconds()

	rec := activity.Record{
		ID:          fmt.Sprintf("act-%d", time.Now().UnixNano()),
		AttemptID:   scope.AttemptID,
		RunID:       scope.RunID,
		OperationID: opID,
		Mode:        "direct",
		Arguments:   input,
		Provenance: activity.Provenance{
			PackageID: pkg.PackageID,
			Version:   pkg.Version,
			Digest:    pkg.Digest,
		},
		DurationMs: dur,
		Status:     "succeeded",
		Result:     out,
	}
	if execErr != nil {
		rec.Status = "failed"
		rec.Error = execErr.Error()
	}
	if s.Activities != nil {
		_ = s.Activities.Record(rec)
	}
	return out, execErr
}

// SubmitResult validates payload against the attempt's pinned schema before
// capturing it in the host outbox, then delivers it to the daemon store.
func (s *Service) SubmitResult(ctx context.Context, token, eventID string, status store.Status, payload json.RawMessage, outbox *host.Outbox) (CompletionStatus, error) {
	scope, err := s.Authorize(token, "complete", time.Now())
	if err != nil {
		return CompletionStatus{}, err
	}
	if len(scope.PinnedSchema) > 0 && status == store.Succeeded {
		if err := ValidateResultPayload(scope.PinnedSchema, payload); err != nil {
			return CompletionStatus{}, err
		}
	}

	var receipt host.LocalReceipt
	if outbox != nil {
		receipt, err = outbox.Capture(eventID, string(status), payload, nil)
		if err != nil {
			return CompletionStatus{}, err
		}
	}

	cs := CompletionStatus{
		State:       "saved_locally",
		AttemptID:   scope.AttemptID,
		EventID:     eventID,
		LocalDigest: receipt.Digest,
	}
	if s.Store == nil {
		return cs, nil
	}
	return DeliverOutboxRecord(ctx, s.Store, scope.AttemptID, eventID, status, payload, outbox, scope.PinnedSchema)
}

// DeliverOutboxRecord validates against the pinned schema and commits to store.
func DeliverOutboxRecord(ctx context.Context, st *store.Store, attemptID, eventID string, status store.Status, payload []byte, outbox *host.Outbox, pinnedSchema json.RawMessage) (CompletionStatus, error) {
	if len(pinnedSchema) > 0 && status == store.Succeeded {
		if err := ValidateResultPayload(pinnedSchema, payload); err != nil {
			return CompletionStatus{}, err
		}
	}
	disp, err := st.Complete(ctx, attemptID, eventID, status, payload)
	if err != nil {
		return CompletionStatus{State: "saved_locally", AttemptID: attemptID, EventID: eventID}, err
	}
	if outbox != nil {
		_ = outbox.MarkDelivered(eventID)
	}
	return CompletionStatus{
		State:     disp.Outcome,
		Reason:    disp.Reason,
		AttemptID: attemptID,
		EventID:   eventID,
	}, nil
}

// ValidateResultPayload checks payload against a JSON schema object (required fields and primitive/array types)
// returning field-level diagnostics before outbox capture.
func ValidateResultPayload(schemaRaw, payloadRaw json.RawMessage) error {
	var schema struct {
		Type       string   `json:"type"`
		Required   []string `json:"required"`
		Properties map[string]struct {
			Type  string `json:"type"`
			Items *struct {
				Type string `json:"type"`
			} `json:"items"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(schemaRaw, &schema); err != nil {
		return fmt.Errorf("invalid pinned schema: %w", err)
	}
	if len(schema.Properties) == 0 && len(schema.Required) == 0 {
		return nil
	}

	var data map[string]any
	if err := json.Unmarshal(payloadRaw, &data); err != nil {
		return &ResultValidationError{Errors: []FieldError{{
			Field: "$", Code: "invalid_json", Message: err.Error(),
		}}}
	}

	var errs []FieldError
	for _, req := range schema.Required {
		if _, ok := data[req]; !ok {
			errs = append(errs, FieldError{
				Field:   req,
				Code:    "missing_required",
				Message: fmt.Sprintf("missing required field %q", req),
			})
		}
	}
	for field, prop := range schema.Properties {
		val, ok := data[field]
		if !ok || prop.Type == "" {
			continue
		}
		if !matchesJSONType(val, prop.Type) {
			errs = append(errs, FieldError{
				Field:   field,
				Code:    "type_mismatch",
				Message: fmt.Sprintf("expected %s for %q", prop.Type, field),
			})
			continue
		}
		if prop.Type == "array" && prop.Items != nil && prop.Items.Type != "" {
			arr, _ := val.([]any)
			for i, elem := range arr {
				if !matchesJSONType(elem, prop.Items.Type) {
					errs = append(errs, FieldError{
						Field:   fmt.Sprintf("%s[%d]", field, i),
						Code:    "type_mismatch",
						Message: fmt.Sprintf("expected array item type %s", prop.Items.Type),
					})
				}
			}
		}
	}
	if len(errs) > 0 {
		slices.SortFunc(errs, func(a, b FieldError) int {
			return strings.Compare(a.Field, b.Field)
		})
		return &ResultValidationError{Errors: errs}
	}
	return nil
}

func matchesJSONType(v any, typ string) bool {
	switch typ {
	case "string":
		_, ok := v.(string)
		return ok
	case "boolean":
		_, ok := v.(bool)
		return ok
	case "number", "integer":
		_, ok := v.(float64)
		return ok
	case "array":
		_, ok := v.([]any)
		return ok
	case "object":
		_, ok := v.(map[string]any)
		return ok
	default:
		return true
	}
}
