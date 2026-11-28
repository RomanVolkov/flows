# Workflow Authoring

Workflows use `schema: flows/v1` (`schema/workflow-v1.json`) and are loaded dynamically from YAML without rebuilding `flowsd`.

## Runnable Example

```yaml
schema: flows/v1
id: hello-flow
version: 1.0.0
description: Print a check message for a repository.
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

Add an optional top-level `description` to explain what the workflow does. Flows shows it below the workflow picker in **New → Run a workflow**.

Validate a workflow or tool package with `flows validate <package-directory>`. Add `--check-dependencies` to check installed tools and commands, configured provider credentials, and harness availability. The check covers declared dependencies across the workflow and its subflows, including optional branches. Values that require launch inputs are reported as deferred. Use a read-only check with the actual repository or resource to verify access.

## Built-in Workflows

- `workflows/validate-package` checks the manifest and the declared dependencies of a workflow or tool package.
- `workflows/review-open-prs` finds open GitHub pull requests assigned to your authenticated GitHub user and starts
  an independent review session for each one, using the default harness configured in Settings.
- `workflows/deliver-change` turns a change request into task groups, asks for approval, and has agents draft and
  review plans for the groups using the default harness in Settings. The workflow ends with a review of how the plans fit together.

## Scheduled Workflows

Create a recurring run in **Settings → Scheduled workflows**. Select a ready workflow, enter its typed input values,
then set a five-field cron expression and time zone. Flows stores the schedule locally and the daemon starts a workflow
run for each due occurrence. Remove a schedule from the same section to stop future runs.

## Evidence-Backed Improvements

Run `improve` proposals link user mid-run interventions and failure evidence to a proposed workflow diff, applied only after explicit user approval.
