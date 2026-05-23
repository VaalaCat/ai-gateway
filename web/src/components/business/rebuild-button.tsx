"use client";

import { useTranslations } from "next-intl";

import { Button } from "@/components/ui/button";
import { useRebuildBillingJobs } from "@/lib/api/billing";

interface RebuildButtonProps {
  onClick: () => void;
}

/**
 * RebuildButton 是 billing 页 "重建数据" 入口。
 *
 * 按钮文案保持不变；当有进行中的 rebuild job 时，右上角浮一个 pulse dot
 * 作为最小化提示。具体状态、进度、选择交给 dialog 处理。
 */
export function RebuildButton({ onClick }: RebuildButtonProps) {
  const t = useTranslations("billing");
  const jobs = useRebuildBillingJobs();
  const hasRunning =
    jobs.data?.jobs?.some((j) => j.status === "running") ?? false;

  return (
    <Button variant="outline" onClick={onClick} className="relative">
      {t("rebuild")}
      {hasRunning && (
        <span
          aria-hidden
          className="pointer-events-none absolute right-1.5 top-1.5 inline-flex size-1.5"
        >
          <span className="absolute inset-0 animate-ping rounded-full bg-primary opacity-70" />
          <span className="relative inline-flex size-1.5 rounded-full bg-primary" />
        </span>
      )}
    </Button>
  );
}
