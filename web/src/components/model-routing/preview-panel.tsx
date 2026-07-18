"use client";

import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { UseFormReturn } from "react-hook-form";
import { useTranslations } from "next-intl";
import { RotateCw, Network, ChevronDown } from "lucide-react";
import { formatDistanceToNow } from "date-fns";
import { Card } from "@/components/ui/card";
import { Button } from "@/components/ui/button";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { useDebounce } from "@/hooks/use-debounce";
import { usePreviewModelRouting } from "@/lib/api/model-routings";
import type { ModelRoutingOwner } from "@/lib/api/model-routings";
import { PriorityCascade } from "./priority-layers";
import { RoutingFormValues } from "./routing-form/types";
import type { RoutingPreview } from "@/lib/types";

export interface PreviewPanelProps {
  form: UseFormReturn<RoutingFormValues>;
  apiMode?: "admin" | "user";
  owner?: ModelRoutingOwner;
  allowedRefs?: string[];
  checkAlias?: boolean;
}

export function PreviewPanel({
  form,
  apiMode = "admin",
  owner = { kind: "scope" },
  allowedRefs,
  checkAlias = false,
}: PreviewPanelProps) {
  const t = useTranslations("modelRoutings.preview");
  const previewMutation = usePreviewModelRouting(apiMode, owner);
  const previewAsync = previewMutation.mutateAsync;

  const watched = form.watch();
  const debounced = useDebounce(watched, 300);
  const [errored, setErrored] = useState(false);
  const requestGeneration = useRef(0);
  const previewEnabled = Boolean(
    debounced.members?.length && debounced.members.every((member) => member.ref),
  );
  const previewRequest = useMemo(() => ({
    members: debounced.members ?? [],
    self_name: debounced.name,
    self_scope: debounced.scope,
    self_user_id: debounced.user_id,
  }), [debounced.members, debounced.name, debounced.scope, debounced.user_id]);
  const [last, setLast] = useState<{
    request: typeof previewRequest;
    result: RoutingPreview;
    at: number;
  } | null>(null);
  const [manualPending, setManualPending] = useState<{
    generation: number;
    request: typeof previewRequest;
  } | null>(null);
  const currentPreview =
    previewEnabled && last?.request === previewRequest ? last : null;
  const refreshing = Boolean(
    previewEnabled &&
    manualPending?.request === previewRequest,
  );
  const whitelistWarning = useMemo(() => {
    if (!currentPreview || allowedRefs === undefined) return null;
    const visible = new Set(allowedRefs);
    const leaves: string[] = [];
    const visit = (nodes: RoutingPreview["root"]["children"] = []) => {
      for (const node of nodes) {
        if (node.kind === "model") leaves.push(node.ref);
        if (node.children) visit(node.children);
      }
    };
    visit(currentPreview.result.root.children);
    const uniqueLeaves = [...new Set(leaves)];
    const blocked = uniqueLeaves.filter((leaf) => !visible.has(leaf));
    const aliasBlocked =
      checkAlias && debounced.enabled && !!debounced.name && !visible.has(debounced.name);
    if (!aliasBlocked && blocked.length === 0) return null;
    return {
      aliasBlocked,
      blocked,
      allLeavesBlocked: uniqueLeaves.length > 0 && blocked.length === uniqueLeaves.length,
    };
  }, [allowedRefs, checkAlias, currentPreview, debounced.enabled, debounced.name]);
  const refresh = useCallback(async () => {
    if (!previewEnabled) return;
    const generation = ++requestGeneration.current;
    setManualPending({ generation, request: previewRequest });
    try {
      const result = await previewAsync(previewRequest);
      if (generation !== requestGeneration.current) return;
      setLast({ request: previewRequest, result, at: Date.now() });
      setErrored(false);
    } catch {
      if (generation === requestGeneration.current) setErrored(true);
    } finally {
      setManualPending((current) =>
        current?.generation === generation ? null : current,
      );
    }
  }, [previewAsync, previewEnabled, previewRequest]);

  useEffect(() => {
    if (!previewEnabled) return;
    const generation = ++requestGeneration.current;
    void previewAsync(previewRequest).then((result) => {
      if (generation !== requestGeneration.current) return;
      setLast({ request: previewRequest, result, at: Date.now() });
      setErrored(false);
    }).catch(() => {
      if (generation === requestGeneration.current) setErrored(true);
    });
    return () => { requestGeneration.current += 1; };
  }, [previewAsync, previewEnabled, previewRequest]);

  const previewBody = (
    <>
      <div className="flex items-center justify-between mb-3">
        <h3 className="text-sm font-semibold hidden lg:block">{t("title")}</h3>
        <div className="flex items-center gap-2 ml-auto">
          {currentPreview && (
            <span className="text-xs text-muted-foreground">
              {t("refreshAt", { ago: formatDistanceToNow(currentPreview.at, { addSuffix: true }) })}
            </span>
          )}
          <Button
            type="button"
            variant="ghost"
            size="icon"
            disabled={!previewEnabled}
            onClick={() => { void refresh(); }}
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
      {whitelistWarning && (
        <Alert className="mb-3">
          <AlertTitle>{t("whitelistWarning")}</AlertTitle>
          <AlertDescription>
            {whitelistWarning.allLeavesBlocked
              ? t("whitelistAllLeavesBlocked")
              : whitelistWarning.blocked.length > 0
                ? t("whitelistLeavesBlocked", { count: whitelistWarning.blocked.length })
                : t("whitelistAliasBlocked")}
          </AlertDescription>
        </Alert>
      )}
      {!currentPreview ? (
        <div className="text-center py-8 text-muted-foreground">
          <Network className="size-12 mx-auto opacity-40 mb-2" />
          <p className="text-sm">{t("empty")}</p>
        </div>
      ) : (
        <PriorityCascade members={currentPreview.result.root.children ?? []} />
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
