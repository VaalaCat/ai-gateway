"use client";

import { useEffect, useMemo, useState, type ReactNode } from "react";
import { useRouter, useSearchParams } from "next/navigation";
import { useTranslations } from "next-intl";
import { toast } from "sonner";
import { ArrowLeft, RefreshCw, ChevronDown, ChevronRight } from "lucide-react";

import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Badge } from "@/components/ui/badge";
import { ProviderAvatar } from "@/components/business/provider-avatar";
import {
  useFetchPricing, useApplyPricing,
  type FetchPricingResponse, type PricingRecommendation, type PricingValues, type PriceCandidate,
} from "@/lib/api/models";
import { getModelProvider } from "@/lib/constants";
import { formatPrice } from "@/lib/utils/format";
import { formatErrorToast } from "@/lib/api/error-toast";

// CR/CW 段默认仅桌面显示（hidden sm:inline）；展开区传 full 强制显示四段。
function PriceLine({ p, cmp, full }: { p: PricingValues; cmp?: PricingValues; full?: boolean }) {
  const hot = (a: number, b?: number) =>
    cmp && b !== undefined && Math.abs(a - b) >= 1e-4 ? "text-green-600 font-medium" : "";
  const showCache = p.cache_read_price > 0 || p.cache_write_price > 0;
  return (
    <span className="text-xs tabular-nums">
      <span className="text-muted-foreground">In</span>{" "}
      <span className={hot(p.input_price, cmp?.input_price)}>{formatPrice(p.input_price)}</span>
      <span className="text-muted-foreground mx-0.5">/</span>
      <span className="text-muted-foreground">Out</span>{" "}
      <span className={hot(p.output_price, cmp?.output_price)}>{formatPrice(p.output_price)}</span>
      {showCache && (
        <span className={full ? "" : "hidden sm:inline"}>
          <span className="text-muted-foreground mx-0.5">/</span>
          <span className="text-muted-foreground">CR</span> {formatPrice(p.cache_read_price)}
          <span className="text-muted-foreground mx-0.5">/</span>
          <span className="text-muted-foreground">CW</span> {formatPrice(p.cache_write_price)}
        </span>
      )}
    </span>
  );
}

