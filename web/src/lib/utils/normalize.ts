/**
 * 把任意 number[] 按 min-max 归一到 [0, 100]。
 * 用于把 spark_24h(原始计数)交给 <DataGlyph kind="line"/"bar"> 之前预处理。
 *
 * 假设:输入值 ≥ 0(spark 数据源都是 request count / token count / latency 等非负指标)。
 * 若将来出现可负指标(如利润差值),全等负值 case 会走 max>0 → 0 分支,语义需要单独评估。
 *
 * 边界:
 *   - 空数组 → 返回空数组
 *   - 全相等 + 值 > 0 → 返回与输入等长的 50 数组(中线,表达"有数据但平稳",避免 spark 全贴底丢"有数据"感)
 *   - 全相等 + 值 === 0 → 返回 0 数组(真无数据,贴底)
 */
export function normalize0to100(values: number[]): number[] {
  if (values.length === 0) return [];
  const min = Math.min(...values);
  const max = Math.max(...values);
  if (max === min) {
    return values.map(() => (max > 0 ? 50 : 0));
  }
  return values.map((v) => ((v - min) / (max - min)) * 100);
}
