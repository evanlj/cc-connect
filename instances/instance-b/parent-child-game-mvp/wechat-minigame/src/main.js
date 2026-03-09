const { ROUND_CONFIGS, COLORS } = require("./constants");
const { intervalsFromTaps, calcRhythmScore, calcSessionSummary } = require("./game-core");
const api = require("./network");

const SCENES = {
  HOME: "HOME",
  PARENT_INPUT: "PARENT_INPUT",
  TRANSITION: "TRANSITION",
  CHILD_INPUT: "CHILD_INPUT",
  ROUND_RESULT: "ROUND_RESULT",
  FINAL_RESULT: "FINAL_RESULT"
};

const canvas = wx.createCanvas();
const ctx = canvas.getContext("2d");
const sysInfo = wx.getSystemInfoSync();
canvas.width = sysInfo.windowWidth;
canvas.height = sysInfo.windowHeight;

const state = {
  scene: SCENES.HOME,
  sessionId: "",
  sessionStartAt: 0,
  currentRoundIndex: 0,
  roundResults: [],
  parentTapTimes: [],
  childTapTimes: [],
  parentIntervals: [],
  childIntervals: [],
  statusText: "点击开始亲子互动",
  networkReady: false,
  leaderboard: []
};

let buttons = [];
let touchBound = false;

function roundedRectPath(c, x, y, w, h, r) {
  const rr = Math.min(r, w / 2, h / 2);
  c.beginPath();
  c.moveTo(x + rr, y);
  c.lineTo(x + w - rr, y);
  c.arcTo(x + w, y, x + w, y + rr, rr);
  c.lineTo(x + w, y + h - rr);
  c.arcTo(x + w, y + h, x + w - rr, y + h, rr);
  c.lineTo(x + rr, y + h);
  c.arcTo(x, y + h, x, y + h - rr, rr);
  c.lineTo(x, y + rr);
  c.arcTo(x, y, x + rr, y, rr);
  c.closePath();
}

function setScene(scene, extra = {}) {
  state.scene = scene;
  Object.assign(state, extra);
  updateButtons();
}

function currentRound() {
  return ROUND_CONFIGS[state.currentRoundIndex];
}

function titleText() {
  const round = currentRound();
  switch (state.scene) {
    case SCENES.HOME:
      return "默契节拍";
    case SCENES.PARENT_INPUT:
      return `第 ${state.currentRoundIndex + 1} 回合：${round.name}`;
    case SCENES.TRANSITION:
      return "轮到孩子模仿";
    case SCENES.CHILD_INPUT:
      return `第 ${state.currentRoundIndex + 1} 回合：孩子模仿`;
    case SCENES.ROUND_RESULT:
      return `第 ${state.currentRoundIndex + 1} 回合结果`;
    case SCENES.FINAL_RESULT:
      return "亲子结算";
    default:
      return "默契节拍";
  }
}

function createButton({ x, y, w, h, text, type = "primary", onTap }) {
  return { x, y, w, h, text, type, onTap };
}

function updateButtons() {
  const centerX = canvas.width / 2;
  const bottomY = canvas.height - 180;
  const mainW = Math.min(300, canvas.width - 60);
  const mainX = centerX - mainW / 2;

  buttons = [];
  if (state.scene === SCENES.HOME) {
    buttons.push(
      createButton({
        x: mainX,
        y: bottomY,
        w: mainW,
        h: 56,
        text: "开始游戏",
        type: "primary",
        onTap: startGame
      })
    );
    return;
  }

  if (state.scene === SCENES.PARENT_INPUT) {
    buttons.push(
      createButton({
        x: mainX,
        y: bottomY,
        w: mainW,
        h: 64,
        text: "家长点击打节拍",
        type: "accent",
        onTap: onParentTap
      })
    );
    return;
  }

  if (state.scene === SCENES.TRANSITION) {
    buttons.push(
      createButton({
        x: mainX,
        y: bottomY,
        w: mainW,
        h: 56,
        text: "孩子开始模仿",
        type: "primary",
        onTap: startChildRound
      })
    );
    return;
  }

  if (state.scene === SCENES.CHILD_INPUT) {
    buttons.push(
      createButton({
        x: mainX,
        y: bottomY,
        w: mainW,
        h: 64,
        text: "孩子点击模仿",
        type: "accent",
        onTap: onChildTap
      })
    );
    return;
  }

  if (state.scene === SCENES.ROUND_RESULT) {
    const isLast = state.currentRoundIndex >= ROUND_CONFIGS.length - 1;
    buttons.push(
      createButton({
        x: mainX,
        y: bottomY,
        w: mainW,
        h: 56,
        text: isLast ? "查看结算" : "下一回合",
        type: "primary",
        onTap: isLast ? toFinalResult : startNextRound
      })
    );
    return;
  }

  if (state.scene === SCENES.FINAL_RESULT) {
    buttons.push(
      createButton({
        x: mainX,
        y: bottomY - 70,
        w: mainW,
        h: 50,
        text: "再来一局",
        type: "primary",
        onTap: resetToHome
      }),
      createButton({
        x: mainX,
        y: bottomY,
        w: mainW,
        h: 50,
        text: "刷新排行榜",
        type: "accent",
        onTap: refreshLeaderboard
      })
    );
  }
}

