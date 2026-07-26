"use client";

import { useMemo } from "react";
import Link from "next/link";
import { useTranslations } from "next-intl";
import { ColumnDef } from "@tanstack/react-table";
import { ExternalLink } from "lucide-react";

import { DataTable } from "@/components/data-table/data-table";
import { DataTableColumnHeader } from "@/components/data-table/column-header";
import { FilterableToolbar } from "@/components/data-table/filterable-toolbar";
import { useFilterState } from "@/components/data-table/use-filter-state";
import { usePaginationState } from "@/components/data-table/use-pagination-state";
import type { FilterSpec } from "@/components/data-table/filter-spec";
import { Button } from "@/components/ui/button";
import { StatusBadge, RoleBadge } from "@/components/business/status-badge";
import { DateCell } from "@/components/business/date-cell";

import { useUsers } from "@/lib/api/users";
import { PAGE_SIZES } from "@/lib/constants";
import type { User } from "@/lib/types";

export function MembersTab({ groupId }: { groupId: number }) {
  const t = useTranslations("userGroups");
  const tu = useTranslations("users");
  const tc = useTranslations("common");

  const [page, pageSize, setPagination] = usePaginationState(PAGE_SIZES.DEFAULT);

  const filterSpec = useMemo(() => ({
    search: { kind: "text", placeholder: tc("search") },
  } satisfies FilterSpec), [tc]);

  const [filterValues, setFilterValues] = useFilterState(filterSpec);

  const { data, isLoading } = useUsers({
    page,
    page_size: pageSize,
    group_id: groupId,
    ...(filterValues.search ? { search: String(filterValues.search) } : {}),
  });

  const users = data?.data ?? [];
  const total = data?.total ?? 0;
  const pageCount = Math.ceil(total / pageSize) || 1;

  const handlePaginationChange = (newPage: number, newPageSize: number) => {
    setPagination(newPageSize === pageSize ? newPage : 1, newPageSize);
  };

  const columns: ColumnDef<User>[] = [
    {
      accessorKey: "username",
      header: ({ column }) => <DataTableColumnHeader column={column} title={tu("username")} />,
    },
    {
      accessorKey: "role",
      header: tu("role"),
      cell: ({ row }) => <span className="hidden md:inline-block"><RoleBadge role={row.original.role} /></span>,
    },
    {
      accessorKey: "status",
      header: tc("status"),
      cell: ({ row }) => <StatusBadge status={row.original.status} />,
    },
    {
      accessorKey: "created_at",
      header: tc("createdAt"),
      cell: ({ row }) => <span className="hidden md:inline-block"><DateCell timestamp={row.original.created_at} /></span>,
    },
    {
      id: "actions",
      header: tc("actions"),
      cell: ({ row }) => (
        <Button variant="ghost" size="xs" asChild>
          <Link href={`/users?id=${row.original.id}`}>
            <ExternalLink className="size-3.5 mr-1" />
            {t("openInUsers")}
          </Link>
        </Button>
      ),
    },
  ];

  return (
    <DataTable
      columns={columns}
      data={users}
      loading={isLoading}
      total={total}
      page={page}
      pageSize={pageSize}
      pageCount={pageCount}
      onPaginationChange={handlePaginationChange}
      toolbar={
        <FilterableToolbar
          spec={filterSpec}
          value={filterValues}
          onChange={setFilterValues}
        />
      }
    />
  );
}
