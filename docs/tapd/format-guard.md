# TAPD 富文本排版防回退（Format Guard）

目的：避免再次出现「需求描述提交后换行/列表丢失、页面挤成一段」的问题。  
原则：**提交前预检 + 提交后回读校验**，不通过即停止。

---

## 1. 为什么会反复出错

TAPD 的 description/comment 是富文本渲染。  
如果直接提交 Markdown 或纯文本换行（`\n`），页面可能折叠成一段，导致可读性变差。

高频误用：

- `# 标题`
- `- 列表项`
- ``` ``` 代码块
- 大段纯文本换行

---

## 2. 工具链（本仓库）

新增脚本：

- `tools/tapd-safe-update.ps1`
- `tools/tapd-safe-update.bat`

新增模板：

- `docs/tapd/templates/story-description.template.html`

---

## 3. 标准流程（必须）

## Step 1) 生成模板

```powershell
pwsh -NoLogo -NoProfile -File tools/tapd-safe-update.ps1 -Action template
```

默认在当前目录生成：

- `tapd-story-description.html`

也可指定输出路径：

```powershell
pwsh -NoLogo -NoProfile -File tools/tapd-safe-update.ps1 `
  -Action template `
  -OutFile D:\tmp\story-1166-desc.html
```

## Step 2) 填充 HTML 内容

编辑生成文件，将 `{{...}}` 占位符替换为真实内容。  
注意：保持 `<p> / <ul><li> / <strong> / <code>` 结构。

## Step 3) 提交前预检（lint）

```powershell
pwsh -NoLogo -NoProfile -File tools/tapd-safe-update.ps1 `
  -Action lint `
  -HtmlFile D:\tmp\story-1166-desc.html
```

## Step 4) 安全更新（update + verify）

```powershell
pwsh -NoLogo -NoProfile -File tools/tapd-safe-update.ps1 `
  -Action update `
  -WorkspaceId 66052431 `
  -StoryId 1166052431001000088 `
  -EntityType stories `
  -HtmlFile D:\tmp\story-1166-desc.html
```

脚本会执行三件事：

1. 提交前 HTML 门禁（不通过则阻断）
2. 调用 `tapd.py update_story_or_task`
3. 立即回读 `get_stories_or_tasks` 并再次校验排版结构

---

## 4. 可选配置

默认 `tapd.py` 路径：

- `C:\Users\Xverse\.agents\skills\tapd\scripts\tapd.py`

可覆盖方式：

1. 命令参数 `-TapdScriptPath <path>`
2. 环境变量 `TAPD_SCRIPT_PATH`

---

## 5. 快速命令（bat）

```bat
tools\tapd-safe-update.bat -Action lint -HtmlFile D:\tmp\story.html
tools\tapd-safe-update.bat -Action update -WorkspaceId 66052431 -StoryId 1166052431001000088 -HtmlFile D:\tmp\story.html
```

---

## 6. 团队执行约束（建议纳入流程）

1. TAPD description/comment 一律走本工具链，不直接手工粘贴 Markdown。  
2. PR/执行记录中保留 `HtmlFile` 源文件，便于复用与审计。  
3. 若 lint 报错，不得绕过；必须先转换为 HTML。  
4. 回读校验失败时，不得进入下一流程（执行/验收/状态流转）。

