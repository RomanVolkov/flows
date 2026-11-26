// Package catalog manages typed operation descriptors, package provenance,
// namespace reservations, readiness state, and catalog search/describe queries.
package catalog

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
)

// ExecutionKind classifies how an operation is executed by the engine.
type ExecutionKind string

const (
	ExecCommand  ExecutionKind = "command"
	ExecHarness  ExecutionKind = "harness"
	ExecWorkflow ExecutionKind = "workflow"
	ExecPlugin   ExecutionKind = "plugin"
	ExecWait     ExecutionKind = "wait"
	ExecNotify   ExecutionKind = "notify"
)

// ReadyState represents an operation's current readiness status.
type ReadyState string

const (
	ReadyUnknown           ReadyState = "unknown"
	ReadyChecking          ReadyState = "checking"
	ReadyOK                ReadyState = "ready"
	ReadyMissingPrereq     ReadyState = "missing_prerequisite"
	ReadyNeedsAuth         ReadyState = "needs_authentication"
	ReadyIncompatible      ReadyState = "incompatible"
	ReadyDisabled          ReadyState = "disabled"
	ReadyUnverifiedOffline ReadyState = "unverified_offline"
)

var (
	ErrReservedNamespace = errors.New("core namespace is reserved for built-ins")
	ErrNameCollision     = errors.New("operation ID already registered")
	ErrNotFound          = errors.New("operation not found")
	ErrInvalidOperation  = errors.New("invalid operation descriptor")
)

// OperationDescriptor defines a typed catalog operation and its runtime state.
type OperationDescriptor struct {
	ID            string          `json:"id"`
	Version       string          `json:"version"`
	Description   string          `json:"description"`
	Help          string          `json:"help,omitempty"`
	ExecutionKind ExecutionKind   `json:"execution_kind"`
	ReadOnly      bool            `json:"read_only,omitempty"`
	InputSchema   json.RawMessage `json:"input_schema,omitempty"`
	OutputSchema  json.RawMessage `json:"output_schema,omitempty"`
	Milestones    []string        `json:"milestones,omitempty"`
	Prompts       []string        `json:"prompts,omitempty"`

	PackageID   string     `json:"package_id"`
	Owner       string     `json:"owner,omitempty"`
	Digest      string     `json:"digest,omitempty"`
	Enabled     bool       `json:"enabled"`
	ReadyState  ReadyState `json:"ready_state"`
	ReadyReason string     `json:"ready_reason,omitempty"`
}

// PackageProvenance records the source package of one or more operations.
type PackageProvenance struct {
	PackageID  string                `json:"package_id"`
	Namespace  string                `json:"namespace"`
	Version    string                `json:"version"`
	Owner      string                `json:"owner,omitempty"`
	Digest     string                `json:"digest,omitempty"`
	Origin     string                `json:"origin"` // "builtin" | "user" | "external" | "bundle"
	SourcePath string                `json:"source_path,omitempty"`
	Enabled    bool                  `json:"enabled"`
	Operations []OperationDescriptor `json:"operations"`
}

// SearchResult separates installed package count, total operations, and enabled results.
type SearchResult struct {
	InstalledPackages int                   `json:"installed_packages"`
	TotalOperations   int                   `json:"total_operations"`
	EnabledOperations int                   `json:"enabled_operations"`
	Operations        []OperationDescriptor `json:"operations"`
}

// Registry stores registered packages and operations keyed by namespaced ID.
type Registry struct {
	mu       sync.RWMutex
	packages map[string]PackageProvenance
	ops      map[string]OperationDescriptor
}

// NewRegistry creates an empty operation catalog.
func NewRegistry() *Registry {
	return &Registry{
		packages: make(map[string]PackageProvenance),
		ops:      make(map[string]OperationDescriptor),
	}
}

