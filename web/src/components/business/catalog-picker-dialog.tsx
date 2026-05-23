"use client";

import { useMemo, useState } from "react";
import { useTranslations } from "next-intl";
import { ChevronRight, Check } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Checkbox } from "@/components/ui/checkbox";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
  DialogTrigger,
} from "@/components/ui/dialog";
import { groupModelsByProvider, getProviderIconKey } from "@/lib/constants";
import { ProviderAvatar } from "@/components/business/provider-avatar";

export interface CatalogPickerDialogProps {
  available: string[];
  alreadySelected: string[];
  triggerLabel?: string;
  disabled?: boolean;
  onConfirm: (added: string[]) => void;
}

export function CatalogPickerDialog({
  available,
  alreadySelected,
  triggerLabel,
  disabled,
  onConfirm,
}: CatalogPickerDialogProps) {
  const t = useTranslations();
  const tc = useTranslations("channels");
  const [open, setOpen] = useState(false);
  const [search, setSearch] = useState("");
  const [picked, setPicked] = useState<Set<string>>(new Set());
  const [expanded, setExpanded] = useState<Set<string>>(new Set());

  const alreadySet = useMemo(() => new Set(alreadySelected), [alreadySelected]);

  // Open: prime picked with alreadySelected so they show as checked. Close: state reset
  // happens on the next open (no need to clear immediately).
  const handleOpenChange = (next: boolean) => {
    if (next) {
      setPicked(new Set(alreadySelected));
      setSearch("");
      setExpanded(new Set());
    }
    setOpen(next);
  };

  const groups = useMemo(() => groupModelsByProvider(available), [available]);
  const filteredGroups = useMemo(() => {
    if (!search.trim()) return groups;
    const q = search.toLowerCase();
    return groups
      .map((g) => ({ ...g, models: g.models.filter((m) => m.toLowerCase().includes(q)) }))
      .filter((g) => g.models.length > 0);
  }, [groups, search]);

  const allFilteredModels = useMemo(
    () => filteredGroups.flatMap((g) => g.models),
    [filteredGroups],
  );

  const toggle = (m: string) => {
    setPicked((prev) => {
      const next = new Set(prev);
      if (next.has(m)) next.delete(m);
      else next.add(m);
      return next;
    });
  };

  // alreadySelected items can never be removed from `picked` via this dialog —
  // this dialog is "Add only"; removal happens in ModelSelectorPanel via Badge ×.
  const removableFrom = (set: Set<string>, models: string[]) => {
    models.forEach((m) => {
      if (!alreadySet.has(m)) set.delete(m);
    });
  };

  const toggleGroup = (models: string[]) => {
    setPicked((prev) => {
      const allIn = models.every((m) => prev.has(m));
      const next = new Set(prev);
      if (allIn) removableFrom(next, models);
      else models.forEach((m) => next.add(m));
      return next;
    });
  };

  const toggleAll = () => {
    setPicked((prev) => {
      const allIn =
        allFilteredModels.length > 0 && allFilteredModels.every((m) => prev.has(m));
      const next = new Set(prev);
      if (allIn) removableFrom(next, allFilteredModels);
      else allFilteredModels.forEach((m) => next.add(m));
      return next;
    });
  };

  const toggleExpand = (key: string) => {
    setExpanded((prev) => {
      const next = new Set(prev);
      if (next.has(key)) next.delete(key);
      else next.add(key);
      return next;
    });
  };

  // Final increment = picked ∖ alreadySelected
  const added = useMemo(
    () => Array.from(picked).filter((m) => !alreadySet.has(m)),
    [picked, alreadySet],
  );

  const handleConfirm = () => {
    onConfirm(added);
    setOpen(false);
  };

  const isSearching = search.trim().length > 0;
  const allSelectedInFilter =
    allFilteredModels.length > 0 && allFilteredModels.every((m) => picked.has(m));

  return (
    <Dialog open={open} onOpenChange={handleOpenChange}>
      <DialogTrigger asChild>
        <Button type="button" variant="outline" size="sm" disabled={disabled}>
          {triggerLabel ?? t("byok.catalog.browseButton")}
        </Button>
      </DialogTrigger>
      <DialogContent className="flex max-h-[85vh] flex-col sm:max-w-lg">
        <DialogHeader>
          <DialogTitle>
            {t("byok.catalog.dialogTitle")}{" "}
            <span className="text-sm font-normal text-muted-foreground">
              {tc("nSelected", { count: picked.size })}
            </span>
          </DialogTitle>
          {available.length === 0 && (
            <DialogDescription>{t("byok.catalog.empty")}</DialogDescription>
          )}
        </DialogHeader>

        {available.length > 0 && (
          <>
            <Input
              placeholder={tc("searchModels")}
              value={search}
              onChange={(e) => setSearch(e.target.value)}
            />

            <div className="flex items-center gap-2 text-sm text-muted-foreground">
              <Checkbox
                checked={allSelectedInFilter}
                onCheckedChange={toggleAll}
              />
              <span>
                {tc("selectAll")} ({allFilteredModels.length})
              </span>
            </div>

            <div className="max-h-[50vh] flex-1 space-y-1 overflow-y-auto">
              {filteredGroups.map((group) => {
                const key = group.provider ?? "_other";
                const isExpanded = isSearching || expanded.has(key);
                const allGroupSelected = group.models.every((m) => picked.has(m));
                const iconKey = group.provider ? getProviderIconKey(group.provider) : null;

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
                      {iconKey && <ProviderAvatar provider={iconKey} size={18} />}
                      <span className="font-medium">{group.displayName}</span>
                      <span className="ml-auto text-xs text-muted-foreground">
                        {tc("nModels", { count: group.models.length })}
                      </span>
                    </button>

                    {isExpanded && (
                      <div className="ml-6 space-y-0.5">
                        {group.models.map((m) => (
                          <label
                            key={m}
                            className="flex cursor-pointer items-center gap-2 rounded-md px-2 py-1 text-sm hover:bg-accent"
                          >
                            <Checkbox
                              checked={picked.has(m)}
                              onCheckedChange={() => toggle(m)}
                            />
                            <span className="font-mono text-xs">{m}</span>
                            {alreadySet.has(m) && (
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
          </>
        )}

        <DialogFooter>
          <Button type="button" variant="outline" onClick={() => setOpen(false)}>
            {t("common.cancel")}
          </Button>
          <Button
            type="button"
            onClick={handleConfirm}
            disabled={available.length === 0 || added.length === 0}
          >
            {tc("addModelsCount", { count: added.length })}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
