"use client";

import { useTranslations } from "next-intl";

import { Leaderboard } from "@/components/business/leaderboard";
import { formatMoneyCompact } from "@/lib/utils/format";
import type { LeaderRow } from "@/lib/api/dashboard";
import type { Breakdown } from "@/lib/api/insights";

import type { BreakdownAxis } from "../registry";

interface Props {
  axes: BreakdownAxis[];
  data: Breakdown;
}

function rowsForAxis(data: Breakdown, axis: BreakdownAxis): LeaderRow[] | undefined {
  switch (axis) {
    case "model":
      return data.by_model;
    case "channel":
      return data.by_channel;
    case "agent":
      return data.by_agent;
    case "user":
      return data.by_user;
    case "token":
      return data.by_token;
    default:
      return undefined;
  }
}

export function BreakdownSection({ axes, data }: Props) {
  const t = useTranslations("insights.breakdown");

  return (
    <div className="space-y-4">
      {axes.map((axis) => {
        const rows = rowsForAxis(data, axis);
        if (!rows) return null;
        return (
          <Leaderboard<LeaderRow>
            key={axis}
            title={t(axis)}
            rows={rows}
            columns={[
              { key: "name", label: "Name" },
              { key: "requests", label: "Reqs" },
              {
                key: "cost",
                label: "Cost",
                render: (r) => formatMoneyCompact(r.cost),
              },
            ]}
          />
        );
      })}
    </div>
  );
}
