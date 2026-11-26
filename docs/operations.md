# Operations Guide

## Daemon & Service Status

`flowsd` runs per-user via `launchd` (`~/Library/LaunchAgents/dev.flows.daemon.plist`, or an isolated hash-suffixed label for dev homes).

- Check daemon health and tool readiness: `flows doctor`
- Inspect logs: `~/Library/Logs/Flows/flowsd.out.log` and `flowsd.err.log`
- Profiles & harnesses: configured in `~/.config/flows/settings.json` with explicit executable paths for GUI launches.

## Usage Attribution & Coverage

Usage collectors ingest session token/cost records from `claude`, `codex`, and `agy` into SQLite (`002_usage.sql`), attributing totals per run, step, and harness.

## Upgrade, Rollback & Uninstall

- **Upgrade:** Stages and validates the new `Flows.app`, freezes new claims, journals in-flight provider receipts, backs up the SQLite store and previous bundle, runs migrations, and rolls back automatically if migration or validation fails.
- **Uninstall:** `make uninstall` (or manifest-based uninstall) unregisters the `launchd` service and removes only owned files (`Flows.app`, owned `~/.local/bin/flows` symlink, `spec/v1`, plist). User config, workflows, worktrees, and usage data are preserved.
- **Manual data removal:** To remove all user data after uninstalling, delete `~/.config/flows` and `~/.local/share/flows`.
