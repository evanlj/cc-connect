# 2026-03-24 Squad Worker禁用飞书连接改造说明

## 背景

在 Squad 模式下，主控实例会拉起多个 Worker（Planner / Executor / Reviewer）。  
Worker 之间通过本地 `api.sock` 的 `/ask` 接口协作，不需要直接接入飞书。

现状问题是：Worker 运行时会继承模板中的飞书平台配置，导致每个 Worker 额外建立飞书 WebSocket 连接，带来不必要的连接成本和噪音日志。

## 变更摘要

本次改造目标：**仅在 Squad Worker 运行时禁用飞书连接，不影响普通单实例/主控实例功能。**

### 1) Worker 运行平台构建逻辑调整

文件：`core/squad_process.go`

- 新增 `buildSquadRuntimePlatforms`：
  - 从模板平台列表中过滤掉 `feishu`；
  - 对保留的平台继续注入 Worker 门禁：
    - `allow_from = "__squad_internal_only__"`
    - `reaction_emoji = "none"`
  - 若过滤后平台为空（例如模板只有飞书），自动回退到 `noop` 平台，保证配置仍可启动。

- 保留 `tuneSquadRuntimePlatform` 用于统一门禁注入。

### 2) 新增 noop 平台（仅占位，不连外网）

文件：`platform/noop/noop.go`

- 注册新平台类型：`noop`
- 行为：
  - `Start/Reply/Send/Stop` 全部为本地 no-op
  - 不建立任何外部连接

### 3) 主程序注册 noop 平台

文件：`cmd/cc-connect/main.go`

- 新增空导入：`_ "github.com/chenhg5/cc-connect/platform/noop"`

## 影响评估

### 正向影响

- Squad Worker 不再连接飞书，避免重复 WebSocket 连接。
- Worker 仍可通过 `api.sock` 正常执行 `/ask` 协作流程。
- 模板只有飞书时也能正常启动（自动 fallback 到 noop）。

### 兼容性

- 普通实例（非 Squad Worker）平台逻辑不变。
- 主控实例飞书消息收发与卡片能力不受影响。

## 验证证据

1. 单元测试（新增）
   - `TestBuildSquadRuntimePlatforms_SkipFeishuAndFallbackNoop`
   - `TestBuildSquadRuntimePlatforms_KeepNonFeishuAndApplyGate`

2. 回归建议
   - `go test ./core`
   - `go build ./cmd/cc-connect`

3. 运行态核验
   - 启动一轮 `/squad start ...`
   - 检查 Worker 运行目录下 `config.toml` 的 `[[projects.platforms]]` 不包含 `feishu`
   - 确认 Worker 启动日志不再出现 `feishu: websocket` 相关连接日志

## 风险与回滚

### 风险

- 若未来 Worker 需要主动使用消息平台发送内容，`noop` 回退需要重新评估。

### 回滚

- 回滚 `core/squad_process.go` 中 `buildSquadRuntimePlatforms` 的过滤逻辑；
- 删除 `platform/noop/noop.go` 及 `main.go` 的空导入；
- 重新构建并验证。
