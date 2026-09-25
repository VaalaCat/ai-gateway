"use client";

import { useMemo } from "react";
import type { ColumnDef, VisibilityState } from "@tanstack/react-table";
import { useTranslations } from "next-intl";
import { Braces, ChevronRight } from "lucide-react";

import { DateCell } from "@/components/business/date-cell";
import { DurationCell } from "@/components/business/duration-cell";
import { CostCell } from "@/components/business/cost-cell";
import { EntityLabel } from "@/components/business/entity-label";
import { HTTPStatusBadge, ProtocolBadge } from "@/components/business/api-badges";
import { Button } from "@/components/ui/button";
import type { APIRequestLog } from "@/lib/api/api-logs";
import { formatFileSize } from "@/lib/utils/format";
import { APILogTokenIdentity } from "./token-identity";

export const defaultLogColumnVisibility: VisibilityState = {
  request_id: false,
  route: false,
  upstream: false,
  protocol: false,
  method: false,
  request_bytes: false,
  response_bytes: false,
};

function SnapshotName({ value }: { value: string | undefined }) {
  return <span className="block max-w-56 truncate" title={value}>{value || "-"}</span>;
}

export function useLogColumns(
  onViewRaw: (request: APIRequestLog) => void,
  { showInternal = true }: { showInternal?: boolean } = {},
) {
  const t = useTranslations("apiLogs");

  return useMemo<ColumnDef<APIRequestLog>[]>(() => {
    const columns: ColumnDef<APIRequestLog>[] = [
    {
      id: "expand",
      header: "",
      enableHiding: false,
      cell: ({ row }) => (
        <Button
          type="button"
          variant="ghost"
          size="icon-sm"
          aria-label={t("expandDetails")}
          aria-expanded={row.getIsExpanded()}
          onClick={() => row.toggleExpanded()}
        >
          <ChevronRight className={row.getIsExpanded() ? "rotate-90 transition-transform" : "transition-transform"} />
        </Button>
      ),
    },
    {
      id: "raw_json",
      header: t("rawJson"),
      enableHiding: false,
      cell: ({ row }) => (
        <Button
          type="button"
          variant="ghost"
          size="icon-sm"
          aria-label={t("viewRawJson")}
          onClick={() => onViewRaw(row.original)}
        >
          <Braces />
        </Button>
      ),
    },
    {
      accessorKey: "request_id",
      header: t("requestID"),
      enableHiding: false,
      cell: ({ row }) => (
        <span className="block max-w-64 truncate font-mono text-meta" title={row.original.request_id}>
          {row.original.request_id}
        </span>
      ),
    },
    {
      id: "service",
      header: t("service"),
      enableHiding: false,
      cell: ({ row }) => <SnapshotName value={row.original.api_service_name} />,
    },
    ...(showInternal ? [{
      accessorKey: "user_id",
      id: "user_id",
      header: t("userID"),
      cell: ({ row }) => <EntityLabel entity="user" id={row.original.user_id ?? 0} />,
    } satisfies ColumnDef<APIRequestLog>] : []),
    {
      id: "route",
      header: t("route"),
      cell: ({ row }) => <SnapshotName value={row.original.api_route_name} />,
    },
    ...(showInternal ? [{
      id: "upstream",
      header: t("upstream"),
      cell: ({ row }) => <SnapshotName value={row.original.api_upstream_name} />,
    } satisfies ColumnDef<APIRequestLog>] : []),
    {
      accessorKey: "token_name",
      id: "token_name",
      header: t("tokenName"),
      cell: ({ row }) => (
        <APILogTokenIdentity
          tokenID={row.original.token_id}
          tokenName={row.original.token_name}
          className="max-w-40 font-mono text-meta"
        />
      ),
    },
    {
      id: "protocol",
      header: t("protocol"),
      cell: ({ row }) => <ProtocolBadge protocol={row.original.protocol} />,
    },
    {
      accessorKey: "method",
      id: "method",
      header: t("method"),
      cell: ({ row }) => <span className="font-mono text-meta">{row.original.method || "-"}</span>,
    },
    {
      accessorKey: "status_code",
      header: t("status"),
      enableHiding: false,
      cell: ({ row }) => (
        <HTTPStatusBadge statusCode={row.original.status_code} />
      ),
    },
    {
      id: "duration",
      header: t("duration"),
      cell: ({ row }) => <DurationCell ms={row.original.duration_ms} />,
    },
    {
      accessorKey: "first_byte_ms",
      id: "first_byte_ms",
      header: t("firstByte"),
      cell: ({ row }) => <DurationCell ms={row.original.first_byte_ms} />,
    },
    {
      accessorKey: "request_bytes",
      id: "request_bytes",
      header: t("requestBytes"),
      cell: ({ row }) => <span className="tabular-nums">{formatFileSize(row.original.request_bytes)}</span>,
    },
    {
      accessorKey: "response_bytes",
      id: "response_bytes",
      header: t("responseBytes"),
      cell: ({ row }) => <span className="tabular-nums">{formatFileSize(row.original.response_bytes)}</span>,
    },
    {
      accessorKey: "total_cost",
      id: "total_cost",
      header: t("totalCost"),
      cell: ({ row }) => <CostCell amount={row.original.total_cost} />,
    },
    {
      id: "createdAt",
      header: t("createdAt"),
      cell: ({ row }) => <DateCell timestamp={row.original.created_at} />,
    },
    ];
    return columns;
  }, [onViewRaw, showInternal, t]);
}
