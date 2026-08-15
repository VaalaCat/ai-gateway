"use client";

import { useTranslations } from "next-intl";
import { NumberUnitInput } from "@/components/business/number-unit-input";
import { Label } from "@/components/ui/label";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import type { ChannelForm } from "../types";
import { humanizeNumberUnit } from "@/lib/utils/number-unit";
import { parseAffinity, stringifyAffinity, AffinityOverride } from "../utils";

type ParticipationValue = "inherit" | "enabled" | "disabled";

function participationValue(value: boolean | undefined): ParticipationValue {
  if (value === true) return "enabled";
  if (value === false) return "disabled";
  return "inherit";
}

export interface AffinitySectionProps {
  form: ChannelForm;
  setForm: (next: ChannelForm) => void;
}

export function AffinitySection({ form, setForm }: AffinitySectionProps) {
  const t = useTranslations("channels");
  const affinity = parseAffinity(form.affinity);
  const updateParticipation = (value: ParticipationValue) => {
    const next: AffinityOverride = { ...affinity };
    if (value === "inherit") delete next.enabled;
    else next.enabled = value === "enabled";
    setForm({ ...form, affinity: stringifyAffinity(next) });
  };
  const updateTTL = (raw: string) => {
    const next: AffinityOverride = { ...affinity };
    if (raw === "") delete next.ttl_sec;
    else {
      const n = Number(raw);
      if (!Number.isNaN(n)) next.ttl_sec = n;
    }
    setForm({ ...form, affinity: stringifyAffinity(next) });
  };
  return (
    <div className="space-y-4">
      <p className="text-xs text-muted-foreground">{t("affinitySectionHint")}</p>
      <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
        <div className="space-y-2">
          <Label>{t("affinityParticipation")}</Label>
          <Select
            value={participationValue(affinity.enabled)}
            onValueChange={(value) => updateParticipation(value as ParticipationValue)}
          >
            <SelectTrigger className="w-full">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value="inherit">{t("inheritGlobal")}</SelectItem>
              <SelectItem value="enabled">{t("enabled")}</SelectItem>
              <SelectItem value="disabled">{t("disabled")}</SelectItem>
            </SelectContent>
          </Select>
          <p className="text-xs text-muted-foreground">{t("affinityParticipationTip")}</p>
        </div>
        <div className="space-y-2">
          <Label>{t("affinityTTLOverride")}</Label>
          <NumberUnitInput
            type="number"
            min={0}
            max={86400}
            unit="s"
            value={affinity.ttl_sec !== undefined ? String(affinity.ttl_sec) : ""}
            humanReadable={humanizeNumberUnit(
              affinity.ttl_sec !== undefined ? String(affinity.ttl_sec) : "",
              "seconds",
            )}
            onChange={(e) => updateTTL(e.target.value)}
          />
          <p className="text-xs text-muted-foreground">{t("affinityTTLOverrideTip")}</p>
        </div>
      </div>
    </div>
  );
}
