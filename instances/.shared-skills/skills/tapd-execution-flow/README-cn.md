# tapd-execution-flow（中文快速使用）

## 1. 这个 Skill 做什么

`tapd-execution-flow` 用于按固定门禁执行 TAPD 需求，避免漏步骤。  
核心流程：

1) 获取需求  
2) 确认主/验收模型  
3) 生成并确认主/验收提示词  
4) 回填提示词到 TAPD  
5) 执行主任务并回填  
6) 执行验收并逐条三段式回填  
7) 回填测试用例并建立 story↔tcase 关联  
8) 按结果流转状态（PASS->`status_6`，FAIL->`status_4`）

---

## 2. 触发方式（推荐说法）

在会话里直接说以下任一类指令即可：

- `用 tapd-execution-flow 执行需求 1000076`
- `按 tapd-execution-flow 跑下一个 TAPD 需求`
- `按 tapd-execution-flow 重跑 1000081 的整改轮`
- `按 tapd-execution-flow 输出可直接回填的 HTML 评论`

---

## 3. 常见输入参数（口头约定）

建议在启动时明确这些信息：

- 需求 ID（story id）
- 主任务模型（例如 `rc + 5.2`）
- 验收任务模型（例如 `rc + 5.3-codex`）
- 关键硬约束（资源全量、场景可验收、Skill 可回滚等）

---

## 4. 输出与回填要求

### 必须回填
- 提示词（主/验收）
- 主任务执行结果
- 验收逐条结果（内容/过程/结果）
- 测试用例菜单内容（非仅评论）
- 关系 ID（story↔tcase）

### 必须留痕
- `jobId`
- 日志路径
- 结果摘要
- TAPD 评论 ID
- 状态流转结果

---

## 5. FAIL 处理

当验收 FAIL：

1) 保持 `status_4`  
2) 回填失败项  
3) 重新确认模型  
4) 重新确认提示词  
5) 修复并复验  
6) 直到 PASS 后再改 `status_6`

---

## 6. 相关文档

- 技能主文档：`SKILL.md`
- 中文流程说明：`SKILL-cn.md`
- 清单：`references/workflow-checklist.md`
- 回填模板：`references/tapd-html-templates.md`
- 整改循环：`references/remediation-loop.md`
