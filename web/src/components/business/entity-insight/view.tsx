"use client";

import { useTranslations } from "next-intl";

import { ObservabilityHeader } from "@/components/business/observability-header";
import { useInsight, type EntityType } from "@/lib/api/insights";
import { useObsRange } from "@/lib/hooks/use-obs-range";

import type { EntityInsightConfig } from "./registry";
import { BreakdownSection } from "./sections/breakdown";
import { ErrorsSection } from "./sections/errors";
import { StageSection } from "./sections/stage";
import { SummarySection } from "./sections/summary";
import { TrendSection } from "./sections/trend";

interface Props {
  type: EntityType;
  id: string;
  cfg: EntityInsightConfig;
}

export function EntityInsightView({ type, id, cfg }: Props) {
  const t = useTranslations("insights");
  const { range, setRange, refresh, refreshKey } = useObsRange();
  const { data, isFetching, refetch } = useInsight(
    { type, id, ...range },
    { refetchKey: refreshKey },
  );

  const Header = cfg.renderHeader;

  return (
    <div className="space-y-6">
      <ObservabilityHeader
        title={t(`title.${type}`)}
        subtitle={id}
        range={range}
        onRangeChange={setRange}
        onRefresh={() => {
          refresh();
          refetch();
        }}
        refreshing={isFetching}
        showGranularity
      />
      {data ? (
        <>
          <Header meta={data.meta} />
          <SummarySection data={data.summary} />
          <div className="grid grid-cols-1 gap-4 lg:grid-cols-2">
            <TrendSection buckets={data.trend.buckets} />
            <BreakdownSection axes={cfg.breakdownAxes} data={data.breakdown} />
          </div>
          {cfg.showStageLatency &&
            data.stage_latency &&
            data.stage_latency.stages.length > 0 && (
              <StageSection data={data.stage_latency} />
            )}
          <ErrorsSection rows={data.errors} />
        </>
      ) : (
        <div className="py-12 text-center text-muted-foreground">
          {t("loading")}
        </div>
      )}
    </div>
  );
}
