"use client";

import { useCallback, useEffect, useRef } from "react";
import { ChevronLeft, ChevronRight } from "lucide-react";
import { useTranslations } from "next-intl";

import { Button } from "@/components/ui/button";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";

interface DataTablePaginationProps {
  page: number;
  pageSize: number;
  pageCount: number;
  disabled?: boolean;
  showPageSize?: boolean;
  onPrefetchPage?: (page: number, pageSize: number) => void;
  onPaginationChange: (page: number, pageSize: number) => void;
}

export function DataTablePagination({
  page,
  pageSize,
  pageCount,
  disabled = false,
  showPageSize = true,
  onPrefetchPage,
  onPaginationChange,
}: DataTablePaginationProps) {
  const t = useTranslations("common");
  const prefetchTimer = useRef<number | undefined>(undefined);
  const cancelPrefetch = useCallback(() => {
    window.clearTimeout(prefetchTimer.current);
    prefetchTimer.current = undefined;
  }, []);
  const schedulePrefetch = () => {
    cancelPrefetch();
    if (!onPrefetchPage || disabled || page >= pageCount) return;
    prefetchTimer.current = window.setTimeout(() => {
      prefetchTimer.current = undefined;
      onPrefetchPage(page + 1, pageSize);
    }, 150);
  };
  useEffect(() => cancelPrefetch, [cancelPrefetch, disabled, onPrefetchPage, page, pageCount, pageSize]);

  return (
    <div className="flex items-center gap-4 text-body">
      {showPageSize && <div className="flex items-center gap-2">
        <Select
          value={String(pageSize)}
          disabled={disabled}
          onValueChange={(value) => {
            onPaginationChange(1, Number(value));
          }}
        >
          <SelectTrigger size="sm" className="w-auto">
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            {[10, 20, 50].map((size) => (
              <SelectItem key={size} value={String(size)}>
                {t("perPage", { size })}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
      </div>}
      <div className="min-w-16 whitespace-nowrap text-center tabular-nums text-muted-foreground">
        {page} / {pageCount || 1}
      </div>
      <div className="flex items-center gap-1">
        <Button
          variant="outline"
          size="icon-sm"
          aria-label={t("previousPage")}
          onClick={() => { cancelPrefetch(); onPaginationChange(page - 1, pageSize); }}
          disabled={disabled || page <= 1}
        >
          <ChevronLeft className="size-4" />
        </Button>
        <Button
          variant="outline"
          size="icon-sm"
          aria-label={t("nextPage")}
          onClick={() => { cancelPrefetch(); onPaginationChange(page + 1, pageSize); }}
          onMouseEnter={schedulePrefetch}
          onMouseLeave={cancelPrefetch}
          onFocus={schedulePrefetch}
          onBlur={cancelPrefetch}
          disabled={disabled || page >= pageCount}
        >
          <ChevronRight className="size-4" />
        </Button>
      </div>
    </div>
  );
}
