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
- `workflows/review-open-prs` lists open GitHub pull requests or GitLab merge requests, runs a review agent for each,
  and sends a notification when the reviews finish.
- `workflows/deliver-change` turns a change request into task groups, asks for approval, and has agents draft and
  review plans for the groups. You can edit the YAML and prompts to adapt it to your process.

## Evidence-Backed Improvements

Run `improve` proposals link user mid-run interventions and failure evidence to a proposed workflow diff, applied only after explicit user approval.
