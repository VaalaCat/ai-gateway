"use client";

import { useMemo, useState } from "react";
import { useTranslations } from "next-intl";
import { useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";
import { AlertCircle, Copy, RotateCcw } from "lucide-react";

import { Input } from "@/components/ui/input";
import { Button } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/skeleton";
import { ExpandedModelsView } from "@/components/business/expanded-models-view";
import { useAvailableModels, AvailableModelsError } from "@/lib/api/available-models";
import { groupModelsByProvider } from "@/lib/constants";

interface TokenAvailableModelsProps {
  tokenKey: string;
}

const SEARCH_THRESHOLD = 20;

export function TokenAvailableModels({ tokenKey }: TokenAvailableModelsProps) {
  const t = useTranslations("tokenDetail");
  const queryClient = useQueryClient();
  const [query, setQuery] = useState("");
  const { data, isLoading, isError, error } = useAvailableModels(tokenKey);

  const handleRetry = () => {
    queryClient.invalidateQueries({ queryKey: ["available-models", tokenKey] });
  };

  const handleChipClick = async (name: string) => {
    if (typeof navigator === "undefined" || !navigator.clipboard) return;
    try {
      await navigator.clipboard.writeText(name);
      toast.success(t("copied", { name }));
    } catch {
      // silent fail OK for single chip
    }
  };

  const handleCopyAll = async () => {
    if (!filtered || filtered.length === 0) return;
    if (typeof navigator === "undefined" || !navigator.clipboard) return;
    try {
      await navigator.clipboard.writeText(filtered.join(", "));
      toast.success(t("copiedAll", { count: filtered.length }));
    } catch {
      toast.error(t("errorGeneric"));
    }
  };

  const filtered = useMemo(() => {
    if (!data) return [];
    if (!query) return data;
    const q = query.toLowerCase();
    return data.filter((name) => name.toLowerCase().includes(q));
  }, [data, query]);

  const groups = useMemo(() => groupModelsByProvider(filtered), [filtered]);
  const showSearch = (data?.length ?? 0) > SEARCH_THRESHOLD;

  if (isLoading) {
    return (
      <section className="space-y-3">
        <Skeleton className="h-5 w-32" />
        <div className="flex flex-wrap gap-1.5">
          {Array.from({ length: 6 }).map((_, i) => (
            <Skeleton key={i} className="h-5 w-20" />
          ))}
        </div>
      </section>
    );
  }

  if (isError) {
    const code = error instanceof AvailableModelsError ? error.code : "generic";
    const message = code === "unauthorized" ? t("errorUnauthorized") : t("errorGeneric");
    return (
      <section className="space-y-2">
        <h3 className="text-sm font-medium">{t("availableModels")}</h3>
        <div className="flex items-start gap-2 rounded-md border border-destructive/30 bg-destructive/5 p-3 text-sm">
          <AlertCircle className="mt-0.5 size-4 shrink-0 text-destructive" />
          <div className="flex-1">
            <p className="text-destructive">{message}</p>
            <Button
              variant="outline"
              size="sm"
              className="mt-2 h-7"
              onClick={handleRetry}
            >
              <RotateCcw className="mr-1 size-3" />
              {t("retry")}
            </Button>
          </div>
        </div>
      </section>
    );
  }

  if (!data || data.length === 0) {
    return (
      <section className="space-y-2">
        <h3 className="text-sm font-medium">{t("availableModelsCount", { count: 0 })}</h3>
        <div className="rounded-md border bg-muted/30 p-3 text-sm">
          <div className="flex items-start gap-2">
            <AlertCircle className="mt-0.5 size-4 shrink-0 text-amber-600" />
            <div className="flex-1">
              <p>{t("empty")}</p>
              <p className="mt-2 text-xs text-muted-foreground">{t("emptyHintTitle")}</p>
              <ul className="mt-1 list-disc list-inside text-xs text-muted-foreground space-y-0.5">
                <li>{t("emptyHintFilter")}</li>
                <li>{t("emptyHintChannels")}</li>
                <li>{t("emptyHintTokenStatus")}</li>
              </ul>
              <Button
                variant="outline"
                size="sm"
                className="mt-2 h-7"
                onClick={handleRetry}
              >
                <RotateCcw className="mr-1 size-3" />
                {t("retry")}
              </Button>
            </div>
          </div>
        </div>
      </section>
    );
  }

  return (
    <section className="space-y-3">
      <div className="flex items-center justify-between gap-2">
        <h3 className="text-sm font-medium">
          {t("availableModelsCount", { count: filtered.length })}
        </h3>
        <Button
          variant="ghost"
          size="sm"
          className="h-7 text-xs"
          onClick={handleCopyAll}
        >
          <Copy className="mr-1 size-3" />
          {t("copyAll")}
        </Button>
      </div>
      {showSearch && (
        <Input
          placeholder={t("searchModels")}
          value={query}
          onChange={(e) => setQuery(e.target.value)}
          className="h-8 text-sm"
          onClick={(e) => e.stopPropagation()}
        />
      )}
      <ExpandedModelsView
        groups={groups}
        totalCount={filtered.length}
        onChipClick={handleChipClick}
        hideHeader={true}
      />
    </section>
  );
}
