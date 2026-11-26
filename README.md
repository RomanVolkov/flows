# Flows

Local macOS desktop & CLI workflow orchestrator for AI coding harnesses (`claude`, `codex`, `agy`), deterministic commands, and provider plugins (`git`, `github`, `gitlab`, `jira`, `youtrack`, `zulip`).

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

## Prerequisites

- **Source builds:** Go (pinned in `build/toolchain.lock`), Xcode Command Line Tools (`clang`), and `staticcheck`.
- **Packaged app (`Flows.app`):** Self-contained Go binaries and prebuilt plugins; no compiler required at runtime. Optional provider CLIs (`gh`, `glab`, `git`) and coding harnesses (`claude`, `codex`, `agy`) are configured in Settings or `PATH`.

## Documentation

- [Operations & Lifecycle](docs/operations.md)
- [Workflow Authoring](docs/workflows.md)
- [Plugin SDK & Contracts](docs/plugins.md)
- [Authoring Spec v1](spec/v1/authoring.md)
- [Protocol Spec v1](spec/v1/protocol.md)
