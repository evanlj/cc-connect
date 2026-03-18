# 多 Bot 讨论控制逻辑详解（精简版）

> 更新时间：2026-03-18  
> 适用：`cc-connect` / `mutil-bot` 分支  
> 本文仅保留：**实现原理 + 时序图/流程图 + 使用说明**

---

## 1. 实现原理（核心）

多 Bot 讨论由 4 个核心数据对象 + 1 个执行器组成：

1. **Room（房间）**
   - 保存讨论元信息：`mode/status/phase/iteration/question/roles`。
   - 是流程状态机的主载体。

2. **Runner（执行器）**
   - 后台协程执行流程。
   - 根据 `mode` 分流：
     - `classic`：按轮次发言
     - `consensus`：按主持人驱动阶段流转

3. **Blackboard（黑板）**
   - 共享上下文：`topic/refined_topic/open_questions/consensus_points/unresolved/role_notes`。
   - 用于跨角色对齐与收敛判定。

4. **Transcript（记录）**
   - 每条发言落盘 JSONL，用于恢复、复盘、总结。

5. **Instance API 调用**
   - `Ask`：向目标角色请求回复（不直接发群）
   - `Send`：将角色回复以该角色名义发到群

---

## 2. 流程图（Flowchart）

## 2.1 顶层控制流（命令到执行）

```mermaid
flowchart TD
    U["用户: /debate start ..."] --> A["cmdDebateStart"]
    A --> B["创建 Room + 初始化 Blackboard + 写入初始 Transcript"]
    B --> C["startDebateRunner(roomID)"]
    C --> D["runDebateRoom"]
    D -->|mode=classic| E["classic 轮次流程"]
    D -->|mode=consensus| F["consensus 阶段流程"]
    E --> G["finalizeDebateSummary"]
    F --> G
    G --> H["room.status = completed"]
```

## 2.2 consensus 阶段流（主持人驱动）

```mermaid
flowchart TD
    I["init"] --> C1["clarify_with_user"]
    C1 -->|用户未补充| W["waiting_input<br/>等待 /debate answer"]
    C1 -->|已补充| HS["host_seed"]

    HS -->|成功| AD["all_diverge"]
    HS -->|超时/失败| HSF["host_seed fallback<br/>兜底立论"] --> AD

    AD --> HC["host_collect"]
    HC --> AR["all_resolve(iteration=1)"]
    AR --> CK["host_consensus_check"]

    CK -->|存在 unresolved| AR2["iteration++"] --> AR
    CK -->|无 unresolved| HF["host_finalize"]

    AR -->|iteration > max_rounds| WD["waiting_input<br/>await_user_decision"] --> C1
    HF --> DONE["completed"]
```

---

## 3. 时序图（Sequence）

下面是“单角色一次发言”的标准时序（classic 和 consensus 通用）：

```mermaid
sequenceDiagram
    participant R as Runner
    participant I as Instance API
    participant G as Group(Chat)
    participant B as Blackboard
    participant T as Transcript

    R->>I: Ask(role, prompt, Speak=false)
    I-->>R: content / error
    alt Ask 成功
        R->>I: Send(role, displayContent)
        I-->>G: 角色发言
        R->>B: ApplyRoleContribution + SaveBlackboard
        R->>T: AppendTranscript
    else Ask 超时
        R->>I: retry Ask（rescue prompt）
        alt 重试成功
            R->>I: Send + 写回黑板 + 记录 transcript
        else 重试失败且在 host_seed
            R->>G: 发送 fallback 主持人立论
            R->>B: 写入 fallback 结果
            R->>T: 记录 transcript
        end
end
```

### 3.1 时序图下的流程说明（文字版）

系统从用户在群里发送 `/debate start ...` 开始，先由命令层创建讨论房间与黑板，然后启动后台 runner。  
runner 读取 room 后，根据 `mode` 分流：

- `classic`：按轮次选发言人，依次 Ask→Send→写黑板→写 transcript，最后统一总结。
- `consensus`：按阶段状态机推进：澄清 → 主持人立论 → 全员发散 → 主持人收集 → 全员收敛（循环）→ 主持人总结。

在 `consensus` 里，如果澄清信息不足，会进入 `waiting_input`，等待用户 `/debate answer`；  
如果主持人立论阶段超时，不会直接失败，而是走兜底立论继续流程。  
每次角色发言都会落盘 transcript，并把结构化观点写回 blackboard，供下一步收敛使用。  
当未一致项清零后进入总结阶段，输出最终结论/风险/行动项并结束房间。

---

## 3.2 对应代码函数调用关系（简版）

### A) 命令入口链路

