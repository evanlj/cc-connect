# TAPD Workflow Checklist

## Pre-Execution
- [ ] Story fetched from TAPD (ID, status, description).
- [ ] Planning model confirmed by user (manual selection; no defaults; used to draft Implementation Plan + DoD).
- [ ] Main model confirmed by user.
- [ ] Acceptance model confirmed by user.
- [ ] Implementation plan + DoD generated and user-confirmed.
- [ ] Plan + DoD backfilled to TAPD (HTML format).
- [ ] (If plan is complex) Detailed plan written to repo file (Context Pack) and its path backfilled to TAPD.
- [ ] Main prompt generated and user-confirmed.
- [ ] Acceptance prompt generated and user-confirmed.
- [ ] Both prompts backfilled to TAPD (HTML format).
- [ ] Dynamic prompts follow role contracts (`references/prompt-spec.md`):
  - Planner Prompt Contract
  - Execution Prompt Contract
  - Acceptance Prompt Contract
- [ ] Prompt Quality Gate passed before confirmation (Fast Lane block + constraints + output schema + stop condition).

## Main Task Execution
- [ ] Execution scope and path boundaries declared.
- [ ] Output artifacts generated (code/resources/docs/logs).
- [ ] Evidence paths recorded.
- [ ] Main-task result backfilled to TAPD.

## Acceptance Execution
- [ ] Acceptance run independently.
- [ ] Every acceptance item includes:
  - acceptance content
  - acceptance process
  - acceptance result (PASS/FAIL + evidence)
- [ ] Acceptance summary includes PASS/FAIL counts and status suggestion.
- [ ] Acceptance result backfilled to TAPD.

## Decision Defaults (Anti-Detour)
- [ ] Overall result is **PASS/FAIL only** (no BLOCK / PARTIAL / UNKNOWN).
- [ ] If any required item is **not executed** or **missing evidence**, mark that item as **FAIL** and set overall result to **FAIL**.
  - Explain “pending CI / blocked by env” in process/evidence, but keep result binary.
- [ ] Status suggestion must be derived from overall result only:
  - PASS -> `status_6`
  - FAIL -> `status_4`
- [ ] Do not spend time on non-critical cleanup/format perfection unless it blocks execution or is required by DoD.

## Test Case Menu
- [ ] Test steps written to TAPD test-case menu (not comment-only).
- [ ] HTML formatting used (`<p>/<br/>/<ul><li>/<code>`).
- [ ] Story-to-testcase relations created.
- [ ] Test case IDs and relation IDs backfilled.

## Status Transition
- [ ] PASS -> `status_6`.
- [ ] FAIL -> `status_4` and remediation loop started.

## Round Traceability
- [ ] `jobId` recorded.
- [ ] Log path recorded.
- [ ] TAPD comment IDs recorded.
- [ ] Status change result recorded.
- [ ] Process verification note recorded.
