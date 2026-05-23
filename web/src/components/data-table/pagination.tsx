"use client";

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
  onPaginationChange: (page: number, pageSize: number) => void;
}

export function DataTablePagination({
  page,
  pageSize,
  pageCount,
  onPaginationChange,
}: DataTablePaginationProps) {
  const t = useTranslations("common");

  return (
    <div className="flex items-center gap-4 text-body">
      <div className="flex items-center gap-2">
        <Select
          value={String(pageSize)}
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
      </div>
      <div className="text-muted-foreground">
        {page} / {pageCount || 1}
      </div>
      <div className="flex items-center gap-1">
        <Button
          variant="outline"
          size="icon-sm"
          onClick={() => onPaginationChange(page - 1, pageSize)}
          disabled={page <= 1}
        >
          <ChevronLeft className="size-4" />
        </Button>
        <Button
          variant="outline"
          size="icon-sm"
          onClick={() => onPaginationChange(page + 1, pageSize)}
          disabled={page >= pageCount}
        >
          <ChevronRight className="size-4" />
        </Button>
      </div>
    </div>
  );
}
