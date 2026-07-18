"use client";

import { useCallback, useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { useRouter, useSearchParams } from "next/navigation";
import { useTranslations } from "next-intl";
import { toast } from "sonner";
import { ArrowLeft, RefreshCw, ChevronDown, ChevronRight, CheckCircle2, AlertTriangle } from "lucide-react";

import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import { Input } from "@/components/ui/input";
import { Badge } from "@/components/ui/badge";
import { ProviderAvatar } from "@/components/business/provider-avatar";
import {
  useFetchPricing, useApplyPricing,
  type FetchPricingResponse, type PricingRecommendation, type PricingValues, type PriceCandidate,
} from "@/lib/api/models";
import { getModelProvider } from "@/lib/constants";
import { formatPriceValue } from "@/lib/utils/format";
import { formatErrorToast } from "@/lib/api/error-toast";

// ---- 价格网格(对齐) ----
// 固定列宽 + tabular-nums 保证跨卡片对齐;In/Out 为主、CR/CW 为次(置灰、移动端隐藏)。
const PM_COL0 = "w-10 shrink-0"; // 价格网格首列(now/new 标签)宽度;表头占位 span 必须复用,保证跨卡对齐
const PM_LABEL = `${PM_COL0} text-2xs text-muted-foreground`;
const PM_VAL = "w-16 shrink-0 text-right tabular-nums text-xs";
const PM_HEAD = "w-16 shrink-0 text-right";

// 跌→绿↓、涨→红↑;无对比或未变→不上色。阈值沿用旧 hot() 的 1e-4。
function pmMark(a: number, b?: number): { cls: string; arrow: string } {
  if (b === undefined || Math.abs(a - b) < 1e-4) return { cls: "", arrow: "" };
  return a < b ? { cls: "text-green-600", arrow: "↓" } : { cls: "text-red-600", arrow: "↑" };
}

function PriceCells({ p, cmp }: { p: PricingValues; cmp?: PricingValues }) {
  const cell = (a: number, b: number | undefined, secondary: boolean) => {
    const m = pmMark(a, cmp ? b : undefined);
    const color = m.cls || (secondary ? "text-muted-foreground" : "");
    return (
      <span className={`${PM_VAL} ${secondary ? "hidden sm:block" : ""} ${color}`}>
        {formatPriceValue(a)}{m.arrow}
      </span>
    );
  };
  return (
    <>
      {cell(p.input_price, cmp?.input_price, false)}
      {cell(p.output_price, cmp?.output_price, false)}
      {cell(p.cache_read_price, cmp?.cache_read_price, true)}
      {cell(p.cache_write_price, cmp?.cache_write_price, true)}
    </>
  );
}

function EmptyCells() {
  return (
    <>
      <span className={PM_VAL}>—</span>
      <span className={PM_VAL}>—</span>
      <span className={`${PM_VAL} hidden sm:block`}>—</span>
      <span className={`${PM_VAL} hidden sm:block`}>—</span>
    </>
  );
}

// compare:now(current)+new(next) 两行;single:仅一行值(候选源,无对比)。
function PriceMatrix(
  props:
    | { mode: "single"; value: PricingValues }
    | { mode: "compare"; current: PricingValues | null; next: PricingValues },
) {
  if (props.mode === "single") {
    return (
      <div className="flex items-center gap-2">
        <span className={PM_LABEL} />
        <PriceCells p={props.value} />
      </div>
    );
  }
  return (
    <div className="space-y-0.5">
      <div className="flex items-center gap-2">
        <span className={PM_LABEL}>now</span>
        {props.current ? <PriceCells p={props.current} /> : <EmptyCells />}
      </div>
      <div className="flex items-center gap-2">
        <span className={PM_LABEL}>new</span>
        <PriceCells p={props.next} cmp={props.current ?? undefined} />
      </div>
    </div>
  );
}

// 每个区块顶部渲染一次;列宽与 PriceMatrix 一致 → 跨卡对齐。
// 表头列宽复用 PM_COL0/PM_HEAD,且 border + px-2.5 必须镜像卡片的 border + p-2.5,
// 否则表头与卡片价格列左缘错位。改卡片 padding 时同步改这里。
function PriceMatrixHeader() {
  return (
    <div className="border border-transparent px-2.5">
      <div className="flex items-center gap-2 text-2xs text-muted-foreground">
        <span className={PM_COL0} />
        <span className={PM_HEAD}>In</span>
        <span className={PM_HEAD}>Out</span>
        <span className={`${PM_HEAD} hidden sm:block`}>CR</span>
        <span className={`${PM_HEAD} hidden sm:block`}>CW</span>
        <span className="ml-1">$ / 1M</span>
      </div>
    </div>
  );
}

// 可见 ∩ 已选:批量与计数都只认「当前可见(经搜索/removed 过滤)且选中」的行,避免幽灵计数。
function pickVisibleSelected(review: PricingRecommendation[], selected: Set<number>): number[] {
  return review.filter((r) => selected.has(r.model_id)).map((r) => r.model_id);
}

export default function PricingSyncPage() {
  const t = useTranslations("models");
  const tc = useTranslations("common");
  const router = useRouter();
  const source = useSearchParams().get("source") ?? "";

  const fetchPricing = useFetchPricing();
  const fetchPricingAsync = fetchPricing.mutateAsync;
  const loadGeneration = useRef(0);
  const applyPricing = useApplyPricing();
  const [resp, setResp] = useState<FetchPricingResponse | null>(null);

  const [removed, setRemoved] = useState<Set<number>>(new Set());
  const [excluded, setExcluded] = useState<Set<number>>(new Set());
  const [overrides, setOverrides] = useState<Record<number, PriceCandidate>>({});
  const [appliedCount, setAppliedCount] = useState(0);
  const [expanded, setExpanded] = useState<Set<number>>(new Set());
  const [selected, setSelected] = useState<Set<number>>(new Set());
  const [search, setSearch] = useState("");
  const [trustedOpen, setTrustedOpen] = useState(false);
  const [noChangeOpen, setNoChangeOpen] = useState(false);
  const [unmatchedOpen, setUnmatchedOpen] = useState(false);

  const applyFetchedPricing = useCallback((result: FetchPricingResponse) => {
    setResp(result);
    setRemoved(new Set());
    setExcluded(new Set());
    setOverrides({});
    setAppliedCount(0);
    setExpanded(new Set());
    setSelected(new Set());
  }, []);

  const load = useCallback(async () => {
    const generation = ++loadGeneration.current;
    try {
      const r = await fetchPricingAsync(source ? { source } : undefined);
      if (generation !== loadGeneration.current) return;
      applyFetchedPricing(r);
    } catch (e) {
      if (generation === loadGeneration.current) toast.error(formatErrorToast(e, t("fetchFailed")));
    }
  }, [applyFetchedPricing, fetchPricingAsync, source, t]);

  useEffect(() => {
    const generation = ++loadGeneration.current;
    void fetchPricingAsync(source ? { source } : undefined).then((result) => {
      if (generation === loadGeneration.current) applyFetchedPricing(result);
    }).catch((error) => {
      if (generation === loadGeneration.current) toast.error(formatErrorToast(error, t("fetchFailed")));
    });
    return () => { loadGeneration.current += 1; };
  }, [applyFetchedPricing, fetchPricingAsync, source, t]);

  const matches = useMemo(() => resp?.matches ?? [], [resp]);

  const outstandingChanged = matches.filter((r) => r.has_change && !removed.has(r.model_id));
  const changedRemaining = outstandingChanged.length;
  const showSearch = changedRemaining > 5;
  const q = showSearch ? search.trim().toLowerCase() : "";
  const matchSearch = (r: PricingRecommendation) => !q || r.model_name.toLowerCase().includes(q);

  const review = outstandingChanged.filter((r) => r.confidence === "needs_review" && matchSearch(r));
  const visibleSelectedIds = pickVisibleSelected(review, selected);
  const selectedCount = visibleSelectedIds.length;
  const allSelected = review.length > 0 && selectedCount === review.length;
  const someSelected = selectedCount > 0 && !allSelected;
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
  const toggleSelect = (id: number, on: boolean) =>
    setSelected((prev) => { const s = new Set(prev); if (on) s.add(id); else s.delete(id); return s; });
  const toggleSelectAll = (on: boolean) => {
    const ids = review.map((r) => r.model_id);
    setSelected((prev) => {
      const s = new Set(prev);
      ids.forEach((id) => (on ? s.add(id) : s.delete(id)));
      return s;
    });
  };
  const clearSelection = () => setSelected(new Set());
  const applySelected = () => {
    const rows = review.filter((r) => selected.has(r.model_id));
    clearSelection();
    applyRows(rows);
  };
  const skipSelected = () => {
    const ids = pickVisibleSelected(review, selected);
    setRemoved((prev) => { const s = new Set(prev); ids.forEach((id) => s.add(id)); return s; });
    clearSelection();
  };

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
              <PriceMatrix mode="single" value={c.price} />
            </button>
          );
        })}
      </div>
    </div>
  );

  const ModelRow = ({
    r, reason, leading, trailing,
  }: {
    r: PricingRecommendation;
    reason?: string;
    leading?: ReactNode;
    trailing: ReactNode;
  }) => {
    const prov = getModelProvider(r.model_name);
    const open = expanded.has(r.model_id);
    return (
      <div className="rounded-lg border p-2.5 space-y-1.5">
        <div className="flex items-center gap-2">
          {leading && <div className="shrink-0">{leading}</div>}
          <button onClick={() => toggleExpand(r.model_id)} className="text-muted-foreground shrink-0">
            {open ? <ChevronDown className="size-3.5" /> : <ChevronRight className="size-3.5" />}
          </button>
          {prov && <ProviderAvatar provider={prov} />}
          <span className="font-mono text-xs truncate flex-1" title={r.model_name}>{r.model_name}</span>
          <div className="flex items-center gap-2 shrink-0">{trailing}</div>
        </div>
        <div className="overflow-x-auto">
          <PriceMatrix mode="compare" current={r.has_price ? r.current : null} next={priceFor(r)} />
        </div>
        {reason && <div className="text-2xs text-yellow-600">{reason}</div>}
        {open && <Candidates r={r} />}
      </div>
    );
  };

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
        <div className="flex items-center justify-center gap-2 rounded-lg border border-dashed py-12 text-center text-sm text-muted-foreground">
          <CheckCircle2 className="size-4 text-green-600" />
          {t("upToDate")}
        </div>
      )}

      {review.length > 0 && (
        <div className="space-y-1.5">
          <div className="text-sm font-medium flex items-center gap-1.5">
            <AlertTriangle className="size-3.5 text-yellow-600 shrink-0" /> {t("reviewTitle", { count: review.length })}
          </div>

          <div className="flex flex-wrap items-center gap-2 pl-0.5">
            <Checkbox
              checked={allSelected ? true : someSelected ? "indeterminate" : false}
              onCheckedChange={(v) => toggleSelectAll(v === true)}
              aria-label={t("selectAll")}
            />
            <span className="text-2xs text-muted-foreground">{t("selectAll")}</span>
            {selectedCount > 0 && (
              <>
                <span className="text-2xs text-muted-foreground">· {t("pickedCount", { count: selectedCount })}</span>
                <Button size="sm" className="h-7" disabled={applyPricing.isPending} onClick={applySelected}>{t("applySelected")}</Button>
                <Button size="sm" variant="ghost" className="h-7" onClick={skipSelected}>{t("skipSelected")}</Button>
                <Button size="sm" variant="ghost" className="h-7 text-muted-foreground" onClick={clearSelection}>{t("clearSelection")}</Button>
              </>
            )}
          </div>

          <PriceMatrixHeader />
          {review.map((r) => (
            <ModelRow
              key={r.model_id}
              r={r}
              reason={reasons(r)}
              leading={
                <Checkbox
                  checked={selected.has(r.model_id)}
                  onCheckedChange={(v) => toggleSelect(r.model_id, v === true)}
                  aria-label={r.model_name}
                />
              }
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
              <CheckCircle2 className="size-3.5 text-green-600 shrink-0" /> {t("trustedSummary", { count: trustedToApply.length })}
            </button>
            <Button size="sm" className="self-end sm:self-auto shrink-0" disabled={applyPricing.isPending || trustedToApply.length === 0} onClick={() => applyRows(trustedToApply)}>
              {t("applyTrusted", { count: trustedToApply.length })}
            </Button>
          </div>
          {trustedOpen && (
            <div className="border-t p-2 space-y-1.5">
              <PriceMatrixHeader />
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
