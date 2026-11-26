# Flows authoring and exchange specification v1

This is the proposed contract for the first release. The CLI commands and installation paths below are implementation targets. Runtime, CLI, desktop, shipped examples and agent-facing schemas must pass the same conformance fixtures.

## Finding the contract

Install a complete, offline reference under `$XDG_CONFIG_HOME/flows/spec/v1/`, defaulting to `~/.config/flows/spec/v1/`. Ship this guide as `authoring.md`, plus `workflow.schema.json`, `tool.schema.json`, `bundle.schema.json`, an expression and prompt reference, SDK/protocol reference, and runnable examples for a command, a source-built tool, a nested workflow and a fan-out. Include the matching Go SDK source and module metadata for building examples. Install revisions atomically alongside older major versions. Shipped reference files are application-owned; user workflows and tools have independent directories. `flows spec path --version 1` prints the effective path; `flows spec show workflow --format json` emits the schema. The app exposes Open authoring guide from the library and template editor.

An agent authoring a package can read this guide, call `flows catalog list --json`, inspect `flows catalog describe <operation> --json`, create a project directory, run `flows validate <path> --json`, then present it for import. Validation returns stable code, file, line, column, value path and explanation for every independent error; exit zero means valid. Static validation has no provider side effects. Runtime readiness is reported separately.

## Workflow package

Place `workflow.yaml` at the root. Store prompts, assets and optional nested workflow packages under that root. UTF-8 text, relative paths and stable names make a package relocatable. IDs use lowercase ASCII letters, digits and hyphens, beginning with a letter. Catalog names for tool operations have the form `<package-id>.<operation-id>`; `core` belongs to Flows. A workflow call uses `workflow.<id>`. A workflow revision is the content digest of its resolved package. Human-facing versions can accompany it. Dependencies resolve in the author's catalog and are locked by ID, version and digest when captured for execution or distribution.

| Workflow key | Meaning |
|---|---|
| schema, id | Required. Use `flows/v1` and the stable workflow ID. |
| version, description | Optional display version and explanation. Export supplies a content digest even for unpublished packages. |
| inputs | Map of input names to type declarations. Support string, boolean, integer, number, array and object, declared properties/items, enum, required, default and description. Validate defaults at compile time. Collect all missing required values at launch or schedule configuration. |
| nodes | Map keyed by stable step ID. Each node supplies `uses`, optional `with`, `needs`, `if`, `for_each`, `profile` and retry/timeout options. Validate bindings against the selected operation or child workflow. Map ordering carries no execution semantics. |
| outputs | Map of public result names to typed bindings. Infer types from references or require an explicit type when inference fails. |
| milestones | Map a public milestone name to a step completion or named child milestone. A caller may wait on a milestone while the called workflow is still running. |

```yaml
schema: flows/v1
id: review-open-prs
version: 1.0.0
inputs:
  repository: {type: string, required: true}
  reviewer: {type: string, default: codex}
nodes:
  discover:
    uses: github.list-pull-requests
    with:
      repository: ${{ inputs.repository }}
      state: open
  review:
    uses: core.harness
    for_each:
      items: ${{ nodes.discover.outputs.pull_requests }}
      key: ${{ item.id }}
    with:
      harness: ${{ inputs.reviewer }}
      prompt_template: ./prompts/review.md
      arguments:
        pull_request: ${{ item }}
    outputs:
      approved: {type: boolean, required: true}
      findings: {type: array, items: {type: string}, required: true}
outputs:
  reviews: ${{ nodes.review.outputs }}
```

Use a whole scalar `${{ path }}` for a typed binding. It preserves the type, including arrays and objects. Supported roots are `inputs`, `nodes.<step>.outputs` and the current collection `item`. Conditions support equality, inequality, boolean AND/OR/NOT and parentheses. Unknown paths, implicit conversions and cycles are errors. `needs: [plan]` waits for successful completion; `needs: [api@merged]` waits for a public milestone on the step named api. Milestone declarations use `milestones: {merged: merge}` or `{merged: child@merged}`. Data dependencies are inferred in addition to explicit control dependencies. Referencing a potential skipped output requires a conditional consumer or an explicitly optional input. A skipped producer emits no success milestone; mark downstream unreachable branches skipped rather than leaving a perpetual wait.

