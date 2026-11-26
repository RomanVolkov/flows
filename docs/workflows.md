# Workflow Authoring

Workflows use `schema: flows/v1` (`schema/workflow-v1.json`) and are loaded dynamically from YAML without rebuilding `flowsd`.

## Runnable Example

```yaml
schema: flows/v1
id: hello-flow
version: 1.0.0
inputs:
  repo:
    type: string
    required: true
nodes:
  check:
    uses: core.command
    with:
      command: /bin/echo
      args: ["checking", "${{ inputs.repo }}"]
outputs:
  done: ${{ nodes.check.outputs.exit_code }}
```

Validate any workflow with `flows validate <path/to/workflow.yaml>`.

## Built-in Workflows

- `workflows/review-open-prs`: Scheduled PR/MR discovery and per-item harness review with repository/head deduplication and schedule overlap protection.
- `workflows/deliver-change`: Five-stage intake → group planning (`flows/plan-group.yaml`) → plan reconciliation → implementation (`flows/implement-group.yaml`) → review/merge watch (`flows/watch-change.yaml`). All steps, prompts, and provider bindings can be edited in YAML without recompiling `flowsd`.

## Evidence-Backed Improvements

Run `improve` proposals link user mid-run interventions and failure evidence to a proposed workflow diff, applied only after explicit user approval.
