/**
 * LTTB(Largest-Triangle-Three-Buckets)降采样.
 * 把 N 个点降到 target 个,首末点保留,中间按"与前后桶平均位置构成的三角形
 * 面积最大"原则选点 → 保留视觉峰谷.
 *
 * 边界: 空数组 / length ≤ target / target < 3 都原样返回(passthrough).
 *
 * 参考: Steinarsson 2013 / github.com/sveinn-steinarsson/lttb (MIT)
 *
 * 行为合约由 web/scripts/verify-data-glyph-downsample.mjs 锁定(9 case).
 * 改动本函数请同步更新该 verifier.
 */
export function downsamplePoints(
  values: number[],
  target: number,
): number[] {
  if (target < 3 || values.length <= target) return values;

  const sampled: number[] = [values[0]];
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
