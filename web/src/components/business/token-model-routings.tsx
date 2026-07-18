"use client";

import Link from "next/link";
import { useMemo, useState } from "react";
import { useTranslations } from "next-intl";
import {
  CircleAlert,
  MoreHorizontal,
  Network,
  Pencil,
  Plus,
  Trash2,
} from "lucide-react";
import { toast } from "sonner";

import { DeleteConfirm } from "@/components/business/delete-confirm";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuGroup,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import {
  Empty,
  EmptyDescription,
  EmptyHeader,
  EmptyMedia,
  EmptyTitle,
} from "@/components/ui/empty";
import { Skeleton } from "@/components/ui/skeleton";
import {
  type ModelRoutingApiMode,
  type ModelRoutingOwner,
  useDeleteModelRouting,
  useModelRoutings,
} from "@/lib/api/model-routings";
import { useAuth } from "@/lib/auth";
import type { ModelRouting, RoutingMember, Token } from "@/lib/types";

interface TokenModelRoutingsProps {
  token: Token;
}

function routingMembers(value: ModelRouting["members"]): RoutingMember[] {
  if (Array.isArray(value)) return value;
  if (typeof value !== "string") return [];
  try {
    const parsed: unknown = JSON.parse(value);
    return Array.isArray(parsed) ? parsed as RoutingMember[] : [];
  } catch {
    return [];
  }
}

export function TokenModelRoutings({ token }: TokenModelRoutingsProps) {
  const t = useTranslations("tokenDetail");
  const tc = useTranslations("common");
  const { isAdmin } = useAuth();
  const apiMode: ModelRoutingApiMode = isAdmin ? "admin" : "user";
  const owner = useMemo<ModelRoutingOwner>(
    () => ({ kind: "token", tokenId: token.id }),
    [token.id],
  );
  const query = useModelRoutings({ page: 1, page_size: 100 }, apiMode, owner);
  const deleteMutation = useDeleteModelRouting(apiMode, owner);
  const [deleteItem, setDeleteItem] = useState<ModelRouting | null>(null);

  const routings = query.data?.data ?? [];
  const total = query.data?.total ?? routings.length;
  const newHref = `/model-routings/new?token_id=${token.id}`;

  async function handleDelete() {
    if (!deleteItem) return;
    try {
      await deleteMutation.mutateAsync(deleteItem.id);
      toast.success(tc("success"));
      setDeleteItem(null);
    } catch {
      toast.error(tc("error"));
    }
  }

  return (
    <section className="flex min-w-0 flex-col gap-3">
      <div className="flex flex-col gap-2 sm:flex-row sm:items-center sm:justify-between">
        <div className="flex min-w-0 items-center gap-2">
          <Network className="size-4 shrink-0 text-muted-foreground" aria-hidden="true" />
          <h3 className="truncate text-sm font-medium">{t("modelRoutings")}</h3>
          {!query.isLoading && <Badge variant="secondary">{total}</Badge>}
        </div>
        <Button size="xs" variant="outline" asChild>
          <Link href={newHref}>
            <Plus data-icon="inline-start" />
            {t("newRouting")}
          </Link>
        </Button>
      </div>

      {query.isLoading ? (
        <div className="flex flex-col gap-2" aria-label={t("routingLoading")}>
          <Skeleton className="h-10 w-full" />
          <Skeleton className="h-10 w-full" />
        </div>
      ) : query.isError ? (
        <Alert variant="destructive">
          <CircleAlert aria-hidden="true" />
          <AlertTitle>{t("routingLoadFailed")}</AlertTitle>
          <AlertDescription>
            <Button size="xs" variant="outline" onClick={() => void query.refetch()}>
              {t("retry")}
            </Button>
          </AlertDescription>
        </Alert>
      ) : routings.length === 0 ? (
        <Empty className="gap-2 border p-4 md:p-4">
          <EmptyHeader>
            <EmptyMedia>
              <Network className="size-4" aria-hidden="true" />
            </EmptyMedia>
            <EmptyTitle className="text-sm">{t("routingEmpty")}</EmptyTitle>
            <EmptyDescription>{t("routingEmptyDescription")}</EmptyDescription>
          </EmptyHeader>
        </Empty>
      ) : (
        <div className="divide-y rounded-md border">
          {routings.map((routing) => (
            <div
              key={routing.id}
              className="flex min-w-0 flex-col gap-2 px-3 py-2.5 sm:flex-row sm:items-center sm:justify-between"
            >
              <div className="flex min-w-0 flex-col gap-1 sm:flex-row sm:items-center sm:gap-3">
                <span className="truncate text-sm font-medium">{routing.name}</span>
                <div className="flex flex-wrap items-center gap-2">
                  <Badge variant={routing.enabled ? "secondary" : "outline"}>
                    {routing.enabled ? tc("enabled") : tc("disabled")}
                  </Badge>
                  <span className="text-xs text-muted-foreground">
                    {t("routingMembers", { count: routingMembers(routing.members).length })}
                  </span>
                </div>
              </div>
              <DropdownMenu>
                <DropdownMenuTrigger asChild>
                  <Button
                    variant="ghost"
                    size="icon-sm"
                    className="self-end sm:self-auto"
                    aria-label={t("routingActions", { name: routing.name })}
                  >
                    <MoreHorizontal data-icon="icon" />
                  </Button>
                </DropdownMenuTrigger>
                <DropdownMenuContent align="end">
                  <DropdownMenuGroup>
                    <DropdownMenuItem asChild>
                      <Link href={`/model-routings/edit?id=${routing.id}&token_id=${token.id}`}>
                        <Pencil />
                        {tc("edit")}
                      </Link>
                    </DropdownMenuItem>
                    <DropdownMenuItem
                      variant="destructive"
                      onClick={() => setDeleteItem(routing)}
                    >
                      <Trash2 />
                      {tc("delete")}
                    </DropdownMenuItem>
                  </DropdownMenuGroup>
                </DropdownMenuContent>
              </DropdownMenu>
            </div>
          ))}
        </div>
      )}

      <DeleteConfirm
        open={deleteItem !== null}
        onOpenChange={(open) => {
          if (!open) setDeleteItem(null);
        }}
        onConfirm={handleDelete}
        title={t("routingDeleteTitle")}
        description={t("routingDeleteDescription", { name: deleteItem?.name ?? "" })}
      />
    </section>
  );
}