```text
cmdDebate
  └─ cmdDebateStart
      ├─ parseDebateStartOptions
      ├─ ValidateDebateStartOptions
      ├─ NewDebateRoom
      ├─ debateStore.SaveRoom
      ├─ debateStore.LoadOrInitBlackboard
      ├─ debateStore.AppendTranscript(room_created)
      └─ startDebateRunner
          └─ go runDebateRoom
```

### B) runner 分流链路

```text
runDebateRoom
  ├─ debateStore.GetRoom
  ├─ if room.Mode == consensus
  │    └─ runConsensusDebateRoom
  └─ else
       └─ classic 轮次流程
```

### C) classic 模式核心链路

```text
for each round:
  selectDebateSpeakers
  for each speaker:
    askDebateRole -> instanceCli.Ask
    extractDebateDisplayContent
    sendDebateRoleSpeech -> instanceCli.Send (失败降级 SendBySessionKey)
    ApplyRoleContribution -> SaveBlackboard
    AppendTranscript
end
finalizeDebateSummary
```

### D) consensus 模式阶段链路

```text
runConsensusDebateRoom
  ├─ splitConsensusRoles
  ├─ clarify_with_user
  │    ├─ defaultClarifyQuestions
  │    ├─ waiting_input (若未补充)
  │    └─ /debate answer 后恢复
  ├─ host_seed
  │    ├─ askDebateRole(buildConsensusHostSeedPrompt)
  │    └─ 超时/失败 -> buildConsensusHostSeedFallback
  ├─ all_diverge
  │    └─ askDebateRole(buildConsensusWorkerDivergePrompt) * workers
  ├─ host_collect
  │    ├─ askDebateRole(buildConsensusHostCollectPrompt)
  │    └─ summarizeConsensusFromBoard
  ├─ all_resolve (循环)
  │    ├─ askDebateRole(buildConsensusWorkerResolvePrompt) * workers
  │    ├─ askDebateRole(buildConsensusHostCheckPrompt)
  │    ├─ summarizeConsensusFromBoard
  │    └─ unresolved>0 && iteration<=max_rounds -> 下一轮
  └─ host_finalize
       └─ finalizeDebateSummary
```

### E) 单次发言通用链路

```text
askDebateRole
  ├─ resolveRoleSocketPath
  ├─ buildRoleSessionKey
  ├─ instanceCli.Ask
  ├─ timeout -> isAskTimeoutErr -> retry + buildTimeoutRescuePrompt
  └─ 返回 content + latency

sendDebateRoleSpeech
  ├─ instanceCli.Send
  └─ 失败降级：SendBySessionKey(owner)
```

### F) 总结与门禁链路

```text
finalizeDebateSummary
  ├─ buildDebateSummary
  ├─ AskSession(summary)
  ├─ debateSummaryNeedsRepair
  ├─ buildDebateSummaryRepairPrompt (必要时)
  └─ fallbackDebateSummary (仍失败时)
```

---

## 4. 容错机制（实现要点）

1. **超时重试**
   - `askDebateRole` 对 timeout 自动重试 1 次。
   - 重试使用 rescue prompt（更短、更聚焦）。

2. **主持人立论兜底（host_seed）**
   - 如果主持人 Ask 连续失败，不中断整场讨论。
   - 自动生成 fallback 主持人立论，继续进入全员发散阶段。

3. **总结门禁**
   - 总结必须包含：结论 / 风险 / 行动项（owner+deadline+验收）。
   - 不合格自动 repair，仍失败则 fallback summary。

---

## 5. 使用说明

## 5.1 启动讨论

### classic（轮次模式）

```text
/debate start --mode classic --rounds 3 --speaking-policy host-decide 请讨论：<话题> @Jarvis
```

### consensus（共识模式，推荐）

```text
/debate start --mode consensus --rounds 4 请讨论：<话题> @Jarvis
```

## 5.2 澄清/拍板（consensus 必用）

```text
/debate answer <room_id> <补充信息或拍板决策> @Jarvis
```

## 5.3 状态查看

```text
/debate status <room_id> @Jarvis
/debate board <room_id> @Jarvis
```

重点看：
- `mode`
- `phase`
- `iteration`
- `unresolved`

## 5.4 手动恢复与停止

```text
/debate continue <room_id> @Jarvis
/debate stop <room_id> @Jarvis
```

---

## 6. 推荐操作顺序（最小闭环）

1. `/debate start --mode consensus ...`
2. 收到澄清问题后，用 `/debate answer ...` 一次给足：目标/范围/非目标/验收标准
3. 用 `/debate status + /debate board` 观察 `phase/unresolved`
4. 若未收敛，继续 `/debate answer` 做拍板
5. 等待 `host_finalize` + 总结输出
