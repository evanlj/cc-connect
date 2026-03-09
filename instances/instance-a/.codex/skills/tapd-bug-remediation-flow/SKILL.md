---
name: tapd-bug-remediation-flow
description: "Drive TAPD bug/defect remediation with a strict gated loop: fetch bug -> clarify defect with user until final conclusion -> sync final conclusion to bug detail -> generate and confirm execution prompt -> backfill prompt -> run fix agent -> generate and confirm acceptance prompt -> backfill prompt -> run acceptance -> if FAIL backfill causes and iterate. Use when users ask to fix TAPD defects/缺陷 with mandatory prompt confirmation and multi-round rework."
---

# TAPD Bug Remediation Flow

Keep defect repair deterministic, auditable, and user-confirmed.
Do not skip any gate.

## Hard Gates (Non-negotiable)
1. Do not start repair before defect conclusion is confirmed by user.
2. Do not start execution agent before execution prompt is confirmed and backfilled.
3. Do not start acceptance agent before acceptance prompt is confirmed and backfilled.
4. If acceptance fails, backfill failure causes first, then start next round.
5. Use TAPD HTML-visible format in all backfills: `<p>`, `<br  />`, `<ul><li>`, `<code>`.

## Step 1 - Fetch Bug Baseline
- Read TAPD bug detail and comments.
- Capture: current status, reopen reasons, latest owner, linked story/task.
- Never execute from oral summary alone.

## Step 2 - Clarify Defect with User (Loop)
- Produce a defect-clarification draft using:
  - observed behavior
  - expected behavior
  - actual behavior
  - stable reproduction steps
  - scope and impact
  - explicit acceptance criteria
- Ask user to refine/confirm.
- Repeat until user explicitly confirms final conclusion.

Load `references/defect-conclusion-template.md` when drafting this section.

## Step 3 - Sync Final Defect Conclusion to Bug Detail
- Update TAPD bug **description** with the confirmed final conclusion.
- Add a TAPD comment: "final defect conclusion confirmed and synced to detail".
- Treat this as the single source of truth for later rounds.

## Step 4 - Build Execution-Agent Prompt
- Generate detailed execution prompt from bug detail (not memory guesses).
- Prompt must include:
  - executor role
  - strengths and capability boundaries
  - in-scope / out-of-scope
  - hard constraints
  - required deliverables and evidence anchors
  - forbidden operations

Load `references/prompt-spec.md` when generating this prompt.

## Step 5 - Confirm Execution Prompt with User (Loop)
- Discuss and revise prompt until user explicitly confirms.
- Do not dispatch execution agent before confirmation.

## Step 6 - Backfill Execution Prompt to TAPD (Mandatory)
- Backfill confirmed execution prompt to TAPD comment.
- Include version tag (e.g., v1/v2) for traceability.

Load `references/tapd-comment-templates.md` for HTML format.

## Step 7 - Dispatch Execution Agent and Backfill Result
- Dispatch execution agent only after Step 6.
- Backfill:
  - changed files
  - key fix points
  - commit id / branch
  - logs and evidence paths
  - known risks / pending checks

## Step 8 - Build Acceptance-Agent Prompt
- Generate acceptance prompt from:
  - confirmed bug detail
  - actual implementation changes
  - required acceptance checklist
- Require itemized verdict: content / process / result(PASS|FAIL) + evidence.

## Step 9 - Confirm Acceptance Prompt with User (Loop)
- Discuss and revise until user explicitly confirms.
- Do not dispatch acceptance agent before confirmation.

## Step 10 - Backfill Acceptance Prompt to TAPD (Mandatory)
- Backfill confirmed acceptance prompt to TAPD comment.
- Keep prompt version history traceable.

## Step 11 - Dispatch Acceptance Agent and Backfill Result
- Execute acceptance independently.
- Backfill itemized acceptance for every checklist item:
  1) Acceptance Content
  2) Acceptance Process
  3) Acceptance Result (PASS/FAIL + evidence)

## Step 12 - FAIL Remediation Loop
- If acceptance FAIL:
  - keep in-progress/reopened status (per workflow)
  - backfill failure causes as mandatory vs suggestion items
  - start next round with combined inputs:
    - confirmed bug detail
    - latest FAIL causes
    - prior fix evidence
  - regenerate execution prompt, reconfirm with user, backfill, rerun
  - regenerate acceptance prompt, reconfirm with user, backfill, rerun
- Repeat until PASS.

## Step 13 - PASS Transition
- If PASS:
  - backfill final acceptance summary
  - set bug status to resolved (and follow verified/closed policy if required)
  - record final trace: jobId, log path, TAPD comment ids, status transition result.

## Resource Files
- `references/defect-conclusion-template.md`: confirmed bug definition template.
- `references/prompt-spec.md`: execution/acceptance prompt specification.
- `references/tapd-comment-templates.md`: HTML templates for TAPD backfills.