Collection inputs must be arrays; keys must be unique, stable strings or integers in the occurrence. Create one child per item and persist the expansion before dispatch. Empty arrays complete successfully. A join returns results in original item order, independent of finish time. Keep failed items visible, and use declared retry behavior before failing the enclosing step. For a bounded review loop, attach a repeat block to a step calling a workflow. Each iteration gets fresh occurrence IDs. The child returns typed outputs, which are visible to the loop condition as result. When the condition succeeds, expose that iteration’s outputs downstream. After max_iterations unsuccessful iterations, persist a Needs attention state with the last output. Retries for transient errors belong to the same iteration. For provider polling, attach a `watch` block to a read-only operation. Each poll uses the ordinary invoke/result protocol and exits. The engine owns the timer and persists the last observation. For example:

```yaml
nodes:
  wait-for-merge:
    uses: gitlab.read-merge-request
    with:
      project: ${{ inputs.project }}
      iid: ${{ inputs.iid }}
    watch:
      interval: 2m
      deadline: 72h
      until: ${{ result.merged == true }}
```

The first read runs immediately. A successful predicate publishes that observation as the step outputs. Otherwise the engine records the next wake and releases the slot. The deadline is relative to watch start, includes time asleep, and moves the step to Needs attention on expiry. For incremental reads add `cursor: {from: next_cursor, to: after}` under `watch`. The first invocation uses the declared default for the optional input `after`. Each subsequent invocation receives the last committed `next_cursor` value. Validate matching types before launch. Resume a due watch after restart with one pending poll. Provider errors follow the operation retry policy and preserve the last successful cursor.

Store prompt text in a UTF-8 relative file. Use Go text/template with missing keys treated as errors. For the example, a prompt can read `Review pull request {{ .pull_request.id }} using its current diff.` Resolve and record the template digest, typed arguments and rendered content on each attempt. Define templates in either a workflow or a tool package; resolve relative to the owning package. Reject escaping paths. Use credential references and explicit credential-aware operations for secrets.

### Composition, retries and prompts

This fragment declares one nested workflow call and a named public milestone. The child workflow accepts a plan artifact, updates it in the run workspace, and returns a boolean called approved. The edit is the input to the next iteration. A retry restarts a failed attempt, while an iteration follows a successful attempt whose output requests more work.

```yaml
nodes:
  cross-review:
    uses: workflow.review-and-fix-plan
    with:
      plan: ${{ inputs.plan }}
    repeat:
      max_iterations: 3
      until: ${{ result.approved == true }}
    retry:
      max_attempts: 2
      backoff: 30s
    timeout: 20m
milestones:
  plan-approved: cross-review
```

A step defaults to one attempt. An author can declare a positive attempt limit, bounded backoff duration and an execution timeout. A timeout requests cancellation, waits for the runtime acknowledgement and records the attempt outcome. Retry externally visible operations only when their recorded effect can be reconciled. For v1 combine iteration with collection processing by putting the loop in a child workflow and mapping the parent over that child. Each invocation receives its own artifacts and iteration count. A schedule supplies its own bindings and time zone outside the portable workflow definition.

A workflow author can reference an asset in the same package by a relative path. A plugin may export a named prompt by a manifest mapping such as `prompts: {review: ./prompts/review.md}`. A harness node can then use `prompt_template: tool://text-tools/prompts/review` if that prompt is declared. Resolve the owner from the locked revision and include it during export even if the workflow calls no operation from that plugin. Expose the final prompt preview with input provenance before launch.

### Declaring a harness result

For a workflow step using `core.harness`, choose the form that matches what the next step consumes.

