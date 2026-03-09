const http = require("http");
const { URL } = require("url");
const path = require("path");
const { DataStore } = require("./store");

const PORT = Number(process.env.PORT || 3000);
const HOST = process.env.HOST || "127.0.0.1";

const store = new DataStore(path.join(__dirname, "data", "db.json"));

function json(res, statusCode, payload) {
  res.writeHead(statusCode, {
    "Content-Type": "application/json; charset=utf-8",
    "Access-Control-Allow-Origin": "*",
    "Access-Control-Allow-Methods": "GET,POST,OPTIONS",
    "Access-Control-Allow-Headers": "Content-Type"
  });
  res.end(JSON.stringify(payload));
}

function readJsonBody(req) {
  return new Promise((resolve, reject) => {
    let raw = "";
    req.on("data", (chunk) => {
      raw += chunk;
      if (raw.length > 1_000_000) {
        reject(new Error("payload too large"));
      }
    });
    req.on("end", () => {
      if (!raw.trim()) {
        resolve({});
        return;
      }
      try {
        resolve(JSON.parse(raw));
      } catch (err) {
        reject(new Error("invalid json body"));
      }
    });
    req.on("error", reject);
  });
}

async function handleRequest(req, res) {
  if (req.method === "OPTIONS") {
    res.writeHead(204, {
      "Access-Control-Allow-Origin": "*",
      "Access-Control-Allow-Methods": "GET,POST,OPTIONS",
      "Access-Control-Allow-Headers": "Content-Type"
    });
    res.end();
    return;
  }

  const url = new URL(req.url, `http://${req.headers.host || "localhost"}`);
  const pathname = url.pathname;

  if (req.method === "GET" && pathname === "/api/v1/health") {
    json(res, 200, { ok: true, time: Date.now() });
    return;
  }

  if (req.method === "POST" && pathname === "/api/v1/game/start") {
    const body = await readJsonBody(req);
    const session = store.createSession(body);
    json(res, 200, {
      sessionId: session.id,
      startAt: session.startAt,
      parentName: session.parentName,
      childName: session.childName
    });
    return;
  }

  if (req.method === "POST" && pathname === "/api/v1/game/round") {
    const body = await readJsonBody(req);
    if (!body.sessionId) {
      json(res, 400, { error: "sessionId required" });
      return;
    }
    const round = store.submitRound(body);
    json(res, 200, { ok: true, round });
    return;
  }

  if (req.method === "POST" && pathname === "/api/v1/game/end") {
    const body = await readJsonBody(req);
    if (!body.sessionId) {
      json(res, 400, { error: "sessionId required" });
      return;
    }
    const session = store.endSession(body);
    json(res, 200, {
      ok: true,
      result: {
        sessionId: session.id,
        totalScore: session.totalScore,
        durationMs: session.durationMs,
        rounds: session.rounds.length
      }
    });
    return;
  }

  if (req.method === "GET" && pathname === "/api/v1/game/leaderboard") {
    const limit = Number(url.searchParams.get("limit") || 20);
    const list = store.getLeaderboard(limit);
    json(res, 200, { items: list });
    return;
  }

  json(res, 404, { error: "not found" });
}

const server = http.createServer((req, res) => {
  handleRequest(req, res).catch((err) => {
    json(res, 500, { error: err.message || "internal error" });
  });
});

server.listen(PORT, HOST, () => {
  // eslint-disable-next-line no-console
  console.log(`[mvp-backend] listening on http://${HOST}:${PORT}`);
});

