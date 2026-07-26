"use client";

import type { ReactNode } from "react";

import { Label } from "@/components/ui/label";
import { cn } from "@/lib/utils";

/** 列表筛选栏统一容器:无边框、可换行、底对齐 */
export function FilterBar({ children, className }: { children: ReactNode; className?: string }) {
  return <div className={cn("flex flex-wrap items-end gap-3", className)}>{children}</div>;
}

/** 单个筛选项:小灰 label + 控件 */
export function FilterField({
  label,
  children,
  className,
}: {
  label: string;
  children: ReactNode;
  className?: string;
}) {
  return (
    <div className={cn("space-y-1", className)}>
      <Label className="text-xs font-normal text-muted-foreground">{label}</Label>
      {children}
    </div>
  );
}
