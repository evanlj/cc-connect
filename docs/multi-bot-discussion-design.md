# 多 AI 同群讨论（Feishu）详细实现方案（单机多机器人优先）

> 分支：`mutil-bot`  
> 文档版本：v3（单机 MVP 已落地）  
> 更新时间：2026-03-17  
> 目标：在**同一个飞书群**里，让 5 个固定角色 AI 围绕同一问题进行多轮讨论，且每个角色由其 own bot 直接发言（非主持人转述）。

---

## 0. 本版结论（冻结）

根据最新确认，本版方案只做 **单机多机器人**：

1. ✅ 固定 5 角色（与参考文章一致）  
2. ✅ 角色本人发言（bot 身份可见）  
3. ✅ 每轮由 Jarvis 动态点名，不强制 5 人全发  
4. ✅ 先在同机闭环实现可用能力（MVP）  
5. ❌ 暂不实现跨机器/跨网络（作为后续扩展章节保留）

---

## 0.1 当前实现状态（2026-03-17）

以下状态基于 `mutil-bot` 分支当前代码：

| 里程碑 | 状态 | 实现摘要 |
|---|---|---|
| M0 脚手架 | ✅ 已完成 | debate store 根目录推导、固定 5 角色映射 |
| M1 命令与房间 | ✅ 已完成 | `/debate start/status/stop/list` + room/transcript 落盘 |
| M2 Ask 通道 | ✅ 已完成 | `POST /ask` + `Engine.AskSession` + `cc-connect ask` |
| M3 多轮编排 | ✅ 已完成（单机） | 后台 runner、多轮发言、动态点名、主持总结 |
| M4 基础门禁 | ✅ 已完成（基础版） | 互斥、超时、失败降级、手动 stop 取消 |
| M5 二期增强 | ⏳ 未开始 | 投票裁决、跨网 relay、任务系统自动化 |

本版对应实操指南见：`docs/multi-bot-discussion-runbook.md`。

---

## 1. 固定角色与实例映射（MVP）

参考文章：  
`https://x.com/xiangqiling5204/status/2033087263794675768`

固定角色（不可替换）：

1. **Jarvis（总管/主持）** → `instance-a`
2. **剑主（工程执行）** → `instance-b`
3. **文胆（内容）** → `instance-c`
4. **行走（增长）** → `instance-d`
5. **掌柜（运营）** → `instance-e`

> 注：当前仓库已常见 `a/b/c`，MVP 需补齐 `d/e` 运行单元（或等价实例）并加入同一个飞书群。

---

## 2. 边界定义（避免范围蔓延）

### 2.1 In Scope（本次必须做）

- `/debate` 指令集（start/status/stop/list）
- 讨论房间状态管理（room + transcript）
- 主持人编排多轮讨论
- 动态点名发言策略（host-decide 为默认）
- 角色实例本体发言（禁止主持人代发正文）
- Ask 能力（同步获取角色回答）
- 单机本地实例通信（Unix socket）

### 2.2 Out of Scope（本次不做）

- 跨机器/跨公网 relay
- 节点鉴权、mTLS、请求签名
- 多机故障转移
- 任意角色扩容（>5）与投票机制

---

## 3. 现有代码基线（事实锚点）

| 能力 | 当前现状 | 证据锚点 |
|---|---|---|
| 命令路由 | 已支持 slash 命令分发，可扩展新命令 | `core/engine.go` `handleCommand(...)` |
| trace 命令模式 | 已有复杂命令解析范式，可复用 | `core/engine.go` `cmdTrace(...)` |
| API 服务 | 已有本地 Unix socket API（/send 等） | `core/api.go` `NewAPIServer`, `handleSend` |
| 主动发消息 | 支持根据 sessionKey 主动发送（已有 cron 机制） | `core/engine.go` `ExecuteCronJob(...)` |
| ReplyCtx 重建 | Feishu 可从 sessionKey 重建 replyCtx | `platform/feishu/feishu.go` `ReconstructReplyCtx(...)` |
| CLI 子命令扩展 | 已有 send/cron/provider 子命令入口 | `cmd/cc-connect/main.go` |

---

## 4. 目标架构（单机）

### 4.1 组件划分

1. **Host 编排器（instance-a / Jarvis）**
   - 接收 `/debate` 命令
   - 创建 Room / 控制轮次 / 收敛总结
2. **Worker 执行器（instance-b/c/d/e）**
   - 接收 `/ask` 请求
   - 产出角色观点
   - 按要求由该实例 bot 发言到群
3. **本地实例客户端（Local Instance Client）**
   - 通过 Unix socket 调用目标实例 API（/ask、/send）
4. **Room Store**
   - 保存房间状态、轮次进度、转录日志

### 4.2 单轮链路（关键）

1. 用户在群里发：`/debate start ...`
2. Jarvis 创建 room，并宣布开始
3. Jarvis 选出本轮 speaker 列表（可 1~5 人）
4. 对每个 speaker：
   - 调用该实例 `/ask` 拿到文本
   - 由该实例 bot 直接发群消息（可通过 `speak=true` 一步完成）
