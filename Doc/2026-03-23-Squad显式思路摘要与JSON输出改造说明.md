# 2026-03-23 Squad显式思路摘要与JSON输出改造说明

## 背景

在 Squad 模式下，角色进程（Planner / Executor / Reviewer）的中间“reasoning”信息主要依赖上游模型流式事件是否提供 `reasoning` 类型数据。  
近期多次运行中，`gpt-5.3-codex` 在 trace 中未稳定产出 `reasoning` 事件，导致用户在 `data/traces/codex/*.jsonl` 中看不到可读的思考过程。

为避免把可观测性建立在“模型是否返回隐藏 reasoning”上，本次采用显式输出策略：由角色在最终回复中固定输出“思路摘要 + JSON结果”。

## 变更摘要

本次改造聚焦 Squad 三类角色提示词与解析兼容性：

1. `core/squad_runner.go`
   - `buildSquadPlannerPrompt`：从“仅输出 JSON”改为强制两段输出：
     - A 段：思路摘要（自然语言）
     - B 段：JSON结果（放入 ```json 代码块）
   - `buildSquadExecutorPrompt`：强化 A/B 双段格式，并明确 A 段禁止使用 `{}`，B 段输出单个 JSON 对象。
   - `buildSquadReviewerPrompt`：由“仅输出 JSON”改为 A/B 双段格式，同样要求 JSON 代码块。

2. `core/squad_parse_test.go`
   - 新增解析测试，验证在“思路摘要 + JSON代码块”混合输出下，仍可正确提取机器可解析 JSON：
     - `TestParseSquadPlan_ThoughtSummaryPlusJSON`
     - `TestParseReviewerFindings_ThoughtSummaryPlusJSON`
     - `TestParseExecutorMeta_ThoughtSummaryPlusJSON`

## 影响与风险

### 正向影响

- 提升可观测性：即使上游未返回隐藏 reasoning，仍能在角色最终回复中看到明确“思路摘要”。
- 兼容现有流程：解析器继续按 JSON 提取，不改变 plan / review / executor 元数据结构。
- 对用户更友好：飞书/聊天窗口可直接查看每轮核心思路，不必依赖 trace 内部字段。

### 风险点

- 若模型未遵守格式（例如 A 段出现大量 `{}` 或 B 段缺失 JSON），可能触发降级解析或失败。
- 输出更长，极端情况下可能增加 token 与延迟开销。

## 验证证据

建议执行以下验证：

1. 单元测试
   - `go test ./core -run ThoughtSummaryPlusJSON -count=1`

2. 全量核心测试
   - `go test ./core`

3. 端到端抽样
   - 发起一个 `/squad start ...` 任务，观察 Planner / Executor / Reviewer 回复是否包含：
     - A) 思路摘要
     - B) JSON结果（```json 代码块）
   - 验证命令解析仍正常推进（计划生成、任务审核、任务批准/返工）。

## 回滚说明

若需要回滚到旧行为，可将以下函数恢复为“仅 JSON 输出”版本：

- `buildSquadPlannerPrompt`
- `buildSquadExecutorPrompt`
- `buildSquadReviewerPrompt`

同时删除本次新增的 `ThoughtSummaryPlusJSON` 测试用例，重新执行 `go test ./core` 验证回滚结果。
