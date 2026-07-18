"use client";

import { useState, useMemo } from "react";
import { useTranslations } from "next-intl";
import { ColumnDef, type VisibilityState } from "@tanstack/react-table";
import { toast } from "sonner";
import { ChevronRight, Copy, Database, MoreHorizontal, Plus, RefreshCw, Ticket, Info } from "lucide-react";
import { useRouter } from "next/navigation";
import { copyTextWithFeedback } from "@/lib/utils/clipboard";

import { DataTable } from "@/components/data-table/data-table";
import { DataTableColumnHeader } from "@/components/data-table/column-header";
import { FilterableToolbar } from "@/components/data-table/filterable-toolbar";
import { useFilterState } from "@/components/data-table/use-filter-state";
import type { FilterSpec } from "@/components/data-table/filter-spec";
import type { ToolbarAction } from "@/components/data-table/toolbar-actions";
import { Button } from "@/components/ui/button";
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
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { Checkbox } from "@/components/ui/checkbox";
import { DeleteConfirm } from "@/components/business/delete-confirm";
import { CopyableText } from "@/components/business/copyable-text";
import { AgentAddressEditor } from "@/components/business/agent-address-editor";
import { AgentConnectionStatus } from "@/components/business/agent-connection-status";
import { AgentEditDialog } from "@/components/business/agent-edit-dialog";
import { formatErrorToast } from "@/lib/api/error-toast";

import {
  Collapsible,
  CollapsibleContent,
  CollapsibleTrigger,
} from "@/components/ui/collapsible";
import { AgentExpandedRow } from "@/components/business/agent-expanded-row";

import {
  useAgents,
  useCreateAgent,
  useDeleteAgent,
  useGenerateEnrollmentToken,
  useFullSyncAgents,
} from "@/lib/api/agents";
import { PAGE_SIZES } from "@/lib/constants";
import type { Agent } from "@/lib/types";
import { useBreakpoint } from "@/lib/hooks/use-breakpoint";

