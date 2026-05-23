"use client";

import Link from "next/link";
import { useRouter, useSearchParams } from "next/navigation";
import { Suspense, useEffect, useMemo, useState } from "react";
import { useTranslations } from "next-intl";
import { ColumnDef } from "@tanstack/react-table";
import type { ExpandedState } from "@tanstack/react-table";
import { toast } from "sonner";
import { MoreHorizontal, Plus } from "lucide-react";

import { DataTable } from "@/components/data-table/data-table";
import { DataTableColumnHeader } from "@/components/data-table/column-header";
import { FilterableToolbar } from "@/components/data-table/filterable-toolbar";
import { useFilterState } from "@/components/data-table/use-filter-state";
import type { FilterSpec } from "@/components/data-table/filter-spec";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import {
  Dialog,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { TagInput } from "@/components/ui/tag-input";
import { Switch } from "@/components/ui/switch";
import { Badge } from "@/components/ui/badge";
import { AgentRouteEditor } from "@/components/agent-route-editor";

import { StatusBadge } from "@/components/business/status-badge";
import { StatusSelect } from "@/components/business/status-select";
import { DeleteConfirm } from "@/components/business/delete-confirm";
import { CopyableText } from "@/components/business/copyable-text";
import { DateCell } from "@/components/business/date-cell";
import { ChannelMultiSelect } from "@/components/business/channel-multi-select";
import { UsernameCell } from "@/components/business/username-cell";
import { TokenDetailPanel } from "@/components/business/token-detail-panel";
import {
  DateRangeInputs,
  isDateRangeValid,
} from "@/components/business/date-range-inputs";

import { useBillingOverview, useTokenBilling } from "@/lib/api/billing";
import { formatErrorToast } from "@/lib/api/error-toast";
import { buildQuery } from "@/lib/api/client";
import { useTokens, useCreateToken, useUpdateToken, useDeleteToken } from "@/lib/api/tokens";
import { useEnabledTokenTemplates } from "@/lib/api/token-templates";
import { useAuth } from "@/lib/auth";
import { PAGE_SIZES } from "@/lib/constants";
import { parseModels, serializeModels } from "@/lib/parse-models";
import { formatSuccessRate, formatMoneyCompact } from "@/lib/utils/format";
import { MoneyCell } from "@/components/business/money-cell";
import type { BillingTokenRow, Token } from "@/lib/types";

function isWeakToken(token: string): boolean {
  const value = token.trim();
  if (!value) return false;
  if (value.length < 16) return true;

  const hasLower = /[a-z]/.test(value);
  const hasUpper = /[A-Z]/.test(value);
  const hasDigit = /\d/.test(value);
  const hasSymbol = /[^A-Za-z0-9]/.test(value);
  const categories = [hasLower, hasUpper, hasDigit, hasSymbol].filter(Boolean).length;
  if (categories < 2) return true;

  if (/^(.)\1+$/.test(value)) return true;
  if (/([A-Za-z0-9])\1{5,}/.test(value)) return true;

  return false;
}

function logHref(tokenId: number): string {
  return `/logs${buildQuery({ token_id: tokenId })}`;
}

export default function TokensPage() {
  return (
    <Suspense fallback={<div className="flex items-center justify-center py-12 text-muted-foreground">Loading...</div>}>
      <TokensPageContent />
    </Suspense>
  );
}

function TokensPageContent() {
  const t = useTranslations("tokens");
  const tb = useTranslations("billing");
  const tc = useTranslations("common");
  const tTpl = useTranslations("tokenTemplates");

  const { user, isAdmin, loading } = useAuth();

  const searchParams = useSearchParams();

  const [page, setPage] = useState(() => Number(searchParams.get("page")) || 1);
  const [pageSize, setPageSize] = useState<number>(
    () => Number(searchParams.get("page_size")) || PAGE_SIZES.DEFAULT,
  );

  const filterSpec = useMemo(() => ({
    search: { kind: "text", placeholder: tc("search") },
    user_id: { kind: "picker", entity: "user", visible: (ctx: { isAdmin: boolean }) => ctx.isAdmin },
    status: {
      kind: "enum",
      options: [
        { value: "1", label: t("statusEnabled") },
        { value: "0", label: t("statusDisabled") },
      ],
      placeholder: t("filterByStatus"),
    },
  } satisfies FilterSpec), [t, tc]);

  const [filterValues, setFilterValues] = useFilterState(filterSpec);

  const router = useRouter();
  const [expandedState, setExpandedState] = useState<ExpandedState>(() => {
    const sel = searchParams.get("selected");
    return sel ? { [sel]: true } : {};
  });

  useEffect(() => {
    const openId = Object.entries(expandedState).find(([, v]) => v)?.[0];
    const params = new URLSearchParams(searchParams.toString());
    if (openId) params.set("selected", openId);
    else params.delete("selected");
    const queryString = params.toString();
    router.replace(queryString ? `?${queryString}` : "", { scroll: false });
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [expandedState]);

  const [billingPage, setBillingPage] = useState(1);
  const [billingPageSize, setBillingPageSize] = useState<number>(PAGE_SIZES.DEFAULT);
  const [billingStartDate, setBillingStartDate] = useState("");
  const [billingEndDate, setBillingEndDate] = useState("");
  const billingDateValid = isDateRangeValid(billingStartDate, billingEndDate);
  const showBillingView = !loading && !isAdmin;

  const { data, isLoading } = useTokens({
    page,
    page_size: pageSize,
    ...(filterValues.search ? { search: String(filterValues.search) } : {}),
    ...(filterValues.user_id ? { user_id: Number(filterValues.user_id) } : {}),
    ...(filterValues.status ? { status: Number(filterValues.status) } : {}),
  });
  const billingOverview = useBillingOverview(
    {
      ...(billingStartDate ? { start_date: billingStartDate } : {}),
      ...(billingEndDate ? { end_date: billingEndDate } : {}),
    },
    { enabled: showBillingView && billingDateValid }
  );
  const billingRows = useTokenBilling(
    {
      page: billingPage,
      page_size: billingPageSize,
      ...(billingStartDate ? { start_date: billingStartDate } : {}),
      ...(billingEndDate ? { end_date: billingEndDate } : {}),
    },
    { enabled: showBillingView && billingDateValid }
  );

  useEffect(() => {
    if (billingOverview.isError) toast.error(tc("error"));
  }, [billingOverview.isError, tc]);
  useEffect(() => {
    if (billingRows.isError) toast.error(tc("error"));
  }, [billingRows.isError, tc]);

  const templates = useEnabledTokenTemplates();

  const tokens = data?.data ?? [];
  const total = data?.total ?? 0;
  const pageCount = Math.ceil(total / pageSize) || 1;
  const billingTotal = billingRows.data?.total ?? 0;
  const billingPageCount = Math.ceil(billingTotal / billingPageSize) || 1;

  const handlePaginationChange = (newPage: number, newPageSize: number) => {
    if (newPageSize !== pageSize) {
      setPage(1);
      setPageSize(newPageSize);
    } else {
      setPage(newPage);
    }
  };

  const createMutation = useCreateToken();
  const updateMutation = useUpdateToken();
  const deleteMutation = useDeleteToken();

  const [createOpen, setCreateOpen] = useState(false);
  const [editItem, setEditItem] = useState<Token | null>(null);
  const [deleteItem, setDeleteItem] = useState<Token | null>(null);

  const [createForm, setCreateForm] = useState({ user_id: String(user?.user_id ?? ""), name: "", key: "", expired_at: "", models: "", template_id: 0, trace_enabled: false, allowed_channel_ids: [] as number[] });
  const [editForm, setEditForm] = useState({ user_id: "", name: "", status: "1", expired_at: "", models: "", trace_enabled: false, allowed_channel_ids: [] as number[] });
  const [weakKeyConfirmOpen, setWeakKeyConfirmOpen] = useState(false);
  const [pendingWeakKeyCreate, setPendingWeakKeyCreate] = useState<null | typeof createForm>(null);

  const submitCreate = async (form: typeof createForm) => {
    if (isAdmin) {
      await createMutation.mutateAsync({
        user_id: Number(form.user_id),
        name: form.name,
        ...(form.key.trim() ? { key: form.key.trim() } : {}),
        ...(form.expired_at ? { expired_at: Number(form.expired_at) } : {}),
        ...(form.models ? { models: form.models } : {}),
        ...(form.template_id ? { template_id: form.template_id } : {}),
        trace_enabled: form.trace_enabled,
        ...(form.allowed_channel_ids.length > 0 ? { allowed_channel_ids: form.allowed_channel_ids } : {}),
      });
    } else {
      await createMutation.mutateAsync({
        name: form.name,
        template_id: form.template_id,
        trace_enabled: form.trace_enabled,
      });
    }
    toast.success(tc("success"));
    setCreateOpen(false);
    setCreateForm({ user_id: String(user?.user_id ?? ""), name: "", key: "", expired_at: "", models: "", template_id: 0, trace_enabled: false, allowed_channel_ids: [] });
    setPendingWeakKeyCreate(null);
  };

  const handleCreate = async () => {
    try {
      if (!isAdmin && !createForm.template_id) {
        toast.error(t("templateRequired"));
        return;
      }

      if (isAdmin && createForm.key.trim() && isWeakToken(createForm.key)) {
        setPendingWeakKeyCreate(createForm);
        setWeakKeyConfirmOpen(true);
        return;
      }

      await submitCreate(createForm);
    } catch (e) {
      toast.error(formatErrorToast(e, tc("error")));
    }
  };

  const handleCreateWithWeakKey = async () => {
    if (!pendingWeakKeyCreate) return;
    try {
      await submitCreate(pendingWeakKeyCreate);
      setWeakKeyConfirmOpen(false);
    } catch (e) {
      toast.error(formatErrorToast(e, tc("error")));
    }
  };

  const handleEdit = async () => {
    if (!editItem) return;
    try {
      if (isAdmin) {
        await updateMutation.mutateAsync({
          id: editItem.id,
          ...(editForm.user_id ? { user_id: Number(editForm.user_id) } : {}),
          name: editForm.name,
          status: Number(editForm.status),
          ...(editForm.expired_at ? { expired_at: Number(editForm.expired_at) } : {}),
          models: editForm.models,
          trace_enabled: editForm.trace_enabled,
          allowed_channel_ids: editForm.allowed_channel_ids,
        });
      } else {
        await updateMutation.mutateAsync({
          id: editItem.id,
          name: editForm.name,
          trace_enabled: editForm.trace_enabled,
        });
      }
      toast.success(tc("success"));
      setEditItem(null);
    } catch (e) {
      toast.error(formatErrorToast(e, tc("error")));
    }
  };

  const handleDelete = async () => {
    if (!deleteItem) return;
    try {
      await deleteMutation.mutateAsync(deleteItem.id);
      toast.success(tc("success"));
      setDeleteItem(null);
    } catch (e) {
      toast.error(formatErrorToast(e, tc("error")));
    }
  };

  const openEdit = (token: Token) => {
    setEditForm({
      user_id: String(token.user_id ?? ""),
      name: token.name,
      status: String(token.status),
      expired_at: token.expired_at ? String(token.expired_at) : "",
      models: token.models ?? "",
      trace_enabled: token.trace_enabled,
      allowed_channel_ids: token.allowed_channel_ids ?? [],
    });
    setEditItem(token);
  };

  const columns = useMemo(() => {
    const cols: ColumnDef<Token>[] = [
      {
        accessorKey: "id",
        header: ({ column }) => <DataTableColumnHeader column={column} title={tc("id")} />,
      },
      {
        accessorKey: "name",
        header: ({ column }) => <DataTableColumnHeader column={column} title={tc("name")} />,
      },
      {
        accessorKey: "key",
        header: t("key"),
        cell: ({ row }) => (
          <div onClick={(e) => e.stopPropagation()}>
            <CopyableText text={row.original.key} display={row.original.key.slice(0, 8) + "..."} />
          </div>
        ),
      },
    ];

    if (isAdmin) {
      cols.push({
        accessorKey: "user_id",
        header: ({ column }) => <DataTableColumnHeader column={column} title={t("user")} />,
        cell: ({ row }) => <UsernameCell userId={row.original.user_id} />,
      });
    }

    cols.push(
      {
        accessorKey: "status",
        header: ({ column }) => <DataTableColumnHeader column={column} title={tc("status")} />,
        cell: ({ row }) => <StatusBadge status={row.original.status} />,
      },
      {
        accessorKey: "expired_at",
        header: ({ column }) => <DataTableColumnHeader column={column} title={t("expiredAt")} />,
        cell: ({ row }) =>
          row.original.expired_at
            ? <DateCell timestamp={row.original.expired_at} />
            : t("neverExpire"),
      },
      {
        accessorKey: "models",
        header: t("models"),
        cell: ({ row }) => {
          const models = parseModels(row.original.models);
          return (
            <span className="max-w-[200px] truncate block">
              {models.length > 0 ? models.join(", ") : t("allModels")}
            </span>
          );
        },
      },
      {
        id: "trace_enabled",
        header: t("traceEnabled"),
        cell: ({ row }) => (
          row.original.trace_enabled ? (
            <Badge variant="secondary">{tc("enabled")}</Badge>
          ) : null
        ),
      },
      {
        accessorKey: "template_id",
        header: t("template"),
        cell: ({ row }) => row.original.template_id || "-",
        enableHiding: true,
      },
      {
        accessorKey: "created_at",
        header: ({ column }) => <DataTableColumnHeader column={column} title={tc("createdAt")} />,
        cell: ({ row }) => <DateCell timestamp={row.original.created_at} />,
      },
      {
        id: "actions",
        header: tc("actions"),
        cell: ({ row }) => (
          <div onClick={(e) => e.stopPropagation()}>
            <DropdownMenu>
              <DropdownMenuTrigger asChild>
                <Button variant="ghost" size="icon" className="size-8">
                  <MoreHorizontal className="size-4" />
                </Button>
              </DropdownMenuTrigger>
              <DropdownMenuContent align="end">
                <DropdownMenuItem onClick={() => openEdit(row.original)}>
                  {tc("edit")}
                </DropdownMenuItem>
                <DropdownMenuItem
                  className="text-destructive"
                  onClick={() => setDeleteItem(row.original)}
                >
                  {tc("delete")}
                </DropdownMenuItem>
              </DropdownMenuContent>
            </DropdownMenu>
          </div>
        ),
      },
    );

    return cols;
  }, [isAdmin, t, tc]);

  const billingColumns = useMemo<ColumnDef<BillingTokenRow>[]>(() => [
    {
      accessorKey: "token_name",
      header: ({ column }) => <DataTableColumnHeader column={column} title={tb("token")} />,
    },
    {
      accessorKey: "total_cost",
      header: ({ column }) => <DataTableColumnHeader column={column} title={tb("totalCost")} />,
      cell: ({ row }) => <MoneyCell quota={row.original.total_cost} />,
    },
    {
      accessorKey: "request_count",
      header: ({ column }) => <DataTableColumnHeader column={column} title={tb("requestCount")} />,
    },
    {
      id: "success_rate",
      header: tb("successRate"),
      cell: ({ row }) => formatSuccessRate(row.original.success_count, row.original.request_count),
    },
    {
      accessorKey: "prompt_tokens",
      header: ({ column }) => <DataTableColumnHeader column={column} title={tb("promptTokens")} />,
    },
    {
      accessorKey: "completion_tokens",
      header: ({ column }) => <DataTableColumnHeader column={column} title={tb("completionTokens")} />,
    },
    {
      accessorKey: "last_used_at",
      header: ({ column }) => <DataTableColumnHeader column={column} title={tb("lastUsedAt")} />,
      cell: ({ row }) => <DateCell timestamp={row.original.last_used_at} />,
    },
    {
      id: "logs",
      header: tb("viewLogs"),
      cell: ({ row }) => (
        <Button variant="outline" size="sm" asChild>
          <Link href={logHref(row.original.token_id)}>{tb("viewLogs")}</Link>
        </Button>
      ),
      enableHiding: false,
    },
  ], [tb]);

  return (
    <div className="space-y-4">
      <div>
        <h1 className="text-2xl font-bold">{t("title")}</h1>
        <p className="text-muted-foreground mt-1">{t("description")}</p>
      </div>

      <DataTable
        columns={columns}
        data={tokens}
        loading={isLoading}
        total={total}
        page={page}
        pageSize={pageSize}
        pageCount={pageCount}
        onPaginationChange={handlePaginationChange}
        defaultColumnVisibility={{ template_id: false }}
        toolbar={
          <FilterableToolbar
            spec={filterSpec}
            value={filterValues}
            onChange={setFilterValues}
            primaryAction={
              <Button size="sm" onClick={() => { setCreateForm({ user_id: String(user?.user_id ?? ""), name: "", key: "", expired_at: "", models: "", template_id: 0, trace_enabled: false, allowed_channel_ids: [] }); setCreateOpen(true); }}>
                <Plus className="mr-2 size-4" />
                {t("createToken")}
              </Button>
            }
          />
        }
        expandedState={expandedState}
        onExpandedStateChange={setExpandedState}
        getRowId={(row) => String(row.id)}
        renderExpandedRow={(row) => <TokenDetailPanel token={row.original} />}
      />

      {showBillingView && (
        <div className="space-y-4 rounded-xl border p-4">
          <div className="space-y-1">
            <h2 className="text-xl font-semibold">{t("billingTitle")}</h2>
            <p className="text-body text-muted-foreground">{t("billingDescription")}</p>
          </div>

          <DateRangeInputs
            startDate={billingStartDate}
            endDate={billingEndDate}
            onStartDateChange={(d) => {
              setBillingStartDate(d);
              setBillingPage(1);
            }}
            onEndDateChange={(d) => {
              setBillingEndDate(d);
              setBillingPage(1);
            }}
          />

          <div className="grid gap-3 md:grid-cols-4">
            <Card>
              <CardHeader className="pb-2">
                <CardTitle className="text-label text-muted-foreground">
                  {tb("totalCost")}
                </CardTitle>
              </CardHeader>
              <CardContent>
                <div className="text-display">
                  {formatMoneyCompact(billingOverview.data?.total_cost ?? 0)}
                </div>
              </CardContent>
            </Card>
            <Card>
              <CardHeader className="pb-2">
                <CardTitle className="text-label text-muted-foreground">
                  {tb("requestCount")}
                </CardTitle>
              </CardHeader>
              <CardContent>
                <div className="text-display">
                  {billingOverview.data?.request_count ?? 0}
                </div>
              </CardContent>
            </Card>
            <Card>
              <CardHeader className="pb-2">
                <CardTitle className="text-label text-muted-foreground">
                  {tb("successRate")}
                </CardTitle>
              </CardHeader>
              <CardContent>
                <div className="text-display">
                  {`${((billingOverview.data?.success_rate ?? 0) * 100).toFixed(1)}%`}
                </div>
              </CardContent>
            </Card>
            <Card>
              <CardHeader className="pb-2">
                <CardTitle className="text-label text-muted-foreground">
                  {tb("activeTokens")}
                </CardTitle>
              </CardHeader>
              <CardContent>
                <div className="text-display">
                  {billingOverview.data?.active_tokens ?? 0}
                </div>
              </CardContent>
            </Card>
          </div>

          <DataTable
            columns={billingColumns}
            data={billingRows.data?.data ?? []}
            loading={billingRows.isLoading}
            total={billingTotal}
            page={billingPage}
            pageSize={billingPageSize}
            pageCount={billingPageCount}
            onPaginationChange={(nextPage, nextPageSize) => {
              if (nextPageSize !== billingPageSize) {
                setBillingPage(1);
                setBillingPageSize(nextPageSize);
                return;
              }
              setBillingPage(nextPage);
            }}
          />
        </div>
      )}

      {/* Create Dialog */}
      <Dialog open={createOpen} onOpenChange={setCreateOpen}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>{t("createToken")}</DialogTitle>
          </DialogHeader>
          <div className="space-y-4 py-4">
            {isAdmin ? (
              <>
                <div className="space-y-2">
                  <Label>{t("user")} ID</Label>
                  <Input
                    type="number"
                    value={createForm.user_id}
                    onChange={(e) => setCreateForm({ ...createForm, user_id: e.target.value })}
                  />
                </div>
                <div className="space-y-2">
                  <Label>{tc("name")}</Label>
                  <Input
                    value={createForm.name}
                    onChange={(e) => setCreateForm({ ...createForm, name: e.target.value })}
                  />
                </div>
                <div className="space-y-2">
                  <Label>{t("customKeyOptional")}</Label>
                  <Input
                    value={createForm.key}
                    onChange={(e) => setCreateForm({ ...createForm, key: e.target.value })}
                    placeholder={t("customKeyOptionalPlaceholder")}
                  />
                </div>
                <div className="space-y-2">
                  <Label>{t("expiredAt")}</Label>
                  <Input
                    type="number"
                    placeholder="Unix timestamp"
                    value={createForm.expired_at}
                    onChange={(e) => setCreateForm({ ...createForm, expired_at: e.target.value })}
                  />
                </div>
                <div className="space-y-2">
                  <Label>{t("models")}</Label>
                  <TagInput
                    value={parseModels(createForm.models)}
                    onChange={(tags) => setCreateForm({ ...createForm, models: serializeModels(tags) })}
                    placeholder={t("allModels")}
                  />
                </div>
                {isAdmin && (
                  <div className="space-y-2">
                    <Label>{tTpl("allowedChannels")}</Label>
                    <ChannelMultiSelect
                      value={createForm.allowed_channel_ids}
                      onChange={(ids) => setCreateForm({ ...createForm, allowed_channel_ids: ids })}
                      placeholder={tTpl("allowedChannelsPlaceholder")}
                    />
                    <p className="text-meta text-muted-foreground">{tTpl("allowedChannelsEmptyHint")}</p>
                  </div>
                )}
                <div className="space-y-2">
                  <Label>{t("template")}</Label>
                  <Select value={String(createForm.template_id || "")} onValueChange={(v) => setCreateForm({ ...createForm, template_id: Number(v) })}>
                    <SelectTrigger><SelectValue placeholder={t("selectTemplate")} /></SelectTrigger>
                    <SelectContent>
                      {templates.data?.data?.map((tpl) => (
                        <SelectItem key={tpl.id} value={String(tpl.id)}>{tpl.name}</SelectItem>
                      ))}
                    </SelectContent>
                  </Select>
                </div>
                <div className="flex items-center justify-between">
                  <Label>{t("traceEnabled")}</Label>
                  <Switch
                    checked={createForm.trace_enabled}
                    onCheckedChange={(checked) =>
                      setCreateForm({ ...createForm, trace_enabled: checked })
                    }
                  />
                </div>
              </>
            ) : (
              <>
                <div className="space-y-2">
                  <Label>{tc("name")}</Label>
                  <Input
                    value={createForm.name}
                    onChange={(e) => setCreateForm({ ...createForm, name: e.target.value })}
                  />
                </div>
                <div className="space-y-2">
                  <Label>{t("template")}</Label>
                  <Select value={String(createForm.template_id || "")} onValueChange={(v) => setCreateForm({ ...createForm, template_id: Number(v) })}>
                    <SelectTrigger><SelectValue placeholder={t("selectTemplate")} /></SelectTrigger>
                    <SelectContent>
                      {templates.data?.data?.map((tpl) => (
                        <SelectItem key={tpl.id} value={String(tpl.id)}>{tpl.name}</SelectItem>
                      ))}
                    </SelectContent>
                  </Select>
                </div>
                <div className="flex items-center justify-between">
                  <Label>{t("traceEnabled")}</Label>
                  <Switch
                    checked={createForm.trace_enabled}
                    onCheckedChange={(checked) =>
                      setCreateForm({ ...createForm, trace_enabled: checked })
                    }
                  />
                </div>
              </>
            )}
          </div>
          <DialogFooter>
            <Button variant="outline" onClick={() => setCreateOpen(false)}>{tc("cancel")}</Button>
            <Button onClick={handleCreate} disabled={createMutation.isPending}>{tc("save")}</Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      {/* Edit Dialog */}
      <Dialog open={!!editItem} onOpenChange={(open) => { if (!open) setEditItem(null); }}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>{tc("edit")}</DialogTitle>
          </DialogHeader>
          <div className="space-y-4 py-4">
            <div className="space-y-2">
              <Label>{tc("name")}</Label>
              <Input
                value={editForm.name}
                onChange={(e) => setEditForm({ ...editForm, name: e.target.value })}
              />
            </div>
            {isAdmin && (
              <>
                <div className="space-y-2">
                  <Label>{t("user")} ID</Label>
                  <Input
                    type="number"
                    value={editForm.user_id}
                    onChange={(e) => setEditForm({ ...editForm, user_id: e.target.value })}
                  />
                  <p className="text-meta text-muted-foreground">{t("ownerChangeHint")}</p>
                </div>
                <StatusSelect value={editForm.status} onChange={(v) => setEditForm({ ...editForm, status: v })} />
                <div className="space-y-2">
                  <Label>{t("expiredAt")}</Label>
                  <Input
                    type="number"
                    placeholder="Unix timestamp"
                    value={editForm.expired_at}
                    onChange={(e) => setEditForm({ ...editForm, expired_at: e.target.value })}
                  />
                </div>
                <div className="space-y-2">
                  <Label>{t("models")}</Label>
                  <TagInput
                    value={parseModels(editForm.models)}
                    onChange={(tags) => setEditForm({ ...editForm, models: serializeModels(tags) })}
                    placeholder={t("allModels")}
                  />
                </div>
                <div className="space-y-2">
                  <Label>{tTpl("allowedChannels")}</Label>
                  <ChannelMultiSelect
                    value={editForm.allowed_channel_ids}
                    onChange={(ids) => setEditForm({ ...editForm, allowed_channel_ids: ids })}
                    placeholder={tTpl("allowedChannelsPlaceholder")}
                  />
                  <p className="text-meta text-muted-foreground">{tTpl("allowedChannelsEmptyHint")}</p>
                </div>
              </>
            )}
            <div className="flex items-center justify-between">
              <Label>{t("traceEnabled")}</Label>
              <Switch
                checked={editForm.trace_enabled}
                onCheckedChange={(checked) =>
                  setEditForm({ ...editForm, trace_enabled: checked })
                }
              />
            </div>
          </div>
          {isAdmin && editItem && <AgentRouteEditor sourceType="token" sourceId={editItem.id} />}
          <DialogFooter>
            <Button variant="outline" onClick={() => setEditItem(null)}>{tc("cancel")}</Button>
            <Button onClick={handleEdit} disabled={updateMutation.isPending}>{tc("save")}</Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      <DeleteConfirm
        open={!!deleteItem}
        onOpenChange={(open) => { if (!open) setDeleteItem(null); }}
        onConfirm={handleDelete}
      />

      <DeleteConfirm
        open={weakKeyConfirmOpen}
        onOpenChange={(open) => {
          setWeakKeyConfirmOpen(open);
          if (!open) {
            setPendingWeakKeyCreate(null);
          }
        }}
        onConfirm={handleCreateWithWeakKey}
        title={t("weakKeyWarningTitle")}
        description={t("weakKeyWarningDesc")}
      />
    </div>
  );
}
