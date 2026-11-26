package catalog_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/romanvolkov/flows/internal/catalog"
	"github.com/romanvolkov/flows/internal/cli"
	"github.com/romanvolkov/flows/internal/localapi"
)

func TestReadinessMissingCLINOAuthOfflineInvalidationAndAPIParity(t *testing.T) {
	ctx := context.Background()
	checker := catalog.NewReadinessChecker()

	// 1. Missing CLI.
	missing := checker.Check(ctx, catalog.ToolCheckSpec{
		PackageID:     "gh-tool",
		Enabled:       true,
		ManifestValid: true,
		SchemaValid:   true,
		HandshakeOK:   true,
		RequiredCLIs:  []catalog.CLIRequirement{{Name: "missing-cli", Path: filepath.Join(t.TempDir(), "no-cli")}},
	})
	if len(missing) != 1 || missing[0].State != catalog.ReadyMissingPrereq || missing[0].RepairHint == "" {
		t.Fatalf("expected ReadyMissingPrereq with hint, got %+v", missing)
	}

	// 2. Missing auth.
	noAuth := checker.Check(ctx, catalog.ToolCheckSpec{
		PackageID:     "jira-tool",
		Enabled:       true,
		ManifestValid: true,
		SchemaValid:   true,
		HandshakeOK:   true,
		Credentials:   []catalog.CredentialCheck{{Ref: "jira/token", Present: false}},
	})
	if len(noAuth) != 1 || noAuth[0].State != catalog.ReadyNeedsAuth {
		t.Fatalf("expected ReadyNeedsAuth, got %+v", noAuth)
	}

	// 3. Network offline probe.
	offline := checker.Check(ctx, catalog.ToolCheckSpec{
		PackageID:     "gitlab-tool",
		Enabled:       true,
		ManifestValid: true,
		SchemaValid:   true,
		HandshakeOK:   true,
		RemoteProbe:   func(context.Context) error { return catalog.ErrNetworkOffline },
	})
	if len(offline) != 1 || offline[0].State != catalog.ReadyUnverifiedOffline {
		t.Fatalf("expected ReadyUnverifiedOffline, got %+v", offline)
	}

	// 4. Cached success invalidation on credential/profile edit + partial package.
	probeCalls := 0
	partialSpec := catalog.ToolCheckSpec{
		PackageID:     "mixed",
		Enabled:       true,
		ManifestValid: true,
		SchemaValid:   true,
		HandshakeOK:   true,
		BuildRevision: "b1",
		CredRevision:  "c1",
		Operations: []catalog.OperationCheckSpec{
			{
				OperationID: "mixed.usable",
				RemoteProbe: func(context.Context) error {
					probeCalls++
					return nil
				},
			},
			{
				OperationID: "mixed.broken",
				Credentials: []catalog.CredentialCheck{{Ref: "secret/key", Present: false}},
			},
		},
	}
	res1 := checker.Check(ctx, partialSpec)
	if res1[0].State != catalog.ReadyOK || res1[1].State != catalog.ReadyNeedsAuth || probeCalls != 1 {
		t.Fatalf("unexpected partial check 1: %+v calls=%d", res1, probeCalls)
	}
	// Repeat with same revision -> cached (probeCalls stays 1).
	_ = checker.Check(ctx, partialSpec)
	if probeCalls != 1 {
		t.Fatalf("expected cached check, got probeCalls=%d", probeCalls)
	}
	// Bump CredRevision -> cache invalidated (probeCalls becomes 2).
	partialSpec.CredRevision = "c2"
	_ = checker.Check(ctx, partialSpec)
	if probeCalls != 2 {
		t.Fatalf("expected cache invalidation on revision edit, got probeCalls=%d", probeCalls)
	}

	// 5. CLI diagnostics match API results.
	specs := []catalog.ToolCheckSpec{partialSpec}
	cliReports := cli.DoctorTools(ctx, checker, specs)

	h := localapi.ToolReadinessHandler(checker, func(context.Context) []catalog.ToolCheckSpec { return specs })
	req := httptest.NewRequest(http.MethodGet, "/v1/tools/readiness", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("API status = %d", rec.Code)
	}
	var apiReports []catalog.ReadinessReport
	if err := json.Unmarshal(rec.Body.Bytes(), &apiReports); err != nil {
		t.Fatal(err)
	}
	if len(apiReports) != len(cliReports) || apiReports[0].State != cliReports[0].State || apiReports[1].State != cliReports[1].State {
		t.Fatalf("CLI vs API mismatch: cli=%+v api=%+v", cliReports, apiReports)
	}
}
