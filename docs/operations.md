# Operations Guide

## Build and install from source

Source builds use the Go version in `build/toolchain.lock`, Xcode Command Line Tools, and Node.js with npm.
Run `make doctor` to check the local toolchain.

```sh
make doctor
make check desktop-check integration
make install
```

`make install` builds and installs Flows in `~/Applications`, links the CLI in `~/.local/bin`, and
registers the per-user daemon. Use `make build plugins` to build the command line binaries and tools,
or `make package` to build the app bundle and archive. `make ui-check` runs the WebKit UI suite against
the production frontend with a mocked desktop bridge.

Pushing a stable version tag such as `v1.2.0` triggers the release workflow. GitHub Actions builds and
checks the macOS installer, publishes it with its checksum, and updates the Homebrew cask.

## Daemon & Service Status

`flowsd` runs per-user via `launchd` (`~/Library/LaunchAgents/dev.flows.daemon.plist`, or an isolated hash-suffixed label for dev homes).

- Check daemon health: `flows status`
- Check toolchain prerequisites: `make doctor`
- Inspect logs: `~/Library/Logs/Flows/flowsd.out.log` and `flowsd.err.log`
- Profiles & harnesses: configured in `~/.config/flows/settings.yaml` (respecting `XDG_CONFIG_HOME` and `FLOWS_HOME`) with explicit executable paths for GUI launches.

## Usage Attribution & Coverage

Usage collectors ingest token records from Claude, Codex, and Antigravity CLI (`agy`) into SQLite. The Analytics view includes local transcript totals. Flows links new `agy` sessions and workflow steps to their transcripts through the CLI's status line callback. Older `agy` conversations can appear in totals without a Flows run association.

## Upgrade, Rollback & Uninstall

- **Upgrade:** Stages and validates the new `Flows.app`, freezes new claims, journals in-flight provider receipts, backs up the SQLite store and previous bundle, runs migrations, and rolls back automatically if migration or validation fails.
- **Uninstall:** `make uninstall` removes `~/Applications/Flows.app` and `~/.local/bin/flows`; manifest-based `internal/install.Uninstall` additionally unregisters `launchd` and removes `spec/v1` and the plist while preserving user config, workflows, worktrees, and usage data.
- **Manual data removal:** To remove all user data after uninstalling, delete `~/.config/flows`, `~/Library/Application Support/Flows`, `~/Library/Caches/Flows`, and `~/Library/Logs/Flows` (or `$FLOWS_HOME` if overridden).
