package install

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestUpgradeRollbackUninstallAndReinstall(t *testing.T) {
	b1 := makeFakeBundle(t)
	b2 := makeFakeBundle(t)
	_ = os.WriteFile(filepath.Join(b2, "Contents/MacOS/VERSION"), []byte("2.0.0"), 0644)

	home := t.TempDir()
	runner := &fakeLaunchdRunner{}
	if _, err := Install(context.Background(), Options{
		HomeDir: home, SourceBundle: b1, IsDevHome: true, Runner: runner,
	}); err != nil {
		t.Fatal(err)
	}

	// Preserve user workflow + usage data across upgrade/uninstall.
	userWF := filepath.Join(home, ".local/share/flows/workflows/custom.yaml")
	_ = os.MkdirAll(filepath.Dir(userWF), 0755)
	_ = os.WriteFile(userWF, []byte("schema: flows/v1\n"), 0644)
	storePath := filepath.Join(home, ".local/share/flows/flows.db")
	_ = os.WriteFile(storePath, []byte("v1-store"), 0600)

	// 1. Failed migration rolls back bundle and store, preserving receipts.
	err := Upgrade(context.Background(), UpgradeOptions{
		HomeDir:   home,
		NewBundle: b2,
		StorePath: storePath,
		Receipts:  []RetainedReceipt{{ID: "rcpt-1", Action: "github.merge-pr", Receipt: "sha-1"}},
		Migrate: func(_ context.Context, p string) error {
			_ = os.WriteFile(p, []byte("corrupted-v2"), 0600)
			return errors.New("schema mismatch")
		},
	})
	if err == nil || !strings.Contains(err.Error(), "rolled back") {
		t.Fatalf("expected rollback error, got %v", err)
	}
	if raw, _ := os.ReadFile(storePath); string(raw) != "v1-store" {
		t.Fatalf("store not rolled back: %q", string(raw))
	}
	if _, err := os.Stat(filepath.Join(home, "Applications/Flows.app/Contents/MacOS/VERSION")); err == nil {
		t.Fatal("v2 bundle file should have been rolled back")
	}

	// Validate failure also rolls back bundle and removes stale WAL/SHM.
	if err := os.WriteFile(storePath+"-wal", []byte("stale-wal"), 0600); err != nil {
		t.Fatal(err)
	}
	err = Upgrade(context.Background(), UpgradeOptions{
		HomeDir:   home,
		NewBundle: b2,
		StorePath: storePath,
		Migrate: func(_ context.Context, p string) error {
			return os.WriteFile(p, []byte("migrated-v2"), 0600)
		},
		Validate: func(context.Context) error { return errors.New("post-check failed") },
	})
	if err == nil || !strings.Contains(err.Error(), "rolled back") {
		t.Fatalf("expected validate rollback, got %v", err)
	}
	if _, err := os.Stat(storePath + "-wal"); !os.IsNotExist(err) {
		t.Fatal("stale WAL should be removed on rollback")
	}

	// 2. Successful upgrade replays retained receipts once and cleans backups.
	replayed := 0
	if err := Upgrade(context.Background(), UpgradeOptions{
		HomeDir:   home,
		NewBundle: b2,
		StorePath: storePath,
		Receipts:  []RetainedReceipt{{ID: "rcpt-1", Action: "github.merge-pr", Receipt: "sha-1"}},
		Migrate: func(_ context.Context, p string) error {
			return os.WriteFile(p, []byte("v2-store"), 0600)
		},
		Validate:      func(context.Context) error { return nil },
		ReplayReceipt: func(_ context.Context, _ RetainedReceipt) error { replayed++; return nil },
	}); err != nil {
		t.Fatalf("upgrade v2: %v", err)
	}
	if replayed != 1 {
		t.Fatalf("expected 1 replayed receipt, got %d", replayed)
	}

	// 3. Uninstall blocks if active hosts exist without CancelActive, then succeeds and preserves user files + foreign CLI link.
	cliLink := filepath.Join(home, ".local/bin/flows")
	_ = os.Remove(cliLink)
	_ = os.Symlink("/usr/local/bin/other-flows", cliLink)

	if err := Uninstall(context.Background(), UninstallOptions{HomeDir: home, ActiveHosts: 1, Runner: runner}); err == nil {
		t.Fatal("expected uninstall to block with active hosts")
	}
	if err := Uninstall(context.Background(), UninstallOptions{HomeDir: home, ActiveHosts: 1, CancelActive: true, Runner: runner}); err != nil {
		t.Fatalf("uninstall: %v", err)
	}
	if _, err := os.Stat(userWF); err != nil {
		t.Fatalf("user workflow should be preserved after uninstall: %v", err)
	}
	if target, err := os.Readlink(cliLink); err != nil || target != "/usr/local/bin/other-flows" {
		t.Fatalf("foreign CLI symlink should be preserved, got target=%q err=%v", target, err)
	}
	// Remove foreign symlink before testing clean reinstall.
	if err := os.Remove(cliLink); err != nil {
		t.Fatal(err)
	}
	// Reinstall after uninstall succeeds cleanly.
	if _, err := Install(context.Background(), Options{
		HomeDir: home, SourceBundle: b2, IsDevHome: true, Runner: runner,
	}); err != nil {
		t.Fatalf("reinstall: %v", err)
	}
}
