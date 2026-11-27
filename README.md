# Flows

Local macOS desktop & CLI workflow orchestrator for AI coding harnesses (`claude`, `codex`, `agy`), deterministic commands, and provider plugins (`git`, `github`, `gitlab`, `jira`, `youtrack`, `zulip`).

## Homebrew

For Apple Silicon on macOS 14 or later, add the release tap to your Brewfile:

```ruby
tap "romanvolkov/flows", "https://github.com/RomanVolkov/flows.git"
cask "romanvolkov/flows/flows"
```

Run `brew bundle` to install, or `brew upgrade --cask romanvolkov/flows/flows` to update. The user-domain package installs Flows.app in ~/Applications, links the CLI, and registers the per-user LaunchAgent. Updates restart the daemon. Its installer clears the quarantine attribute on the installed app.

Pushing a stable `vMAJOR.MINOR.PATCH` tag builds and smoke-tests the installer, publishes it to GitHub Releases, then updates the checksummed cask on the default branch. The workflow needs permission to push that cask update. The first successful release creates the tap entry.

## Quickstart

1. Verify toolchain prerequisites:
   ```sh
   make doctor
   ```
2. Build core binaries (`flows`, `flowsd`, `flows-host`) and plugins:
   ```sh
   make build plugins
   ```
3. Run unit tests, `go vet`, `staticcheck`, desktop checks, and integration tests:
   ```sh
   make check desktop-check integration
   ```
4. Package and install `~/Applications/Flows.app` and `~/.local/bin/flows`:
   ```sh
   make package install
   ```

Run `make ui-check` for the WebKit UI tests alone. They load the production frontend with a mocked Wails bridge. Fixtures and interaction tests live in `desktop/app/frontend/tests/ui`. The first run downloads the test browser.

## CLI & Build Targets

- `flows`: `version`, `status`, `shutdown`, `validate <workflow.yaml>`, `spec {path|show [authoring|protocol|workflow|tool]}`
- `flowsd`: `run`, `--foreground`
- `flows-host`: `serve`, `attach`
- `Makefile`: `build`, `plugins`, `desktop`, `package`, `install`, `install-spec`, `uninstall`, `check`, `desktop-check`, `integration`, `doctor` (plus `build/macos.mk`: `deps`, `fonts`, `libghostty`, `terminal-probe`, `terminal-probe-check`)

## Prerequisites

- **Source builds:** Go (pinned in `build/toolchain.lock`), Xcode Command Line Tools (`clang`), Node.js with npm for the Svelte frontend, and `staticcheck`.
- **Packaged app (`Flows.app`):** Self-contained Go binaries and prebuilt plugins; no compiler required at runtime. Optional provider CLIs (`gh`, `glab`, `git`) and coding harnesses (`claude`, `codex`, `agy`) are configured in Settings or `PATH`.

## Documentation

- [Operations & Lifecycle](docs/operations.md)
- [Workflow Authoring](docs/workflows.md)
- [Plugin SDK & Contracts](docs/plugins.md)
- [Authoring Spec v1](spec/v1/authoring.md)
- [Protocol Spec v1](spec/v1/protocol.md)
