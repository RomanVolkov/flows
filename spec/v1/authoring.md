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
4. Validate statically with `flows validate <dir> --json` until exit code is 0.
5. Import into the user library with `flows import <dir>`.
