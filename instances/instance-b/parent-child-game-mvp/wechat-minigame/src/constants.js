const API_BASE_URL = "http://127.0.0.1:3000/api/v1";

const ROUND_CONFIGS = [
  { name: "热身节拍", taps: 5 },
  { name: "默契挑战", taps: 6 },
  { name: "专家模式", taps: 7 }
];

const COLORS = {
  bg: "#f6f8ff",
  title: "#1e2a48",
  text: "#2e3b60",
  primary: "#4f7cff",
  accent: "#ff9f5a",
  success: "#3cb371",
  danger: "#e35d6a",
  card: "#ffffff"
};

module.exports = {
  API_BASE_URL,
  ROUND_CONFIGS,
  COLORS
};

