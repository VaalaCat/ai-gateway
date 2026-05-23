// 跨模块共享的可观测性基础类型，消除各文件中的重复定义

/** 时间粒度 */
export type ObsGranularity = "day" | "hour";

/** 已解析的时间范围（unix 秒），用于 hook 和组件 */
export interface ObsRange {
  start: number; // unix seconds
  end: number;   // unix seconds
  gran: ObsGranularity;
}

/** API 请求时间范围参数（字段全必填） */
export interface ObsRangeParams {
  start: number;
  end: number;
  gran: ObsGranularity;
}

/** 单指标时间序列桶，含展示用 label */
export interface TimeBucket {
  ts: number;
  label: string;
  cost: number;
  requests: number;
  tokens: number;
}

/** 多系列堆叠桶，含展示用 label */
export interface StackedBucket {
  ts: number;
  label: string;
  series: Record<string, number>;
}
