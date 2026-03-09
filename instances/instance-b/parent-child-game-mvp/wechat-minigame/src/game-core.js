function intervalsFromTaps(timestamps) {
  if (!Array.isArray(timestamps) || timestamps.length < 2) {
    return [];
  }
  const output = [];
  for (let i = 1; i < timestamps.length; i += 1) {
    output.push(Math.max(1, Number(timestamps[i]) - Number(timestamps[i - 1])));
  }
  return output;
}

function normalizeLength(a, b) {
  const maxLen = Math.max(a.length, b.length);
  if (maxLen === 0) {
    return { a2: [], b2: [] };
  }
  const a2 = a.slice(0, maxLen);
  const b2 = b.slice(0, maxLen);
  while (a2.length < maxLen) a2.push(a2[a2.length - 1] || 600);
  while (b2.length < maxLen) b2.push(b2[b2.length - 1] || 600);
  return { a2, b2 };
}

function calcRhythmScore(parentIntervals, childIntervals) {
  const p = Array.isArray(parentIntervals) ? parentIntervals : [];
  const c = Array.isArray(childIntervals) ? childIntervals : [];
  if (!p.length || !c.length) {
    return 0;
  }
  const { a2, b2 } = normalizeLength(p, c);
  const totalDiff = a2.reduce((acc, value, idx) => {
    return acc + Math.abs(Number(value) - Number(b2[idx]));
  }, 0);
  const avgDiff = totalDiff / a2.length;
  const ratio = 1 - Math.min(avgDiff / 400, 1);
  return Math.round(ratio * 100);
}

function calcSessionSummary(rounds) {
  const list = Array.isArray(rounds) ? rounds : [];
  const totalScore = list.reduce((acc, item) => acc + Number(item.score || 0), 0);
  const averageScore = list.length ? Math.round(totalScore / list.length) : 0;
  return {
    totalScore,
    averageScore
  };
}

module.exports = {
  intervalsFromTaps,
  calcRhythmScore,
  calcSessionSummary
};

