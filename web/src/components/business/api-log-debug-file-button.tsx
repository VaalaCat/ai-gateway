"use client";

import { useState } from "react";
import { Download, LoaderCircle } from "lucide-react";
import { useTranslations } from "next-intl";
import { toast } from "sonner";

import { Button } from "@/components/ui/button";
import { downloadAPIRequestLogDebugFile } from "@/lib/api/api-log-debug-file";
import type { APIRequestLog, APIRequestLogScope } from "@/lib/api/api-logs";

export function APILogDebugFileButton({
  log,
  scope,
}: {
  log: APIRequestLog;
  scope: APIRequestLogScope;
}) {
  const t = useTranslations("apiLogs");
  const [downloading, setDownloading] = useState(false);

  const download = async () => {
    setDownloading(true);
    try {
      await downloadAPIRequestLogDebugFile(log, scope);
    } catch {
      toast.error(t("debugFileDownloadFailed"));
    } finally {
      setDownloading(false);
    }
  };

  return (
    <Button
      type="button"
      variant="outline"
      size="sm"
      disabled={downloading}
      onClick={() => void download()}
    >
      {downloading
        ? <LoaderCircle data-icon="inline-start" className="animate-spin" />
        : <Download data-icon="inline-start" />}
      {t("downloadDebugFile")}
    </Button>
  );
}
