"use client";

import { useState, useMemo, useEffect, useCallback } from "react";
import { useTranslations } from "next-intl";
import { ChevronDown, ChevronRight } from "lucide-react";

import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogFooter,
} from "@/components/ui/dialog";
import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";
import {
  Tooltip,
  TooltipContent,
  TooltipProvider,
  TooltipTrigger,
} from "@/components/ui/tooltip";

import { useIsMobile } from "@/hooks/use-mobile";
import type { FetchPricingResponse, PricingMatch } from "@/lib/api/models";

// --- Types ---

interface PricingUpdate {
  model_id: number;
  input_price: number;
  output_price: number;
  cache_read_price: number;
  cache_write_price: number;
}

interface PricingPreviewDialogProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  data: FetchPricingResponse;
  onApply: (updates: PricingUpdate[]) => void;
  isApplying: boolean;
}

type FilterKey = "all" | "all_incl_zero" | "changed" | "price_up" | "price_down" | "fuzzy" | "no_price";
type Selection = string; // source key or "skip"

// --- Helpers ---

function fmt(price: number | null | undefined): string {
  if (price == null) return "-";
  if (price === 0) return "$0";
  if (price < 0.01) return `$${price}`;
  return `$${Number(price.toFixed(4))}`;
}

function getSourceKeys(data: FetchPricingResponse): string[] {
  const keys = new Set<string>();
  for (const m of data.matches ?? []) {
    for (const k of Object.keys(m.sources)) keys.add(k);
  }
  return Array.from(keys);
}

function sourceLabel(key: string): string {
  if (key === "basellm") return "basellm";
  if (key === "models.dev" || key === "models_dev") return "models.dev";
  return key;
}

// --- Compact price display: "In $2.50 / Out $10 / CR $0.25 / CW -" ---

function PriceSummary({ input, output, cr, cw }: {
  input: number; output: number; cr: number | null; cw: number | null;
}) {
  const hasCR = cr != null && cr > 0;
  const hasCW = cw != null && cw > 0;
  return (
    <span className="text-xs tabular-nums">
      <span className="text-muted-foreground">In</span> {fmt(input)}
      <span className="text-muted-foreground mx-0.5">/</span>
      <span className="text-muted-foreground">Out</span> {fmt(output)}
      {(hasCR || hasCW) && (
        <>
          <span className="text-muted-foreground mx-0.5">/</span>
          <span className="text-muted-foreground">CR</span> {fmt(cr)}
          <span className="text-muted-foreground mx-0.5">/</span>
          <span className="text-muted-foreground">CW</span> {fmt(cw)}
        </>
      )}
    </span>
  );
}

// Price with diff highlight
const pEq = (a: number, b: number) => Math.abs(a - b) < 0.0001;

function PriceDiff({ input, output, cr, cw, cur }: {
  input: number; output: number; cr: number | null; cw: number | null;
  cur: { input_price: number; output_price: number; cache_read_price: number; cache_write_price: number };
}) {
  const hasCR = cr != null && cr > 0;
  const hasCW = cw != null && cw > 0;
  const inChanged = !pEq(input, cur.input_price);
  const outChanged = !pEq(output, cur.output_price);

  return (
    <span className="text-xs tabular-nums">
      <span className="text-muted-foreground">In</span>{" "}
      <span className={inChanged ? "text-green-600 font-medium" : ""}>{fmt(input)}</span>
      <span className="text-muted-foreground mx-0.5">/</span>
      <span className="text-muted-foreground">Out</span>{" "}
      <span className={outChanged ? "text-green-600 font-medium" : ""}>{fmt(output)}</span>
      {(hasCR || hasCW) && (
        <>
          <span className="text-muted-foreground mx-0.5">/</span>
          <span className="text-muted-foreground">CR</span> {fmt(cr)}
          <span className="text-muted-foreground mx-0.5">/</span>
          <span className="text-muted-foreground">CW</span> {fmt(cw)}
        </>
      )}
    </span>
  );
}

// --- Main ---

