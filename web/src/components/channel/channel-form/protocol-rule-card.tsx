"use client";

import { useTranslations } from "next-intl";
import { X } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";

export type ProtocolInbound = "*" | "openai_chat" | "openai_responses" | "claude";
export type ProtocolOutbound = "auto" | "openai_chat" | "openai_responses" | "claude";

export interface ProtocolRule {
  model: string;
  overrides: Partial<Record<ProtocolInbound, ProtocolOutbound>>;
}

export interface ProtocolRuleCardProps {
  rule: ProtocolRule;
  enabledOutbounds: Array<{ value: Exclude<ProtocolOutbound, "auto">; label: string }>;
  onChange: (next: ProtocolRule) => void;
  onDelete: () => void;
}

export function ProtocolRuleCard({
  rule,
  enabledOutbounds,
  onChange,
  onDelete,
}: ProtocolRuleCardProps) {
  const t = useTranslations("channels");

  // Wire shape: rule.overrides has a single key (the inbound). Read it out for UI.
  const inboundKey = (Object.keys(rule.overrides)[0] ?? "*") as ProtocolInbound;
  const outboundValue = (rule.overrides[inboundKey] ?? "auto") as ProtocolOutbound;

  let regexInvalid = false;
  if (rule.model) {
    try {
      new RegExp("^" + rule.model + "$");
    } catch {
      regexInvalid = true;
    }
  }

  const handleInboundChange = (v: string) => {
    const next: ProtocolRule = {
      ...rule,
      overrides: { [v as ProtocolInbound]: outboundValue } as ProtocolRule["overrides"],
    };
    onChange(next);
  };

  const handleOutboundChange = (v: string) => {
    const next: ProtocolRule = {
      ...rule,
      overrides: { [inboundKey]: v as ProtocolOutbound } as ProtocolRule["overrides"],
    };
    onChange(next);
  };

  return (
    <div className="rounded-md border p-3 space-y-3">
      <div className="flex items-start gap-2">
        <div className="flex-1 space-y-1">
          <Label className="text-xs text-muted-foreground">
            {t("protocolOverridePerModelLabel")}
          </Label>
          <Input
            value={rule.model}
            placeholder="gpt-4o or deepseek-.*"
            className={regexInvalid ? "border-destructive" : undefined}
            onChange={(e) => onChange({ ...rule, model: e.target.value })}
          />
          {regexInvalid && (
            <p className="text-xs text-destructive">
              {t("protocolOverridePerModelInvalidRegex")}
            </p>
          )}
        </div>
        <Button
          type="button"
          variant="ghost"
          size="icon"
          onClick={onDelete}
          aria-label={t("delete")}
        >
          <X className="size-4" />
        </Button>
      </div>

      <div className="flex gap-2 items-end">
        <div className="flex-1 space-y-1">
          <Label className="text-xs text-muted-foreground">
            {t("protocolOverridePerModelInbound")}
          </Label>
          <Select value={inboundKey} onValueChange={handleInboundChange}>
            <SelectTrigger>
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value="*">
                {t("protocolOverridePerModelInboundWildcard")}
              </SelectItem>
              {enabledOutbounds.map((p) => (
                <SelectItem key={p.value} value={p.value}>
                  {p.label}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        </div>
        <div className="pb-2 text-muted-foreground">→</div>
        <div className="flex-1 space-y-1">
          <Label className="text-xs text-muted-foreground">
            {t("protocolOverridePerModelOutbound")}
          </Label>
          <Select value={outboundValue} onValueChange={handleOutboundChange}>
            <SelectTrigger>
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value="auto">{t("protocolOverrideAuto")}</SelectItem>
              {enabledOutbounds.map((p) => (
                <SelectItem key={p.value} value={p.value}>
                  {p.label}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        </div>
      </div>
    </div>
  );
}
