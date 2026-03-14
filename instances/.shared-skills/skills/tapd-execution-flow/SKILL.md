---
name: tapd-execution-flow
description: "Execute TAPD requirements with a fixed gated workflow: fetch requirement -> confirm models -> align implementation plan & DoD -> generate/confirm prompts -> backfill -> execute -> itemized acceptance -> update TAPD test cases -> transition status. Use when users ask to run/redo TAPD stories, enforce gated confirmations, produce HTML-formatted TAPD comments, or enter FAIL remediation loops (`status_4` -> fix -> re-accept)."
---

# TAPD Execution Flow

## Overview
Use this skill to keep TAPD execution consistent, auditable, and reversible.  
Follow gate order strictly; do not skip user confirmations.

## Workflow Decision Tree
1. **Need to execute a TAPD story from scratch** -> run full workflow (Step 1 to Step 10).
2. **Need to redo after FAIL** -> run remediation workflow (Step 8 only, then Step 9/10).
3. **Need only TAPD backfill formatting** -> load `references/tapd-html-templates.md`.
4. **Need checklist validation before status change** -> load `references/workflow-checklist.md`.

## Step 1 - Fetch Requirement
- Read TAPD story content first.
- Confirm scope, hard constraints, and current status.
- Do not execute based on oral summary only.

## Step 2 - Confirm Models
- Confirm **planning model** (used to draft the Implementation Plan + DoD), **main-task model**, and **acceptance-task model** separately.
- Planning model must be **explicitly specified by user** (manual selection) — do **not** assume it equals main/acceptance model.
- Record the model triple in TAPD backfills (planning backfill + prompt backfill) for traceability.

## Step 3 - Align Implementation Plan & DoD (Mandatory)
> This gate is **mandatory for ALL requirements**, not only refactors.

Goal: before writing prompts / touching code, align *how* we will implement and *what "done" looks like*.

### Context Pack (Repo Plan Doc) for Complex Plans
If the plan is long/complex and may exceed prompt context:
- Write the detailed plan into a **repo file** (recommended, versionable), e.g.:
  - `.tapd/plan/story-{{story_id}}-plan-v{{plan_version}}.md`
- Keep the TAPD comment as the **baseline index**:
  - short executive summary (1 screen)
  - DoD / evidence requirements
  - risks & rollback
  - **plan doc path** (repo-relative) + (optional) commit id when available

Must output (concise but concrete):
- **Implementation Plan** (recommended option + alternatives):
  - target modules / files (expected touch list)
  - API / behavior change policy (what must not change)
  - data migration considerations (if any)
  - rollout / rollback strategy (how to revert safely)
- **DoD (Definition of Done)**:
  - acceptance items mapping (each item -> evidence type)
  - "code-side evidence" vs "runtime smoke" ownership split (if applicable)
  - explicit non-goals / out-of-scope list
- **Risk checklist** (top 3 risks + mitigations)

Hard gate:
- Ask user to confirm the plan + DoD.
- After confirmation, **backfill the plan + DoD to TAPD as an independent comment** (HTML formatting) before generating prompts.
- The backfill must explicitly record the **planning model** used to draft the plan.
  - If a repo plan doc is used, the backfill must also include the **plan doc path**.

## Step 4 - Generate Prompts
- Generate full prompts for main/acceptance tasks.
- Prompts must be aligned with the confirmed Implementation Plan + DoD baseline; do not drift from baseline without re-confirming Step 3.
- Prompt must include:
  - role and objective
  - strengths and capability boundaries
  - hard constraints
  - required deliverables and evidence format
- Ask user to confirm prompts.

## Step 5 - Backfill Prompts to TAPD (Mandatory)
- Backfill prompts to TAPD even if they were already confirmed earlier.
- Use HTML-visible line breaks and lists.
- Keep prompt version/history traceable.

### HTML newline handling (Critical)
TAPD description/comments are rendered as **rich text** and will usually **collapse raw `\n`**.  
When backfilling any multi-line content (prompts / logs / checklists), do **not** paste raw plain-text with newlines.

Use this rule:
1) **HTML-escape** the text first (at least `& < > "`)  
2) Convert newlines: `\n` → `<br/>`

In `references/tapd-html-templates.md`, placeholders that end with `_html` (e.g. `main_prompt_html`) **must** be the processed string above.

## Step 6 - Execute Main Task
- Execute only after prompt confirmation and prompt backfill complete.
- Collect evidence artifacts (logs/reports/output paths/commit).
- Backfill main-task execution result to TAPD.

## Step 7 - Execute Acceptance Task
- Accept independently; do not restate main-task claim.
- For every check item, output exactly:
  1) Acceptance Content
  2) Acceptance Process
  3) Acceptance Result (PASS/FAIL + evidence path)
- Backfill full itemized acceptance to TAPD.

## Step 8 - FAIL Remediation Loop
- If FAIL:
  - keep `status_4`
  - backfill failure causes (mandatory + suggestion items)
  - re-confirm models
  - if fix plan deviates from the confirmed baseline -> go back to **Step 3** and re-confirm + re-backfill plan/DoD
  - regenerate/re-confirm prompts (focus on failed points)
  - run fix + re-accept
- Repeat until PASS.

## Step 9 - Test Case Menu Update (Mandatory)
- Put test steps into TAPD **test-case menu** (not only comments).
- Ensure HTML formatting is readable.
- Create/Update `tcase` entities and link `story ↔ tcase`.
- Backfill test-case IDs and relation IDs.

## Step 10 - Status Transition and Final Trace
- PASS -> set `status_6`
- FAIL -> keep `status_4`
- Record every round:
  - `jobId`
  - log path
  - summary
  - TAPD comment IDs
  - status transition result
  - process verification note (whether cc-connect process was restarted)

## Resource Files
- `references/workflow-checklist.md`: pre/during/post checklists.
- `references/tapd-html-templates.md`: ready-to-use HTML templates for TAPD comments.
- `references/remediation-loop.md`: fail-loop playbook and status rules.