| Step declaration | What the harness submits | What downstream nodes can use |
|---|---|---|
| Omit `outputs` | An explicit completion with no result data | A dependency such as `needs: [implement]` can wait for success. A named output reference is a compile error. |
| Set `outputs` to a map of named fields | An object matching those declarations | A single review exposes `nodes.review.outputs.approved` and `nodes.review.outputs.findings`. With fan-out, `nodes.review.outputs` is an ordered array of review objects. |
| Set `outputs` to a local schema reference | An object matching the referenced JSON Schema | Declared object properties become the same typed ports as inline fields. The schema travels with the package. |

For example, a grouping harness can use the following step declaration:

```yaml
group-tickets:
  uses: core.harness
  with:
    harness: claude
    prompt_template: ./prompts/group-tickets.md
  outputs:
    $ref: ./schemas/task-groups.json
```

Use either a standalone `$ref` or inline fields. The compiler resolves package-relative schema paths and their transitive local references, validates that the root result represents an object, and pins the resolved contract with the workflow revision. Report missing files, paths outside the package and unsupported schema constructs at validation. The live editor and all three adapters use the same resolved contract. Top-level workflow `outputs` still maps public names to bindings from its steps.

### Reporting harness completion

The bridge exposes the pinned result contract to the harness. The host validates the payload locally before recording the event, including while the daemon is offline. A shape error returns the field path and expected type to the running harness, leaves the attempt open and issues no receipt. The harness can correct its submission in the same turn. Once a receipt is issued, retries use the same event ID and payload.

A completion receipt can say **saved locally** while the daemon is offline. The host has durably captured the submission and the harness may finish its turn. Flows records **accepted** or **rejected** after daemon validation. Dependencies wait for acceptance. Preserve the original event ID when retrying the same submission. The run inspector shows the final disposition and any rejection reason. A local storage error means the submission still needs to be retried.

## Tool package and executable contract

A tool package has `tool.yaml`, its implementation, any `go.mod` and `go.sum` required by the build, optional prompts/workflows, and optionally vendored dependencies. One executable can implement multiple catalog operations. This example is a deterministic tool whose input and output can be checked offline.

```yaml
schema: flows/tool-v1
id: text-tools
version: 1.0.0
description: Deterministic text operations
build:
  language: go
  package: ./cmd/text-tools
operations:
  count-lines:
    description: Count logical lines in UTF-8 text
    inputs:
      text: {type: string, required: true}
    outputs:
      count: {type: integer}
```

Implement the declared operation in a Go main package using the shipped SDK. The implementation receives a context, invocation ID, typed input and a scoped Flows client. Return the declared output or a structured error. For this example, empty text yields 0, a nonempty final line counts once, and a trailing newline does not add another line. Ship a complete buildable main, module files and expected output fixture in the SDK example. The SDK wraps the following protocol for any compatible executable.

| Frame | Contract |
|---|---|
| Startup | Parent launches the pinned executable with a private callback endpoint and invocation scope. Negotiate protocol major, package ID, version and operations using hello/ready. Stop on mismatch before dispatch. |
| Invoke | One JSON object per UTF-8 line on stdin/stdout. Every frame carries `type`, `protocol` and `id`. Invoke additionally carries operation and inputs. Example: `{"type":"invoke","protocol":1,"id":"i-1","operation":"text-tools.count-lines","inputs":{"text":"abc"}}`. |
| Observe and finish | Progress frames may repeat. One result or error terminates the invocation. Example: `{"type":"result","protocol":1,"id":"i-1","outputs":{"count":1}}`. Errors include code, message and retryability. Diagnostics use stderr. The daemon checks the result schema and associates any artifacts with the attempt. |
| Callback and cancel | The scoped API supports progress, question, artifact, notification and authorized graph actions. Durable questions release the appropriate scheduler resource. On cancellation request cooperative shutdown, then terminate the process group after the configured grace period. Deduplicate effects by invocation ID. |

The manifest may also declare executable/version requirements, credential reference names, supported read-only readiness probes, named prompt templates and bundled workflow paths. Show all required external programs and accounts at import. Importing a source tool requires the user to select it for build/activation. A catalog listing or archive preview never executes package code. A direct call under a harness and a scheduled node use the same schema, implementation, deadlines and result validation.

### Requirements and readiness

