package catalog

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// CLIRequirement declares an external CLI binary required by a tool or operation.
type CLIRequirement struct {
	Name       string `json:"name"`
	Path       string `json:"path,omitempty"`
	MinVersion string `json:"min_version,omitempty"`
}

// CredentialCheck declares a required credential reference.
type CredentialCheck struct {
	Ref     string `json:"ref"`
	Present bool   `json:"present"`
}

// OperationCheckSpec allows per-operation prerequisite overrides for partially
// functional packages.
type OperationCheckSpec struct {
	OperationID  string
	RequiredCLIs []CLIRequirement
	Credentials  []CredentialCheck
	RemoteProbe  func(ctx context.Context) error
}

// ToolCheckSpec describes all local and optional remote readiness checks for a package.
type ToolCheckSpec struct {
	PackageID      string
	Enabled        bool
	ManifestValid  bool
	SchemaValid    bool
	BuildPath      string
	HandshakeOK    bool
	HandshakeError string
	BuildRevision  string
	ProfileRev     string
	CredRevision   string
	RequiredCLIs   []CLIRequirement
	Credentials    []CredentialCheck
	RemoteProbe    func(ctx context.Context) error
	ProbeTimeout   time.Duration
	Operations     []OperationCheckSpec
}

// ReadinessReport is the cached diagnostic result for one operation.
type ReadinessReport struct {
	PackageID    string     `json:"package_id"`
	OperationID  string     `json:"operation_id"`
	State        ReadyState `json:"state"`
	Reason       string     `json:"reason,omitempty"`
	RepairHint   string     `json:"repair_hint,omitempty"`
	ResolvedPath string     `json:"resolved_path,omitempty"`
	CheckedAt    int64      `json:"checked_at"`
	RevisionKey  string     `json:"revision_key"`
}

var ErrNetworkOffline = errors.New("network offline")

// ReadinessChecker evaluates and caches tool readiness by build/profile/credential revision.
type ReadinessChecker struct {
	mu    sync.Mutex
	cache map[string]ReadinessReport
}

// NewReadinessChecker creates an empty ReadinessChecker.
func NewReadinessChecker() *ReadinessChecker {
	return &ReadinessChecker{
		cache: make(map[string]ReadinessReport),
	}
}

// Invalidate clears cached readiness entries for packageID (or all if empty).
func (c *ReadinessChecker) Invalidate(packageID string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if packageID == "" {
		c.cache = make(map[string]ReadinessReport)
		return
	}
	for k, v := range c.cache {
		if v.PackageID == packageID {
			delete(c.cache, k)
		}
	}
}

// Check evaluates all operations in spec, returning cached results when the
// build/profile/credential revision matches.
func (c *ReadinessChecker) Check(ctx context.Context, spec ToolCheckSpec) []ReadinessReport {
	revKey := revisionKey(spec.BuildRevision, spec.ProfileRev, spec.CredRevision)
	ops := spec.Operations
	if len(ops) == 0 {
		ops = []OperationCheckSpec{{OperationID: spec.PackageID}}
	}

	reports := make([]ReadinessReport, 0, len(ops))
	for _, op := range ops {
		cacheKey := spec.PackageID + ":" + op.OperationID
		c.mu.Lock()
		if cached, ok := c.cache[cacheKey]; ok && cached.RevisionKey == revKey {
			c.mu.Unlock()
			reports = append(reports, cached)
			continue
		}
		c.mu.Unlock()

		rep := evaluateOperation(ctx, spec, op, revKey)
		c.mu.Lock()
		c.cache[cacheKey] = rep
		c.mu.Unlock()
		reports = append(reports, rep)
	}
	return reports
}

func evaluateOperation(ctx context.Context, spec ToolCheckSpec, op OperationCheckSpec, revKey string) ReadinessReport {
	now := time.Now().UnixNano()
	rep := ReadinessReport{
		PackageID:    spec.PackageID,
		OperationID:  op.OperationID,
		ResolvedPath: spec.BuildPath,
		CheckedAt:    now,
		RevisionKey:  revKey,
	}

	if !spec.Enabled {
		rep.State = ReadyDisabled
		rep.Reason = "package is disabled"
		rep.RepairHint = "Enable the package in Settings -> Tools"
		return rep
	}
	if !spec.ManifestValid || !spec.SchemaValid {
		rep.State = ReadyIncompatible
		rep.Reason = "invalid tool manifest or schema"
		rep.RepairHint = "Run flows validate on the tool package"
		return rep
	}
	if !spec.HandshakeOK {
		rep.State = ReadyIncompatible
		rep.Reason = spec.HandshakeError
		if rep.Reason == "" {
			rep.Reason = "plugin protocol handshake failed"
		}
		rep.RepairHint = "Rebuild the tool against the current SDK"
		return rep
	}

	clis := append(append([]CLIRequirement(nil), spec.RequiredCLIs...), op.RequiredCLIs...)
	for _, req := range clis {
		target := req.Path
		if target == "" {
			target = req.Name
		}
		resolved, err := exec.LookPath(target)
		if err != nil {
			rep.State = ReadyMissingPrereq
			rep.Reason = fmt.Sprintf("missing required CLI %q", req.Name)
			rep.RepairHint = fmt.Sprintf("Install %s or configure its path in Settings", req.Name)
			return rep
		}
		rep.ResolvedPath = resolved
	}

	creds := append(append([]CredentialCheck(nil), spec.Credentials...), op.Credentials...)
	for _, cred := range creds {
		if !cred.Present {
			rep.State = ReadyNeedsAuth
			rep.Reason = fmt.Sprintf("missing credential %q", cred.Ref)
			rep.RepairHint = fmt.Sprintf("Authenticate or configure credential %s", cred.Ref)
			return rep
		}
	}

	probe := op.RemoteProbe
	if probe == nil {
		probe = spec.RemoteProbe
	}
	if probe != nil {
		timeout := spec.ProbeTimeout
		if timeout <= 0 {
			timeout = 3 * time.Second
		}
		pCtx, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()
		if err := probe(pCtx); err != nil {
			if errors.Is(err, ErrNetworkOffline) || errors.Is(err, context.DeadlineExceeded) || strings.Contains(strings.ToLower(err.Error()), "offline") {
				rep.State = ReadyUnverifiedOffline
				rep.Reason = err.Error()
				rep.RepairHint = "Check network connectivity and retry validation"
				return rep
			}
			rep.State = ReadyNeedsAuth
			rep.Reason = err.Error()
			rep.RepairHint = "Verify provider authentication and retry"
			return rep
		}
	}

	rep.State = ReadyOK
	return rep
}

func revisionKey(buildRev, profileRev, credRev string) string {
	h := sha256.New()
	fmt.Fprintf(h, "%s|%s|%s", buildRev, profileRev, credRev)
	return hex.EncodeToString(h.Sum(nil))[:16]
}
