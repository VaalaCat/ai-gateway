"use client";

import { useState } from "react";
import { useTranslations } from "next-intl";

import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { AlertDialog, AlertDialogAction, AlertDialogCancel, AlertDialogContent, AlertDialogDescription, AlertDialogFooter, AlertDialogHeader, AlertDialogTitle } from "@/components/ui/alert-dialog";

interface DeleteConfirmationDialogProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  subject: string;
  onConfirm: () => Promise<unknown>;
}

export function DeleteConfirmationDialog({ open, onOpenChange, subject, onConfirm }: DeleteConfirmationDialogProps) {
  const t = useTranslations("apiAccess");
  const [pending, setPending] = useState(false);
  const [error, setError] = useState<string>();
  const confirm = async () => {
    setPending(true);
    setError(undefined);
    try { await onConfirm(); onOpenChange(false); }
    catch (reason) { setError(reason instanceof Error ? reason.message : t("mutationFailed")); }
    finally { setPending(false); }
  };
  return <AlertDialog open={open} onOpenChange={(next) => { setError(undefined); onOpenChange(next); }}>
    <AlertDialogContent>
      <AlertDialogHeader><AlertDialogTitle>{t("confirmDeleteTitle")}</AlertDialogTitle><AlertDialogDescription>{t("confirmDeleteDescription", { subject })}</AlertDialogDescription></AlertDialogHeader>
      {error ? <Alert variant="destructive"><AlertTitle>{t("mutationFailed")}</AlertTitle><AlertDescription>{error}</AlertDescription></Alert> : null}
      <AlertDialogFooter><AlertDialogCancel disabled={pending}>{t("cancel")}</AlertDialogCancel><AlertDialogAction variant="destructive" disabled={pending} onClick={(event) => { event.preventDefault(); void confirm(); }}>{t("confirmDelete")}</AlertDialogAction></AlertDialogFooter>
    </AlertDialogContent>
  </AlertDialog>;
}
