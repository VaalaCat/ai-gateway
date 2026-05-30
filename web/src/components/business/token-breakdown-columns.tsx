"use client";

import type { ColumnDef } from "@tanstack/react-table";
import { useTranslations } from "next-intl";
import { DataTableColumnHeader } from "@/components/data-table/column-header";
import { TokensCell } from "@/components/business/tokens-cell";
import { totalTokens } from "@/lib/utils/format";

/**
 * 任何含 {prompt,completion,cache_read,cache_write}_tokens 的 row 类型都可用。
 * 与 `totalTokens` 共用的最小结构,见 lib/utils/format.ts。
 */
export interface TokenBreakdownRow {
  prompt_tokens: number;
  completion_tokens: number;
  cache_read_tokens: number;
  cache_write_tokens: number;
}

/**
 * 生成 5 列定义:prompt / completion / cache_read / cache_write / total。
 * 三处计费表(billing token / billing channel / tokens 页非管理员计费)共用,避免漂移。
 * - 4 个原始字段用 accessorKey + TokensCell(可排序);
 * - total 用 accessorFn 派生(无对应列,但可排序,与前 4 列行为一致)。
 *
 * @param t 必须是 `useTranslations("billing")` 返回的实例,以便 helper 内能解析
 *   `promptTokens` / `completionTokens` / `cacheReadTokens` / `cacheWriteTokens` / `totalTokens` 键。
 */
export function buildTokenBreakdownColumns<R extends TokenBreakdownRow>(
  t: ReturnType<typeof useTranslations<"billing">>,
): ColumnDef<R>[] {
  return [
    {
      accessorKey: "prompt_tokens",
      header: ({ column }) => (
        <DataTableColumnHeader column={column} title={t("promptTokens")} />
      ),
      cell: ({ row }) => <TokensCell tokens={row.original.prompt_tokens} />,
    },
    {
      accessorKey: "completion_tokens",
      header: ({ column }) => (
        <DataTableColumnHeader column={column} title={t("completionTokens")} />
      ),
      cell: ({ row }) => <TokensCell tokens={row.original.completion_tokens} />,
    },
    {
      accessorKey: "cache_read_tokens",
      header: ({ column }) => (
        <DataTableColumnHeader column={column} title={t("cacheReadTokens")} />
      ),
      cell: ({ row }) => <TokensCell tokens={row.original.cache_read_tokens} />,
    },
    {
      accessorKey: "cache_write_tokens",
      header: ({ column }) => (
        <DataTableColumnHeader column={column} title={t("cacheWriteTokens")} />
      ),
      cell: ({ row }) => <TokensCell tokens={row.original.cache_write_tokens} />,
    },
    {
      id: "total_tokens",
      accessorFn: (row) => totalTokens(row),
      header: ({ column }) => (
        <DataTableColumnHeader column={column} title={t("totalTokens")} />
      ),
      cell: ({ row }) => <TokensCell tokens={totalTokens(row.original)} />,
    },
  ];
}
