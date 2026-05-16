"use client";

import { useEffect } from "react";
import { UseFormReturn } from "react-hook-form";
import { useTranslations } from "next-intl";
import { Button } from "@/components/ui/button";
import { RoutingFormValues } from "./types";

export interface SaveBarProps {
  form: UseFormReturn<RoutingFormValues>;
  onCancel?: () => void;
}

export function SaveBar({ form, onCancel }: SaveBarProps) {
  const t = useTranslations("modelRoutings");
  const tc = useTranslations("common");
  const isDirty = form.formState.isDirty;
  const isSubmitting = form.formState.isSubmitting;

  useEffect(() => {
    function handleKeyDown(e: KeyboardEvent) {
      if ((e.metaKey || e.ctrlKey) && e.key === "s") {
        e.preventDefault();
        if (!isSubmitting) {
          form.handleSubmit(() => {})();
        }
      }
    }
    document.addEventListener("keydown", handleKeyDown);
    return () => document.removeEventListener("keydown", handleKeyDown);
  }, [form, isSubmitting]);

  return (
    <div className="lg:col-span-2 sticky bottom-0 z-10 flex items-center justify-between gap-4 rounded-t-lg border-t bg-background/95 px-4 py-3 backdrop-blur supports-[backdrop-filter]:bg-background/60">
      <p className="text-sm text-muted-foreground">
        {isDirty ? t("saveBar.unsaved") : null}
      </p>
      <div className="flex gap-2">
        {onCancel && (
          <Button type="button" variant="outline" onClick={onCancel} disabled={isSubmitting}>
            {tc("cancel")}
          </Button>
        )}
        <Button type="submit" disabled={isSubmitting}>
          {tc("save")}
        </Button>
      </div>
    </div>
  );
}
