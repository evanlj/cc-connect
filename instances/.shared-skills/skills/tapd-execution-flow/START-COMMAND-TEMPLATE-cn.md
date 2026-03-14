# 一键开工口令模板（tapd-execution-flow）

> 用法：复制一段到会话中，把占位符替换成实际值即可。

---

## 模板 A：标准开工（推荐）

```text
用 tapd-execution-flow 执行 TAPD 需求 {{story_id}}。
主任务模型用 {{main_model}}，验收任务模型用 {{accept_model}}。
按固定流程执行：先拉需求 -> 确认模型 -> 生成并确认提示词 -> 提示词回填 -> 主任务 -> 逐条验收 -> 测试用例菜单回填 -> 状态流转。
硬约束：{{hard_constraints}}
```

示例：

```text
用 tapd-execution-flow 执行 TAPD 需求 1166052431001000081。
主任务模型用 rc + 5.2，验收任务模型用 rc + 5.3-codex。
按固定流程执行：先拉需求 -> 确认模型 -> 生成并确认提示词 -> 提示词回填 -> 主任务 -> 逐条验收 -> 测试用例菜单回填 -> 状态流转。
硬约束：资源全量同步、验收场景可切换全量对象、Skill 可安装卸载回滚、不得改写源项目。
```

---

## 模板 B：FAIL 后整改轮

```text
用 tapd-execution-flow 对需求 {{story_id}} 进入整改轮。
保持 status_4，不新开需求。
主任务模型 {{main_model}}，验收任务模型 {{accept_model}}。
按整改流程执行：失败项回填 -> 重新确认模型 -> 重新确认提示词 -> 修复 -> 复验 -> 回填测试用例 -> 状态建议。
本轮重点修复：{{failed_points}}
```

---

## 模板 C：只做验收复核

```text
用 tapd-execution-flow 对需求 {{story_id}} 只执行验收子任务复核。
验收模型 {{accept_model}}。
必须逐条输出：验收内容 / 验收过程 / 验收结果（含证据路径），并按 HTML 格式回填 TAPD。
```

---

## 模板 D：只做 TAPD 回填（不执行实现）

```text
用 tapd-execution-flow 对需求 {{story_id}} 只做 TAPD 回填。
回填项：提示词（主/验收）+ 验收逐条模板 + 测试用例菜单模板（HTML 格式）。
不执行代码实现，不做状态流转。
```

---

## 执行后必须拿到的回执（检查项）

1. 提示词回填评论 ID（主/验收）
2. 主任务回填评论 ID
3. 验收回填评论 ID（逐条）
4. 测试用例 ID + story↔tcase 关系 ID
5. `jobId` + 日志路径 + 结果摘要
6. 最终状态建议（PASS->status_6 / FAIL->status_4）
