# cc-connect instances 稳定版说明（A/B/超级管家）

本文档用于固定“可用优先”的实例配置规则，避免再次出现「缺少 API Key」等运行时问题。

## 1) 推荐原则（稳定优先）

- 每个实例使用独立 `config.toml` 与独立 `data_dir`。
- `provider` 只保留最小必要字段：
  - `name`
  - `api_key`
  - `base_url`
  - `model`
- **先不要使用** `[[projects.agent.providers]].env` 注入 `CODEX_HOME`（已验证会导致部分场景认证异常）。

## 2) 推荐配置模板（Codex）

```toml
[[projects.agent.providers]]
name = "openai-main"
api_key = "sk-xxxx"
base_url = "https://right.codes/codex/v1"
model = "gpt-5.3-codex"
```

> 注意：不要在 provider 段落里加 `env = { CODEX_HOME = ... }`，除非你明确知道其副作用并做好回滚。

## 3) 已知坑（本机已遇到）

### 现象
- 飞书可正常收消息，但调用模型报错：
  - `{"error":"缺少 API Key"}`
  - URL 常见为 `.../responses`

### 根因（本机复盘）
- 启用 `providers.env` 注入 `CODEX_HOME` 后，运行态认证链路异常，导致请求未携带有效 key。

### 处理
- 删除 `providers.env`（尤其 `CODEX_HOME`）后恢复正常。

## 4) 标准排查流程（2 分钟）

1. 查看当前 provider：
   - 飞书里执行：`/provider list`
2. 强制切换并重建会话：
   - `/provider switch <name>`
   - `/new`
3. 若仍失败，重启对应实例进程（按 `-config` 精确重启）。
4. 若主线路失败，切到备用 provider 做链路对比（`/provider switch zwenooo`）。

## 5) 快速回滚（30 秒恢复）

当出现认证异常时，先执行最小回滚：

1. 从实例 `config.toml` 删除所有 `providers.env`。
2. 保留 provider 的 `api_key/base_url/model`。
3. 重启该实例。
4. 飞书内执行：
   - `/provider switch <目标provider>`
   - `/new`

## 6) 关于 reasoning_effort

- `model_reasoning_effort` 属于 Codex 配置，不是 cc-connect 原生字段。
- 为了稳定，建议先使用全局 `~/.codex/config.toml` 设置；
- 需要实例级区分时，建议用“启动脚本注入环境变量”的方式做灰度，不要直接放进 `providers.env`。

