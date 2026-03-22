# 多Bot讨论简化流程说明（Consensus v3）

> 更新时间：2026-03-20  
> 适用仓库：`D:\ai-github\cc-connect`  
> 适用分支：`mutil-bot`  
> 适用模式：`/debate start --mode consensus`

---

## 1. 目标

本版本将讨论流程简化为“**主持人先自动完善议题 → 用户确认最终议题 → 主持人首轮回答 → 满意即收尾，不满意再多人讨论**”。

核心原则：

1. 议题先收敛，再讨论方案；
2. 用户只需在关键节点做确认（最终议题、首轮满意度、参与角色）；
3. 多轮分歧收敛由主持人主导，不再每轮等待用户拍板；
4. 会话结束自动产出最终 MD 文档。

---

## 2. 新流程（对应执行顺序）

### 第 1 步：用户抛出原始话题

```text
/debate start --mode consensus 请讨论：如何把需求拆成可并行执行的任务
```

系统进入：`topic_refine_host`。

---

### 第 2 步：主持人自动完善议题（第一版草案）

主持人会输出：

- 增强议题草案
- 补充假设
- 边界与不做项
- 验收标准

然后系统等待用户提交最终议题。

---

### 第 3 步：用户提交最终议题

推荐命令：

```text
/debate topic <room_id> <最终议题>
```

兼容旧命令（等价）：

```text
/debate answer <room_id> <最终议题>
```

系统进入：`host_first_proposal`。

---

### 第 4 步：主持人给第一轮回答

主持人基于“最终议题”给出首轮方案。  
随后进入用户评审阶段。

---

### 第 5 步：用户评审首轮回答

满意：

```text
/debate decision <room_id> approve [可选反馈]
```

不满意：

```text
/debate decision <room_id> reject <反馈>
```

- `approve`：直接收尾，输出结果到 MD；
- `reject`：进入多人讨论选角阶段。

---

### 第 6 步：先列出角色编号+身份，用户按编号选人

系统会自动输出角色清单（示例）：

1. 剑主（jianzhu）- 架构与技术拆解  
2. 文胆（wendan）- 表达与方案文档  
3. 行走（xingzou）- 执行路径与落地推进  
4. 掌柜（zhanggui）- 资源评估与风险控制

用户按编号选择：

```text
/debate participants <room_id> 1,2,4
```

---

### 第 7 步：进入多轮讨论并持续收敛

流程循环：

`all_diverge -> host_collect -> all_resolve -> host_consensus_check`

规则：

- 主持人有收敛权；
- 只要 `unresolved` 非空，就继续下一轮；
- 当 `unresolved` 清空时，自动结束讨论。

---

### 第 8 步：主持人整理结果并落地 MD

会话结束后自动生成：

`.../discussion/.../reports/<room_id>-final.md`

报告包含：

1. 议题演进（原始议题 -> 增强议题草案 -> 用户最终议题）
2. 主持人首轮方案
3. 用户评审记录
4. 多轮分歧收敛轨迹
5. 最终一致结论与行动项

---

## 3. 状态机（简化版）

1. `init`
2. `topic_refine_host`
3. `topic_confirm_user`
4. `host_first_proposal`
5. `user_proposal_review`
6. `participant_confirm`（仅 reject 时进入）
7. `all_diverge`
8. `host_collect`
9. `all_resolve`
10. `host_consensus_check`
11. `finalize_single | host_finalize`
12. `completed`

---

## 4. 常用命令速查

```text
/debate start --mode consensus <原始话题>
/debate topic <room_id> <最终议题>
/debate decision <room_id> approve|reject [反馈]
/debate participants <room_id> 1,2,3
/debate status [room_id]
/debate board [room_id]
/debate list
/debate stop <room_id>
```

---

## 5. 一次完整示例（可复制）

```text
/debate start --mode consensus 请讨论：如何把需求拆成可并行执行的任务
```

（收到主持人增强议题草案后）

```text
/debate topic <room_id> 最终议题：围绕“需求并行拆分”，输出可执行任务表（含owner/依赖/交付物/验收标准），并给出关键路径与风险应对。
```

（收到主持人首轮方案后）

```text
/debate decision <room_id> reject 首轮拆解粒度还不够，需补充跨团队依赖与验收门禁
```

（系统列出编号后）

```text
/debate participants <room_id> 1,2,3,4
```

讨论结束后使用：

```text
/debate status <room_id>
/debate board <room_id>
```

查看最终产物路径（群内会自动通知 `reports/<room_id>-final.md`）。

---

## 6. 注意事项

1. `decision` 仅在“首轮方案评审阶段”可用；
2. `participants` 仅在“参与者确认阶段”可用；
3. `topic/answer` 仅在“最终议题确认阶段”可用；
4. 多轮分歧由主持人收敛，不需要用户每轮介入；
5. 若模型调用超时，系统会启用兜底草案，避免流程中断。

---

## 7. 飞书卡片快捷入口（可选）

发送 `菜单`（或 `/menu`）后，可点击“讨论控制卡”，打开专用讨论卡片。

讨论控制卡支持：

1. 输入 `room_id`（可选）；
2. 一键执行：状态 / 黑板 / 列表 / 停止；
3. 一键填充模板：
   - 最终议题模板
   - 满意/不满意评审模板
   - 选人模板
   - 发起讨论模板

说明：

- 若未填写 `room_id`，模板会保留 `<room_id>` 占位符；
- 停止命令建议务必填写 `room_id`。
