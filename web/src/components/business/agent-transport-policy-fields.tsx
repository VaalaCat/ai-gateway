"use client";

import { useTranslations } from "next-intl";

import {
  Field,
  FieldLabel,
  FieldLegend,
  FieldSet,
} from "@/components/ui/field";
import { Switch } from "@/components/ui/switch";
import type { AgentTransportPolicy } from "@/lib/types";

type PolicyKey = keyof AgentTransportPolicy;

const policyRows = [
  {
    path: "direct",
    inbound: "direct_inbound_enabled",
    outbound: "direct_outbound_enabled",
  },
  {
    path: "relay",
    inbound: "relay_inbound_enabled",
    outbound: "relay_outbound_enabled",
  },
] as const satisfies ReadonlyArray<{
  path: "direct" | "relay";
  inbound: PolicyKey;
  outbound: PolicyKey;
}>;

interface AgentTransportPolicyFieldsProps {
  value: AgentTransportPolicy;
  disabled?: boolean;
  onChange: (next: AgentTransportPolicy) => void;
}

export function AgentTransportPolicyFields({
  value,
  disabled = false,
  onChange,
}: AgentTransportPolicyFieldsProps) {
  const t = useTranslations("agents.transportPolicy");
  const updateDirection = (key: PolicyKey, checked: boolean) => {
    onChange({ ...value, [key]: checked });
  };

  return (
    <FieldSet className="min-w-0 gap-3">
      <FieldLegend variant="label">{t("title")}</FieldLegend>
      <div
        data-testid="agent-transport-policy-grid"
        className="grid min-w-0 grid-cols-[minmax(0,1fr)_minmax(4rem,auto)_minmax(4rem,auto)] items-center gap-x-3 gap-y-2"
      >
        <span aria-hidden="true" />
        <span className="min-w-0 break-words text-xs font-medium text-muted-foreground">
          {t("inbound")}
        </span>
        <span className="min-w-0 break-words text-xs font-medium text-muted-foreground">
          {t("outbound")}
        </span>
        {policyRows.map((row) => (
          <TransportPolicyRow
            key={row.path}
            path={t(row.path)}
            inboundID={`agent-transport-policy-${row.inbound}`}
            outboundID={`agent-transport-policy-${row.outbound}`}
            inboundLabel={t(row.inbound)}
            outboundLabel={t(row.outbound)}
            inboundChecked={value[row.inbound]}
            outboundChecked={value[row.outbound]}
            disabled={disabled}
            onInboundChange={(checked) => updateDirection(row.inbound, checked)}
            onOutboundChange={(checked) => updateDirection(row.outbound, checked)}
          />
        ))}
      </div>
    </FieldSet>
  );
}

function TransportPolicyRow({
  path,
  inboundID,
  outboundID,
  inboundLabel,
  outboundLabel,
  inboundChecked,
  outboundChecked,
  disabled,
  onInboundChange,
  onOutboundChange,
}: {
  path: string;
  inboundID: string;
  outboundID: string;
  inboundLabel: string;
  outboundLabel: string;
  inboundChecked: boolean;
  outboundChecked: boolean;
  disabled: boolean;
  onInboundChange: (checked: boolean) => void;
  onOutboundChange: (checked: boolean) => void;
}) {
  return (
    <>
      <span className="min-w-0 break-words text-sm">{path}</span>
      <Field orientation="horizontal" className="min-w-0 justify-start" data-disabled={disabled || undefined}>
        <FieldLabel htmlFor={inboundID} className="sr-only">{inboundLabel}</FieldLabel>
        <Switch
          id={inboundID}
          checked={inboundChecked}
          disabled={disabled}
          aria-label={inboundLabel}
          onCheckedChange={onInboundChange}
        />
      </Field>
      <Field orientation="horizontal" className="min-w-0 justify-start" data-disabled={disabled || undefined}>
        <FieldLabel htmlFor={outboundID} className="sr-only">{outboundLabel}</FieldLabel>
        <Switch
          id={outboundID}
          checked={outboundChecked}
          disabled={disabled}
          aria-label={outboundLabel}
          onCheckedChange={onOutboundChange}
        />
      </Field>
    </>
  );
}
