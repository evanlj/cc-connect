# 父子互动小游戏 MVP（微信小游戏 + Node API）

本目录提供一个可直接二次开发的 **“父子互动”小游戏 MVP**：

- 前端：微信小游戏（纯 JS，无构建步骤）
- 后端：Node.js 原生 HTTP API（无第三方依赖）
- 测试：Node 内置 `node:test`（最小单测）

游戏名（MVP）：**《默契节拍》**

- 玩法：家长先打节拍，孩子后模仿；共 3 回合
- 核心指标：每回合节拍匹配得分 + 总分
- 数据闭环：会话开始 → 回合上报 → 结束结算 → 排行榜查询

---

## 1. 目录结构

```text
parent-child-game-mvp/
  docs/
    mvp-plan.md                  # MVP 方案文档
  backend/
    package.json
    server.js                    # API Server
    store.js                     # 持久化与会话存储
    test/
      store.test.js
  wechat-minigame/
    game.js                      # 微信小游戏入口
    game.json
    project.config.json
    src/
      constants.js
      game-core.js               # 纯算法（可单测）
      main.js                    # 场景状态机 + 渲染 + 触摸交互
      network.js                 # wx.request 封装
  test/
    game-core.test.js
```

---

## 2. 快速启动

### 2.1 启动后端 API

```powershell
cd backend
node server.js
```

默认监听：`http://127.0.0.1:3000`

健康检查：

```powershell
curl http://127.0.0.1:3000/api/v1/health
```

### 2.2 打开微信小游戏

1. 打开微信开发者工具（小游戏）
2. 导入目录：`wechat-minigame/`
3. 修改后端地址（如需）：
   - 文件：`wechat-minigame/src/constants.js`
   - 字段：`API_BASE_URL`

> 真机调试时，`127.0.0.1` 需替换为局域网 IP 或可访问域名，并配置微信合法域名。

---

## 3. API 一览

- `POST /api/v1/game/start`
  - 入参：`{ parentName, childName, roundCount }`
  - 出参：`{ sessionId, startAt }`
- `POST /api/v1/game/round`
  - 入参：`{ sessionId, roundIndex, roundName, parentIntervals, childIntervals, score }`
- `POST /api/v1/game/end`
  - 入参：`{ sessionId, totalScore, durationMs }`
- `GET /api/v1/game/leaderboard?limit=20`

---

## 4. 运行测试

```powershell
# 后端存储测试
cd backend
npm test

# 核心算法测试
cd ..
node --test .\test\game-core.test.js
```

---

## 5. MVP 下一步建议

- 接入微信登录（`openid`）与家庭关系绑定
- 云开发 / 云函数部署，替换本地 Node 服务
- 加入语音鼓励、成就徽章、分享卡片
- 加入“父母端数据看板”（连续天数、互动时长、进步曲线）

