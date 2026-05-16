"use client";

import { useState } from "react";
import { useTranslations } from "next-intl";
import { toast } from "sonner";
import { Loader2 } from "lucide-react";

import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { DateRangeInputs, isDateRangeValid } from "@/components/business/date-range-inputs";
import { useRebuildBilling } from "@/lib/api/billing";

interface RebuildDialogProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
}

export function RebuildDialog({ open, onOpenChange }: RebuildDialogProps) {
  const t = useTranslations("billing");
  const tc = useTranslations("common");

  const [startDate, setStartDate] = useState("");
  const [endDate, setEndDate] = useState("");
  const rebuild = useRebuildBilling();

  const hasDate = !!(startDate || endDate);
  const validRange = isDateRangeValid(startDate, endDate);
  const canSubmit = hasDate && validRange && !rebuild.isPending;

  const handleClose = (nextOpen: boolean) => {
    if (!nextOpen) {
      setStartDate("");
      setEndDate("");
    }
    onOpenChange(nextOpen);
  };

  const handleSubmit = async () => {
    try {
      const result = await rebuild.mutateAsync({
        ...(startDate ? { start_date: startDate } : {}),
        ...(endDate ? { end_date: endDate } : {}),
      });
      toast.success(t("rebuildSuccess", { count: result.replayed_logs }));
      handleClose(false);
    } catch {
      toast.error(t("rebuildFailed"));
    }
  };

  return (
    <Dialog open={open} onOpenChange={handleClose}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{t("rebuild")}</DialogTitle>
        </DialogHeader>
        <p className="text-sm text-muted-foreground">{t("rebuildHint")}</p>
        <DateRangeInputs
          startDate={startDate}
          endDate={endDate}
          onStartDateChange={setStartDate}
          onEndDateChange={setEndDate}
        />
        {!hasDate && (
          <p className="text-sm text-muted-foreground">{t("rebuildDateRequired")}</p>
        )}
        <DialogFooter>
          <Button variant="outline" onClick={() => handleClose(false)}>
            {tc("cancel")}
          </Button>
          <Button onClick={handleSubmit} disabled={!canSubmit}>
            {rebuild.isPending && <Loader2 className="mr-2 size-4 animate-spin" />}
            {t("rebuildConfirm")}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
