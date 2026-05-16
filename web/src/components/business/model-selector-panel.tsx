"use client";

import { useState, useMemo } from "react";
import { ChevronRight, Search, Plus } from "lucide-react";
import { useTranslations } from "next-intl";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { groupModelsByProvider, getProviderIconKey } from "@/lib/constants";
import { ProviderAvatar } from "@/components/business/provider-avatar";

interface ModelSelectorPanelProps {
  value: string[];
  onChange: (models: string[]) => void;
}

export function ModelSelectorPanel({ value, onChange }: ModelSelectorPanelProps) {
  const t = useTranslations("channels");
  const tc = useTranslations("common");
  const [search, setSearch] = useState("");
  const [expanded, setExpanded] = useState<Set<string>>(new Set());
  const [manualInput, setManualInput] = useState("");

  const selected = useMemo(() => new Set(value), [value]);

  const groups = useMemo(() => groupModelsByProvider(value), [value]);

  const filteredGroups = useMemo(() => {
    if (!search.trim()) return groups;
    const q = search.toLowerCase();
    return groups
      .map((g) => ({ ...g, models: g.models.filter((m) => m.toLowerCase().includes(q)) }))
      .filter((g) => g.models.length > 0);
  }, [groups, search]);

  const toggleExpand = (key: string) => {
    const next = new Set(expanded);
    if (next.has(key)) next.delete(key);
    else next.add(key);
    setExpanded(next);
  };

  const expandAll = () => setExpanded(new Set(filteredGroups.map(g => g.provider ?? "_other")));
  const collapseAll = () => setExpanded(new Set());

  const removeModel = (model: string) => {
    onChange(value.filter((m) => m !== model));
  };

  const removeGroup = (models: string[]) => {
    const toRemove = new Set(models);
    onChange(value.filter((m) => !toRemove.has(m)));
  };

  const handleManualAdd = () => {
    const newModels = manualInput
      .split(",")
      .map((s) => s.trim())
      .filter((s) => s && !selected.has(s));
    if (newModels.length > 0) {
      onChange([...value, ...newModels]);
    }
    setManualInput("");
  };

  const handleManualKeyDown = (e: React.KeyboardEvent) => {
    if (e.key === "Enter") {
      e.preventDefault();
      handleManualAdd();
    }
  };

  const isSearching = search.trim().length > 0;

  return (
    <div className="space-y-3">
      <div className="flex items-center gap-2">
        <div className="relative flex-1">
          <Search className="absolute left-2.5 top-1/2 size-4 -translate-y-1/2 text-muted-foreground" />
          <Input
            placeholder={t("searchModels")}
            value={search}
            onChange={(e) => setSearch(e.target.value)}
            className="pl-8"
          />
        </div>
        <span className="text-sm text-muted-foreground whitespace-nowrap">
          {t("nSelected", { count: value.length })}
        </span>
      </div>

      <div className="flex gap-1 justify-end">
        <Button variant="ghost" size="sm" className="h-6 text-xs" onClick={expandAll}>
          {t("expandAll")}
        </Button>
        <Button variant="ghost" size="sm" className="h-6 text-xs" onClick={collapseAll}>
          {t("collapseAll")}
        </Button>
      </div>

      <div className="max-h-[300px] overflow-y-auto space-y-1 rounded-md border p-2">
        {filteredGroups.length === 0 ? (
          <p className="text-sm text-muted-foreground text-center py-4">{tc("noData")}</p>
        ) : (
          filteredGroups.map((group) => {
            const key = group.provider ?? "_other";
            const isExpanded = isSearching || expanded.has(key);

            return (
              <div key={key}>
                <div className="flex items-center gap-2 rounded-md px-2 py-1 hover:bg-accent">
                  <button
                    type="button"
                    className="flex flex-1 items-center gap-2 text-sm"
                    onClick={() => toggleExpand(key)}
                  >
                    <ChevronRight className={`size-3.5 shrink-0 transition-transform ${isExpanded ? "rotate-90" : ""}`} />
                    {group.provider && getProviderIconKey(group.provider) && <ProviderAvatar provider={getProviderIconKey(group.provider)!} size={16} />}
                    <span className="font-medium">{group.displayName}</span>
                    <span className="text-xs text-muted-foreground">({group.models.length})</span>
                  </button>
                  <Button
                    type="button"
                    variant="ghost"
                    size="sm"
                    className="h-5 text-xs text-destructive hover:text-destructive"
                    onClick={() => removeGroup(group.models)}
                  >
                    {tc("delete")}
                  </Button>
                </div>
                {isExpanded && (
                  <div className="ml-6 flex flex-wrap gap-1 py-1">
                    {group.models.map((m) => (
                      <Badge
                        key={m}
                        variant="secondary"
                        className="text-xs font-mono px-1.5 py-0 cursor-pointer hover:line-through"
                        onClick={() => removeModel(m)}
                        title={tc("delete")}
                      >
                        {m} ×
                      </Badge>
                    ))}
                  </div>
                )}
              </div>
            );
          })
        )}
      </div>

      <div className="flex gap-2">
        <Input
          value={manualInput}
          onChange={(e) => setManualInput(e.target.value)}
          onKeyDown={handleManualKeyDown}
          placeholder={t("manualAddPlaceholder")}
          className="flex-1"
        />
        <Button
          type="button"
          variant="outline"
          size="sm"
          onClick={handleManualAdd}
          disabled={!manualInput.trim()}
        >
          <Plus className="mr-1 size-4" />
          {t("addModels")}
        </Button>
      </div>
    </div>
  );
}