function startGame() {
  state.statusText = "正在创建会话...";
  api
    .startSession({ parentName: "爸爸", childName: "孩子", roundCount: ROUND_CONFIGS.length })
    .then((resp) => {
      state.networkReady = true;
      state.sessionId = resp.sessionId || "";
      state.sessionStartAt = Date.now();
      state.currentRoundIndex = 0;
      state.roundResults = [];
      state.statusText = "会话已创建";
      enterParentRound();
    })
    .catch(() => {
      state.networkReady = false;
      state.sessionId = `offline_${Date.now()}`;
      state.sessionStartAt = Date.now();
      state.currentRoundIndex = 0;
      state.roundResults = [];
      state.statusText = "离线模式：网络不可用";
      enterParentRound();
    });
}

function enterParentRound() {
  state.parentTapTimes = [];
  state.childTapTimes = [];
  state.parentIntervals = [];
  state.childIntervals = [];
  const round = currentRound();
  setScene(SCENES.PARENT_INPUT, {
    statusText: `请家长点击 ${round.taps} 次，生成节拍`
  });
}

function startChildRound() {
  setScene(SCENES.CHILD_INPUT, {
    childTapTimes: [],
    childIntervals: [],
    statusText: `请孩子点击 ${currentRound().taps} 次，尝试模仿`
  });
}

function onParentTap() {
  addTap("parent");
}

function onChildTap() {
  addTap("child");
}

function addTap(role) {
  const now = Date.now();
  const round = currentRound();
  const tapField = role === "parent" ? "parentTapTimes" : "childTapTimes";
  const tapTimes = state[tapField];
  const last = tapTimes[tapTimes.length - 1];
  if (last && now - last < 120) {
    return;
  }
  tapTimes.push(now);

  const remain = Math.max(0, round.taps - tapTimes.length);
  state.statusText =
    role === "parent"
      ? `家长已点击 ${tapTimes.length}/${round.taps}（剩余 ${remain} 次）`
      : `孩子已点击 ${tapTimes.length}/${round.taps}（剩余 ${remain} 次）`;

  if (tapTimes.length >= round.taps) {
    if (role === "parent") {
      state.parentIntervals = intervalsFromTaps(state.parentTapTimes);
      setScene(SCENES.TRANSITION, {
        statusText: "家长节拍已记录，请交给孩子"
      });
      return;
    }
    state.childIntervals = intervalsFromTaps(state.childTapTimes);
    settleRound();
  }
}

function settleRound() {
  const round = currentRound();
  const score = calcRhythmScore(state.parentIntervals, state.childIntervals);
  const result = {
    roundIndex: state.currentRoundIndex + 1,
    roundName: round.name,
    score,
    parentIntervals: state.parentIntervals.slice(),
    childIntervals: state.childIntervals.slice()
  };
  state.roundResults.push(result);

  if (state.networkReady) {
    api.submitRound({
      sessionId: state.sessionId,
      roundIndex: result.roundIndex,
      roundName: result.roundName,
      parentIntervals: result.parentIntervals,
      childIntervals: result.childIntervals,
      score: result.score
    }).catch(() => {
      state.statusText = "回合已本地记录（网络上报失败）";
    });
  }

  setScene(SCENES.ROUND_RESULT, {
    statusText: `本回合得分：${score}`
  });
}

function startNextRound() {
  state.currentRoundIndex += 1;
  enterParentRound();
}

function toFinalResult() {
  const summary = calcSessionSummary(state.roundResults);
  const durationMs = Math.max(1000, Date.now() - state.sessionStartAt);
  setScene(SCENES.FINAL_RESULT, {
    statusText: `总分 ${summary.totalScore}，平均 ${summary.averageScore}`
  });

  if (state.networkReady) {
    api.endSession({
      sessionId: state.sessionId,
      totalScore: summary.totalScore,
      durationMs
    })
      .then(() => refreshLeaderboard())
      .catch(() => {
        state.statusText = "已结算（排行榜刷新失败）";
      });
  }
}

function resetToHome() {
  setScene(SCENES.HOME, {
    sessionId: "",
    sessionStartAt: 0,
    currentRoundIndex: 0,
    roundResults: [],
    parentTapTimes: [],
    childTapTimes: [],
    parentIntervals: [],
    childIntervals: [],
    statusText: "点击开始亲子互动"
  });
}

