# TAPD Skills Catalog（路由表 / 触发词 / 输入输出契约）

目的：把“用户意图 → 该用哪个 skill”做成稳定、可审计的路由表，降低误路由与重复沟通成本。

> 原则：**description 即路由**。每个 skill 的 `description` 都要写清楚 *when* 条件（在什么场景/什么输入下触发）。

---

## 1) 需求（Story）链路

### 1.1 仅创建需求（不执行）
- Skill：`project-requirement-clarification`
- 典型触发语：
  - “创建tapd需求”
  - “创建TAPD简单需求”
  - “tapd新需求”
  - “只创建，不执行”
- 输入（最小集）：
  - workspace_id（可默认/可从上下文推断；不确定必须问）
  - 标题（name）
  - 目的/验收点（最少 1~3 条）
- 输出契约（必须）：
  - `END_STATE: CREATED_ONLY`
  - `TAPD_ENTITY_TYPE: story|task`
  - `TAPD_WORKSPACE_ID: <number>`
  - `TAPD_ID: <id>`
  - `TAPD_LINK: <url>`
  - `NEXT_STEP_QUESTION: Do you want to proceed to execution workflow (tapd-execution-flow)?`
- Stop Boundary（强制）：
  - 创建后必须停止，不得自动进入执行流

### 1.2 执行需求（含验收/回填/测试用例/状态流转）
- Skill：`tapd-execution-flow`
- 典型触发语：
  - “按tapd需求执行skill来完成需求 <story_id>”
  - “执行 <story_id>”
  - “复验/整改 <story_id>”
- 必须门禁（摘要）：
  - 先拉取需求详情（不得口述开工）
  - 方案/主/验收模型分别确认（**方案模型必须手动指定，不得默认等于主任务模型**；用于“实现方案/DoD”产出）
  - **实现方案/DoD 必须先达成一致并回填 TAPD**，完成后才允许进入“提示词生成/确认/执行”
  - 主/验收提示词确认后再执行，并回填 TAPD（HTML 可见换行）
  - 验收逐条三段式（内容/过程/结果 + 证据）
  - 测试用例菜单更新 + story↔tcase 关联
  - PASS→`status_6`；FAIL→`status_4` 并进入整改轮

---

## 2) 缺陷（Bug）链路

- Skill：`tapd-bug-remediation-flow`
- 典型触发语：
  - “修复缺陷/bug <bug_id>”
  - “验收失败了，继续整改”
- 必须门禁（摘要）：
  - 先拉取缺陷详情 + 评论 + 重开原因
  - 与用户确认“最终缺陷结论”并同步写回缺陷 description（含复现/预期/实际/验收）
  - 方案/执行/验收模型分别确认（**方案模型必须手动指定，不得默认等于执行模型**；用于“修复方案/DoD”产出）
  - **修复方案/DoD 必须先达成一致并回填 TAPD**，完成后才允许进入“修复/验收提示词生成/确认/执行”
  - 修复提示词与验收提示词均需：用户确认 → TAPD 回填 → 才能执行
  - FAIL 必须回填原因并进入下一轮整改，直到 PASS

---

## 3) 底层 API（操作层，不保证流程完整）

- Skill：`tapd`
- 适用：
  - 查询/创建/更新 story/task/bug/iteration/comment/tcase/wiki/todo 等
  - 获取字段配置、状态映射、附件链接
- 边界：
  - 只做“点状 API 操作”
  - 不自动补齐执行流门禁（由 flow skill 负责）

---

## 4) 排版门禁（防描述/评论塌版）

- Skill：`tapd-format-guard`
- 典型触发语：
  - “TAPD 排版又乱了”
  - “更新需求描述，注意富文本排版”
  - “先做 lint 再更新 TAPD”
- 必须门禁（摘要）：
  - 写入前：HTML lint（禁止原始 Markdown 直写）
  - 写入后：read-after-write 回读校验
  - 失败即停止，不得进入执行/验收后续步骤
- 配套工具：
  - `tools/tapd-safe-update.ps1`
  - `tools/tapd-safe-update.bat`
  - `docs/tapd/format-guard.md`
