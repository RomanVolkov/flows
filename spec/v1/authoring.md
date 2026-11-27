# Flows Authoring & Exchange Specification (v1)

Schema versions:
- Workflow: `flows/v1` (`workflow.schema.json`)
- Tool: `flows/tool/v1` (`tool.schema.json`)
- Plugin Protocol: `1` (`protocol.md`)

## Harness Output Forms (`core.harness`)
1. **Omitted `outputs`**: step signals completion only.
2. **Named field map**: `outputs: { approved: {type: boolean, required: true}, findings: {type: array, items: {type: string}} }`.
3. **Standalone local `$ref`**: `outputs: { $ref: ./schemas/result.json }`.

## Offline Agent Authoring Procedure
1. Read `authoring.md` and `protocol.md` from `flows spec path --version 1`.
2. Inspect available operations via `flows catalog list --json` and `flows catalog describe <id> --json`.
3. Author `workflow.yaml` or `tool.yaml` (using the local `sdk/go` module reference in `examples/sdk-tool/go.mod`).
4. Run `flows validate "<dir>" --json` for each tool or workflow package. The directory contains the manifest. Fix reported diagnostics and rerun until the exit code is zero and the report has `valid: true`. This checks package compatibility. Also run `flows validate "<dir>" --json --check-dependencies` to check declared CLIs, credential configuration, active plugin builds, and harnesses. Put external command names in tool manifests under `required_clis`. Verify resource access with representative inputs and read-only checks.
5. Import the validated package from **New → Import a workflow or external tool**.

The bundled `validate-package` workflow runs the same validator as a tool node. Pass the local package directory as its `directory` input. The command emits the JSON diagnostic report and exits nonzero for an invalid package.
