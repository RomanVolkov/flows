Address confirmed review findings for request #{{ request_number }} in {{ repository }} at expected head {{ expected_head_sha }}. GitHub enabled: {{ use_github }}. GitLab enabled: {{ use_gitlab }}.

Read the open discussions and CI results for the enabled provider. Compare the live head with the expected head before editing. Escalate a stale or ambiguous target. Read the surrounding source for every finding and classify it as confirmed, already addressed, or unsupported. Check both human and automated reports and avoid repeating resolved work.

Fix confirmed defects within the request's scope. Cover regressions and run the repository's required checks. Record the handled thread IDs, what changed, and what the checks showed. Leave thread resolution and merging to the workflow.

Return head_sha as the observed remote head for the readiness decision. Set ready_for_github or ready_for_gitlab to true only for the enabled provider when the returned head is verified, required approvals and CI are current and passing, and blocking threads are clear. If local changes are awaiting publication or evidence is missing, keep both readiness flags false and report what is pending.