An author can add requirements to the manifest and narrow them for an individual operation. The following fragment declares a machine dependency and an account dependency. The credential name identifies material supplied by the recipient. Configure its backing store in Settings.

```yaml
requirements:
  executables:
    - {id: git, command: git, version_args: [--version]}
  credentials:
    - {id: tracker-api, description: Read access to the selected tracker}
operations:
  find-issues:
    description: Find issues matching a saved query
    requires: [tracker-api]
    inputs:
      project: {type: string, required: true}
    outputs:
      issues: {type: array, items: {type: object}}
```

Package requirements form the inventory; an operation with an explicit requires list needs the named subset, otherwise it inherits all package requirements. A static package check validates names and schema without contacting a service. A user-triggered live check may call the plugin’s declared read-only probe. Emit Ready, Missing prerequisite, Needs authentication or Unverified with a timestamp, short explanation and suggested repair. Keep operations with distinct requirements independently usable.

## Portable .flows exchange

The canonical transport file is a ZIP archive with extension `.flows`. An ordinary unpacked folder with the same manifest is a valid authoring/import source. Preview its declared entry workflow or tool and dependency closure before anything is activated. Finder Open With, drag-and-drop, the library Import action and the CLI use the same importer.

```text
review-open-prs.flows
  bundle.json
  workflows/review-open-prs/workflow.yaml
  workflows/review-open-prs/prompts/review.md
  tools/github/tool.yaml
  tools/github/go.mod
  tools/github/cmd/plugin/main.go
  ...other locked package files, assets, module sources and licenses
```

`bundle.json` includes format `flows/bundle-v1`, entry kind and ID, engine protocol range, packages with versions/digests/relative roots, built-in requirements, external requirements, and a path/size/SHA-256 index covering every payload file. A package lock describes transitive workflow and tool references. Normalize entry order, timestamps and permissions for deterministic export. Check hashes for integrity; they do not establish publisher identity.

1. Export the selected definition revision, walk all static nested workflows, catalog references, prompts, schemas and package assets. Copy source and actual vendored dependencies or an indexed offline module cache for source-built tools, including the matching SDK. Module version and checksum entries alone do not supply the bytes required to build. Include required licenses. Refuse an incomplete portable export and list missing assets or dependencies. Built-ins and installed harnesses are declared version/capability requirements. Dynamic graph discovery depends on the recipient's catalog; allow the author to explicitly include additional candidate packages and show that boundary in preview.
2. Include the code and configuration needed to reconstruct the workflow on a compatible installation. List Git, provider CLIs, a Go compiler where needed, accounts, service access and credential references as recipient prerequisites. Exclude user tokens, live sessions, private transcripts, working repositories, machine-specific paths and schedules' private bindings. A portable bundle can therefore remain runnable with different credentials on the recipient machine.
3. On import, parse the manifest under bounded archive size, entry count and expansion limits. Reject absolute paths, traversal, symlinks, duplicate or case-colliding paths and unexpected entries. Verify file hashes, schemas, versions, closed dependency graph and package IDs **before** writing to the library or starting a build.
4. Show New / Identical / Conflict per package, expected tools, external requirements, and missing credentials. Reuse identical content. Let the user keep the current revision or explicitly install a conflicting revision side by side in the immutable digest-keyed package store without overwriting the user’s editable working copy. Keep the existing active alias unless the user chooses to switch it. Resolve references inside the imported workflow to its lock. Display unsupported engine and SDK versions without partial activation.
5. Stage and validate all packages, build opted-in tools and atomically publish one catalog generation. Use a durable import transaction journal to recover if the process dies between file promotion and DB commit. Failed/cancelled imports preserve the previous active catalog and retain diagnostics. Any unavailable prerequisite remains visible in readiness.
6. Verify with `flows export workflow review-open-prs --output review-open-prs.flows`, transfer only that file, `flows import --preview review-open-prs.flows`, import, then validate and run in a fresh home. The conformance suite includes an offline nested-workflow/tool-source round trip with a locally installed compiler, an initially empty Go module cache and GOPROXY=off, a corrupt hash, a conflicting revision, an interrupted import, and an archive exported a second time with identical contents.
