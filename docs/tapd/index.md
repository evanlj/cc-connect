# TAPD Harness（需求/缺陷）— 工程化执行体系（System of Record）

目的：把“工程师经验（门禁/流程/证据）”固化为 **可路由、可执行、可验证、可审计** 的工作手册，支撑 cc-connect 的 TAPD 自动化链路逐步右移自治（Trust Spectrum）。

适用范围：
- 需求（Story）执行：从拉取需求 → 模型确认（方案/主/验收）→ 实现方案/DoD 对齐（确认+回填）→ 提示词确认/回填 → 主任务执行 → 逐条验收 → 测试用例菜单更新/关联 → 状态流转
- 缺陷（Bug）修复：从拉取缺陷 → 最终缺陷结论确认/同步 → 模型确认（方案/执行/验收）→ 修复方案/DoD 对齐（确认+回填）→ 修复提示词确认/回填 → 修复 → 验收提示词确认/回填 → 复验 → 状态流转

---

## 1) 快速路由（用户一句话 → 用哪个 Skill）

| 用户意图 | 推荐 Skill | 边界（Stop Boundary） |
|---|---|---|
| 只创建 TAPD 简单需求（不执行） | `project-requirement-clarification` | 创建完成必须 `END_STATE: CREATED_ONLY` 并停止，不得自动进入执行流 |
| 执行/验收一个 story（从需求开始到关闭） | `tapd-execution-flow` | 允许流转状态，但必须完成提示词确认+回填、验收三段式、tcase 菜单与关联等门禁 |
| 修复一个 bug（含复验/整改轮） | `tapd-bug-remediation-flow` | FAIL 必须留在 `status_4` 并回填失败原因，再进入整改轮 |
| 只做 API 查询/更新/评论/附件/测试用例等具体操作 | `tapd`（脚本 skill） | 仅执行具体 API；不承诺流程完整性（由上层 flow skill 负责门禁） |

更完整的路由规则、触发词、输入输出契约见：
- `docs/tapd/skill-catalog.md`

---

## 2) 证据与可审计（Evidence First）

### 2.1 为什么要“证据优先”
流程可右移自治的前提是：每次执行都能产出可复验的证据，而不是“口头说完成了”。

### 2.2 复杂方案的上下文承载（Context Pack）
当“实现/修复方案”步骤较多、较复杂，容易超过提示词上下文时：
- 将详细方案写入 **目标项目仓库文件**（可版本化），作为 Context Pack  
  - 示例：`.tapd/plan/story-<id>-plan-vX.md` / `.tapd/plan/bug-<id>-fix-plan-vX.md`
- TAPD 上仍需独立 comment 回填“方案/DoD”作为基线索引，并包含：
  - `plan_model`（方案模型）
  - `plan_doc_path`（repo 相对路径）
  - 方案摘要 + DoD + 风险/回滚

### 2.2 Run Manifest（建议作为统一审计摘要）
建议为每次 TAPD 执行生成一个结构化 `run manifest`（JSON），记录：
- workspace_id / entity_id / entity_type
- 主/验收模型
- 提示词版本与对应 comment_id
- 验收结论与证据锚点（日志/报告路径）
- tcase_id / relation_id
- status_before / status_after

契约草案见：
- `docs/tapd/run-manifest-contract.md`

---

## 3) Closeout Gate（状态流转前门禁）

推荐在将 story 置为完成（例如 `status_6`）前执行一次 closeout gate：
- 默认放行（PASS）
- 仅在“缺证据/缺门禁/存在风险差异”时阻塞（BLOCK）并输出 unblock checklist

检查项清单见：
- `docs/tapd/closeout-gate.md`

---

## 4) 信任光谱（Trust Spectrum）— 落地策略（C）

建议分 4 档推进（先 L1，后 L2/L3）：
- **L0**：只分析/生成提示词，不写入外部系统
- **L1**：允许拉取信息与产出证据/manifest，但写入（评论/状态/关联）仍需人工确认
- **L2**：证据齐全时允许自动回填评论、自动建/更 tcase 与关联；状态流转仍需最终确认
- **L3**：低风险链路允许自动闭环（含状态流转），但必须可回滚

---

## 5) 本试点（Pilot）

建议先以单项目（例如 AGame workspace）做试点，跑通：
1) 路由稳定（catalog + when 条件）
2) 证据标准化（manifest + evidence 目录）
3) closeout gate（默认放行，有证据才拦）
