# Plugin SDK & Contracts

Plugins implement `schema: flows/tool/v1` (`schema/tool-v1.json`) and communicate over versioned NDJSON stdio using `sdk/go` (`sdk.Serve`).

## Manifest & Readiness

Each plugin package includes `tool.yaml` declaring:
- `id`, `version`, `owner`
- `credentials` (provider & required flag)
- `operations` (`execution_kind: plugin`, `read_only`, `input_schema`, `output_schema`)

## Shipped Plugins

- `plugins/git`: `git.prepare-checkout`, `git.push-branch`, `git.cleanup-checkout`
- `plugins/github`: `github.list-pull-requests`, `github.get-pull-request`, `github.open-or-reuse-pr`, `github.reply-comment`, `github.request-review`, `github.merge-pr`
- `plugins/gitlab`: `gitlab.list-merge-requests`, `gitlab.get-merge-request`, `gitlab.open-or-reuse-mr`, `gitlab.reply-discussion`, `gitlab.request-review`, `gitlab.merge-mr`
- `plugins/jira`: `jira.search-issues`, `jira.get-issue`, `jira.transition-issue`
- `plugins/youtrack`: `youtrack.get-issue`, `youtrack.transition-issue`
- `plugins/zulip`: `zulip.open-or-reuse-topic`, `zulip.reply-topic`, `zulip.resolve-topic`

## Callback & Idempotency Contract

Long-running or externally visible operations persist idempotency keys before mutating remote state and reconcile ambiguous crashes via `EffectJournal` without creating duplicate PRs, MRs, transitions, or chat topics.
