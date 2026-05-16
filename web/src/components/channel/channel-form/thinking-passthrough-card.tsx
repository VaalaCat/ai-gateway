"use client";

import { useTranslations } from "next-intl";
import { X } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Switch } from "@/components/ui/switch";
import { FieldTip } from "@/components/business/field-tip";

export interface ThinkingPassthroughRule {
  model_pattern: string;
  send_back_thinking: boolean;
}

export interface ThinkingPassthroughCardProps {
  rule: ThinkingPassthroughRule;
  onChange: (next: ThinkingPassthroughRule) => void;
  onDelete: () => void;
}

export function ThinkingPassthroughCard({
  rule,
  onChange,
  onDelete,
}: ThinkingPassthroughCardProps) {
  const t = useTranslations("channels");

  // 后端 Go regexp.MatchString 是部分匹配语义；前端校验时不锚定。
  let regexInvalid = false;
  if (rule.model_pattern) {
    try {
      new RegExp(rule.model_pattern);
    } catch {
      regexInvalid = true;
    }
  }

  return (
    <div className="rounded-md border p-3 space-y-3">
      {/* 顶部：regex input + 删除按钮 */}
      <div className="flex items-start gap-2">
        <div className="flex-1 space-y-1">
          <Label className="text-xs text-muted-foreground">
            {t("thinkingPassthroughModelLabel")}
          </Label>
          <Input
            value={rule.model_pattern}
            placeholder=".*"
            className={regexInvalid ? "border-destructive" : undefined}
            onChange={(e) =>
              onChange({ ...rule, model_pattern: e.target.value })
            }
          />
          {regexInvalid && (
            <p className="text-xs text-destructive">
              {t("thinkingPassthroughInvalidRegex")}
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

      {/* 底部：开关 row（label + Switch） */}
      <div className="flex items-center justify-between">
        <Label className="text-sm">
          {t("thinkingPassthroughSendBack")}
          <FieldTip text={t("thinkingPassthroughSendBackTip")} />
        </Label>
        <Switch
          checked={rule.send_back_thinking}
          onCheckedChange={(v) =>
            onChange({ ...rule, send_back_thinking: v })
          }
        />
      </div>
    </div>
  );
}
