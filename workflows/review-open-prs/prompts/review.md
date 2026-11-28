Review GitHub pull request #{{ pr_number }} in {{ repository }}: {{ title }}
PR URL: {{ url }}
Expected head: {{ head_sha }}

This is an independent, read-only review. Complete the review, summarize findings in the conversation, then keep this harness session open so the user can continue chatting about this PR. Do not exit after the initial review unless the user ends the session.

If the workspace does not already contain this repository, clone it with `gh repo clone {{ repository }} pr-{{ pr_number }}` and work in that directory. Check out the PR with `gh pr checkout {{ pr_number }} --repo {{ repository }}`. Confirm the checked out head matches the expected SHA. If it moved, report the review as stale and do not present findings for the newer revision as if they applied to this one.

Read the PR description, linked requirements, target branch, changed files, CI results and open review discussions. Compare the diff with its target branch, then inspect relevant surrounding code and callers. Check user-visible paths, edge cases, failures, compatibility and whether tests would catch regressions. Keep confirmed failures, pending checks and unavailable evidence distinct. Verify each finding in code; skip resolved and duplicate comments. Identify out-of-scope changes separately.

Return findings in severity order. For each confirmed finding, give the changed file and line, triggering condition and impact. Include the reviewed head, CI and discussion status, checks performed, blocked verification and any decision needing the user's input. A clean review should say what you checked. Do not publish comments, change code, push branches or modify the pull request.
