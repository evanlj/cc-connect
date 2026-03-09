const { API_BASE_URL } = require("./constants");

function request(path, method, data) {
  return new Promise((resolve, reject) => {
    wx.request({
      url: `${API_BASE_URL}${path}`,
      method,
      data,
      timeout: 4000,
      success: (res) => {
        if (res.statusCode >= 200 && res.statusCode < 300) {
          resolve(res.data || {});
          return;
        }
        reject(new Error(`http status ${res.statusCode}`));
      },
      fail: reject
    });
  });
}

function startSession(payload) {
  return request("/game/start", "POST", payload);
}

function submitRound(payload) {
  return request("/game/round", "POST", payload);
}

function endSession(payload) {
  return request("/game/end", "POST", payload);
}

function fetchLeaderboard(limit = 10) {
  return request(`/game/leaderboard?limit=${limit}`, "GET");
}

module.exports = {
  startSession,
  submitRound,
  endSession,
  fetchLeaderboard
};