5. Jarvis 判断是否进入下一轮
6. 结束后 Jarvis 发送总结（结论/风险/行动项/验收）

---

## 5. 详细设计（文件级）

## 5.1 命令层（Engine）

### 新增命令

- `/debate start --preset tianji-five --rounds 3 <问题>`
- `/debate status [room_id]`
- `/debate stop <room_id>`
- `/debate list`

### 代码落点

- `core/engine.go`
  - `handleCommand(...)` 增加 `case "/debate":`
  - 新增 `cmdDebate(...)`
  - 新增参数解析 helper（仿照 `cmdTrace` 风格）

---

## 5.2 讨论域模型（Room / Transcript）

### 建议新增文件

- `core/discussion_types.go`
- `core/discussion_store.go`

### 数据路径（instance-a）

- `<data_dir>/discussion/rooms/<room_id>.json`
- `<data_dir>/discussion/transcripts/<room_id>.jsonl`

### Room 示例

```json
{
  "room_id": "debate_20260317_001",
  "status": "running",
  "created_at": "2026-03-17T20:00:00+08:00",
  "owner_session_key": "feishu:oc_chat_xxx:ou_user_xxx",
  "group_chat_id": "oc_chat_xxx",
  "question": "如何实现同群多AI讨论",
  "max_rounds": 3,
  "current_round": 1,
  "speaking_policy": "host-decide",
  "roles": [
    {"role":"jarvis","instance":"instance-a","project":"instance-a-project","socket":"instances/instance-a/data/run/api.sock"},
    {"role":"jianzhu","instance":"instance-b","project":"instance-b-project","socket":"instances/instance-b/data/run/api.sock"},
    {"role":"wendan","instance":"instance-c","project":"instance-c-project","socket":"instances/instance-c/data/run/api.sock"},
    {"role":"xingzou","instance":"instance-d","project":"instance-d-project","socket":"instances/instance-d/data/run/api.sock"},
    {"role":"zhanggui","instance":"instance-e","project":"instance-e-project","socket":"instances/instance-e/data/run/api.sock"}
  ]
}
```

### Transcript 行示例

```json
{"round":1,"speaker":"jianzhu","posted_by":"instance-b","content":"...","latency_ms":12654,"at":"2026-03-17T20:03:02+08:00"}
```

---

## 5.3 编排层（Orchestrator）

### 建议新增文件

- `core/discussion_orchestrator.go`

### 核心方法

- `StartRoom(...)`
- `RunRoom(...)`
- `RunRound(...)`
- `SelectSpeakers(...)`
- `BuildRolePrompt(...)`
- `AskAndSpeak(...)`
- `SummarizeAndClose(...)`

### 回合伪代码

```text
for round in 1..max_rounds:
  speakers = SelectSpeakers(policy, round, transcript)
  for speaker in speakers:
    prompt = BuildRolePrompt(role=speaker, question, transcript_tail)
    resp = AskAndSpeak(target=speaker.instance, prompt, speak=true)
    AppendTranscript(resp)
  if ShouldStop(round, transcript):
    break
SummarizeAndClose()
```

---

## 5.4 实例通信层（单机 Unix Socket）

### 建议新增文件

- `core/instance_client.go`

### 能力

- `CallSend(socketPath, project, sessionKey, message)`
- `CallAsk(socketPath, AskRequest) -> AskResponse`

> 说明：单机 MVP 下，Host 直接访问各实例 `data/run/api.sock`，不引入 relay。

---

## 5.5 Ask 能力（关键）

### API 扩展（每个实例）

在 `core/api.go` 增加：

- `POST /ask`

### 请求体（建议）

```json
{
  "project": "instance-b-project",
  "session_key": "feishu:oc_chat_xxx:debate_jianzhu_room001",
  "prompt": "请从工程执行角度给出3个实现风险和2个替代方案",
  "timeout_sec": 120,
  "speak": true,
  "speak_prefix": "【剑主】"
}
```

### 响应体（建议）

```json
{
  "status": "ok",
  "content": "1) ... 2) ... 3) ...",
  "latency_ms": 18340,
  "model": "gpt-4.1"
}
```

### Engine 扩展（建议）

- `core/engine.go`
  - 新增 `AskSession(sessionKey, prompt, timeout)`：同步拿最终文本
  - 新增 `SendBySessionKey(sessionKey, content)`：通过 `ReconstructReplyCtx` 主动发送

> `SendBySessionKey` 可复用 `ExecuteCronJob(...)` 与 `trace_translate.go` 中 `sendToSession(...)` 的模式。

---

## 5.6 Prompt 规范（主任务 / 角色 / 验收）

### 统一结构

```text
【角色立场】
【核心观点（<=3条）】
【依据/数据】
【风险与反例】
【建议动作（含优先级）】
```

### 角色模板