// RegisterPackage validates and registers all operations in pkg.
func (r *Registry) RegisterPackage(pkg PackageProvenance) error {
	if pkg.PackageID == "" || pkg.Namespace == "" || pkg.Version == "" {
		return fmt.Errorf("%w: package_id, namespace, and version are required", ErrInvalidOperation)
	}
	if pkg.Namespace == "core" && pkg.Origin != "builtin" {
		return fmt.Errorf("%w: package %q", ErrReservedNamespace, pkg.PackageID)
	}
	if len(pkg.Operations) == 0 {
		return fmt.Errorf("%w: package %q has no operations", ErrInvalidOperation, pkg.PackageID)
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.packages[pkg.PackageID]; exists {
		return fmt.Errorf("%w: package %q", ErrNameCollision, pkg.PackageID)
	}

	staged := make([]OperationDescriptor, 0, len(pkg.Operations))
	seen := make(map[string]bool, len(pkg.Operations))
	for _, op := range pkg.Operations {
		ns, name, ok := strings.Cut(op.ID, ".")
		if !ok || ns == "" || name == "" || ns != pkg.Namespace {
			return fmt.Errorf("%w: operation %q must belong to namespace %q", ErrInvalidOperation, op.ID, pkg.Namespace)
		}
		if seen[op.ID] {
			return fmt.Errorf("%w: duplicate operation %q in package %q", ErrNameCollision, op.ID, pkg.PackageID)
		}
		if existing, exists := r.ops[op.ID]; exists {
			return fmt.Errorf("%w: %q already registered by package %q", ErrNameCollision, op.ID, existing.PackageID)
		}
		seen[op.ID] = true

		op.PackageID = pkg.PackageID
		if op.Version == "" {
			op.Version = pkg.Version
		}
		if op.Owner == "" {
			op.Owner = pkg.Owner
		}
		if op.Digest == "" {
			op.Digest = pkg.Digest
		}
		op.Enabled = pkg.Enabled
		if op.ReadyState == "" {
			if pkg.Enabled {
				op.ReadyState = ReadyOK
			} else {
				op.ReadyState = ReadyDisabled
			}
		}
		staged = append(staged, op)
	}

	pkg.Operations = staged
	r.packages[pkg.PackageID] = pkg
	for _, op := range staged {
		r.ops[op.ID] = op
	}
	return nil
}

// RegisterBuiltins registers the core built-in operations through the same
// descriptor interface visible to plugins.
func (r *Registry) RegisterBuiltins() error {
	return r.RegisterPackage(PackageProvenance{
		PackageID: "core",
		Namespace: "core",
		Version:   "1.0.0",
		Owner:     "flows",
		Origin:    "builtin",
		Enabled:   true,
		Operations: []OperationDescriptor{
			{
				ID:            "core.harness",
				Description:   "Launch an interactive AI coding harness session",
				ExecutionKind: ExecHarness,
				InputSchema:   json.RawMessage(`{"type":"object","required":["harness"],"properties":{"harness":{"type":"string"},"prompt_template":{"type":"string"},"arguments":{"type":"object"}}}`),
			},
			{
				ID:            "core.command",
				Description:   "Execute a deterministic local command",
				ExecutionKind: ExecCommand,
				InputSchema:   json.RawMessage(`{"type":"object","required":["command"],"properties":{"command":{"type":"string"},"args":{"type":"array","items":{"type":"string"}},"cwd":{"type":"string"}}}`),
				OutputSchema:  json.RawMessage(`{"type":"object","properties":{"exit_code":{"type":"integer"},"stdout":{"type":"string"},"stderr":{"type":"string"}}}`),
			},
			{
				ID:            "core.wait",
				Description:   "Wait on a timer or watch condition",
				ExecutionKind: ExecWait,
				ReadOnly:      true,
			},
			{
				ID:            "core.notify",
				Description:   "Send a user notification or question",
				ExecutionKind: ExecNotify,
			},
		},
	})
}

// Search queries operations by substring on ID, description, or owner.
func (r *Registry) Search(query string, includeDisabled bool) SearchResult {
	r.mu.RLock()
	defer r.mu.RUnlock()

	q := strings.ToLower(strings.TrimSpace(query))
	res := SearchResult{
		InstalledPackages: len(r.packages),
		TotalOperations:   len(r.ops),
	}
	for _, op := range r.ops {
		if op.Enabled {
			res.EnabledOperations++
		}
		if !includeDisabled && !op.Enabled {
			continue
		}
		if q == "" ||
			strings.Contains(strings.ToLower(op.ID), q) ||
			strings.Contains(strings.ToLower(op.Description), q) ||
			strings.Contains(strings.ToLower(op.Owner), q) {
			res.Operations = append(res.Operations, op)
		}
	}
	slices.SortFunc(res.Operations, func(a, b OperationDescriptor) int {
		return strings.Compare(a.ID, b.ID)
	})
	return res
}

// Describe returns the operation descriptor and its package provenance.
func (r *Registry) Describe(id string) (OperationDescriptor, PackageProvenance, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	op, ok := r.ops[id]
	if !ok {
		return OperationDescriptor{}, PackageProvenance{}, fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	pkg := r.packages[op.PackageID]
	return op, pkg, nil
}

// SetOperationReady updates the readiness state and diagnostic reason for id.
func (r *Registry) SetOperationReady(id string, state ReadyState, reason string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	op, ok := r.ops[id]
	if !ok {
		return fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	op.ReadyState = state
	op.ReadyReason = reason
	r.ops[id] = op
	return nil
}

// SetPackageEnabled enables or disables all operations in pkgID.
func (r *Registry) SetPackageEnabled(pkgID string, enabled bool) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	pkg, ok := r.packages[pkgID]
	if !ok {
		return fmt.Errorf("%w: package %s", ErrNotFound, pkgID)
	}
	pkg.Enabled = enabled
	for i := range pkg.Operations {
		pkg.Operations[i].Enabled = enabled
		if !enabled {
			pkg.Operations[i].ReadyState = ReadyDisabled
		} else if pkg.Operations[i].ReadyState == ReadyDisabled {
			pkg.Operations[i].ReadyState = ReadyOK
		}
		r.ops[pkg.Operations[i].ID] = pkg.Operations[i]
	}
	r.packages[pkgID] = pkg
	return nil
}
