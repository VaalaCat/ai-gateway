"use client";

import { useState, useCallback, useRef, useMemo, useEffect } from "react";
import { useTranslations } from "next-intl";
import { Loader2, Search, Play, Square } from "lucide-react";

import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Switch } from "@/components/ui/switch";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import {
  Tooltip,
  TooltipContent,
  TooltipProvider,
  TooltipTrigger,
} from "@/components/ui/tooltip";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { ModelName } from "@/components/business/model-name";
import { api } from "@/lib/api/client";
import { API_TYPES } from "@/lib/constants";
import { useDebounce } from "@/hooks/use-debounce";
import type { Channel, ChannelTestResponse } from "@/lib/types";
import type { OnlineAgentInfo } from "@/lib/types";
import { useOnlineAgents } from "@/lib/api/agents";
import { useAgentRoutes } from "@/lib/api/agent-routes";

type TestStatus = "pending" | "testing" | "success" | "failed";

interface ModelTestResult {
  status: TestStatus;
  error?: string;
  timeCost?: number;
}

interface ChannelTestDialogProps {
  channel: Channel;
  open: boolean;
  onOpenChange: (open: boolean) => void;
}

const PAGE_SIZE = 20;

const ENDPOINT_TYPES = [
  { value: API_TYPES.CHAT_COMPLETION, labelKey: "chatCompletion" },
  { value: API_TYPES.RESPONSES, labelKey: "responsesApi" },
  { value: "anthropic", labelKey: "claudeMessages" },
] as const;

type EndpointLabelKey = (typeof ENDPOINT_TYPES)[number]["labelKey"];

