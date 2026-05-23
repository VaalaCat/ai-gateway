"use client";

import { useState, useMemo } from "react";
import { Download, Loader2, Check, ChevronRight } from "lucide-react";
import { useTranslations } from "next-intl";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Checkbox } from "@/components/ui/checkbox";
import {
  Dialog,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { useFetchUpstreamModels } from "@/lib/api/channels";
import { formatErrorToast } from "@/lib/api/error-toast";
import { groupModelsByProvider, getProviderIconKey } from "@/lib/constants";
import { ProviderAvatar } from "@/components/business/provider-avatar";

interface FetchModelsButtonProps {
  baseUrl: string;
  apiKey: string;
  channelType: number;
  endpoints?: string;
  proxyUrl?: string;
  agentId?: string;
  existingModels: string[];
  onModelsSelected: (models: string[]) => void;
}

export function FetchModelsButton({
  baseUrl,
  apiKey,
  channelType,
  endpoints,
  proxyUrl,
  agentId,
  existingModels,
  onModelsSelected,
}: FetchModelsButtonProps) {
  const t = useTranslations("channels");
  const tc = useTranslations("common");
  const fetchMutation = useFetchUpstreamModels();

  const [dialogOpen, setDialogOpen] = useState(false);
  const [fetchedModels, setFetchedModels] = useState<string[]>([]);
  const [selected, setSelected] = useState<Set<string>>(new Set());
  const [search, setSearch] = useState("");
  const [expanded, setExpanded] = useState<Set<string>>(new Set());

  const canFetch = apiKey.trim() !== "";

  const existingSet = useMemo(() => new Set(existingModels), [existingModels]);

  const groups = useMemo(
    () => groupModelsByProvider(fetchedModels),
    [fetchedModels]
  );

  const filteredGroups = useMemo(() => {
    if (!search.trim()) return groups;
    const q = search.toLowerCase();
    return groups
      .map((group) => ({
        ...group,
        models: group.models.filter((m) => m.toLowerCase().includes(q)),
      }))
      .filter((group) => group.models.length > 0);
  }, [groups, search]);

  const allFilteredModels = useMemo(
    () => filteredGroups.flatMap((g) => g.models),
    [filteredGroups]
  );

  const handleFetch = async () => {
    try {
      const result = await fetchMutation.mutateAsync({
        base_url: baseUrl,
        key: apiKey,
        type: channelType,
        endpoints,
        proxy_url: proxyUrl,
        agent_id: agentId,
      });
      if (result.error) {
        toast.error(result.error);
        return;
      }
      const models = result.models || [];
      setFetchedModels(models);
      const existing = new Set(existingModels);
      setSelected(new Set(models.filter((m: string) => !existing.has(m))));
      setSearch("");
      setExpanded(new Set());
      setDialogOpen(true);
    } catch (e) {
      toast.error(formatErrorToast(e, tc("error")));
    }
  };

  const groupKey = (group: { provider: string | null; displayName: string }) =>
    group.provider ?? `_${group.displayName}`;

  const toggleExpand = (key: string) => {
    const next = new Set(expanded);
    if (next.has(key)) next.delete(key);
    else next.add(key);
    setExpanded(next);
  };

  const toggleModel = (model: string) => {
    const next = new Set(selected);
    if (next.has(model)) next.delete(model);
    else next.add(model);
    setSelected(next);
  };

  const toggleGroup = (models: string[]) => {
    const allSelected = models.every((m) => selected.has(m));
    const next = new Set(selected);
    if (allSelected) {
      for (const m of models) next.delete(m);
    } else {
      for (const m of models) next.add(m);
    }
    setSelected(next);
  };

  const toggleAll = () => {
    const allSelected =
      allFilteredModels.length > 0 &&
      allFilteredModels.every((m) => selected.has(m));
    if (allSelected) {
      const next = new Set(selected);
      for (const m of allFilteredModels) next.delete(m);
      setSelected(next);
    } else {
      setSelected(new Set([...selected, ...allFilteredModels]));
    }
  };

  const handleConfirm = () => {
    const newModels = Array.from(selected).filter((m) => !existingSet.has(m));
    onModelsSelected([...existingModels, ...newModels]);
    setDialogOpen(false);
  };

  const isSearching = search.trim().length > 0;

  return (
    <>
      <Button
        type="button"
        variant="outline"
        size="sm"
        disabled={!canFetch || fetchMutation.isPending}
        onClick={handleFetch}
      >
        {fetchMutation.isPending ? (
          <Loader2 className="mr-2 size-4 animate-spin" />
        ) : (
          <Download className="mr-2 size-4" />
        )}
        {t("fetchModels")}
      </Button>

      <Dialog open={dialogOpen} onOpenChange={setDialogOpen}>
        <DialogContent className="flex max-h-[85vh] flex-col sm:max-w-lg">
          <DialogHeader>
            <DialogTitle>
              {t("fetchModelsTitle")}{" "}
              <span className="text-sm font-normal text-muted-foreground">
                {t("nSelected", { count: selected.size })}
              </span>
            </DialogTitle>
          </DialogHeader>

          <Input
            placeholder={t("searchModels")}
            value={search}
            onChange={(e) => setSearch(e.target.value)}
          />

          <div className="flex items-center gap-2 text-sm text-muted-foreground">
            <Checkbox
              checked={
                allFilteredModels.length > 0 &&
                allFilteredModels.every((m) => selected.has(m))
              }
              onCheckedChange={toggleAll}
            />
            <span>
              {t("selectAll")} ({allFilteredModels.length})
            </span>
          </div>

          <div className="max-h-[50vh] flex-1 space-y-1 overflow-y-auto">
            {filteredGroups.map((group) => {
              const key = groupKey(group);
              const isExpanded = isSearching || expanded.has(key);
              const allGroupSelected = group.models.every((m) =>
                selected.has(m)
              );

              return (
                <div key={key}>
                  <button
                    type="button"
                    className="flex w-full items-center gap-2 rounded-md px-2 py-1.5 text-sm hover:bg-accent"
                    onClick={() => toggleExpand(key)}
                  >
                    <ChevronRight
                      className={`size-4 shrink-0 transition-transform ${
                        isExpanded ? "rotate-90" : ""
                      }`}
                    />
                    <Checkbox
                      checked={allGroupSelected}
                      onCheckedChange={() => toggleGroup(group.models)}
                      onClick={(e) => e.stopPropagation()}
                    />
                    {group.provider && getProviderIconKey(group.provider) && (
                      <ProviderAvatar provider={getProviderIconKey(group.provider)!} size={18} />
                    )}
                    <span className="font-medium">{group.displayName}</span>
                    <span className="ml-auto text-xs text-muted-foreground">
                      {t("nModels", { count: group.models.length })}
                    </span>
                  </button>

                  {isExpanded && (
                    <div className="ml-6 space-y-0.5">
                      {group.models.map((model) => (
                        <label
                          key={model}
                          className="flex cursor-pointer items-center gap-2 rounded-md px-2 py-1 text-sm hover:bg-accent"
                        >
                          <Checkbox
                            checked={selected.has(model)}
                            onCheckedChange={() => toggleModel(model)}
                          />
                          <span className="font-mono text-xs">{model}</span>
                          {existingSet.has(model) && (
                            <Check className="size-3 text-muted-foreground" />
                          )}
                        </label>
                      ))}
                    </div>
                  )}
                </div>
              );
            })}
          </div>

          <DialogFooter>
            <Button variant="outline" onClick={() => setDialogOpen(false)}>
              {tc("cancel")}
            </Button>
            <Button onClick={handleConfirm}>
              {t("addSelectedModels")} ({selected.size})
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </>
  );
}
