"use client";
import { useState, useEffect } from "react";
import { toast } from "sonner";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Switch } from "@/components/ui/switch";
import { useUpdateUserGroup } from "@/lib/api/user-groups";
import type { UserGroup } from "@/lib/types";
import { ApiError } from "@/lib/api/client";
import { useTranslations } from "next-intl";

// nullable-override 双件 pattern：
//   - override switch off  → 持久化 `null`（继承全局 setting）
//   - override switch on   → 持久化 value（明确为本组写入 true/false/数字）
// 拆分成 (value, override) 双 state，避免在 Select 三选项里塞 enum，
// boolean 字段语义 Switch 比 Select 更直接。
function splitTri(stored: boolean | null | undefined): { override: boolean; value: boolean } {
  if (stored == null) return { override: false, value: true };
  return { override: true, value: stored };
}

function splitMax(stored: number | null | undefined, fallback: number): {
  override: boolean;
  value: number;
} {
  if (stored == null) return { override: false, value: fallback };
  return { override: true, value: stored };
}

const MAX_VALUE_FALLBACK = 20;

export function BYOKCard({ group }: { group: UserGroup }) {
  const t = useTranslations("byok.groupCard");
  const updateMutation = useUpdateUserGroup();

  const initialEnabled = splitTri(group.byok_enabled);
  const initialMax = splitMax(group.byok_max_channels, MAX_VALUE_FALLBACK);

  const [enabledOverride, setEnabledOverride] = useState<boolean>(initialEnabled.override);
  const [enabledValue, setEnabledValue] = useState<boolean>(initialEnabled.value);
  const [maxOverride, setMaxOverride] = useState<boolean>(initialMax.override);
  const [maxValue, setMaxValue] = useState<number>(initialMax.value);

  useEffect(() => {
    const e = splitTri(group.byok_enabled);
    const m = splitMax(group.byok_max_channels, MAX_VALUE_FALLBACK);
    setEnabledOverride(e.override);
    setEnabledValue(e.value);
    setMaxOverride(m.override);
    setMaxValue(m.value);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [group.id]);

  const dirty =
    enabledOverride !== initialEnabled.override ||
    (enabledOverride && enabledValue !== initialEnabled.value) ||
    maxOverride !== initialMax.override ||
    (maxOverride && maxValue !== initialMax.value);

  const handleSave = async () => {
    try {
      await updateMutation.mutateAsync({
        id: group.id,
        byok_enabled: enabledOverride ? enabledValue : null,
        byok_max_channels: maxOverride ? maxValue : null,
      });
      toast.success(t("savedToast"));
    } catch (e) {
      const msg =
        e instanceof ApiError
          ? `${e.status}: ${e.message}`
          : e instanceof Error
            ? e.message
            : t("saveFailedToast");
      toast.error(msg);
    }
  };

  return (
    <Card>
      <CardHeader>
        <CardTitle className="text-base">{t("title")}</CardTitle>
      </CardHeader>
      <CardContent className="space-y-6">
        <section className="space-y-3">
          <div className="flex items-center justify-between gap-4">
            <Label htmlFor="byok-enabled" className="text-sm">
              {t("enabledLabel")}
            </Label>
            <Switch
              id="byok-enabled"
              checked={enabledOverride && enabledValue}
              onCheckedChange={setEnabledValue}
              disabled={!enabledOverride}
            />
          </div>
          <div className="flex items-center justify-between gap-4">
            <Label
              htmlFor="byok-enabled-override"
              className="text-sm text-muted-foreground"
            >
              {t("useOverride")}
            </Label>
            <Switch
              id="byok-enabled-override"
              checked={enabledOverride}
              onCheckedChange={setEnabledOverride}
            />
          </div>
          <p className="text-xs text-muted-foreground">{t("enabledDesc")}</p>
        </section>

        <section className="space-y-3">
          <div className="flex items-center justify-between gap-4">
            <Label htmlFor="byok-max" className="text-sm">
              {t("maxLabel")}
            </Label>
            <Input
              id="byok-max"
              type="number"
              min={1}
              value={maxValue}
              onChange={(e) => setMaxValue(Number(e.target.value))}
              disabled={!maxOverride}
              className="w-32"
            />
          </div>
          <div className="flex items-center justify-between gap-4">
            <Label
              htmlFor="byok-max-override"
              className="text-sm text-muted-foreground"
            >
              {t("useOverride")}
            </Label>
            <Switch
              id="byok-max-override"
              checked={maxOverride}
              onCheckedChange={setMaxOverride}
            />
          </div>
          <p className="text-xs text-muted-foreground">{t("maxDesc")}</p>
        </section>

        <div className="flex justify-end">
          <Button
            onClick={handleSave}
            disabled={!dirty || updateMutation.isPending}
          >
            {t("saveButton")}
          </Button>
        </div>
      </CardContent>
    </Card>
  );
}