- Jarvis：控场、点名、收敛、裁决
- 剑主：实现路径、技术风险、工期成本
- 文胆：信息表达、内容结构、认知负担
- 行走：增长杠杆、实验设计、指标影响
- 掌柜：运营可落地性、资源排班、SOP

### Fast Lane（本项目定义）

当问题清晰、目标明确时，Jarvis 可走 Fast Lane：

1. 每轮只点名最关键 2~3 角色
2. 每位角色输出上限 6 行
3. 2 轮内必须给出“可执行方案 + 风险 + 验收”
4. 避免全员长篇并行导致噪音与 token 膨胀

---

## 6. 每轮是否全员发言（规则）

不要求每轮 5 人都发。  
默认策略：`host-decide`

支持策略（MVP 可先实现前两项）：

- `host-decide`：Jarvis 自由点名（默认）
- `at-least-2`：每轮至少 2 角色
- `cover-all-by-end`：结束前 5 角色至少各一次（可作为 M2）

---

## 7. 大文档场景的上下文治理（重点）

当讨论对象是“大型实现方案/开发计划 md”时，采用 **文档分片 + 上下文包**，避免上下文溢出。

### 7.1 文档分片建议

- `00-overview.md`（目标/边界）
- `10-requirements.md`（需求/约束）
- `20-architecture.md`（架构）
- `30-impl-plan.md`（文件级实现）
- `40-test-and-acceptance.md`（测试与验收）
- `50-risks-and-rollback.md`（风险与回滚）

### 7.2 上下文包（Context Pack）

每轮只给模型“必要片段”，而不是整份大文档：

1. 固定注入：`00-overview + 当前任务相关分片摘要`
2. 动态注入：最近 1~2 轮讨论结论
3. 禁止注入：历史无关长日志/重复段落

### 7.3 版本锚点

- 每次更新写 `changelog`（3 行内）
- 在讨论中只引用“最新版本号 + 段落锚点”
- 避免重复粘贴整段正文

> 配套模板见：`docs/templates/`

---

## 8. 开发里程碑（单机 MVP）

## M0：脚手架与配置（0.5d）

- 建 `discussion` 相关类型与 store
- 固定 5 角色映射配置
- 加载 socket/project 映射

## M1：命令与房间管理（1d）

- `/debate start/status/stop/list`
- Room 创建、状态更新、落盘

## M2：Ask 通道（1.5d）

- `/ask` API
- `Engine.AskSession`
- `InstanceClient.CallAsk`

## M3：多轮编排 + 本人发言（1.5d）

- Speaker 选择
- Ask + Speak 链路
- 主持人总结

## M4：门禁与验收（1d）

- 并发互斥、超时熔断、长度限制、防循环
- 验收脚本/日志证据

---

## 9. 测试与验收（DoD）

### 功能验收

1. 一条 `/debate start` 能创建 room 并进入 running
2. 至少 2 轮讨论可执行
3. 每轮 speaker 由 Jarvis 动态选择，不强制全员发言
4. Worker 发言必须由 Worker bot 发送（消息发送者正确）
5. `/debate status` 可查看进度，`/debate stop` 可停止

### 稳定性验收

1. 单角色超时不崩全局（降级继续）
2. room 锁避免并发回合冲突
3. transcript 与 room 状态一致可追溯

### 证据要求

- room JSON 文件
- transcript JSONL 文件
- 关键日志片段（含 room_id / round / role / latency）
- 飞书群消息截图（可选）

---

## 10. 风险与回滚

| 风险 | 影响 | 缓解 |
|---|---|---|
| 指令解析复杂导致误触发 | 误开讨论 | 严格子命令解析 + help |
| Ask 耗时过长 | 回合阻塞 | per-role timeout + skip |
| 消息过长刷屏 | 群体验差 | 限长 + 自动摘要 |
| 状态文件损坏 | 房间不可恢复 | 原子写 + 临时文件替换 |
| 角色发言漂移 | 讨论失焦 | 角色 prompt + 结构化输出 |

回滚策略：

1. Feature flag：`debate_enabled=false` 可一键关闭
2. 保留 `/debate` 入口但返回“功能关闭”
3. 不影响现有 `/send`、`/cron`、普通对话链路

---

## 11. 后续扩展（明确为二期）

1. 跨机器/跨网络 relay
2. 节点鉴权与签名
3. 动态角色池（>5）
4. 投票/裁决机制
5. 讨论结果自动转 TAPD / Issue

---

## 12. 配套文档模板

- 实现方案模板：`docs/templates/implementation-plan-template.md`
- 开发计划模板：`docs/templates/development-plan-template.md`
- 实操 Runbook：`docs/multi-bot-discussion-runbook.md`

---

## 13. 最终结论

在当前约束下，**最短可落地路径**是：

**单机 Host 编排 + Worker `/ask` 同步回答 + Worker own bot 发言 + Room 状态机 + 强门禁。**

先把这个 MVP 跑通，再进入跨网版本，能显著降低复杂度与返工成本。