export function PricingPreviewDialog({
  open, onOpenChange, data, onApply, isApplying,
}: PricingPreviewDialogProps) {
  const t = useTranslations("models");
  const isMobile = useIsMobile();
  const matches = useMemo(() => data.matches ?? [], [data]);
  const unmatchedModels = useMemo(() => data.unmatched_models ?? [], [data]);
  const sourceKeys = useMemo(() => getSourceKeys(data), [data]);

  const [selections, setSelections] = useState<Record<number, Selection>>({});
  const [filter, setFilter] = useState<FilterKey>("all");
  const [unmatchedOpen, setUnmatchedOpen] = useState(false);

  // Float comparison with tolerance (prices have ~4 decimal precision)
  const priceEq = (a: number, b: number) => Math.abs(a - b) < 0.0001;

  // Check if a source would change the current price (null = source doesn't provide, skip)
  const sourceHasChange = useCallback((s: PricingMatch["sources"][string], cur: PricingMatch["current"]) => {
    if (!priceEq(s.input_price, cur.input_price)) return true;
    if (!priceEq(s.output_price, cur.output_price)) return true;
    if (s.cache_read_price != null && !priceEq(s.cache_read_price, cur.cache_read_price)) return true;
    if (s.cache_write_price != null && !priceEq(s.cache_write_price, cur.cache_write_price)) return true;
    return false;
  }, []);

  const hasChange = useCallback((m: PricingMatch) => {
    return Object.values(m.sources).some((s) => sourceHasChange(s, m.current));
  }, [sourceHasChange]);

  // Count changed models to decide default filter
  const changedCount = useMemo(() => matches.filter(hasChange).length, [matches, hasChange]);

  // Reset on new data — default to "changed" if there are changes, else "all"
  useEffect(() => {
    const init: Record<number, Selection> = {};
    for (const m of matches) init[m.model_id] = "skip";
    setSelections(init);
    setFilter(changedCount > 0 && changedCount < matches.length ? "changed" : "all");
  }, [matches, changedCount]);

  // Check if any source has higher/lower total price than current (must also have real change)
  const hasPriceUp = useCallback((m: PricingMatch) => {
    const curTotal = m.current.input_price + m.current.output_price;
    return Object.values(m.sources).some((s) => {
      const srcTotal = s.input_price + s.output_price;
      return srcTotal - curTotal > 0.0001 && sourceHasChange(s, m.current);
    });
  }, [sourceHasChange]);
  const hasPriceDown = useCallback((m: PricingMatch) => {
    if (!m.has_price) return false;
    const curTotal = m.current.input_price + m.current.output_price;
    return Object.values(m.sources).some((s) => {
      const srcTotal = s.input_price + s.output_price;
      return curTotal - srcTotal > 0.0001 && sourceHasChange(s, m.current);
    });
  }, [sourceHasChange]);

  // A match where all sources return $0 price — usually not useful
  const hasZeroSourcePrice = useCallback((m: PricingMatch) => {
    return Object.values(m.sources).every((s) => s.input_price === 0 && s.output_price === 0);
  }, []);
  const zeroSourceCount = useMemo(() => matches.filter(hasZeroSourcePrice).length, [matches, hasZeroSourcePrice]);

  const filteredMatches = useMemo(() => {
    return matches.filter((m) => {
      // Exclude zero-source-price matches unless explicitly showing all
      if (filter !== "all_incl_zero" && hasZeroSourcePrice(m)) return false;

      switch (filter) {
        case "changed":
          return hasChange(m);
        case "price_up":
          return hasPriceUp(m);
        case "price_down":
          return hasPriceDown(m);
        case "fuzzy":
          return Object.values(m.sources).some((s) => s.match_type === "fuzzy");
        case "no_price":
          return !m.has_price;
        default:
          return true;
      }
    });
  }, [matches, filter, hasChange, hasZeroSourcePrice]);

  const toggle = useCallback((modelId: number, key: Selection) => {
    setSelections((prev) => ({
      ...prev,
      [modelId]: prev[modelId] === key ? "skip" : key,
    }));
  }, []);

  const batchSelect = useCallback((action: string) => {
    setSelections((prev) => {
      const next = { ...prev };
      for (const m of filteredMatches) {
        if (action === "skip") {
          next[m.model_id] = "skip";
        } else {
          const src = m.sources[action];
          // Only select if source exists AND has actual price change
          if (src && sourceHasChange(src, m.current)) {
            next[m.model_id] = action;
          }
        }
      }
      return next;
    });
  }, [filteredMatches, sourceHasChange]);

  const handleApply = useCallback(() => {
    const updates: PricingUpdate[] = [];
    for (const m of matches) {
      const sel = selections[m.model_id];
      if (!sel || sel === "skip") continue;
      const src = m.sources[sel];
      if (!src) continue;
      updates.push({
        model_id: m.model_id,
        input_price: src.input_price,
        output_price: src.output_price,
        cache_read_price: src.cache_read_price ?? 0,
        cache_write_price: src.cache_write_price ?? 0,
      });
    }
    onApply(updates);
  }, [matches, selections, onApply]);

  const selectedCount = Object.values(selections).filter((v) => v !== "skip").length;
  const totalCount = matches.length;

  // --- Row / Card ---

  const SourceButton = ({ srcKey, modelId, srcData, current, isSelected }: {
    srcKey: string; modelId: number; srcData?: PricingMatch["sources"][string]; current: PricingMatch["current"]; isSelected: boolean;
  }) => {
    const available = !!srcData && sourceHasChange(srcData, current);
    return (
      <button
        disabled={!available}
        onClick={() => available && toggle(modelId, srcKey)}
        className={`text-2xs px-1.5 py-0.5 rounded border transition-colors whitespace-nowrap ${
          isSelected
            ? "bg-primary text-primary-foreground border-primary"
            : available
            ? "border-input hover:bg-accent"
            : "opacity-30 cursor-not-allowed border-input"
        }`}
      >
        {sourceLabel(srcKey)}
      </button>
    );
  };

  const Row = ({ m }: { m: PricingMatch }) => {
    const sel = selections[m.model_id] ?? "skip";
    return (
      <tr className={`border-b text-xs hover:bg-muted/30 ${m.has_price && sel !== "skip" ? "bg-orange-50 dark:bg-orange-950/20" : ""}`}>
        <td className="py-1.5 px-2 font-mono whitespace-nowrap max-w-[160px] truncate" title={m.model_name}>
          {m.model_name}
          {Object.values(m.sources).some((s) => s.match_type === "fuzzy") && (
            <TooltipProvider delayDuration={100}>
              <Tooltip>
                <TooltipTrigger>
                  <Badge variant="outline" className="ml-1 text-2xs py-0 h-3.5 text-yellow-600 border-yellow-400">{t("matchFuzzy")}</Badge>
                </TooltipTrigger>
                <TooltipContent className="text-xs">
                  {Object.entries(m.sources).filter(([, s]) => s.match_type === "fuzzy").map(([k, s]) => `${sourceLabel(k)}: ${s.matched_name}`).join(", ")}
                </TooltipContent>
              </Tooltip>
            </TooltipProvider>
          )}
        </td>
        <td className="py-1.5 px-2 whitespace-nowrap">
          {m.has_price ? (
            <PriceSummary input={m.current.input_price} output={m.current.output_price} cr={m.current.cache_read_price} cw={m.current.cache_write_price} />
          ) : (
            <span className="text-muted-foreground text-xs">-</span>
          )}
        </td>
        {sourceKeys.map((sk) => {
          const src = m.sources[sk];
          return (
            <td key={sk} className="py-1.5 px-2 whitespace-nowrap">
              {src ? (
                <PriceDiff input={src.input_price} output={src.output_price} cr={src.cache_read_price} cw={src.cache_write_price} cur={m.current} />
              ) : (
                <span className="text-muted-foreground text-xs">-</span>
              )}
            </td>
          );
        })}
        <td className="py-1.5 px-2">
          <div className="flex items-center gap-0.5">
            {sourceKeys.map((sk) => (
              <SourceButton key={sk} srcKey={sk} modelId={m.model_id} srcData={m.sources[sk]} current={m.current} isSelected={sel === sk} />
            ))}
            {sel !== "skip" && (
              <button onClick={() => toggle(m.model_id, sel)} className="text-2xs px-1 text-muted-foreground hover:text-foreground">✕</button>
            )}
          </div>
        </td>
      </tr>
    );
  };

  const Card = ({ m }: { m: PricingMatch }) => {
    const sel = selections[m.model_id] ?? "skip";
    return (
      <div className={`rounded-lg border p-2.5 space-y-1.5 ${m.has_price && sel !== "skip" ? "border-orange-300 bg-orange-50/50 dark:border-orange-800 dark:bg-orange-950/20" : ""}`}>
        <div className="flex items-center gap-1.5">
          <span className="font-mono text-xs font-medium truncate" title={m.model_name}>{m.model_name}</span>
          {!m.has_price && <Badge variant="secondary" className="text-2xs py-0 h-3.5 shrink-0">{t("filterNoPrice")}</Badge>}
          {Object.values(m.sources).some((s) => s.match_type === "fuzzy") && (
            <Badge variant="outline" className="text-2xs py-0 h-3.5 text-yellow-600 border-yellow-400 shrink-0">{t("matchFuzzy")}</Badge>
          )}
        </div>

        {m.has_price && (
          <div className="text-2xs text-muted-foreground">
            {t("currentPrice")}: <PriceSummary input={m.current.input_price} output={m.current.output_price} cr={m.current.cache_read_price} cw={m.current.cache_write_price} />
          </div>
        )}

        <div className="space-y-1">
          {sourceKeys.map((sk) => {
            const src = m.sources[sk];
            const isSelected = sel === sk;
            const available = !!src && sourceHasChange(src, m.current);
            return (
              <button
                key={sk}
                disabled={!available}
                onClick={() => available && toggle(m.model_id, isSelected ? "skip" : sk)}
                className={`w-full text-left rounded border px-2 py-1.5 transition-colors ${
                  isSelected ? "ring-1 ring-primary border-primary bg-primary/5" : available ? "border-input hover:bg-accent/50" : "opacity-30 cursor-not-allowed border-input"
                }`}
              >
                <div className="flex items-center justify-between">
                  <span className="text-2xs font-medium">{sourceLabel(sk)}</span>
                  {src?.match_type === "fuzzy" && <span className="text-2xs text-muted-foreground">({src.matched_name})</span>}
                </div>
                {src && (
                  <div className="mt-0.5">
                    <PriceDiff input={src.input_price} output={src.output_price} cr={src.cache_read_price} cw={src.cache_write_price} cur={m.current} />
                  </div>
                )}
              </button>
            );
          })}
        </div>
      </div>
    );
  };

  // --- Render ---

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className={isMobile ? "max-w-full h-full flex flex-col p-0 gap-0 rounded-none" : "max-w-5xl max-h-[85vh] flex flex-col gap-0 p-0"}>
        {/* Header */}
        <DialogHeader className="px-4 pt-3 pb-1.5 shrink-0">
          <DialogTitle className="text-base flex items-center gap-2">
            {t("previewPricing")}
            <span className="text-sm font-normal text-muted-foreground">({matches.length})</span>
            {sourceKeys.map((src) => {
              const err = data.source_errors?.[src];
              return (
                <Badge key={src} variant="outline" className={`text-2xs ${err ? "border-destructive text-destructive" : "border-green-500 text-green-600"}`}>
                  {sourceLabel(src)} {err ? "✗" : "✓"}
                </Badge>
              );
            })}
          </DialogTitle>
        </DialogHeader>

        {/* Filter + Batch */}
        <div className="px-4 py-1.5 shrink-0 border-b space-y-1.5">
          <div className="flex flex-wrap gap-1">
            {([
              { key: "all" as FilterKey, label: t("filterAll"), count: matches.length - zeroSourceCount, alwaysShow: true },
              { key: "changed" as FilterKey, label: t("filterChanged"), count: changedCount, alwaysShow: false },
              { key: "price_up" as FilterKey, label: "↑ " + t("priceUp"), count: matches.filter(hasPriceUp).length, alwaysShow: false },
              { key: "price_down" as FilterKey, label: "↓ " + t("priceDown"), count: matches.filter(hasPriceDown).length, alwaysShow: false },
              { key: "fuzzy" as FilterKey, label: t("filterFuzzy"), count: matches.filter((m) => Object.values(m.sources).some((s) => s.match_type === "fuzzy")).length, alwaysShow: false },
              { key: "no_price" as FilterKey, label: t("filterNoPrice"), count: matches.filter((m) => !m.has_price).length, alwaysShow: false },
              { key: "all_incl_zero" as FilterKey, label: t("filterAllInclZero"), count: matches.length, alwaysShow: zeroSourceCount > 0 },
            ]).filter(({ count, alwaysShow }) => alwaysShow || count > 0).map(({ key, label, count }) => (
              <Button
                key={key}
                variant={filter === key ? "default" : "outline"}
                size="sm"
                className="h-7 text-xs px-2.5"
                onClick={() => setFilter(key)}
              >
                {label}
                {count < matches.length && <span className="ml-1 opacity-70">({count})</span>}
              </Button>
            ))}
          </div>
          <div className="flex items-center gap-1 flex-wrap">
            {sourceKeys.map((src) => (
              <Button key={src} variant="outline" size="sm" className="h-6 text-2xs px-2" onClick={() => batchSelect(src)}>
                {t("selectAllFrom", { source: sourceLabel(src) })}
              </Button>
            ))}
            <Button variant="outline" size="sm" className="h-6 text-2xs px-2" onClick={() => batchSelect("skip")}>{t("selectAllSkip")}</Button>
          </div>
        </div>

        {/* Content */}
        <div className="flex-1 overflow-y-auto">
          {filteredMatches.length === 0 ? (
            <div className="text-center text-muted-foreground py-12 text-sm">{t("noMatches")}</div>
          ) : isMobile ? (
            <div className="p-3 space-y-2">{filteredMatches.map((m) => <Card key={m.model_id} m={m} />)}</div>
          ) : (
            <div className="overflow-x-auto">
              <table className="w-full">
                <thead className="sticky top-0 bg-background z-10">
                  <tr className="border-b text-2xs text-muted-foreground">
                    <th className="py-1.5 px-2 font-medium text-left">{t("modelName")}</th>
                    <th className="py-1.5 px-2 font-medium text-left">{t("currentPrice")}</th>
                    {sourceKeys.map((src) => (
                      <th key={src} className="py-1.5 px-2 font-medium text-left">{sourceLabel(src)}</th>
                    ))}
                    <th className="py-1.5 px-2 font-medium text-left">{t("applyPricing")}</th>
                  </tr>
                </thead>
                <tbody>{filteredMatches.map((m) => <Row key={m.model_id} m={m} />)}</tbody>
              </table>
            </div>
          )}

          {/* Unmatched */}
          {unmatchedModels.length > 0 && (
            <div className="px-4 py-2 border-t">
              <button className="flex items-center gap-1 text-xs text-muted-foreground hover:text-foreground" onClick={() => setUnmatchedOpen((v) => !v)}>
                {unmatchedOpen ? <ChevronDown className="size-3.5" /> : <ChevronRight className="size-3.5" />}
                {t("unmatchedModels")} ({unmatchedModels.length})
              </button>
              {unmatchedOpen && (
                <div className="mt-1.5 pl-5 space-y-0.5">
                  {unmatchedModels.map((n) => <div key={n} className="text-2xs text-muted-foreground font-mono">{n}</div>)}
                </div>
              )}
            </div>
          )}
        </div>

        {/* Footer */}
        <DialogFooter className="px-4 py-2 border-t shrink-0">
          <div className="flex items-center justify-between w-full">
            <span className="text-xs text-muted-foreground">
              {t("selectedCount", { count: selectedCount, skip: totalCount - selectedCount })}
            </span>
            <div className="flex gap-2">
              <Button variant="outline" size="sm" onClick={() => onOpenChange(false)} disabled={isApplying}>{t("skip")}</Button>
              <Button size="sm" onClick={handleApply} disabled={isApplying || selectedCount === 0}>
                {t("applyPricing")} ({selectedCount})
              </Button>
            </div>
          </div>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
