# Prompt Spec

## A. Execution-Agent Prompt (Must Contain)
1. Role and objective
2. Strengths and capability boundaries
3. In-scope / out-of-scope
4. Inputs (confirmed bug detail + current code state + failure history)
5. Constraints (no unrelated edits, no process restart unless requested)
6. Required outputs:
   - file changes
   - evidence/log paths
   - commit message proposal
   - TAPD HTML backfill body
7. Forbidden actions

## B. Acceptance-Agent Prompt (Must Contain)
1. Independent auditor role
2. Strengths and capability boundaries
3. Validation checklist (itemized)
4. Evidence requirements for each checklist item
5. Output format per item:
   - Acceptance Content
   - Acceptance Process
   - Acceptance Result (PASS/FAIL + evidence)
6. Final status suggestion rule

## C. Iteration Rule (After FAIL)
- New execution prompt must include:
  1) confirmed bug detail
  2) latest FAIL reasons
  3) prior round changes and residual gaps
- New acceptance prompt must include:
  1) regression checks for previous fail points
  2) full checklist rerun with evidence
