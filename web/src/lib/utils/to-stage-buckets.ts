import { RELAY_STAGE_ORDER } from "@/lib/constants/relay/stages";
import type { ErrBucket } from "@/lib/api/logs-insights";
import type { StageBucket } from "@/components/business/stage-distribution-bar";

/**
 * 把后端 errors.by_stage(ORDER BY count DESC)翻译成
 * StageDistributionBar 需要的 StageBucket[],含 i18n label.
 *
 * @param errors  后端聚合结果数组
 * @param tStage  next-intl `useTranslations("common.relayStage")` 返回的函数
 */
export function toStageBuckets(
  errors: ErrBucket[] | undefined,
  tStage: (key: string) => string,
): StageBucket[] {
  return (errors ?? []).map((e) => {
    const stage = e.stage ?? "?";
    return {
      stage,
      count: e.count,
      label: RELAY_STAGE_ORDER.includes(stage) ? tStage(stage) : stage,
    };
  });
}
