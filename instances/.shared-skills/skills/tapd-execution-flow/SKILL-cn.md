# TAPD 执行流程（中文说明）

> 对应技能：`tapd-execution-flow`  
> 说明用途：提供该技能的中文流程说明，不替换原 `SKILL.md`。

## 技能作用

该技能用于按固定门禁执行 TAPD 需求，确保流程可审计、可复盘、可回滚。  
核心链路：

`获取需求 -> 确认模型 -> 对齐方案/DoD -> 生成/确认提示词 -> 回填提示词 -> 主任务执行 -> 验收执行 -> 测试用例回填 -> 状态流转`

适用场景：
- 用户要求“执行/重做 TAPD 需求”
- 需要强制提示词确认与回填
- 需要输出 HTML 格式 TAPD 评论
- 需要进入 FAIL 整改循环（`status_4 -> 修复 -> 复验`）

---

## 流程决策

1. 从零执行 TAPD 需求：走完整流程（第 1-10 步）。
2. 验收失败后重做：重点走整改循环（第 8 步），再执行第 9/10 步。
3. 仅需回填格式模板：使用 `references/tapd-html-templates.md`。
4. 仅需执行前后核对：使用 `references/workflow-checklist.md`。

---

## 标准流程

### 第 1 步：获取需求
- 先从 TAPD 读取需求详情（ID、描述、状态、约束）。
- 禁止仅凭口头摘要直接开工。

### 第 2 步：确认模型
- **规划模型（plan model）**、主任务模型、验收任务模型分别确认：
  - 规划模型：仅用于“实现方案 + DoD”的产出与对齐，必须由用户明确指定（不可默认等于主/验收模型）。
  - 主任务模型：用于代码实现/执行。
  - 验收任务模型：用于逐条验收与结论输出。
- 模型组合必须先回填 TAPD（用于可追溯）。
- 若用户未提供规划模型：只问一次（给 2-3 个候选 + 推荐项），等待用户选择后再进入下一步。

### 第 3 步：对齐实现方案 + DoD（必做）
- 在写主任务/验收提示词之前，必须先产出并对齐“实现方案 + DoD”基线。
- 若由模型自动生成规划内容，按 `references/prompt-spec.md` 执行：
  - 先应用 **Global Fast Lane Block**（0 节）
  - 再按 **Planner Prompt Contract**（1 节）生成规划提示词
- 本步输出至少包含：
  - 目标模块/文件触点
  - 行为不变性与边界
  - DoD（逐条+证据类型）
  - 风险与回滚
- 用户确认后，先回填 TAPD（方案/DoD comment）再进入下一步。

### 第 4 步：生成提示词（执行者 / 验收者）
- 分别生成主任务与验收任务提示词（动态生成时必须走角色契约）：
  - 执行者：`references/prompt-spec.md` 的 **Execution Prompt Contract**（2 节）
  - 验收者：`references/prompt-spec.md` 的 **Acceptance Prompt Contract**（3 节）
- 两个提示词都必须前置 **Global Fast Lane Block**（0 节）。
- 生成后执行 **Prompt Quality Gate**（4 节），通过后再给用户确认。

### 第 5 步：回填提示词（必做）
- 无论是否已确认过，都必须回填 TAPD。
- 使用 HTML 可见换行格式（`<p>/<br/>/<ul><li>/<code>`）。
- 重要：TAPD 富文本会折叠纯文本换行（`\n`），多行提示词/日志不要直接粘贴原文；应先做 **HTML escape**，并将 `\n` 转为 `<br/>`（参考 `references/tapd-html-templates.md` 中的 `*_html` 占位符约定）。

### 第 6 步：执行主任务
- 仅在“提示词确认 + 回填完成”后执行。
- 产出证据：日志、报告、输出路径、提交记录等。
- 回填主任务执行结果到 TAPD。

### 第 7 步：执行验收任务
- 独立验收，不复述主任务结论。
- 每条验收必须三段式：
  1) 验收内容  
  2) 验收过程  
  3) 验收结果（PASS/FAIL + 证据路径）
- **总结果只允许 PASS / FAIL**（不要引入 BLOCK / PARTIAL / UNKNOWN）。
- 若某条验收“未执行 / 等 CI / 被环境阻塞”：仍按 **FAIL** 记该条结果，并在验收过程/证据里说明原因。
- 回填逐条验收到 TAPD。

### 第 8 步：FAIL 整改循环
- FAIL 时保持 `status_4`。
- 回填失败项（必须项/建议项）。
- 重新确认模型。
- 重新生成并确认提示词（聚焦失败点）。
- 执行修复并复验，直到 PASS。

### 第 9 步：测试用例菜单回填（必做）
- 测试步骤必须写入 TAPD 测试用例菜单（非仅评论）。
- 创建/更新 `tcase`，建立 `story ↔ tcase` 关联。
- 回填测试用例 ID 与关系 ID。

### 第 10 步：状态流转与留痕
- PASS -> `status_6`
- FAIL -> `status_4`
- 每轮留痕必须包含：
  - `jobId`
  - 日志路径
  - 结果摘要
  - TAPD 评论 ID
  - 状态变更结果
  - 进程核验说明（是否重启 cc-connect）

---

## 配套参考文件

- `references/workflow-checklist.md`：流程检查清单
- `references/tapd-html-templates.md`：TAPD HTML 回填模板
- `references/remediation-loop.md`：FAIL 整改循环细则
- `references/prompt-spec.md`：角色化提示词规范（规划者/执行者/验收者）