export function ChannelTestDialog({
  channel,
  open,
  onOpenChange,
}: ChannelTestDialogProps) {
  const t = useTranslations("channels");
  const tc = useTranslations("common");

  const [endpointType, setEndpointType] = useState<string>(API_TYPES.CHAT_COMPLETION);
  const [stream, setStream] = useState(false);
  const [searchQuery, setSearchQuery] = useState("");
  const debouncedSearch = useDebounce(searchQuery, 300);
  const [page, setPage] = useState(1);
  const [results, setResults] = useState<Record<string, ModelTestResult>>({});
  const [batchRunning, setBatchRunning] = useState(false);
  const abortRef = useRef<AbortController | null>(null);

  const [agentId, setAgentId] = useState<string>("");
  const { data: onlineAgents } = useOnlineAgents();

  // Auto-resolve default agent from channel's routing rules
  const { data: channelRoutes } = useAgentRoutes({ source_type: "channel", source_id: channel.id });
  const defaultAgentId = (channelRoutes?.data ?? []).find(r => !r.model)?.agent_id;
  useEffect(() => {
    if (defaultAgentId && !agentId) {
      setAgentId(defaultAgentId);
    }
  }, [defaultAgentId]); // eslint-disable-line react-hooks/exhaustive-deps

  const allModels = useMemo(() => {
    if (!channel.models) return [];
    return channel.models
      .split(",")
      .map((s) => s.trim())
      .filter(Boolean);
  }, [channel.models]);

  const filteredModels = useMemo(() => {
    if (!debouncedSearch) return allModels;
    const lower = debouncedSearch.toLowerCase();
    return allModels.filter((m) => m.toLowerCase().includes(lower));
  }, [allModels, debouncedSearch]);

  const totalPages = Math.max(1, Math.ceil(filteredModels.length / PAGE_SIZE));
  const currentPage = Math.min(page, totalPages);
  const pagedModels = filteredModels.slice(
    (currentPage - 1) * PAGE_SIZE,
    currentPage * PAGE_SIZE
  );

  const handleEndpointTypeChange = useCallback(
    (value: string) => {
      setEndpointType(value);
      setResults({});
    },
    []
  );

  const testModel = useCallback(
    async (model: string, signal?: AbortSignal) => {
      setResults((prev) => ({
        ...prev,
        [model]: { status: "testing" },
      }));
      try {
        const res = await api.request<ChannelTestResponse>(
          `/admin/channels/${channel.id}/test`,
          {
            method: "POST",
            body: JSON.stringify({
              model,
              endpoint_type: endpointType,
              stream,
              ...(agentId ? { agent_id: agentId } : {}),
            }),
            signal,
          }
        );
        if (res.success) {
          setResults((prev) => ({
            ...prev,
            [model]: { status: "success", timeCost: res.time_cost },
          }));
        } else {
          setResults((prev) => ({
            ...prev,
            [model]: {
              status: "failed",
              error: res.error || "Unknown error",
              timeCost: res.time_cost,
            },
          }));
        }
      } catch (err) {
        if (signal?.aborted) return;
        setResults((prev) => ({
          ...prev,
          [model]: {
            status: "failed",
            error: err instanceof Error ? err.message : "Unknown error",
          },
        }));
      }
    },
    [channel.id, endpointType, stream, agentId]
  );

  const handleSingleTest = useCallback(
    (model: string) => {
      testModel(model);
    },
    [testModel]
  );

  const handleBatchTest = useCallback(async () => {
    const controller = new AbortController();
    abortRef.current = controller;
    setBatchRunning(true);

    for (const model of filteredModels) {
      if (controller.signal.aborted) break;
      await testModel(model, controller.signal);
    }

    setBatchRunning(false);
    abortRef.current = null;
  }, [filteredModels, testModel]);

  const handleStopTest = useCallback(() => {
    abortRef.current?.abort();
    abortRef.current = null;
    setBatchRunning(false);
  }, []);

  const renderStatusBadge = (model: string) => {
    const result = results[model];
    if (!result || result.status === "pending") {
      return <Badge variant="secondary">{t("statusPending")}</Badge>;
    }
    if (result.status === "testing") {
      return (
        <Badge variant="secondary">
          <Loader2 className="mr-1 size-3 animate-spin" />
          {t("statusTesting")}
        </Badge>
      );
    }
    if (result.status === "success") {
      return (
        <Badge className="bg-green-100 text-green-800 dark:bg-green-900 dark:text-green-200">
          {t("statusSuccess")}
          {result.timeCost != null && (
            <span className="ml-1">({result.timeCost.toFixed(2)}s)</span>
          )}
        </Badge>
      );
    }
    // failed
    return (
      <TooltipProvider delayDuration={200}>
        <Tooltip>
          <TooltipTrigger asChild>
            <Badge variant="destructive">
              {t("statusFailed")}
              {result.timeCost != null && (
                <span className="ml-1">({result.timeCost.toFixed(2)}s)</span>
              )}
            </Badge>
          </TooltipTrigger>
          <TooltipContent side="top" className="max-w-xs">
            <p className="text-xs">{result.error}</p>
          </TooltipContent>
        </Tooltip>
      </TooltipProvider>
    );
  };

  const testedCount = Object.values(results).filter(
    (r) => r.status === "success" || r.status === "failed"
  ).length;

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-w-3xl max-h-[85vh] flex flex-col">
        <DialogHeader>
          <DialogTitle>{t("testDialogTitle")} - {channel.name}</DialogTitle>
          <p className="text-sm text-muted-foreground">
            {t("testDialogSubtitle", { count: allModels.length })}
          </p>
        </DialogHeader>

        <div className="flex flex-wrap items-center gap-4 py-2">
          <div className="flex items-center gap-2">
            <Label>{t("endpointType")}</Label>
            <Select
              value={endpointType}
              onValueChange={handleEndpointTypeChange}
            >
              <SelectTrigger className="w-[200px]">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {ENDPOINT_TYPES.map((ep) => (
                  <SelectItem key={ep.value} value={ep.value}>
                    {t(ep.labelKey as EndpointLabelKey)}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </div>

          <div className="flex items-center gap-2">
            <Label>{t("streamMode")}</Label>
            <Switch checked={stream} onCheckedChange={setStream} />
          </div>

          <div className="flex items-center gap-2">
            <Label>{t("agentSelector")}</Label>
            <Select value={agentId || "local"} onValueChange={(v) => setAgentId(v === "local" ? "" : v)}>
              <SelectTrigger className="w-[200px]">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="local">{t("localTest")}</SelectItem>
                {onlineAgents?.map((a: OnlineAgentInfo) => (
                  <SelectItem key={a.agent_id} value={a.agent_id}>
                    {a.name} ({a.agent_id.slice(0, 8)}...)
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </div>
        </div>

        <div className="flex items-center gap-2 py-2">
          <div className="relative flex-1">
            <Search className="absolute left-2.5 top-2.5 size-4 text-muted-foreground" />
            <Input
              className="pl-9"
              placeholder={t("searchModel")}
              value={searchQuery}
              onChange={(e) => {
                setSearchQuery(e.target.value);
                setPage(1);
              }}
            />
          </div>
          {batchRunning ? (
            <Button variant="destructive" onClick={handleStopTest}>
              <Square className="mr-2 size-4" />
              {t("stopTest")}
            </Button>
          ) : (
            <Button onClick={handleBatchTest} disabled={filteredModels.length === 0}>
              <Play className="mr-2 size-4" />
              {t("batchTest")}
              {filteredModels.length > 0 && (
                <span className="ml-1">({filteredModels.length})</span>
              )}
            </Button>
          )}
        </div>

        <div className="flex-1 overflow-auto min-h-0">
          {filteredModels.length === 0 ? (
            <div className="flex items-center justify-center py-12 text-muted-foreground">
              {tc("noData")}
            </div>
          ) : (
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead className="w-[50%]">
                    {t("models")}
                  </TableHead>
                  <TableHead>{tc("status")}</TableHead>
                  <TableHead className="text-right">
                    {tc("actions")}
                  </TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {pagedModels.map((model) => (
                  <TableRow key={model}>
                    <TableCell>
                      <ModelName name={model} />
                    </TableCell>
                    <TableCell>{renderStatusBadge(model)}</TableCell>
                    <TableCell className="text-right">
                      <Button
                        variant="outline"
                        size="sm"
                        disabled={
                          batchRunning ||
                          results[model]?.status === "testing"
                        }
                        onClick={() => handleSingleTest(model)}
                      >
                        {results[model]?.status === "testing" ? (
                          <Loader2 className="mr-1 size-3 animate-spin" />
                        ) : null}
                        {t("test")}
                      </Button>
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          )}
        </div>

        {filteredModels.length > 0 && (
          <div className="flex items-center justify-between border-t pt-3 text-sm text-muted-foreground">
            <span>
              {tc("total", { count: filteredModels.length })}
              {testedCount > 0 && (
                <span className="ml-2">
                  ({testedCount}/{filteredModels.length})
                </span>
              )}
            </span>
            <div className="flex items-center gap-2">
              <Button
                variant="outline"
                size="sm"
                disabled={currentPage <= 1}
                onClick={() => setPage((p) => p - 1)}
              >
                &lt;
              </Button>
              <span>
                {currentPage} / {totalPages}
              </span>
              <Button
                variant="outline"
                size="sm"
                disabled={currentPage >= totalPages}
                onClick={() => setPage((p) => p + 1)}
              >
                &gt;
              </Button>
            </div>
          </div>
        )}
      </DialogContent>
    </Dialog>
  );
}