export default function PricingSyncPage() {
  const t = useTranslations("models");
  const tc = useTranslations("common");
  const router = useRouter();
  const source = useSearchParams().get("source") ?? "";

  const fetchPricing = useFetchPricing();
  const applyPricing = useApplyPricing();
  const [resp, setResp] = useState<FetchPricingResponse | null>(null);

  const [removed, setRemoved] = useState<Set<number>>(new Set());
  const [excluded, setExcluded] = useState<Set<number>>(new Set());
  const [overrides, setOverrides] = useState<Record<number, PriceCandidate>>({});
  const [appliedCount, setAppliedCount] = useState(0);
  const [expanded, setExpanded] = useState<Set<number>>(new Set());
  const [search, setSearch] = useState("");
  const [trustedOpen, setTrustedOpen] = useState(false);
  const [noChangeOpen, setNoChangeOpen] = useState(false);
  const [unmatchedOpen, setUnmatchedOpen] = useState(false);

  const load = useMemo(() => async () => {
    try {
      const r = await fetchPricing.mutateAsync(source ? { source } : undefined);
      setResp(r);
      setRemoved(new Set());
      setExcluded(new Set());
      setOverrides({});
      setAppliedCount(0);
      setExpanded(new Set());
    } catch (e) { toast.error(formatErrorToast(e, t("fetchFailed"))); }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [source]);

  useEffect(() => { load(); }, [load]);

  const matches = useMemo(() => resp?.matches ?? [], [resp]);

  const outstandingChanged = matches.filter((r) => r.has_change && !removed.has(r.model_id));
  const changedRemaining = outstandingChanged.length;
  const showSearch = changedRemaining > 5;
  const q = showSearch ? search.trim().toLowerCase() : "";
  const matchSearch = (r: PricingRecommendation) => !q || r.model_name.toLowerCase().includes(q);

  const review = outstandingChanged.filter((r) => r.confidence === "needs_review" && matchSearch(r));
  const trusted = outstandingChanged.filter((r) => r.confidence === "high");
  const trustedVisible = trusted.filter((r) => !excluded.has(r.model_id) && matchSearch(r));
  const trustedToApply = trusted.filter((r) => !excluded.has(r.model_id));
  const noChange = matches.filter((r) => !r.has_change && matchSearch(r));
  const unmatched = (resp?.unmatched_models ?? []).filter((n) => !q || n.toLowerCase().includes(q));

  const priceFor = (r: PricingRecommendation): PricingValues => overrides[r.model_id]?.price ?? r.recommended;
  const toUpdate = (r: PricingRecommendation) => {
    const p = priceFor(r);
    return {
      model_id: r.model_id,
      input_price: p.input_price, output_price: p.output_price,
      cache_read_price: p.cache_read_price, cache_write_price: p.cache_write_price,
    };
  };

  const applyRows = async (rows: PricingRecommendation[]) => {
    if (rows.length === 0) return;
    try {
      const res = await applyPricing.mutateAsync({ updates: rows.map(toUpdate) });
      const n = res.updated ?? rows.length;
      setAppliedCount((c) => c + n);
      setRemoved((prev) => { const s = new Set(prev); rows.forEach((r) => s.add(r.model_id)); return s; });
      toast.success(t("pricingApplied", { count: n }));
    } catch (e) { toast.error(formatErrorToast(e, tc("error"))); }
  };

  const skip = (r: PricingRecommendation) => setRemoved((prev) => new Set(prev).add(r.model_id));
  const exclude = (id: number) => setExcluded((prev) => new Set(prev).add(id));
  const toggleExpand = (id: number) =>
    setExpanded((prev) => { const s = new Set(prev); if (s.has(id)) s.delete(id); else s.add(id); return s; });

  const reasons = (r: PricingRecommendation) => (r.review_reasons ?? []).map((k) => t(`reason_${k}` as never)).join("、");

  const Candidates = ({ r }: { r: PricingRecommendation }) => (
    <div className="mt-1.5 overflow-x-auto">
      <div className="flex flex-wrap gap-1.5">
        <span className="w-full text-2xs text-muted-foreground">{t("candidatesTitle")}</span>
        {r.candidates.map((c, i) => {
          const chosen = overrides[r.model_id];
          const isChosen = chosen
            ? chosen.source === c.source && chosen.provider === c.provider
            : Math.abs(c.price.input_price - r.recommended.input_price) < 1e-4 &&
              Math.abs(c.price.output_price - r.recommended.output_price) < 1e-4;
          return (
            <button
              key={`${c.source}-${c.provider}-${i}`}
              onClick={() => setOverrides((p) => ({ ...p, [r.model_id]: c }))}
              className={`text-2xs rounded border px-2 py-1 text-left ${isChosen ? "border-primary bg-primary/5" : "border-input hover:bg-accent/50"}`}
            >
              <div className="font-medium">{c.source}{c.provider ? ` (${c.provider})` : ""}{c.match_type === "fuzzy" ? ` · ${c.matched_name}` : ""}</div>
              <PriceLine p={c.price} full />
            </button>
          );
        })}
      </div>
    </div>
  );

  const RowHead = ({ r }: { r: PricingRecommendation }) => {
    const prov = getModelProvider(r.model_name);
    const open = expanded.has(r.model_id);
    return (
      <div className="min-w-0">
        <div className="flex items-center gap-1.5">
          <button onClick={() => toggleExpand(r.model_id)} className="text-muted-foreground shrink-0">
            {open ? <ChevronDown className="size-3.5" /> : <ChevronRight className="size-3.5" />}
          </button>
          {prov && <ProviderAvatar provider={prov} />}
          <span className="font-mono text-xs truncate" title={r.model_name}>{r.model_name}</span>
        </div>
        <div className="mt-0.5 flex flex-wrap items-center gap-1 pl-5 text-xs text-muted-foreground">
          {r.has_price ? <PriceLine p={r.current} /> : <span>—</span>}
          <span className="mx-0.5">→</span>
          <PriceLine p={priceFor(r)} cmp={r.current} />
        </div>
      </div>
    );
  };

  const ModelRow = ({ r, reason, trailing }: { r: PricingRecommendation; reason?: string; trailing: ReactNode }) => (
    <div className="rounded-lg border p-2.5">
      <div className="flex flex-col gap-2 sm:flex-row sm:items-center sm:justify-between sm:gap-3">
        <div className="min-w-0 flex-1 space-y-1">
          <RowHead r={r} />
          {reason && <div className="pl-5 text-2xs text-yellow-600">{reason}</div>}
        </div>
        <div className="flex items-center gap-2 self-end sm:self-auto shrink-0">{trailing}</div>
      </div>
      {expanded.has(r.model_id) && <Candidates r={r} />}
    </div>
  );

  return (
    <div className="space-y-4 max-w-4xl">
      <div className="space-y-2">
        <div className="flex items-center gap-2">
          <Button variant="ghost" size="icon" className="size-8 shrink-0" onClick={() => router.push("/models")}>
            <ArrowLeft className="size-4" />
          </Button>
          <h1 className="flex-1 truncate text-xl font-bold sm:text-2xl">{t("pricingSyncTitle")}</h1>
          <Button variant="outline" size="sm" className="shrink-0" onClick={() => load()} disabled={fetchPricing.isPending}>
            <RefreshCw className={`size-3.5 sm:mr-1.5 ${fetchPricing.isPending ? "animate-spin" : ""}`} />
            <span className="hidden sm:inline">{t("refetch")}</span>
          </Button>
        </div>
        <p className="text-muted-foreground text-sm">
          {t("changedCount", { count: changedRemaining })}
          {appliedCount > 0 && <span className="ml-2 text-green-600">· {t("appliedSummary", { count: appliedCount })}</span>}
        </p>
        {showSearch && (
          <Input value={search} onChange={(e) => setSearch(e.target.value)} placeholder={tc("search")} className="h-9 w-full sm:w-60" />
        )}
      </div>

      {resp?.source_errors && Object.entries(resp.source_errors).map(([s, e]) => (
        <p key={s} className="text-xs text-destructive">{s}: {e}</p>
      ))}

      {!fetchPricing.isPending && changedRemaining === 0 && (
        <div className="rounded-lg border border-dashed py-12 text-center text-sm text-muted-foreground">
          ✅ {t("upToDate")}
        </div>
      )}

      {review.length > 0 && (
        <div className="space-y-1.5">
          <div className="text-sm font-medium flex items-center gap-1.5">
            <span className="text-yellow-600">⚠</span> {t("reviewTitle", { count: review.length })}
          </div>
          {review.map((r) => (
            <ModelRow
              key={r.model_id}
              r={r}
              reason={reasons(r)}
              trailing={
                <>
                  <Button size="sm" variant="outline" disabled={applyPricing.isPending} onClick={() => applyRows([r])}>{t("accept")}</Button>
                  <Button size="sm" variant="ghost" onClick={() => skip(r)}>{t("skipOne")}</Button>
                </>
              }
            />
          ))}
        </div>
      )}

      {trusted.length > 0 && (
        <div className="rounded-lg border">
          <div className="flex flex-col gap-2 p-2.5 sm:flex-row sm:items-center sm:justify-between">
            <button onClick={() => setTrustedOpen((v) => !v)} className="flex items-center gap-1.5 text-sm text-left">
              {trustedOpen ? <ChevronDown className="size-4 shrink-0" /> : <ChevronRight className="size-4 shrink-0" />}
              <span className="text-green-600">✅</span> {t("trustedSummary", { count: trustedToApply.length })}
            </button>
            <Button size="sm" className="self-end sm:self-auto shrink-0" disabled={applyPricing.isPending || trustedToApply.length === 0} onClick={() => applyRows(trustedToApply)}>
              {t("applyTrusted", { count: trustedToApply.length })}
            </Button>
          </div>
          {trustedOpen && (
            <div className="border-t p-2 space-y-1.5">
              {trustedVisible.map((r) => (
                <ModelRow
                  key={r.model_id}
                  r={r}
                  trailing={<Button size="sm" variant="ghost" className="text-muted-foreground" onClick={() => exclude(r.model_id)}>{t("exclude")}</Button>}
                />
              ))}
            </div>
          )}
        </div>
      )}

      <div className="space-y-1.5 pt-2">
        {noChange.length > 0 && (
          <div>
            <button className="flex items-center gap-1 text-xs text-muted-foreground hover:text-foreground" onClick={() => setNoChangeOpen((v) => !v)}>
              {noChangeOpen ? <ChevronDown className="size-3.5" /> : <ChevronRight className="size-3.5" />}
              {t("noChangeGroup")} ({noChange.length})
            </button>
            {noChangeOpen && (
              <div className="mt-1 pl-5 flex flex-wrap gap-1">
                {noChange.map((r) => <Badge key={r.model_id} variant="outline" className="text-2xs font-mono">{r.model_name}</Badge>)}
              </div>
            )}
          </div>
        )}
        {unmatched.length > 0 && (
          <div>
            <button className="flex items-center gap-1 text-xs text-muted-foreground hover:text-foreground" onClick={() => setUnmatchedOpen((v) => !v)}>
              {unmatchedOpen ? <ChevronDown className="size-3.5" /> : <ChevronRight className="size-3.5" />}
              {t("unmatchedModels")} ({unmatched.length})
            </button>
            {unmatchedOpen && (
              <div className="mt-1 pl-5 flex flex-wrap gap-1">
                {unmatched.map((n) => <Badge key={n} variant="outline" className="text-2xs font-mono">{n}</Badge>)}
              </div>
            )}
          </div>
        )}
      </div>
    </div>
  );
}
