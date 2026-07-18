"use client";

import { useTranslations } from "next-intl";

import { Badge } from "@/components/ui/badge";
import type {
  ControlSnapshot,
  DirectSummary,
  RelaySnapshot,
  RelaySummary,
} from "@/lib/types";

type Props =
  | { kind: "admin"; status: number }
  | { kind: "control"; value: ControlSnapshot }
  | { kind: "relay"; value: RelaySnapshot | RelaySummary }
  | { kind: "direct"; value: DirectSummary };

function labelKey(value: string) {
  return value.replace(/_([a-z])/g, (_, letter: string) => letter.toUpperCase());
}

export function AgentConnectionStatus(props: Props) {
  const t = useTranslations("agents.connection");

  if (props.kind === "admin") {
    const enabled = props.status === 1;
    return <Badge variant={enabled ? "default" : "secondary"}>{t(enabled ? "enabled" : "disabled")}</Badge>;
  }

  if (props.kind === "control") {
    const { state, health, reason_codes: reasons } = props.value;
    return (
      <div className="flex min-w-0 flex-wrap items-center gap-1.5">
        <Badge variant={state === "connected" ? "default" : "destructive"}>{t(state)}</Badge>
        {state === "connected" && health !== "unknown" ? (
          <Badge variant={health === "degraded" ? "destructive" : "secondary"}>{t(health)}</Badge>
        ) : null}
        {reasons.length > 0 ? <span className="truncate text-xs text-muted-foreground">{reasons.join(", ")}</span> : null}
      </div>
    );
  }

  if (props.kind === "relay") {
    const relay = props.value;
    if (relay.support === "unsupported") {
      return <Badge variant="secondary">{t("unsupported")}</Badge>;
    }
    if (relay.config === "not_configured" || relay.config === "disabled") {
      return <Badge variant="secondary">{t(labelKey(relay.config))}</Badge>;
    }
    return (
      <div className="flex min-w-0 flex-wrap items-center gap-1.5">
        <Badge variant={relay.availability === "ready" ? "default" : relay.availability === "draining" ? "outline" : "destructive"}>
          {t(relay.availability)}
        </Badge>
        {relay.convergence !== "converged" ? (
          <Badge variant={relay.convergence === "degraded" ? "destructive" : "outline"}>{t(relay.convergence)}</Badge>
        ) : null}
        {"streams" in relay ? (
          <span className="text-xs tabular-nums text-muted-foreground">{t("streamCount", { count: relay.streams })}</span>
        ) : null}
      </div>
    );
  }

  const direct = props.value;
  return (
    <div className="flex min-w-0 flex-wrap items-center gap-1.5">
      <Badge variant={direct.state === "reachable" ? "default" : direct.state === "unknown" || direct.state === "checking" || direct.state === "stale" ? "secondary" : "destructive"}>
        {t(direct.state)}
      </Badge>
      <span className="text-xs tabular-nums text-muted-foreground">
        {t("directCounts", { reachable: direct.reachable, total: direct.total, degraded: direct.degraded, unreachable: direct.unreachable })}
      </span>
    </div>
  );
}
