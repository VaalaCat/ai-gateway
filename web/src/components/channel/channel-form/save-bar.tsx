"use client";

import { useTranslations } from "next-intl";
import { Button } from "@/components/ui/button";
import { cn } from "@/lib/utils";

export interface SaveBarProps {
  isDirty: boolean;
  dirtyFieldCount: number;
  saving: boolean;
  onSave: () => void;
  onCancel: () => void;
  /** When set, save is blocked and this reason is shown (e.g. a missing required field). */
  blockReason?: string;
}

export function SaveBar({ isDirty, dirtyFieldCount, saving, onSave, onCancel, blockReason }: SaveBarProps) {
  const t = useTranslations("channels");
  const tc = useTranslations("common");

  return (
    <div className="sticky bottom-0 z-10 bg-background/95 backdrop-blur border-t px-4 py-3 pb-[max(env(safe-area-inset-bottom),0.75rem)] flex flex-col-reverse md:flex-row md:items-center md:justify-between gap-3 md:py-4">
      <div className={cn("text-sm", blockReason ? "text-destructive" : "text-muted-foreground")}>
        {blockReason
          ? blockReason
          : isDirty
            ? `${t("unsavedChanges")} · ${t("unsavedChangesCount", { count: dirtyFieldCount })}`
            : ""}
      </div>
      <div className="flex gap-2 md:justify-end">
        <Button type="button" variant="outline" onClick={onCancel} className="flex-1 md:flex-none">
          {tc("cancel")}
        </Button>
        <Button type="button" onClick={onSave} disabled={!isDirty || saving || !!blockReason} className="flex-1 md:flex-none">
          {tc("save")}
        </Button>
      </div>
    </div>
  );
}
