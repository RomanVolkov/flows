package install

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// RetainedReceipt records an in-flight provider callback/receipt captured at the
// upgrade barrier so it is replayed once after migration succeeds or rolled back.
type RetainedReceipt struct {
	ID      string `json:"id"`
	Action  string `json:"action"`
	Receipt string `json:"receipt"`
}

// UpgradeOptions configures a transactional upgrade with rollback.
type UpgradeOptions struct {
	HomeDir       string
	NewBundle     string
	StorePath     string
	Receipts      []RetainedReceipt
	Freeze        func(ctx context.Context) error
	Migrate       func(ctx context.Context, storePath string) error
	Validate      func(ctx context.Context) error
	ReplayReceipt func(ctx context.Context, r RetainedReceipt) error
}

// Upgrade stages and validates newBundle, freezes claims, backs up storePath and
// the previous app bundle, runs migration/validation, and rolls back on failure.
func Upgrade(ctx context.Context, opts UpgradeOptions) error {
	if opts.HomeDir == "" || opts.NewBundle == "" {
		return errors.New("home_dir and new_bundle are required")
	}
	if _, err := os.Stat(filepath.Join(opts.NewBundle, "Contents", "MacOS", "flows")); err != nil {
		return fmt.Errorf("validate staged bundle: %w", err)
	}

	appDir := filepath.Join(opts.HomeDir, "Applications", "Flows.app")
	prevApp := appDir + ".prev"
	_ = os.RemoveAll(prevApp)

	// Freeze new claims/graph mutations and persist retained receipts at barrier.
	if opts.Freeze != nil {
		if err := opts.Freeze(ctx); err != nil {
			return fmt.Errorf("freeze dispatch: %w", err)
		}
	}
	receiptsPath := filepath.Join(opts.HomeDir, ".config", "flows", "upgrade-receipts.json")
	if len(opts.Receipts) > 0 {
		if err := os.MkdirAll(filepath.Dir(receiptsPath), 0755); err != nil {
			return fmt.Errorf("create receipts dir: %w", err)
		}
		raw, err := json.Marshal(opts.Receipts)
		if err != nil {
			return fmt.Errorf("marshal receipts: %w", err)
		}
		if err := os.WriteFile(receiptsPath, raw, 0600); err != nil {
			return fmt.Errorf("write receipts: %w", err)
		}
	}

	// Backup store (including -wal and -shm) and swap bundle, keeping prevApp + backups until validation passes.
	var storeBak string
	if opts.StorePath != "" {
		if _, err := os.Stat(opts.StorePath); err == nil {
			data, err := os.ReadFile(opts.StorePath)
			if err != nil {
				return fmt.Errorf("read store for backup: %w", err)
			}
			storeBak = opts.StorePath + ".bak"
			if err := os.WriteFile(storeBak, data, 0600); err != nil {
				return fmt.Errorf("backup store: %w", err)
			}
			for _, suffix := range []string{"-wal", "-shm"} {
				if extra, err := os.ReadFile(opts.StorePath + suffix); err == nil {
					if err := os.WriteFile(storeBak+suffix, extra, 0600); err != nil {
						return fmt.Errorf("backup store %s: %w", suffix, err)
					}
				}
			}
		} else if !os.IsNotExist(err) {
			return fmt.Errorf("stat store: %w", err)
		}
	}
	if _, err := os.Stat(appDir); err == nil {
		if err := os.Rename(appDir, prevApp); err != nil {
			return fmt.Errorf("backup previous bundle: %w", err)
		}
	}

	rollback := func() {
		_ = os.RemoveAll(appDir)
		if _, err := os.Stat(prevApp); err == nil {
			_ = os.Rename(prevApp, appDir)
		}
		if storeBak != "" {
			if data, err := os.ReadFile(storeBak); err == nil {
				_ = os.WriteFile(opts.StorePath, data, 0600)
			}
			for _, suffix := range []string{"-wal", "-shm"} {
				if extra, err := os.ReadFile(storeBak + suffix); err == nil {
					_ = os.WriteFile(opts.StorePath+suffix, extra, 0600)
				} else {
					_ = os.Remove(opts.StorePath + suffix)
				}
			}
		}
	}

	if err := copyDirPreserveExisting(opts.NewBundle, appDir, true); err != nil {
		rollback()
		return fmt.Errorf("stage new bundle: %w", err)
	}
	if opts.Migrate != nil {
		if err := opts.Migrate(ctx, opts.StorePath); err != nil {
			rollback()
			return fmt.Errorf("migration failed (rolled back): %w", err)
		}
	}
	if opts.Validate != nil {
		if err := opts.Validate(ctx); err != nil {
			rollback()
			return fmt.Errorf("validation failed (rolled back): %w", err)
		}
	}

	// Replay retained receipts once, then clean up backups.
	for _, r := range opts.Receipts {
		if opts.ReplayReceipt != nil {
			if err := opts.ReplayReceipt(ctx, r); err != nil {
				rollback()
				return fmt.Errorf("replay receipt %s (rolled back): %w", r.ID, err)
			}
		}
	}
	_ = os.Remove(receiptsPath)
	_ = os.RemoveAll(prevApp)
	if storeBak != "" {
		_ = os.Remove(storeBak)
		_ = os.Remove(storeBak + "-wal")
		_ = os.Remove(storeBak + "-shm")
	}
	return nil
}
