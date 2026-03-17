---
name: tapd-format-guard
description: "Enforce TAPD rich-text formatting gate for story/task description updates: HTML lint before write, then read-after-write verification. Use this skill whenever updating TAPD description/comment content that must keep readable layout."
---

# TAPD Format Guard

Use this skill to prevent TAPD text-layout regressions (e.g. content collapsed into one paragraph).

## When to use

- User asks to create/update TAPD story/task description.
- User complains TAPD content is badly formatted / lost line breaks.
- You are about to backfill long execution notes, DoD, prompt blocks, or acceptance content to TAPD.

## Scope

In scope:
- Story/task description rich-text guard
- HTML template generation
- Lint before update
- Read-after-write verification

Out of scope:
- Full TAPD execution workflow orchestration (use `tapd-execution-flow`)
- Bug remediation workflow (use `tapd-bug-remediation-flow`)
- TAPD entity creation-only flow (use `project-requirement-clarification`)

## Hard gates (must)

1. Do **not** submit raw Markdown (`#`, `-`, `1.`, ```).
2. Description/comment should use HTML-visible tags: `<p>`, `<ul><li>`, `<strong>`, `<code>`.
3. Must run lint before write.
4. Must read back from TAPD and verify formatting after write.
5. If verification fails, stop and report; do not continue execution flow.

## Tooling (repo local)

- Guard script:
  - `tools/tapd-safe-update.ps1`
  - `tools/tapd-safe-update.bat`
- HTML template:
  - `docs/tapd/templates/story-description.template.html`
- User guide:
  - `docs/tapd/format-guard.md`

## Standard procedure

### Step 1) Generate template

```powershell
pwsh -NoLogo -NoProfile -File tools/tapd-safe-update.ps1 -Action template
```

### Step 2) Fill HTML content

Replace placeholders in generated HTML file and keep structural tags.

### Step 3) Lint

```powershell
pwsh -NoLogo -NoProfile -File tools/tapd-safe-update.ps1 `
  -Action lint `
  -HtmlFile D:\tmp\story-desc.html
```

### Step 4) Safe update (write + verify)

```powershell
pwsh -NoLogo -NoProfile -File tools/tapd-safe-update.ps1 `
  -Action update `
  -WorkspaceId 66052431 `
  -StoryId 1166052431001000088 `
  -EntityType stories `
  -HtmlFile D:\tmp\story-desc.html
```

## Output contract (when completed)

Return a concise summary including:

- `FORMAT_GUARD: PASS|FAIL`
- `WORKSPACE_ID: <id>`
- `ENTITY_TYPE: stories|tasks`
- `ENTITY_ID: <id>`
- `TAPD_LINK: <url>`
- `LINT_RESULT: PASS|FAIL`
- `READBACK_VERIFY: PASS|FAIL`

