# Operations Guide

## Daemon & Service Status

`flowsd` runs per-user via `launchd` (`~/Library/LaunchAgents/dev.flows.daemon.plist`, or an isolated hash-suffixed label for dev homes).

- Check daemon health: `flows status`
- Check toolchain prerequisites: `make doctor`
- Inspect logs: `~/Library/Logs/Flows/flowsd.out.log` and `flowsd.err.log`
- Profiles & harnesses: configured in `~/.config/flows/settings.yaml` (respecting `XDG_CONFIG_HOME` and `FLOWS_HOME`) with explicit executable paths for GUI launches.

## Usage Attribution & Coverage

Usage collectors ingest session token/cost records from `claude`, `codex`, and `agy` into SQLite (`002_usage.sql`), attributing totals per run, step, and harness.

## Upgrade, Rollback & Uninstall

- **Upgrade:** Stages and validates the new `Flows.app`, freezes new claims, journals in-flight provider receipts, backs up the SQLite store and previous bundle, runs migrations, and rolls back automatically if migration or validation fails.
- **Uninstall:** `make uninstall` removes `~/Applications/Flows.app` and `~/.local/bin/flows`; manifest-based `internal/install.Uninstall` additionally unregisters `launchd` and removes `spec/v1` and the plist while preserving user config, workflows, worktrees, and usage data.
- **Manual data removal:** To remove all user data after uninstalling, delete `~/.config/flows`, `~/Library/Application Support/Flows`, `~/Library/Caches/Flows`, and `~/Library/Logs/Flows` (or `$FLOWS_HOME` if overridden).
