"use client";

import { useTranslations } from "next-intl";
import { toast } from "sonner";

import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetFooter,
  SheetHeader,
  SheetTitle,
} from "@/components/ui/sheet";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { CopyableText } from "@/components/business/copyable-text";
import { EntityLabel } from "@/components/business/entity-label";
import { RateLimitSection } from "@/components/business/rate-limit-section";
import { FallbackChain } from "@/components/business/fallback-chain";
import { useInterruptInflight } from "@/lib/api/agents";
import { formatErrorToast } from "@/lib/api/error-toast";
import type { GlobalInflightRow } from "@/lib/types";

function Field({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div className="flex items-start justify-between gap-4 py-1.5 text-sm">
      <span className="text-muted-foreground shrink-0">{label}</span>
      <span className="text-right break-all">{children}</span>
    </div>
  );
}

export function InflightBlockDetail({
  row,
  onClose,
}: {
  row: GlobalInflightRow | null;
  onClose: () => void;
}) {
  const t = useTranslations("observability");
  const tc = useTranslations("common");
  const interrupt = useInterruptInflight();

  const handleInterrupt = () => {
    if (!row) return;
    interrupt.mutate(
      { agent_id: row.agent_id, id: row.id },
      {
        onSuccess: () => {
          toast.success(t("interrupted"));
          onClose();
        },
        onError: (e) => toast.error(formatErrorToast(e, tc("error"))),
      },
    );
  };

  const view = row?.view;

  return (
    <Sheet open={row !== null} onOpenChange={(o) => { if (!o) onClose(); }}>
      <SheetContent className="w-full gap-0 sm:max-w-md overflow-y-auto">
        <SheetHeader>
          <SheetTitle>{t("detailTitle")}</SheetTitle>
          <SheetDescription className="font-mono text-xs">
            {row ? <CopyableText text={row.req_id} /> : null}
          </SheetDescription>
        </SheetHeader>

        {row && view && (
          <div className="flex flex-col px-4 pb-4 gap-3">
            <div className="divide-y">
              <Field label={t("detailUser")}>
                <EntityLabel entity="user" id={view.user_id} />
              </Field>
              <Field label={t("detailChannel")}>
                <EntityLabel entity="channel" id={view.channel_id} />
              </Field>
              <Field label={t("detailModel")}>{view.model_name}</Field>
              {view.upstream_model && (
                <Field label={t("detailUpstreamModel")}>{view.upstream_model}</Field>
              )}
              <Field label={t("detailInbound")}>{view.inbound_protocol ?? "—"}</Field>
              <Field label={t("detailOutbound")}>{view.outbound_protocol ?? "—"}</Field>
              <Field label={t("detailStream")}>
                {view.is_stream ? (
                  <Badge variant="secondary" className="text-xs px-1.5 py-0">{t("detailStreamYes")}</Badge>
                ) : (
                  <span className="text-muted-foreground">{t("detailStreamNo")}</span>
                )}
              </Field>
              <Field label={t("detailAgent")}>{row.agent_name}</Field>
              <Field label={t("stage")}>{row.stage}</Field>
              <Field label={t("detailElapsed")}>{(row.elapsed_ms / 1000).toFixed(1)}s</Field>
              {row.queued_ms > 0 && (
                <>
                  <Field label={t("detailQueued")}>
                    <span className="text-amber-600">{t("queuedFor", { s: (row.queued_ms / 1000).toFixed(1) })}</span>
                  </Field>
                  {row.queued_reason && (
                    <Field label={t("detailQueuedReason")}>{row.queued_reason}</Field>
                  )}
                </>
              )}
              {typeof view.prompt_tokens === "number" && view.prompt_tokens > 0 && (
                <Field label={t("detailPromptTokens")}>{view.prompt_tokens}</Field>
              )}
              {typeof view.completion_tokens === "number" && view.completion_tokens > 0 && (
                <Field label={t("detailCompletionTokens")}>{view.completion_tokens}</Field>
              )}
              {((view.cache_read_tokens ?? 0) > 0 || (view.cache_write_tokens ?? 0) > 0) && (
                <Field label={t("detailCacheTokens")}>
                  {(view.cache_read_tokens ?? 0)} / {(view.cache_write_tokens ?? 0)}
                </Field>
              )}
              {typeof view.first_response_ms === "number" && view.first_response_ms > 0 && (
                <Field label={t("detailFirstResponse")}>{view.first_response_ms}ms</Field>
              )}
              {view.routing_name && (
                <Field label={t("detailRouting")}>{view.routing_name}</Field>
              )}
              {view.client_ip && (
                <Field label={t("detailClientIp")}>
                  <span className="font-mono text-xs">{view.client_ip}</span>
                </Field>
              )}
            </div>

            {view.rate_limit_decision && (
              <RateLimitSection
                decision={view.rate_limit_decision}
                waitMs={view.rate_limit_wait_ms}
                reason={view.rate_limit_reason}
                hits={view.rate_limit_hits}
              />
            )}

            {view.fallback_chain && view.fallback_chain.length > 0 && (
              <FallbackChain chain={view.fallback_chain} requestId={row.req_id} />
            )}
          </div>
        )}

        <SheetFooter>
          <Button
            variant="destructive"
            onClick={handleInterrupt}
            disabled={interrupt.isPending}
          >
            {t("interrupt")}
          </Button>
        </SheetFooter>
      </SheetContent>
    </Sheet>
  );
}
