Cross-review the group plans for this request.

Request
{{ request }}

Group plan results
{{ plans }}

Read each referenced plan and compare the interfaces, file ownership, artifact paths, verification steps, and milestones across groups. Trace every dependency from producer to consumer. Flag missing outputs, incompatible assumptions, overlapping edits, and cycles. Identify independent work that can proceed in parallel. Check that the plans together meet the acceptance criteria for the request.

Report discrepancies with the affected groups and the smallest change that resolves each one. Set approved to true only if all required plans are available and the combined design can proceed. A missing plan or incompatible interface requires approved=false.
