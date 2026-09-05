"use client";

import { useState } from "react";
import { Download, LoaderCircle } from "lucide-react";
import { useTranslations } from "next-intl";
import { toast } from "sonner";

import { Button } from "@/components/ui/button";
import { downloadLogDebugFile } from "@/lib/api/log-debug-file";
import type { UsageLog } from "@/lib/types";

export function LogDebugFileButton({ log }: { log: UsageLog }) {
  const t = useTranslations("logs");
  const [downloading, setDownloading] = useState(false);

  const download = async () => {
    setDownloading(true);
    try {
      await downloadLogDebugFile(log);
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
