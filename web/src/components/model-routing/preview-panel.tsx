"use client";

import { useEffect, useState } from "react";
import { UseFormReturn } from "react-hook-form";
import { useTranslations } from "next-intl";
import { RotateCw, Network, ChevronDown } from "lucide-react";
import { formatDistanceToNow } from "date-fns";
import { Card } from "@/components/ui/card";
import { Button } from "@/components/ui/button";
import { useDebounce } from "@/hooks/use-debounce";
import { usePreviewModelRouting } from "@/lib/api/model-routings";
import { PriorityCascade } from "./priority-layers";
import { RoutingFormValues } from "./routing-form/types";
import type { RoutingPreview } from "@/lib/types";

export interface PreviewPanelProps {
  form: UseFormReturn<RoutingFormValues>;
  apiMode?: "admin" | "user";
}

export function PreviewPanel({ form, apiMode = "admin" }: PreviewPanelProps) {
  const t = useTranslations("modelRoutings.preview");
  const previewMut = usePreviewModelRouting(apiMode);

  const watched = form.watch();
  const debounced = useDebounce(watched, 300);
  const [last, setLast] = useState<RoutingPreview | null>(null);
  const [lastAt, setLastAt] = useState<number | null>(null);
  const [refreshing, setRefreshing] = useState(false);
  const [errored, setErrored] = useState(false);

  const trigger = async () => {
    if (
      !debounced.members ||
      debounced.members.length === 0 ||
      debounced.members.some((m) => !m.ref)
    ) {
      setLast(null);
      return;
    }
    setRefreshing(true);
    try {
      const res = await previewMut.mutateAsync({
        members: debounced.members,
        self_name: debounced.name,
        self_scope: debounced.scope,
        self_user_id: debounced.user_id,
      });
      setLast(res);
      setLastAt(Date.now());
      setErrored(false);
    } catch {
      setErrored(true);
    } finally {
      setRefreshing(false);
    }
  };

  useEffect(
    () => {
      void trigger();
    },
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [JSON.stringify(debounced)]
  );

  const previewBody = (
    <>
      <div className="flex items-center justify-between mb-3">
        <h3 className="text-sm font-semibold hidden lg:block">{t("title")}</h3>
        <div className="flex items-center gap-2 ml-auto">
          {lastAt && (
            <span className="text-xs text-muted-foreground">
              {t("refreshAt", { ago: formatDistanceToNow(lastAt, { addSuffix: true }) })}
            </span>
          )}
          <Button
            type="button"
            variant="ghost"
            size="icon"
            onClick={trigger}
            aria-label={t("refresh")}
          >
            <RotateCw className="size-4" />
          </Button>
        </div>
      </div>
      {refreshing && <div className="h-0.5 bg-primary/40 animate-pulse mb-2" />}
      {errored && !refreshing && (
        <div className="h-0.5 bg-destructive mb-2" aria-label={t("fail")} />
      )}
      {!last ? (
        <div className="text-center py-8 text-muted-foreground">
          <Network className="size-12 mx-auto opacity-40 mb-2" />
          <p className="text-sm">{t("empty")}</p>
        </div>
      ) : (
        <PriorityCascade members={last.root.children ?? []} />
      )}
    </>
  );

  return (
    <Card className="p-4 lg:sticky lg:top-4 max-h-[calc(100vh-6rem)] lg:overflow-auto">
      {/* mobile: collapsible details/summary */}
      <details className="lg:hidden [&>summary::-webkit-details-marker]:hidden [&>summary::marker]:hidden">
        <summary className="text-sm font-semibold cursor-pointer flex items-center justify-between list-none select-none [&::-webkit-details-marker]:hidden">
          {t("title")}
          <ChevronDown className="size-4" />
        </summary>
        <div className="mt-3">{previewBody}</div>
      </details>
      {/* desktop: always visible */}
      <div className="hidden lg:block">{previewBody}</div>
    </Card>
  );
}
