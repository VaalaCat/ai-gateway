"use client";

import { useId } from "react";
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
import type { ChannelOtherSettings } from "./types";

type ToolSchemaCompatibilityRule = NonNullable<ChannelOtherSettings["model_tool_schema_compatibility"]>[number];

interface ToolSchemaCompatibilityCardProps {
  rule: ToolSchemaCompatibilityRule;
  onChange: (next: ToolSchemaCompatibilityRule) => void;
  onDelete: () => void;
}

export function ToolSchemaCompatibilityCard({ rule, onChange, onDelete }: ToolSchemaCompatibilityCardProps) {
  const t = useTranslations("channels");
  const id = useId();

  return (
    <div className="rounded-md border p-3 space-y-3">
      <div className="flex items-start gap-2">
        <div className="flex-1 space-y-1">
          <Label htmlFor={`${id}-model`} className="text-xs text-muted-foreground">
            {t("toolSchemaCompatibilityModelLabel")}
          </Label>
          <Input
            id={`${id}-model`}
            value={rule.model_pattern}
            placeholder="^gemini-.*$"
            onChange={(event) => onChange({ ...rule, model_pattern: event.target.value })}
          />
        </div>
        <Button type="button" variant="ghost" size="icon" onClick={onDelete} aria-label={t("delete")}>
          <X className="size-4" />
        </Button>
      </div>
      <div className="space-y-1">
        <Label htmlFor={`${id}-profile`} className="text-xs text-muted-foreground">
          {t("toolSchemaCompatibilityProfileLabel")}
        </Label>
        <Select
          value={rule.profile}
          onValueChange={(profile: ToolSchemaCompatibilityRule["profile"]) => onChange({ ...rule, profile })}
        >
          <SelectTrigger id={`${id}-profile`} className="w-full">
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value="gemini_compat">{t("toolSchemaCompatibilityGemini")}</SelectItem>
            <SelectItem value="none">{t("toolSchemaCompatibilityNone")}</SelectItem>
          </SelectContent>
        </Select>
      </div>
    </div>
  );
}
