# 多 AI 同群讨论（单机）Runbook

> 适用分支：`mutil-bot`  
> 适用版本：单机多机器人（M0~M4 基础能力）  
> 更新时间：2026-03-17

---

## 1. 目标

在同一台机器上运行 5 个实例（a/b/c/d/e），让它们在同一个飞书群内围绕同一问题进行多轮讨论：

- Jarvis（instance-a）负责主持与总结；
- 其他角色由各自实例 `own bot` 直接发言；
- 支持 `/debate start/status/stop/list`；
- 支持 `/ask` 同步调用链路。

---

## 2. 前置条件

## 2.1 实例要求

需准备 5 个实例目录（或等价运行单元）：

- `instances/instance-a`
- `instances/instance-b`
- `instances/instance-c`
- `instances/instance-d`
- `instances/instance-e`

每个实例都应有：

1. 可启动的 cc-connect 配置（含至少一个 project）  
2. 连接飞书所需的 app_id/app_secret  
3. 有效的 agent provider（Codex/Claude/Gemini 等）

## 2.2 群聊要求

同一个飞书群内，加入 5 个 bot（对应 5 实例）。  
建议在群里先手动 @每个 bot 验证可收发消息。

## 2.3 Socket 要求

每个实例进程启动后，应存在本地 API socket（示例）：

- `instances/instance-a/data/run/api.sock`
- `instances/instance-b/data/run/api.sock`
- `instances/instance-c/data/run/api.sock`
- `instances/instance-d/data/run/api.sock`
- `instances/instance-e/data/run/api.sock`

---

## 3. 启动顺序（建议）

1. 先启动 b/c/d/e（worker）；
2. 再启动 a（host/jarvis）；
3. 最后在飞书群中通过 instance-a 触发 `/debate start`。

> 如你有批量启动脚本（例如本地 `.bat`），可继续沿用；本 runbook 不强依赖脚本名。

---

## 4. 启动后健康检查

## 4.1 Socket 检查（PowerShell）

```powershell
$base = "D:\ai-github\cc-connect\instances"
"a","b","c","d","e" | ForEach-Object {
  $p = Join-Path $base ("instance-{0}\data\run\api.sock" -f $_)
  "{0}: {1}" -f $_, (Test-Path $p)
}
```

预期：5 个都为 `True`。

## 4.2 /ask 链路冒烟（本地 CLI）

```powershell
cc-connect ask -s "feishu:oc_xxx:debate_healthcheck" "仅回复OK"
```

若使用多实例独立 data-dir，请在对应实例目录执行，或补 `--data-dir`。

---

## 5. 讨论执行步骤

## 5.1 发起讨论（在飞书群里）

```text
/debate start --preset tianji-five --rounds 3 --speaking-policy host-decide 讨论同群多AI方案落地路径
```

### 参数建议

- `--preset`：固定 `tianji-five`
- `--rounds`：建议 2~3（MVP）
- `--speaking-policy`：
  - `host-decide`（默认，推荐）
  - `at-least-2`
  - `cover-all-by-end`

## 5.2 查看进度

```text
/debate status
```

或指定 room：

```text
/debate status <room_id>
```

## 5.3 查看历史房间

```text
/debate list
```

## 5.4 手动终止

```text
/debate stop <room_id>
```

---

## 6. 产物与证据路径

默认在 host 实例（instance-a）的数据目录落盘：

- Room：`<data_dir>/discussion/<project>/rooms/<room_id>.json`
- Transcript：`<data_dir>/discussion/<project>/transcripts/<room_id>.jsonl`

可作为 TAPD/复盘证据：

1. room 状态流转  
2. 每轮 speaker 与内容  
3. 角色调用时延（latency_ms）  
4. stop/fail 降级痕迹

---

## 7. 常见问题排查

## 7.1 某角色不发言

优先排查：

1. 目标实例进程是否在运行；
2. 目标 socket 是否存在；
3. 目标实例飞书配置是否有效；
4. 群里是否已加入该 bot。

## 7.2 /debate start 成功但中途失败

查看：

- `/debate status <room_id>` 的状态/stop_reason；
- 对应 transcript 里的 `ERROR:` 条目；
- worker 实例日志中的 `/ask` 调用报错。

## 7.3 权限类工具调用卡住

`/ask` 模式默认不等待人工授权（会拒绝权限请求以防挂死）。  
若角色任务依赖外部工具，需在 agent 侧改为无需权限交互的执行路径，或改走普通会话人工确认流程。

---

## 8. 回滚与降级

若需临时下线讨论能力：

1. 仅停止发起入口（不再使用 `/debate start`）；
2. 保留基础对话、`/send`、`/cron`；
3. 必要时直接回退到不含 debate 变更的 commit。

---

## 9. 运行清单（值班版）

- [ ] 5 实例进程在线  
- [ ] 5 个 api.sock 均存在  
- [ ] 5 个 bot 在同一个飞书群  
- [ ] `/debate start` 可创建房间  
- [ ] `/debate status` 可追踪进度  
- [ ] 至少 2 轮讨论完成  
- [ ] room/transcript 产物落盘  
- [ ] Jarvis 最终总结消息已发出

