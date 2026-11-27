Independently review the plan for group {{ group_id }} at {{ plan_path }}. Work read-only.

Read the whole plan and verify important claims against the repository. Check that it addresses the stated goal through the real entry points, uses the project conventions, and accounts for existing callers, old data, failure paths, and retries where applicable. Check that tasks can run in order, interfaces and dependencies are explicit, and verification would detect a broken implementation. Flag unrelated scope and unnecessary complexity that would put delivery at risk.

Report every blocking issue once. Reference the plan section and source evidence, describe the consequence, and suggest a correction. Set approved to true only when there are zero blocking issues. Set issues_count to the number of blocking issues you reported.
