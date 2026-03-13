---
name: tapd-bug-remediation-flow
description: "Drive TAPD bug/defect remediation with a strict gated loop: fetch bug -> clarify defect with user until final conclusion -> sync final conclusion to bug detail -> align fix plan & DoD -> generate and confirm execution prompt -> backfill -> run fix -> generate and confirm acceptance prompt -> backfill -> run acceptance -> if FAIL backfill causes and iterate. Use when users ask to fix TAPD defects/缺陷 with mandatory confirmations and multi-round rework."
---

# TAPD Bug Remediation Flow

Keep defect repair deterministic, auditable, and user-confirmed.
Do not skip any gate.

## Hard Gates (Non-negotiable)
1. Do not start repair before defect conclusion is confirmed by user.
2. Do not generate fix plan before the **plan model** is confirmed by user (manual selection; no defaults).
3. Do not generate prompts / touch code before **fix plan + DoD** is aligned with user and backfilled.
4. Do not start execution agent before execution prompt is confirmed and backfilled.
5. Do not start acceptance agent before acceptance prompt is confirmed and backfilled.
6. If acceptance fails, backfill failure causes first, then start next round.
7. Use TAPD HTML-visible format in all backfills: `<p>`, `<br  />`, `<ul><li>`, `<code>`.

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

## Step 4 - Align Fix Plan & DoD (Mandatory)
> This gate is mandatory even if the defect is "small".

Goal: before generating prompts, align *how we will fix* and *what counts as done*.

### Context Pack (Repo Fix Plan Doc) for Complex Fixes
If the fix plan is long/complex and may exceed prompt context:
- Write the detailed plan into a **repo file** (recommended, versionable), e.g.:
  - `.tapd/plan/bug-{{bug_id}}-fix-plan-v{{plan_version}}.md`
- Keep the TAPD comment as the **baseline index** and include the **plan doc path**.

Must output (concise but concrete):
- **Fix Plan** (recommended option + alternatives):
  - target modules / files (expected touch list)
  - safety policy (what must not change / forbidden scope expansion)
  - data / compatibility considerations (if any)
  - rollback strategy (how to revert safely)
- **DoD (Definition of Done)**:
  - acceptance criteria mapping -> evidence type for each item
  - regression / side-effect checks (minimum set)
  - "code-side evidence" vs "runtime smoke" ownership split (if applicable)
- **Risk checklist** (top 3 risks + mitigations)

Hard gate:
- Confirm the **plan model** used to draft the Fix Plan + DoD (can be the same as execution model, but must be explicit).
- Ask user to confirm the Fix Plan + DoD.
- After confirmation, **backfill Fix Plan + DoD to TAPD as an independent comment** (HTML format) before building prompts.
- The backfill must explicitly record the **plan model** used to draft the plan.
  - If a repo fix plan doc is used, the backfill must also include the **plan doc path**.

Load `references/tapd-comment-templates.md` for HTML format.

## Step 5 - Build Execution-Agent Prompt
- Generate detailed execution prompt from bug detail (not memory guesses).
- Prompt must be aligned with the confirmed Fix Plan + DoD baseline; do not drift without re-confirming Step 4.
- Prompt must include:
  - executor role
  - strengths and capability boundaries
  - in-scope / out-of-scope
  - hard constraints
  - required deliverables and evidence anchors
  - forbidden operations

Load `references/prompt-spec.md` when generating this prompt.

## Step 6 - Confirm Execution Prompt with User (Loop)
- Discuss and revise prompt until user explicitly confirms.
- Do not dispatch execution agent before confirmation.

## Step 7 - Backfill Execution Prompt to TAPD (Mandatory)
- Backfill confirmed execution prompt to TAPD comment.
- Include version tag (e.g., v1/v2) for traceability.

Load `references/tapd-comment-templates.md` for HTML format.

## Step 8 - Dispatch Execution Agent and Backfill Result
- Dispatch execution agent only after Step 7.
- Backfill:
  - changed files
  - key fix points
  - commit id / branch
  - logs and evidence paths
  - known risks / pending checks

## Step 9 - Build Acceptance-Agent Prompt
- Generate acceptance prompt from:
  - confirmed bug detail
  - confirmed Fix Plan + DoD baseline
  - actual implementation changes
  - required acceptance checklist
- Require itemized verdict: content / process / result(PASS|FAIL) + evidence.

## Step 10 - Confirm Acceptance Prompt with User (Loop)
- Discuss and revise until user explicitly confirms.
- Do not dispatch acceptance agent before confirmation.

## Step 11 - Backfill Acceptance Prompt to TAPD (Mandatory)
- Backfill confirmed acceptance prompt to TAPD comment.
- Keep prompt version history traceable.

## Step 12 - Dispatch Acceptance Agent and Backfill Result
- Execute acceptance independently.
- Backfill itemized acceptance for every checklist item:
  1) Acceptance Content
  2) Acceptance Process
  3) Acceptance Result (PASS/FAIL + evidence)

## Step 13 - FAIL Remediation Loop
- If acceptance FAIL:
  - keep in-progress/reopened status (per workflow)
  - backfill failure causes as mandatory vs suggestion items
  - if next-round fix approach deviates from the confirmed Fix Plan/DoD baseline -> go back to **Step 4** and re-confirm + re-backfill
  - start next round with combined inputs:
    - confirmed bug detail
    - latest FAIL causes
    - prior fix evidence
  - regenerate execution prompt, reconfirm with user, backfill, rerun
  - regenerate acceptance prompt, reconfirm with user, backfill, rerun
- Repeat until PASS.

## Step 14 - PASS Transition
- If PASS:
  - backfill final acceptance summary
  - set bug status to resolved (and follow verified/closed policy if required)
  - record final trace: jobId, log path, TAPD comment ids, status transition result.

## Resource Files
- `references/defect-conclusion-template.md`: confirmed bug definition template.
- `references/prompt-spec.md`: execution/acceptance prompt specification.
- `references/tapd-comment-templates.md`: HTML templates for TAPD backfills.
