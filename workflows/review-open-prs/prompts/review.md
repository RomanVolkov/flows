Review {{ provider }} request #{{ pr_number }} in {{ repository }} at head {{ head_sha }}. The title is "{{ title }}".

1. Read the description, linked requirements, target branch, changed files, CI results, and review discussions for this revision. If the head has moved, report that the review is stale and set findings_count to 0. Keep confirmed failures, pending checks, and unavailable evidence distinct.
2. Inspect the diff against the target branch, then read the surrounding code and affected callers. Check the user-visible entry points, edge cases, failure paths, compatibility, and whether tests would catch a regression. Identify changes outside the stated scope separately.
3. Read resolved and open discussions, including automated findings. Verify a finding in the code before reporting it. Skip resolved or duplicate issues. Keep the review read-only and leave publication to the workflow.
4. Report each confirmed finding in severity order with a changed file and line, the condition that triggers it, and its impact. Mention blocked verification and any decision that needs user input.

Return findings_count as the number of confirmed findings. Return summary with the reviewed head, CI and thread status, actionable findings, and verification limits. A clean review should say what you checked.
