Break down this request into delivery groups.

Request
{{ request }}

Repositories
{{ repositories }}

Read the available requirements and confirm which repository owns each change. Group tickets that share a deliverable or must be verified together. Split work that can be implemented and verified independently. Name the exact artifact or interface that another group must wait for. Use a milestone only when it unlocks concrete downstream work.

Return a groups array following schemas/task-groups.json. Give each group a stable id, one testable goal, its repository, the source ticket IDs, and a reason for the grouping. Record dependencies as group_id and milestone pairs. Each ticket should appear once, and dependencies must refer to groups in the result without forming a cycle. Surface missing ticket or repository information before assigning the work.
