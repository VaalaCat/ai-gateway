/** 为监控请求合并查询取消信号与二十秒超时。 */
export function monitoringSignal(signal?: AbortSignal): AbortSignal {
  // 1. 保留调用方取消，并限制本次监控请求的等待时间。
  const timeout = AbortSignal.timeout(20_000);
  return signal ? AbortSignal.any([signal, timeout]) : timeout;
}
