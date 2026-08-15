"use client";

import { useEffect, useRef, useState } from "react";

import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";

export interface FormErrorReport {
  message: string;
  occurrence: number;
}

export function useFormErrorReport() {
  const occurrence = useRef(0);
  const [error, setError] = useState<FormErrorReport>();
  const clearError = () => setError(undefined);
  const reportError = (message: string) => {
    occurrence.current += 1;
    setError({ message, occurrence: occurrence.current });
  };
  return { error, clearError, reportError };
}

export function FormErrorSummary({ error, title }: { error?: FormErrorReport; title: string }) {
  const summaryRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (!error) return;
    summaryRef.current?.focus();
    summaryRef.current?.scrollIntoView({ block: "nearest" });
  }, [error]);

  if (!error) return null;

  return (
    <Alert ref={summaryRef} variant="destructive" tabIndex={-1}>
      <AlertTitle>{title}</AlertTitle>
      <AlertDescription>{error.message}</AlertDescription>
    </Alert>
  );
}
