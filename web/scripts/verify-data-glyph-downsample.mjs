// 仓库无测试框架,本脚本作为 downsamplePoints 行为合约的 verifier.
// 用法: node web/scripts/verify-data-glyph-downsample.mjs
// 算法逻辑与 web/src/lib/utils/data-glyph-downsample.ts 保持手工同步.

import assert from "node:assert/strict";

function downsamplePoints(values, target) {
  if (target < 3 || values.length <= target) return values;
  const sampled = [values[0]];
  const bucketSize = (values.length - 2) / (target - 2);
  let prevSelectedIdx = 0;
  for (let i = 0; i < target - 2; i++) {
    const nextStart = Math.floor((i + 1) * bucketSize) + 1;
    const nextEnd = Math.min(
      Math.floor((i + 2) * bucketSize) + 1,
      values.length,
    );
    const avgX = (nextStart + nextEnd - 1) / 2;
    let avgY = 0;
    for (let j = nextStart; j < nextEnd; j++) avgY += values[j];
    avgY /= nextEnd - nextStart;
    const curStart = Math.floor(i * bucketSize) + 1;
    const curEnd = Math.floor((i + 1) * bucketSize) + 1;
    let maxArea = -1;
    let chosen = curStart;
    for (let j = curStart; j < curEnd; j++) {
      const area =
        Math.abs(
          (prevSelectedIdx - avgX) *
            (values[j] - values[prevSelectedIdx]) -
            (prevSelectedIdx - j) * (avgY - values[prevSelectedIdx]),
        ) / 2;
      if (area > maxArea) {
        maxArea = area;
        chosen = j;
      }
    }
    sampled.push(values[chosen]);
    prevSelectedIdx = chosen;
  }
  sampled.push(values[values.length - 1]);
  return sampled;
}

// Case 0: 空数组 → 原样(下游 spark_24h 经常用 ?? [] 兜底)
assert.deepEqual(downsamplePoints([], 8), []);

// Case 1: len <= target → 原样
assert.deepEqual(downsamplePoints([10, 20, 30], 8), [10, 20, 30]);

// Case 2: target < 3 → 原样
assert.deepEqual(downsamplePoints([1, 2, 3, 4, 5], 2), [1, 2, 3, 4, 5]);

// Case 3: 全相同值
{
  const input = Array(24).fill(5);
  const out = downsamplePoints(input, 8);
  assert.equal(out.length, 8);
  assert.ok(out.every((v) => v === 5));
}

// Case 4: 单峰中段(idx 5 = 95)
{
  const input = [
    10, 20, 15, 30, 25, 95, 20, 18, 15, 12, 10, 15, 20, 25, 30, 35, 40, 45,
    50, 55, 60, 50, 40, 30,
  ];
  const out = downsamplePoints(input, 8);
  assert.ok(out.includes(95), `expected output to contain 95, got ${out}`);
}

// Case 5: 单调递增 [1..24]
{
  const input = Array.from({ length: 24 }, (_, i) => i + 1);
  const out = downsamplePoints(input, 8);
  assert.equal(out.length, 8);
  assert.equal(out[0], 1);
  assert.equal(out[out.length - 1], 24);
  for (let i = 1; i < out.length; i++) {
    assert.ok(out[i] >= out[i - 1], "must be non-decreasing");
  }
}

// Case 6: 两个尖峰(idx 5 和 idx 15)
{
  const input = [
    10, 20, 15, 30, 25, 95, 20, 18, 15, 12, 10, 15, 20, 25, 30, 90, 40, 45,
    50, 55, 60, 50, 40, 30,
  ];
  const out = downsamplePoints(input, 8);
  assert.ok(out.includes(95), `peak at idx 5 missing: ${out}`);
  assert.ok(out.includes(90), `peak at idx 15 missing: ${out}`);
}

// Case 7: 首末点永远保留
{
  const input = Array.from({ length: 24 }, (_, i) => i * 3);
  const out = downsamplePoints(input, 8);
  assert.equal(out[0], 0);
  assert.equal(out[out.length - 1], 69);
}

// Case 8: target === len → 原样
{
  const input = [10, 20, 30, 40, 50];
  assert.deepEqual(downsamplePoints(input, 5), input);
}

console.log("✓ downsamplePoints 9/9 cases passed");
