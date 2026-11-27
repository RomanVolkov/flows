Review implementation group {{ group_id }} in {{ checkout_path }} against its plan at {{ plan_path }}. Work read-only.

Compare the checkout with {{ base_branch }} and include uncommitted changes. Read the relevant source and tests around each changed call path. Check correctness against the plan, handling of failures and retries, compatibility with existing callers, and regression coverage. Identify unrelated changes. Verify every finding against actual behavior before reporting it.

List actionable findings in severity order with file and line, triggering conditions, and impact. Record verification gaps separately. Set approved to true only when the available evidence supports the required behavior and no blocking findings remain.
