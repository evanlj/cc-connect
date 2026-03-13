---
name: project-requirement-clarification
description: "Turn a user's brief requirement hint into a final, execution-ready requirement baseline by iterative clarification: restate understanding -> propose detailed scope -> ask targeted questions -> reconcile conflicts -> confirm final requirement baseline -> (optional) create a TAPD story/task. IMPORTANT: this skill ends at TAPD creation only; it must NOT execute/implement the requirement."
---

# Project Requirement Clarification

Use this skill to convert a short prompt into a concrete, testable requirement baseline.
Default mode is **iterative conversation** (not one-shot drafting).

## Scope / Non-Goals (Critical)
- **In scope**:
  - Clarify requirements until the user confirms a final baseline.
  - When explicitly requested, **create** a TAPD story/task that reflects the confirmed baseline.
- **Out of scope (must not do in this skill)**:
  - Do **not** implement or execute the requirement (no coding, no task execution, no acceptance run).
  - Do **not** transition TAPD status (unless the user separately requests it in another skill/workflow).
  - Do **not** start any "execution flow" automatically after creating the TAPD item.

## Auto Trigger Keywords
- 创建tapd需求
- 创建 TAPD 需求
- 创建TAPD简单需求
- 创建 TAPD 简单需求
- tapd新需求
- TAPD requirement create

If user input contains one of the keywords above, trigger this skill first.

## Simple Mode (Fast Path): “创建TAPD简单需求”

When the user explicitly uses **创建TAPD简单需求** (or equivalent) and indicates they want a lightweight/test requirement:
- Goal: create a TAPD story/task with a **rough-but-usable** baseline.
- Keep the flow intentionally minimal while preserving **security** and **explicit user confirmation**.

### Simple Mode Rules (Overrides / Simplifications)
1. Still do **Step 1** (parse brief prompt) and **restate understanding**.
2. Allow **only one** clarification round:
   - Ask **<= 3** highest-impact questions total.
   - If user says “按默认/就这样/不用复杂”，accept defaults and proceed.
3. Skip Step 4 conflict check **unless** there is an obvious contradiction.
4. Step 5 baseline can be minimal:
   - Objective: 1~2 lines
   - In-scope: 1~3 bullets
   - Acceptance: 1~3 bullets (testable enough)
   - Explicitly note what is “assumed/defaulted”.
5. TAPD field handling:
   - Prefer auto-fill `workspace_id` from local memory/caches/config when available.
   - `owner/priority/iteration/module` are optional by default.
6. Credential handling is unchanged: **never** ask for secrets in plain text.

### Interaction UX Rule: One Question at a Time (一问一答)
To reduce user burden and improve completion rate:
- When collecting inputs required for TAPD creation (e.g., workspace_id, title/name, minimal acceptance bullets, env var set/not-set, final confirmation),
  **ask exactly one question per turn** and wait for the user's answer before asking the next.
- In Simple Mode, keep total questions <= 3, and still follow one-question-per-turn.
- In Standard Mode, you may ask 3-7 high-impact questions overall, but they must be sequenced as one-question-per-turn across multiple turns.

## Hard Gates (Must Follow)
1. Do not treat the first user sentence as final requirement.
2. Always restate understanding before proposing details.
3. Keep asking focused questions until user explicitly confirms "final requirement baseline".
4. If key dimensions are missing (scope/acceptance/constraints), do not proceed to execution planning.
5. Mark all assumptions explicitly and ask user to accept/reject each assumption.
6. **Stop boundary**: even after TAPD item is created, **stop here**. This skill's final action is *TAPD creation only*. Do not execute/accept/backfill execution results.
7. **Credential handling**: never request secrets in plain text. If TAPD credential is missing, instruct the user to set env vars locally and only confirm "set/not set".

## Step 1 - Parse the Brief Prompt
- Extract and list:
  - business goal
  - target users
  - expected outcome
  - constraints already stated by user
- Separate **facts** from **assumptions**.

## Step 2 - Draft v0 Requirement Skeleton
Produce a concise v0 with:
- Objective
- In-scope / Out-of-scope
- Core workflow
- Functional requirements
- Non-functional requirements
- Dependencies
- Risks
- Open questions

Load `references/requirement-detail-template.md` for structure.

## Step 3 - Clarification Loop (Mandatory)
Run loop until user confirms:
1. Show delta between previous and current understanding.
2. Ask 3-7 high-impact questions (prioritize blockers).
3. Update requirement draft based on user response.
4. Explicitly call out what is now fixed vs still undecided.

Use `references/question-bank.md` to pick domain-agnostic questions.

## Step 4 - Conflict & Boundary Check
- Detect contradictions:
  - timeline vs scope
  - quality target vs resource
  - must-have vs excluded items
- Propose at least 2 resolution options with trade-offs.
- Ask user to choose one.

## Step 5 - Final Requirement Baseline
When user confirms, output a frozen baseline:
- Final objective
- Scope baseline (in/out)
- Acceptance criteria (testable)
- Deliverables
- Milestones
- Constraints
- Risks and fallback
- Change control rule (what needs reconfirmation)

## Step 6 - TAPD Creation Hand-off (When Requested)
If user explicitly asks to create a TAPD requirement after confirmation:
1. Convert final baseline into TAPD-ready fields.
2. Reconfirm mandatory fields with user (workspace/module/priority/owner/iteration).
3. Create TAPD requirement via TAPD tooling.
4. Return created entity ID/link and backfill summary.

### TAPD description formatting (Must)
TAPD `description` is typically rendered as **rich text** and may **collapse raw newlines**.  
To keep the requirement readable in TAPD:
- Prefer HTML-visible formatting: `<p>`, `<br/>`, `<ul><li>`, `<code>`
- Do **not** rely on Markdown headings like `## ...` for layout
- If you start from plain multi-line text, apply: `html.escape(text).replace("\n", "<br/>")`

**Mandatory stop after Step 6**:
- After returning `story_id/link`, ask the user whether they want to proceed to an execution workflow.
- If the user says yes, *handoff only* (e.g., recommend `tapd-execution-flow`) — do not start execution within this skill.

### Step 6 - Output Contract (Machine-checkable)
When Step 6 is completed, the assistant output **must** include the following fields (plain text is fine):

- `END_STATE: CREATED_ONLY`
- `TAPD_ENTITY_TYPE: story|task`
- `TAPD_WORKSPACE_ID: <number>`
- `TAPD_ID: <id>`
- `TAPD_LINK: <url>`

And it must also include a final question:
- `NEXT_STEP_QUESTION: Do you want to proceed to execution workflow (tapd-execution-flow)?`

**Strict rule**:
- If the user does **not** explicitly confirm to proceed, do **not** start any execution planning or execution steps in this skill. Stop after the question.

## Output Format (Recommended)
Use this compact structure in every clarification round:

1) Current understanding  
2) New assumptions (to confirm)  
3) Questions needing user decision  
4) Updated requirement draft (delta only)  
5) Remaining unknowns  

For final baseline, switch to:

1) Final requirement summary  
2) Detailed requirement list  
3) Acceptance checklist  
4) Boundary and exclusions  
5) Risks and rollback/fallback  
