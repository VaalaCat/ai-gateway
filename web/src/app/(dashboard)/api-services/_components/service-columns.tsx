"use client";

import Link from "next/link";
import type { ColumnDef } from "@tanstack/react-table";
import { useTranslations } from "next-intl";
import { MoreHorizontal } from "lucide-react";
import { toast } from "sonner";

import { StatusBadge } from "@/components/business/status-badge";
import { Button } from "@/components/ui/button";
import { DropdownMenu, DropdownMenuContent, DropdownMenuGroup, DropdownMenuItem, DropdownMenuTrigger } from "@/components/ui/dropdown-menu";
import type { APIService } from "@/lib/api/api-services";
import { formatMoneyCompact } from "@/lib/utils/format";

interface ServiceColumnOptions {
  includeActions: boolean;
  canManage: (service: APIService) => boolean;
  onToggleStatus: (service: APIService) => Promise<unknown>;
  onDelete: (service: APIService) => void;
}

function formatTimestamp(value: number | undefined) {
  if (!value) return "—";
  return new Intl.DateTimeFormat(undefined, { dateStyle: "medium", timeStyle: "short" }).format(new Date(value * 1000));
}

export function useServiceColumns(options: ServiceColumnOptions): ColumnDef<APIService>[] {
  const t = useTranslations("apiServices");
  const columns: ColumnDef<APIService>[] = [
    {
      accessorKey: "name",
      header: t("name"),
      cell: ({ row }) => <Link className="font-medium hover:underline" href={`/api-services/detail?id=${row.original.id}`}>{row.original.name}</Link>,
    },
    { accessorKey: "slug", header: t("slug"), cell: ({ row }) => <code className="text-sm text-muted-foreground">{row.original.slug}</code> },
    { accessorKey: "price_per_call", header: t("pricePerCall"), cell: ({ row }) => <span className="tabular-nums">{formatMoneyCompact(row.original.price_per_call)}</span> },
    { accessorKey: "status", header: t("status"), cell: ({ row }) => <StatusBadge status={row.original.status} /> },
    { accessorKey: "updated_at", header: t("updatedAt"), cell: ({ row }) => <span className="tabular-nums text-muted-foreground">{formatTimestamp(row.original.updated_at)}</span> },
  ];

  return [...columns, {
    id: "actions",
    header: t("actions"),
    cell: ({ row }) => {
      const service = row.original;
      const canManage = options.includeActions && options.canManage(service);
      const toggle = async () => {
        try { await options.onToggleStatus(service); }
        catch (error) { toast.error(error instanceof Error ? error.message : t("mutationFailed")); }
      };
      return (
        <DropdownMenu>
          <DropdownMenuTrigger asChild><Button variant="ghost" size="icon-sm" aria-label={t("actions")}><MoreHorizontal /></Button></DropdownMenuTrigger>
          <DropdownMenuContent align="end"><DropdownMenuGroup>
            <DropdownMenuItem asChild><Link href={`/api-services/detail?id=${service.id}`}>{t("viewServiceDetails")}</Link></DropdownMenuItem>
            {canManage ? <><DropdownMenuItem asChild><Link href={`/api-services/edit?id=${service.id}`}>{t("editService")}</Link></DropdownMenuItem><DropdownMenuItem onSelect={() => { void toggle(); }}>{service.status === 1 ? t("disableService") : t("enableService")}</DropdownMenuItem><DropdownMenuItem variant="destructive" onSelect={() => options.onDelete(service)}>{t("deleteService")}</DropdownMenuItem></> : null}
          </DropdownMenuGroup></DropdownMenuContent>
        </DropdownMenu>
      );
    },
  }];
}
