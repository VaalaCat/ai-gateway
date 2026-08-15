"use client";

import { useState, type ReactNode } from "react";
import { useTranslations } from "next-intl";

import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@/components/ui/alert-dialog";
import { apiServiceErrorMessage } from "./api-service-error";

interface DeleteConfirmationDialogProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  subject: string;
  onConfirm: () => Promise<unknown | false>;
  description?: string;
  errorMessage?: (error: unknown) => string;
  title?: string;
  confirmLabel?: string;
  details?: ReactNode;
}

export function DeleteConfirmationDialog({ open, onOpenChange, subject, onConfirm, description, errorMessage, title, confirmLabel, details }: DeleteConfirmationDialogProps) {
  const t = useTranslations("apiServices");
  const [error, setError] = useState<string>();
  const [pending, setPending] = useState(false);

  const confirm = async () => {
    setPending(true);
    setError(undefined);
    try {
      const result = await onConfirm();
      if (result !== false) onOpenChange(false);
    } catch (reason) {
      setError(errorMessage?.(reason) ?? apiServiceErrorMessage(t, reason));
    } finally {
      setPending(false);
    }
  };

  return (
    <AlertDialog open={open} onOpenChange={(next) => { if (pending && !next) return; setError(undefined); onOpenChange(next); }}>
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>{title ?? t("confirmDeleteTitle")}</AlertDialogTitle>
          <AlertDialogDescription>{description ?? t("confirmDeleteDescription", { subject })}</AlertDialogDescription>
        </AlertDialogHeader>
        {details}
        {error ? <Alert variant="destructive"><AlertTitle>{t("mutationFailed")}</AlertTitle><AlertDescription>{error}</AlertDescription></Alert> : null}
        <AlertDialogFooter>
          <AlertDialogCancel disabled={pending}>{t("cancel")}</AlertDialogCancel>
          <AlertDialogAction
            variant="destructive"
            disabled={pending}
            onClick={(event) => { event.preventDefault(); void confirm(); }}
          >
            {confirmLabel ?? t("confirmDelete")}
          </AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  );
}