export default function AgentsPage() {
  const t = useTranslations("agents");
  const tc = useTranslations("common");
  const router = useRouter();

  const [page, setPage] = useState(1);
  const [pageSize, setPageSize] = useState<number>(PAGE_SIZES.DEFAULT);

  const filterSpec = useMemo(() => ({
    search: { kind: "text", placeholder: tc("search") },
    status: {
      kind: "enum",
      options: [
        { value: "1", label: t("connection.enabled") },
        { value: "0", label: t("connection.disabled") },
      ],
      placeholder: t("filterByStatus"),
    },
  } satisfies FilterSpec), [t, tc]);

  const [filterValues, setFilterValues] = useFilterState(filterSpec);

  const { data, isLoading } = useAgents({
    page,
    page_size: pageSize,
    ...(filterValues.search ? { search: String(filterValues.search) } : {}),
    ...(filterValues.status !== undefined && filterValues.status !== "" ? { status: Number(filterValues.status) } : {}),
  });

  const agents = data?.data ?? [];
  const total = data?.total ?? 0;
  const pageCount = Math.ceil(total / pageSize) || 1;

  const breakpoint = useBreakpoint();
  const [desktopColumnVisibility, setDesktopColumnVisibility] = useState<VisibilityState>({});
  const columnVisibility = breakpoint === "xs"
    ? { select: false, admin: false, control: false, direct: false, relay: false }
    : desktopColumnVisibility;

  const handlePaginationChange = (newPage: number, newPageSize: number) => {
    if (newPageSize !== pageSize) {
      setPage(1);
      setPageSize(newPageSize);
    } else {
      setPage(newPage);
    }
  };

  const [rowSelection, setRowSelection] = useState<Record<string, boolean>>({});

  const createMutation = useCreateAgent();
  const deleteMutation = useDeleteAgent();
  const enrollMutation = useGenerateEnrollmentToken();
  const fullSyncMutation = useFullSyncAgents();

  const [createOpen, setCreateOpen] = useState(false);
  const [editAgentId, setEditAgentId] = useState<number | null>(null);
  const [deleteItem, setDeleteItem] = useState<Agent | null>(null);
  const [enrollOpen, setEnrollOpen] = useState(false);

  const [createForm, setCreateForm] = useState({ name: "", agent_id: "", secret: "", tags: "", http_addresses: "", proxy_url: "" });
  const [enrollTTL, setEnrollTTL] = useState("3600");
  const [enrollToken, setEnrollToken] = useState("");

  const handleCreate = async () => {
    try {
      await createMutation.mutateAsync({
        name: createForm.name,
        ...(createForm.agent_id ? { agent_id: createForm.agent_id } : {}),
        ...(createForm.secret ? { secret: createForm.secret } : {}),
        tags: createForm.tags,
        http_addresses: createForm.http_addresses,
        proxy_url: createForm.proxy_url,
      });
      toast.success(tc("success"));
      setCreateOpen(false);
      setCreateForm({ name: "", agent_id: "", secret: "", tags: "", http_addresses: "", proxy_url: "" });
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

  const handleGenerateToken = async () => {
    try {
      const result = await enrollMutation.mutateAsync({ ttl: Number(enrollTTL) });
      setEnrollToken(result.enrollment_token);
      toast.success(tc("success"));
    } catch (e) {
      toast.error(formatErrorToast(e, tc("error")));
    }
  };

  const handleFullSync = async (agentIds?: string[], all?: boolean) => {
    try {
      const result = await fullSyncMutation.mutateAsync(
        all ? { all: true } : { agent_ids: agentIds }
      );
      const succeeded = result.results.filter((r) => r.success).length;
      const failed = result.results.filter((r) => !r.success).length;
      if (failed === 0) {
        toast.success(t("fullSyncSuccess", { count: succeeded }));
      } else {
        toast.warning(`${t("fullSyncSuccess", { count: succeeded })}, ${t("fullSyncFailed", { count: failed })}`);
      }
      setRowSelection({});
    } catch (e) {
      toast.error(formatErrorToast(e, tc("error")));
    }
  };

  const columns: ColumnDef<Agent>[] = [
    {
      id: "select",
      header: ({ table }) => (
        <Checkbox
          checked={table.getIsAllPageRowsSelected()}
          onCheckedChange={(value) => table.toggleAllPageRowsSelected(!!value)}
          aria-label="Select all"
        />
      ),
      cell: ({ row }) => (
        <span onClick={(event) => event.stopPropagation()}>
          <Checkbox
            checked={row.getIsSelected()}
            onCheckedChange={(value) => row.toggleSelected(!!value)}
            aria-label="Select row"
          />
        </span>
      ),
      enableHiding: false,
    },
    {
      id: "agent",
      accessorKey: "name",
      header: ({ column }) => <DataTableColumnHeader column={column} title={t("connection.agent")} />,
      enableHiding: false,
      cell: ({ row }) => (
        <div className="flex min-w-48 items-start gap-2">
          <Button
            type="button"
            variant="ghost"
            size="icon-xs"
            aria-label={row.getIsExpanded() ? t("connection.collapse") : t("connection.expand")}
            aria-expanded={row.getIsExpanded()}
            onClick={(event) => {
              event.stopPropagation();
              row.toggleExpanded();
            }}
          >
            <ChevronRight data-icon="inline-start" className={row.getIsExpanded() ? "rotate-90 transition-transform" : "transition-transform"} />
          </Button>
          <div className="flex min-w-0 flex-1 flex-col gap-1.5">
            <div className="min-w-0">
              <div className="truncate text-sm font-medium">{row.original.name}</div>
              <div className="truncate font-mono text-xs text-muted-foreground">{row.original.agent_id}</div>
            </div>
            {breakpoint === "xs" ? (
              <dl className="grid min-w-0 grid-cols-[auto_minmax(0,1fr)] items-center gap-x-2 gap-y-1">
                <dt className="text-xs text-muted-foreground">{t("connection.admin")}</dt>
                <dd className="min-w-0"><AgentConnectionStatus kind="admin" status={row.original.status} /></dd>
                <dt className="text-xs text-muted-foreground">{t("connection.control")}</dt>
                <dd className="min-w-0"><AgentConnectionStatus kind="control" value={row.original.connection.control} /></dd>
                <dt className="text-xs text-muted-foreground">{t("connection.direct")}</dt>
                <dd className="min-w-0"><AgentConnectionStatus kind="direct" value={row.original.connection.direct} /></dd>
                <dt className="text-xs text-muted-foreground">{t("connection.relay")}</dt>
                <dd className="min-w-0"><AgentConnectionStatus kind="relay" value={row.original.connection.relay} /></dd>
              </dl>
            ) : null}
          </div>
        </div>
      ),
    },
    {
      id: "admin",
      header: t("connection.admin"),
      cell: ({ row }) => <AgentConnectionStatus kind="admin" status={row.original.status} />,
    },
    {
      id: "control",
      header: t("connection.control"),
      cell: ({ row }) => <AgentConnectionStatus kind="control" value={row.original.connection.control} />,
    },
    {
      id: "direct",
      header: t("connection.direct"),
      cell: ({ row }) => <AgentConnectionStatus kind="direct" value={row.original.connection.direct} />,
    },
    {
      id: "relay",
      header: t("connection.relay"),
      cell: ({ row }) => <AgentConnectionStatus kind="relay" value={row.original.connection.relay} />,
    },
    {
      id: "actions",
      size: 48,
      header: tc("actions"),
      enableHiding: false,
      cell: ({ row }) => (
        <div className="flex w-8 justify-end" onClick={(event) => event.stopPropagation()}>
        <DropdownMenu>
          <DropdownMenuTrigger asChild>
            <Button variant="ghost" size="icon-sm" aria-label={t("connection.actionsFor", { name: row.original.name })}>
              <MoreHorizontal data-icon="inline-start" />
            </Button>
          </DropdownMenuTrigger>
          <DropdownMenuContent align="end">
            <DropdownMenuItem onClick={() => router.push(`/agents/detail?id=${row.original.id}`)}>
              {t("detail")}
            </DropdownMenuItem>
            <DropdownMenuItem
              onClick={() => handleFullSync([row.original.agent_id])}
              disabled={row.original.connection.control.state !== "connected" || fullSyncMutation.isPending}
            >
              {t("fullSync")}
            </DropdownMenuItem>
            <DropdownMenuItem onClick={() => setEditAgentId(row.original.id)}>
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
  ];

  return (
    <div className="flex flex-col gap-4">
      <Collapsible>
        <div className="flex items-start justify-between">
          <div>
            <h1 className="text-2xl font-bold">{t("title")}</h1>
            <p className="text-muted-foreground mt-1">{t("description")}</p>
          </div>
          <CollapsibleTrigger asChild>
            <Button variant="ghost" size="sm" className="h-7 gap-1.5 text-xs text-muted-foreground shrink-0">
              <Info className="size-3.5" />
              {t("usageGuide")}
            </Button>
          </CollapsibleTrigger>
        </div>
        <CollapsibleContent>
          <div className="mt-3 flex flex-col gap-3 rounded-md border p-4">
            <p className="text-sm text-muted-foreground">{t("usageGuideDesc")}</p>
            <div className="overflow-x-auto">
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead>{t("headerName")}</TableHead>
                    <TableHead>{t("headerPurpose")}</TableHead>
                    <TableHead>{t("headerExample")}</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  <TableRow>
                    <TableCell><code className="bg-muted rounded px-1.5 py-0.5">X-Vaala-Agent-ID</code></TableCell>
                    <TableCell>{t("headerAgentId")}</TableCell>
                    <TableCell><code className="text-muted-foreground">agent-xxxxx</code></TableCell>
                  </TableRow>
                  <TableRow>
                    <TableCell><code className="bg-muted rounded px-1.5 py-0.5">X-Vaala-Agent-Tag</code></TableCell>
                    <TableCell>{t("headerAgentTag")}</TableCell>
                    <TableCell><code className="text-muted-foreground">us-west</code></TableCell>
                  </TableRow>
                  <TableRow>
                    <TableCell><code className="bg-muted rounded px-1.5 py-0.5">X-Vaala-Agent-Address-Tag</code></TableCell>
                    <TableCell>{t("headerAddressTag")}</TableCell>
                    <TableCell><code className="text-muted-foreground">internal</code></TableCell>
                  </TableRow>
                  <TableRow>
                    <TableCell><code className="bg-muted rounded px-1.5 py-0.5">X-Vaala-Channel-ID</code></TableCell>
                    <TableCell>{t("headerChannelId")}</TableCell>
                    <TableCell><code className="text-muted-foreground">42</code></TableCell>
                  </TableRow>
                </TableBody>
              </Table>
            </div>
            <div>
              <p className="text-xs font-medium mb-1.5">{t("curlExample")}</p>
              <pre className="text-xs bg-muted rounded-md p-3 overflow-x-auto"><code>{`curl -X POST https://your-gateway/v1/chat/completions \\
  -H "Authorization: Bearer sk-xxx" \\
  -H "X-Vaala-Agent-ID: agent-xxxxx" \\
  -d '{"model":"gpt-4","messages":[{"role":"user","content":"hello"}]}'`}</code></pre>
            </div>
          </div>
        </CollapsibleContent>
      </Collapsible>

      <DataTable
        columns={columns}
        data={agents}
        loading={isLoading}
        defaultColumnVisibility={breakpoint === "xs" ? undefined : {}}
        columnVisibilityState={columnVisibility}
        onColumnVisibilityChange={(next) => {
          if (breakpoint !== "xs") setDesktopColumnVisibility(next);
        }}
        total={total}
        page={page}
        pageSize={pageSize}
        pageCount={pageCount}
        onPaginationChange={handlePaginationChange}
        getRowId={(row) => String(row.id)}
        tableLayout={breakpoint === "xs" ? "fixed" : "auto"}
        renderExpandedRow={(row) => <AgentExpandedRow agent={row.original} expanded={row.getIsExpanded()} />}
        rowSelection={rowSelection}
        onRowSelectionChange={setRowSelection}
        toolbar={
          <FilterableToolbar
            spec={filterSpec}
            value={filterValues}
            onChange={setFilterValues}
            secondaryActions={[
              Object.keys(rowSelection).length > 0 && {
                label: t("fullSyncSelected", { count: Object.keys(rowSelection).length }),
                icon: <RefreshCw className="size-4" />,
                loading: fullSyncMutation.isPending,
                onClick: () => {
                  const selectedIds = Object.keys(rowSelection)
                    .map((id) => agents.find((agent) => String(agent.id) === id)?.agent_id)
                    .filter((id): id is string => Boolean(id));
                  handleFullSync(selectedIds);
                },
              },
              {
                label: t("viewCache"),
                icon: <Database className="size-4" />,
                href: "/agents/cache",
                variant: "outline",
              },
              {
                label: t("fullSyncAll"),
                icon: <RefreshCw className="size-4" />,
                loading: fullSyncMutation.isPending,
                onClick: () => handleFullSync(undefined, true),
                variant: "outline",
              },
              {
                label: t("generateToken"),
                icon: <Ticket className="size-4" />,
                onClick: () => {
                  setEnrollToken("");
                  setEnrollTTL("3600");
                  setEnrollOpen(true);
                },
                variant: "outline",
              },
            ].filter(Boolean) as ToolbarAction[]}
            primaryAction={
              <Button size="sm" onClick={() => {
                setCreateForm({ name: "", agent_id: "", secret: "", tags: "", http_addresses: "", proxy_url: "" });
                setCreateOpen(true);
              }}>
                <Plus className="mr-2 size-4" />
                {t("createAgent")}
              </Button>
            }
          />
        }
      />

      {/* Create Dialog */}
      <Dialog open={createOpen} onOpenChange={setCreateOpen}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>{t("createAgent")}</DialogTitle>
          </DialogHeader>
          <div className="flex flex-col gap-4 py-4">
            <div className="flex flex-col gap-2">
              <Label>{tc("name")}</Label>
              <Input
                value={createForm.name}
                onChange={(e) => setCreateForm({ ...createForm, name: e.target.value })}
              />
            </div>
            <div className="flex flex-col gap-2">
              <Label>{t("agentId")}</Label>
              <Input
                value={createForm.agent_id}
                onChange={(e) => setCreateForm({ ...createForm, agent_id: e.target.value })}
              />
            </div>
            <div className="flex flex-col gap-2">
              <Label>{t("secret")}</Label>
              <Input
                value={createForm.secret}
                onChange={(e) => setCreateForm({ ...createForm, secret: e.target.value })}
              />
            </div>
            <div className="flex flex-col gap-2">
              <Label>{t("tags")}</Label>
              <Input
                placeholder={t("tagsPlaceholder")}
                value={createForm.tags}
                onChange={(e) => setCreateForm({ ...createForm, tags: e.target.value })}
              />
            </div>
            <AgentAddressEditor
              value={createForm.http_addresses}
              onChange={(v) => setCreateForm({ ...createForm, http_addresses: v })}
            />
            <div className="flex flex-col gap-2">
              <Label>{t("proxyUrl")}</Label>
              <Input
                placeholder={t("proxyUrlPlaceholder")}
                value={createForm.proxy_url}
                onChange={(e) => setCreateForm({ ...createForm, proxy_url: e.target.value })}
              />
            </div>
          </div>
          <DialogFooter>
            <Button variant="outline" onClick={() => setCreateOpen(false)}>{tc("cancel")}</Button>
            <Button onClick={handleCreate} disabled={createMutation.isPending}>{tc("save")}</Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      <AgentEditDialog
        open={editAgentId !== null}
        agentId={editAgentId}
        onOpenChange={(open) => { if (!open) setEditAgentId(null); }}
      />

      {/* Enrollment Token Dialog */}
      <Dialog open={enrollOpen} onOpenChange={setEnrollOpen}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>{t("enrollmentToken")}</DialogTitle>
          </DialogHeader>
          <div className="flex flex-col gap-4 py-4">
            {!enrollToken ? (
              <div className="flex flex-col gap-2">
                <Label>{t("ttl")}</Label>
                <Input
                  type="number"
                  value={enrollTTL}
                  onChange={(e) => setEnrollTTL(e.target.value)}
                />
              </div>
            ) : (
              <div className="flex flex-col gap-2">
                <Label>{t("enrollmentToken")}</Label>
                <CopyableText text={enrollToken} />
                <p className="text-sm text-muted-foreground">{t("tokenGenerated")}</p>
                <div className="mt-4 flex flex-col gap-2 rounded-md bg-muted p-3 text-sm">
                  <p className="font-medium">{t("enrollmentGuide")}</p>
                  <p className="text-muted-foreground">{t("enrollmentStep1")}</p>
                  <p className="text-muted-foreground">{t("enrollmentStep2")}</p>
                  <div className="flex items-center gap-2">
                    <code className="flex-1 rounded bg-background p-2 text-xs break-all">
                      {`./ai-gateway agent --master ${typeof window !== "undefined" ? window.location.origin : "http://localhost:8140"} --enrollment-token ${enrollToken}`}
                    </code>
                    <Button
                      variant="outline"
                      size="icon"
                      className="shrink-0"
                      onClick={() =>
                        copyTextWithFeedback(
                          `./ai-gateway agent --master ${window.location.origin} --enrollment-token ${enrollToken}`,
                          { success: tc("copied"), error: tc("copyFailed") }
                        )
                      }
                    >
                      <Copy className="size-4" />
                    </Button>
                  </div>
                  <p className="text-muted-foreground">{t("enrollmentStep3")}</p>
                </div>
              </div>
            )}
          </div>
          <DialogFooter>
            {!enrollToken ? (
              <>
                <Button variant="outline" onClick={() => setEnrollOpen(false)}>{tc("cancel")}</Button>
                <Button onClick={handleGenerateToken} disabled={enrollMutation.isPending}>
                  {t("generateToken")}
                </Button>
              </>
            ) : (
              <Button onClick={() => setEnrollOpen(false)}>{tc("confirm")}</Button>
            )}
          </DialogFooter>
        </DialogContent>
      </Dialog>

      <DeleteConfirm
        open={!!deleteItem}
        onOpenChange={(open) => { if (!open) setDeleteItem(null); }}
        onConfirm={handleDelete}
      />
    </div>
  );
}
