# 《默契节拍》父子互动小游戏 MVP 方案

## 1) 目标与边界

### 1.1 目标
- 在 **1~2 周** 内落地可演示、可复用、可扩展的微信小游戏 MVP。
- 验证“父子互动”核心价值：**共同参与、即时反馈、可量化进步**。

### 1.2 非目标（本阶段不做）
- 不做复杂社交裂变（群排行、战队赛）。
- 不做重运营活动系统（任务中心、积分商城）。
- 不做高成本实时语音对战。

---

## 2) 核心玩法（MVP）

游戏名：**默契节拍**

- 回合制：共 3 回合（简单 → 中等 → 困难）
- 每回合流程：
  1. 家长点击按钮打出节拍（N 次点击）
  2. 孩子尝试模仿同样节奏（N 次点击）
  3. 系统按节拍间隔差异给分（0~100）
- 结算页：展示每回合得分、总分、排行入口

评分逻辑（MVP 公式）：
- 抽取点击时间间隔数组 `parentIntervals`、`childIntervals`
- 计算平均绝对误差 `avgDiffMs`
- 得分 `score = round((1 - min(avgDiffMs / 400, 1)) * 100)`

---

## 3) 技术架构

### 3.1 前端（微信小游戏）
- 入口：`game.js`
- 渲染：Canvas 2D
- 输入：`wx.onTouchStart`
- 状态机：Home / ParentInput / ChildInput / RoundResult / FinalResult
- 网络：`wx.request` 调用后端 API

### 3.2 后端（Node 原生 HTTP）
- `POST /game/start`：创建会话
- `POST /game/round`：上报回合
- `POST /game/end`：提交结算
- `GET /game/leaderboard`：读取排行榜

数据存储（MVP）：
- 本地 JSON 文件持久化
- 可平滑迁移到 MySQL/云数据库

---

## 4) 数据模型（MVP）

`Session`
- `id`
- `parentName`
- `childName`
- `roundCount`
- `status`（active/finished）
- `startAt`
- `endAt`
- `rounds[]`
- `totalScore`
- `durationMs`

`Round`
- `roundIndex`
- `roundName`
- `parentIntervals[]`
- `childIntervals[]`
- `score`
- `submittedAt`

---

## 5) 里程碑建议

### M1（D1~D2）
- 搭建小游戏骨架、状态机、基本交互

### M2（D3~D4）
- 节拍采集 + 评分算法 + 回合切换

### M3（D5~D6）
- 后端 API + 数据落库 + 排行榜

### M4（D7）
- 联调、真机验证、Bug 修复、演示版本打包

---

## 6) 风险与应对

- 风险：网络不可达导致上报失败  
  应对：前端本地缓存 + 重试队列（MVP先做失败降级提示）

- 风险：节拍采样噪声（误触、多触点）  
  应对：每次只取首触点 + 最小点击间隔阈值

- 风险：微信域名限制  
  应对：提早申请并配置 request 合法域名

---

## 7) 未来扩展（MVP+）

- 亲子成长档案：周/月趋势图
- 亲子协作挑战：双人接力、语音节奏关卡
- 家长控制台：互动时长、专注度、进步率
- A/B 测试：节拍长度、UI 引导、奖励策略

