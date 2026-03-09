const fs = require("fs");
const path = require("path");
const crypto = require("crypto");

function now() {
  return Date.now();
}

function createId(prefix) {
  return `${prefix}_${crypto.randomBytes(6).toString("hex")}`;
}

class DataStore {
  constructor(filePath) {
    this.filePath = filePath || path.join(__dirname, "data", "db.json");
    this.data = {
      sessions: [],
      leaderboard: []
    };
    this._ensureLoaded();
  }

  _ensureLoaded() {
    const dir = path.dirname(this.filePath);
    if (!fs.existsSync(dir)) {
      fs.mkdirSync(dir, { recursive: true });
    }
    if (fs.existsSync(this.filePath)) {
      const raw = fs.readFileSync(this.filePath, "utf8");
      if (raw.trim()) {
        this.data = JSON.parse(raw);
      }
      return;
    }
    this._persist();
  }

  _persist() {
    fs.writeFileSync(this.filePath, JSON.stringify(this.data, null, 2), "utf8");
  }

  createSession(payload) {
    const parentName = String(payload.parentName || "爸爸").slice(0, 20);
    const childName = String(payload.childName || "孩子").slice(0, 20);
    const roundCount = Number(payload.roundCount || 3);

    const session = {
      id: createId("sess"),
      parentName,
      childName,
      roundCount,
      status: "active",
      startAt: now(),
      endAt: null,
      rounds: [],
      totalScore: 0,
      durationMs: 0
    };
    this.data.sessions.push(session);
    this._persist();
    return session;
  }

  getSession(sessionId) {
    return this.data.sessions.find((s) => s.id === sessionId) || null;
  }

  submitRound(payload) {
    const session = this.getSession(payload.sessionId);
    if (!session) {
      throw new Error("session not found");
    }
    if (session.status !== "active") {
      throw new Error("session is not active");
    }
    const round = {
      roundIndex: Number(payload.roundIndex || 0),
      roundName: String(payload.roundName || ""),
      parentIntervals: Array.isArray(payload.parentIntervals) ? payload.parentIntervals : [],
      childIntervals: Array.isArray(payload.childIntervals) ? payload.childIntervals : [],
      score: Number(payload.score || 0),
      submittedAt: now()
    };
    session.rounds.push(round);
    this._persist();
    return round;
  }

  endSession(payload) {
    const session = this.getSession(payload.sessionId);
    if (!session) {
      throw new Error("session not found");
    }
    const totalScore = Number(payload.totalScore || 0);
    const durationMs = Number(payload.durationMs || Math.max(0, now() - session.startAt));

    session.totalScore = totalScore;
    session.durationMs = durationMs;
    session.endAt = now();
    session.status = "finished";

    this.data.leaderboard.push({
      id: createId("rank"),
      sessionId: session.id,
      parentName: session.parentName,
      childName: session.childName,
      totalScore: session.totalScore,
      durationMs: session.durationMs,
      endAt: session.endAt
    });
    this.data.leaderboard.sort((a, b) => {
      if (b.totalScore !== a.totalScore) {
        return b.totalScore - a.totalScore;
      }
      return a.durationMs - b.durationMs;
    });
    this.data.leaderboard = this.data.leaderboard.slice(0, 200);
    this._persist();
    return session;
  }

  getLeaderboard(limit = 20) {
    const n = Math.max(1, Math.min(100, Number(limit || 20)));
    return this.data.leaderboard.slice(0, n);
  }
}

module.exports = {
  DataStore
};

