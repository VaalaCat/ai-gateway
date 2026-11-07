"use client";

import { useId } from "react";
import { useTranslations } from "next-intl";
import { X } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Field, FieldGroup, FieldLabel } from "@/components/ui/field";
import {
  Select,
  SelectContent,
  SelectGroup,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import type { ChannelOtherSettings } from "./types";

type ToolCompatibilityRule = NonNullable<ChannelOtherSettings["model_tool_schema_compatibility"]>[number];

interface ToolCompatibilityCardProps {
  rule: ToolCompatibilityRule;
  onChange: (next: ToolCompatibilityRule) => void;
  onDelete: () => void;
}

export function ToolCompatibilityCard({ rule, onChange, onDelete }: ToolCompatibilityCardProps) {
  const t = useTranslations("channels");
  const id = useId();

  return (
    <FieldGroup className="rounded-md border p-3 gap-3">
      <div className="flex items-start gap-2">
        <Field className="flex-1 gap-1">
          <FieldLabel htmlFor={`${id}-model`} className="text-xs text-muted-foreground">
            {t("toolCompatibilityModelLabel")}
          </FieldLabel>
          <Input
            id={`${id}-model`}
            value={rule.model_pattern}
            placeholder="^gemini-.*$"
            onChange={(event) => onChange({ ...rule, model_pattern: event.target.value })}
          />
        </Field>
        <Button type="button" variant="ghost" size="icon" onClick={onDelete} aria-label={t("delete")}>
          <X />
        </Button>
      </div>
      <Field className="gap-1">
        <FieldLabel htmlFor={`${id}-profile`} className="text-xs text-muted-foreground">
          {t("toolCompatibilityProfileLabel")}
        </FieldLabel>
        <Select
          value={rule.profile}
          onValueChange={(profile: ToolCompatibilityRule["profile"]) => onChange({ ...rule, profile })}
        >
          <SelectTrigger id={`${id}-profile`} className="w-full">
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            <SelectGroup>
              <SelectItem value="none">{t("toolCompatibilityNone")}</SelectItem>
              <SelectItem value="gemini_compat">{t("toolCompatibilityGemini")}</SelectItem>
            </SelectGroup>
          </SelectContent>
        </Select>
      </Field>
      <Field className="gap-1">
        <FieldLabel htmlFor={`${id}-signature`} className="text-xs text-muted-foreground">
          {t("toolCompatibilitySignatureLabel")}
        </FieldLabel>
        <Select
          value={rule.signature_fallback ?? "none"}
          onValueChange={(signatureFallback: NonNullable<ToolCompatibilityRule["signature_fallback"]>) => onChange({ ...rule, signature_fallback: signatureFallback })}
        >
          <SelectTrigger id={`${id}-signature`} className="w-full">
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            <SelectGroup>
              <SelectItem value="none">{t("toolCompatibilitySignaturePreserve")}</SelectItem>
              <SelectItem value="gemini">{t("toolCompatibilitySignatureFallback")}</SelectItem>
            </SelectGroup>
          </SelectContent>
        </Select>
      </Field>
    </FieldGroup>
  );
}
