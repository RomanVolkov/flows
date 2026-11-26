package catalog

import (
	"encoding/json"
	"errors"
	"testing"
)

func TestEmptyCatalogAndBuiltinsPlusExtensions(t *testing.T) {
	reg := NewRegistry()

	empty := reg.Search("", false)
	if empty.InstalledPackages != 0 || empty.TotalOperations != 0 || empty.EnabledOperations != 0 || len(empty.Operations) != 0 {
		t.Fatalf("expected empty catalog, got %+v", empty)
	}

	if err := reg.RegisterBuiltins(); err != nil {
		t.Fatalf("RegisterBuiltins: %v", err)
	}

	// Non-builtin cannot register under reserved core namespace.
	err := reg.RegisterPackage(PackageProvenance{
		PackageID: "fake-core",
		Namespace: "core",
		Version:   "0.1.0",
		Origin:    "user",
		Enabled:   true,
		Operations: []OperationDescriptor{
			{ID: "core.evil", Description: "evil", ExecutionKind: ExecPlugin},
		},
	})
	if !errors.Is(err, ErrReservedNamespace) {
		t.Fatalf("expected ErrReservedNamespace, got %v", err)
	}

	// Register enabled github extension and disabled jira extension.
	if err := reg.RegisterPackage(PackageProvenance{
		PackageID: "github",
		Namespace: "github",
		Version:   "1.2.0",
		Owner:     "team-gh",
		Digest:    "sha256:abc",
		Origin:    "user",
		Enabled:   true,
		Operations: []OperationDescriptor{
			{
				ID:            "github.list-pull-requests",
				Description:   "List open pull requests",
				ExecutionKind: ExecPlugin,
				ReadOnly:      true,
				OutputSchema:  json.RawMessage(`{"type":"object","properties":{"pull_requests":{"type":"array"}}}`),
			},
		},
	}); err != nil {
		t.Fatalf("register github: %v", err)
	}

	if err := reg.RegisterPackage(PackageProvenance{
		PackageID: "jira",
		Namespace: "jira",
		Version:   "1.0.0",
		Owner:     "team-jira",
		Origin:    "user",
		Enabled:   false,
		Operations: []OperationDescriptor{
			{ID: "jira.search", Description: "Search Jira issues", ExecutionKind: ExecPlugin, ReadOnly: true},
		},
	}); err != nil {
		t.Fatalf("register jira: %v", err)
	}

	// Name collision rejected.
	err = reg.RegisterPackage(PackageProvenance{
		PackageID: "github-dup",
		Namespace: "github",
		Version:   "1.0.0",
		Origin:    "external",
		Enabled:   true,
		Operations: []OperationDescriptor{
			{ID: "github.list-pull-requests", Description: "dup", ExecutionKind: ExecPlugin},
		},
	})
	if !errors.Is(err, ErrNameCollision) {
		t.Fatalf("expected ErrNameCollision, got %v", err)
	}

	// Search separates package count (3), total ops (6), and enabled ops (5).
	res := reg.Search("", false)
	if res.InstalledPackages != 3 || res.TotalOperations != 6 || res.EnabledOperations != 5 || len(res.Operations) != 5 {
		t.Fatalf("unexpected counts: %+v", res)
	}

	// Describe by ID returns operation and provenance.
	op, pkg, err := reg.Describe("github.list-pull-requests")
	if err != nil {
		t.Fatalf("Describe: %v", err)
	}
	if op.Owner != "team-gh" || pkg.Digest != "sha256:abc" || op.ReadyState != ReadyOK {
		t.Fatalf("unexpected describe result: op=%+v pkg=%+v", op, pkg)
	}
}
