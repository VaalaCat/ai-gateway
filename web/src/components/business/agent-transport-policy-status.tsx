"use client";

import { useTranslations } from "next-intl";

import { Badge } from "@/components/ui/badge";
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip";
import type {
  AgentTransportPolicySnapshot,
  TransportDirectionSnapshot,
} from "@/lib/types";
import { agentTransportPolicyReasonLabelKey } from "@/lib/agent-transport-policy";
import { cn } from "@/lib/utils";

const directions = [
  { key: "direct_inbound", path: "direct", direction: "inbound" },
  { key: "direct_outbound", path: "direct", direction: "outbound" },
  { key: "relay_inbound", path: "relay", direction: "inbound" },
  { key: "relay_outbound", path: "relay", direction: "outbound" },
] as const;

export function AgentTransportPolicyStatus({
  value,
  compact = false,
}: {
  value: AgentTransportPolicySnapshot;
  compact?: boolean;
}) {
  const t = useTranslations("agents.transportPolicy");
  const tc = useTranslations("agents.connection");

  return (
    <section
      data-testid="agent-transport-policy-status"
      data-compact={compact}
      className={cn("flex min-w-0 flex-col", compact ? "gap-2" : "gap-3")}
    >
      <h3 className="text-xs font-medium text-muted-foreground">{t("title")}</h3>
      <div className="grid min-w-0 grid-cols-2 gap-2">
        {directions.map(({ key, path, direction }) => {
          const snapshot = value[key];
          const reasonKey = agentTransportPolicyReasonLabelKey(snapshot.reason_code);
          const reasonLabel = reasonKey ? tc(reasonKey) : snapshot.reason_code;
          return (
            <DirectionStatus
              key={key}
              testID={`transport-policy-${path}-${direction}`}
              path={t(path)}
              direction={t(direction)}
              snapshot={snapshot}
              effectiveLabel={t("effective")}
              configuredLabel={t("configured")}
              onLabel={t("on")}
              offLabel={t("off")}
              reasonLabel={reasonLabel}
            />
          );
        })}
      </div>
    </section>
  );
}

function DirectionStatus({
  testID,
  path,
  direction,
  snapshot,
  effectiveLabel,
  configuredLabel,
  onLabel,
  offLabel,
  reasonLabel,
}: {
  testID: string;
  path: string;
  direction: string;
  snapshot: TransportDirectionSnapshot;
  effectiveLabel: string;
  configuredLabel: string;
  onLabel: string;
  offLabel: string;
  reasonLabel?: string;
}) {
  const stateLabel = (enabled: boolean) => enabled ? onLabel : offLabel;
  const differs = snapshot.configured !== snapshot.effective;
  const configuredBadge = (
    <Badge
      variant="outline"
      {...(reasonLabel && snapshot.reason_code ? {
        tabIndex: 0,
        "aria-label": `${configuredLabel}: ${stateLabel(snapshot.configured)}. ${reasonLabel} (${snapshot.reason_code})`,
      } : {})}
    >
      {configuredLabel} {stateLabel(snapshot.configured)}
    </Badge>
  );

  return (
    <div data-testid={testID} className="flex min-w-0 flex-col gap-1.5">
      <div className="flex min-w-0 flex-wrap gap-1 text-xs">
        <span className="font-medium">{path}</span>
        <span className="text-muted-foreground">{direction}</span>
      </div>
      <div className="flex min-w-0 flex-wrap items-center gap-1">
        <Badge variant={snapshot.effective ? "default" : "secondary"}>
          {effectiveLabel} {stateLabel(snapshot.effective)}
        </Badge>
        {differs ? (
          reasonLabel ? (
            <Tooltip>
              <TooltipTrigger asChild>{configuredBadge}</TooltipTrigger>
              <TooltipContent>
                <span>{reasonLabel}</span>
                {snapshot.reason_code ? <code className="ml-1 font-mono">{snapshot.reason_code}</code> : null}
              </TooltipContent>
            </Tooltip>
          ) : configuredBadge
        ) : null}
      </div>
    </div>
  );
}
