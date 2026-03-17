---
name: tapd-format-guard
description: "TAPD 富文本排版门禁：写入前做 HTML lint，写入后回读校验，防止描述/评论排版塌陷。凡涉及 TAPD 描述回填都应先走此技能。"
---

# TAPD 排版门禁（Format Guard）

用于避免 TAPD 内容出现“换行丢失、列表塌缩、整段挤在一起”等问题。

## 适用场景

- 更新 TAPD 需求/任务 description；
- 回填方案、DoD、提示词、验收过程等长文本；
- 用户反馈“排版又乱了”。

## 强制门禁

1. 禁止直接提交 Markdown（`#`、`-`、`1.`、```）。
2. 必须使用 HTML 结构：`<p> <ul><li> <strong> <code>`。
3. 提交前必须 lint。
4. 提交后必须回读并验证。
5. 校验失败即停止，不得继续执行流。

## 工具入口

- 脚本：`tools/tapd-safe-update.ps1` / `tools/tapd-safe-update.bat`
- 模板：`docs/tapd/templates/story-description.template.html`
- 文档：`docs/tapd/format-guard.md`

## 标准流程

### 1) 生成模板

```powershell
pwsh -NoLogo -NoProfile -File tools/tapd-safe-update.ps1 -Action template
```

### 2) 填写 HTML 内容

编辑模板占位符，保留结构标签。

### 3) lint

```powershell
pwsh -NoLogo -NoProfile -File tools/tapd-safe-update.ps1 `
  -Action lint `
  -HtmlFile D:\tmp\story-desc.html
```

### 4) 安全更新（含回读校验）

```powershell
pwsh -NoLogo -NoProfile -File tools/tapd-safe-update.ps1 `
  -Action update `
  -WorkspaceId 66052431 `
  -StoryId 1166052431001000088 `
  -EntityType stories `
  -HtmlFile D:\tmp\story-desc.html
```

## 完成输出（建议）

- `FORMAT_GUARD: PASS|FAIL`
- `WORKSPACE_ID: <id>`
- `ENTITY_TYPE: stories|tasks`
- `ENTITY_ID: <id>`
- `TAPD_LINK: <url>`
- `LINT_RESULT: PASS|FAIL`
- `READBACK_VERIFY: PASS|FAIL`

