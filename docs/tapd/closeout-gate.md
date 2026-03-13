# TAPD Closeout Gate（状态流转前门禁）检查清单

目标：在将 story/bug 置为“完成/关闭”前执行一次机械校验，做到：
- 默认放行（PASS）
- 缺证据/缺门禁时阻塞（BLOCK）并给出 unblock checklist（可执行修复步骤）

---

## 1) Story Closeout Gate（需求关闭前）

### 必须项（缺任一项 = BLOCK）
1. 已拉取并记录需求详情（含最新 description / status / owner 等）
2. 方案/主/验收模型已分别确认，并回填到 TAPD（可追溯 comment_id）
3. 实现方案/DoD 已与用户达成一致，并回填到 TAPD（可追溯 comment_id）
4. 主任务提示词已回填（HTML 可见换行）
5. 主任务执行结果已回填（含证据路径/日志）
6. 验收逐条三段式已回填：
   - 验收内容
   - 验收过程
   - 验收结果（PASS/FAIL + 证据锚点）
7. 测试用例菜单已更新（不是仅评论）
8. story ↔ tcase 关联已建立（relation_id 可追溯）
9. 本轮留痕齐全：
   - jobId / 日志路径 / 结果摘要 / comment IDs / 状态流转结果

### 建议项（不阻塞，但应提示）
- 提示词/验收清单有版本号（v1/v2…）
- evidence 目录有统一命名（便于批量检索）

---

## 2) Bug Closeout Gate（缺陷关闭前）

### 必须项（缺任一项 = BLOCK）
1. 已拉取缺陷详情 + 评论 + 重开原因（如有）
2. “最终缺陷结论”已写回缺陷 description（复现/预期/实际/验收标准）
3. 方案/执行/验收模型已分别确认，并回填到 TAPD（comment_id 可追溯）
4. 修复方案/DoD 已与用户达成一致，并回填到 TAPD（comment_id 可追溯）
5. 修复提示词已确认并回填（comment_id 可追溯）
6. 验收提示词已确认并回填（comment_id 可追溯）
7. 复验逐条三段式已回填（含证据锚点）