function refreshLeaderboard() {
  if (!state.networkReady) {
    state.statusText = "离线模式：无排行榜";
    return;
  }
  api
    .fetchLeaderboard(5)
    .then((resp) => {
      state.leaderboard = Array.isArray(resp.items) ? resp.items : [];
      state.statusText = "排行榜已刷新";
    })
    .catch(() => {
      state.statusText = "排行榜刷新失败";
    });
}

function drawButton(btn) {
  ctx.save();
  ctx.fillStyle = btn.type === "accent" ? COLORS.accent : COLORS.primary;
  roundedRectPath(ctx, btn.x, btn.y, btn.w, btn.h, 10);
  ctx.fill();
  ctx.fillStyle = "#fff";
  ctx.font = "20px sans-serif";
  ctx.textAlign = "center";
  ctx.textBaseline = "middle";
  ctx.fillText(btn.text, btn.x + btn.w / 2, btn.y + btn.h / 2);
  ctx.restore();
}

function drawCard(x, y, w, h) {
  ctx.save();
  ctx.fillStyle = COLORS.card;
  roundedRectPath(ctx, x, y, w, h, 14);
  ctx.fill();
  ctx.restore();
}

function drawScene() {
  ctx.clearRect(0, 0, canvas.width, canvas.height);
  ctx.fillStyle = COLORS.bg;
  ctx.fillRect(0, 0, canvas.width, canvas.height);

  ctx.fillStyle = COLORS.title;
  ctx.font = "bold 30px sans-serif";
  ctx.textAlign = "center";
  ctx.fillText(titleText(), canvas.width / 2, 72);

  drawCard(24, 100, canvas.width - 48, canvas.height - 300);

  ctx.fillStyle = COLORS.text;
  ctx.font = "20px sans-serif";
  ctx.textAlign = "left";
  ctx.fillText(state.statusText, 44, 145);

  if (state.scene === SCENES.PARENT_INPUT || state.scene === SCENES.CHILD_INPUT) {
    const role = state.scene === SCENES.PARENT_INPUT ? "家长" : "孩子";
    const taps = state.scene === SCENES.PARENT_INPUT ? state.parentTapTimes.length : state.childTapTimes.length;
    const round = currentRound();
    ctx.font = "22px sans-serif";
    ctx.fillText(`${role}点击进度：${taps}/${round.taps}`, 44, 188);
  }

  if (state.scene === SCENES.ROUND_RESULT) {
    const result = state.roundResults[state.roundResults.length - 1];
    if (result) {
      ctx.font = "24px sans-serif";
      ctx.fillStyle = COLORS.success;
      ctx.fillText(`得分：${result.score}`, 44, 188);
      ctx.fillStyle = COLORS.text;
      ctx.font = "18px sans-serif";
      ctx.fillText(`家长节拍：${result.parentIntervals.join(", ")}`, 44, 228);
      ctx.fillText(`孩子节拍：${result.childIntervals.join(", ")}`, 44, 260);
    }
  }

  if (state.scene === SCENES.FINAL_RESULT) {
    const summary = calcSessionSummary(state.roundResults);
    ctx.font = "24px sans-serif";
    ctx.fillStyle = COLORS.success;
    ctx.fillText(`总分：${summary.totalScore}`, 44, 188);
    ctx.fillStyle = COLORS.text;
    ctx.font = "18px sans-serif";
    ctx.fillText(`平均分：${summary.averageScore}`, 44, 220);

    ctx.font = "bold 20px sans-serif";
    ctx.fillStyle = COLORS.title;
    ctx.fillText("排行榜 Top5", 44, 270);
    ctx.font = "16px sans-serif";
    ctx.fillStyle = COLORS.text;

    const list = state.leaderboard.slice(0, 5);
    if (!list.length) {
      ctx.fillText("暂无榜单数据", 44, 298);
    } else {
      list.forEach((item, idx) => {
        const line = `${idx + 1}. ${item.parentName}&${item.childName} - ${item.totalScore}`;
        ctx.fillText(line, 44, 298 + idx * 24);
      });
    }
  }

  buttons.forEach(drawButton);
}

function hitTestButton(x, y) {
  return buttons.find((btn) => x >= btn.x && x <= btn.x + btn.w && y >= btn.y && y <= btn.y + btn.h);
}

function bindTouch() {
  if (touchBound) return;
  wx.onTouchStart((evt) => {
    const t = evt.touches && evt.touches[0];
    if (!t) return;
    const btn = hitTestButton(t.clientX, t.clientY);
    if (btn && typeof btn.onTap === "function") {
      btn.onTap();
    }
  });
  touchBound = true;
}

function startRenderLoop() {
  setInterval(drawScene, 33);
}

function boot() {
  bindTouch();
  updateButtons();
  startRenderLoop();
}

boot();
