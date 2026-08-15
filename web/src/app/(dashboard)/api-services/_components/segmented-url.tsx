"use client";

import { Button } from "@/components/ui/button";
import { Tooltip, TooltipContent, TooltipProvider, TooltipTrigger } from "@/components/ui/tooltip";
import { copyTextWithFeedback } from "@/lib/utils/clipboard";
import { Copy } from "lucide-react";
import { useId } from "react";
import { useTranslations } from "next-intl";
import { cn } from "@/lib/utils";

export type URLSegmentKind = "service" | "route" | "endpoint";

export interface URLSegment {
  start: number;
  end: number;
  kind: URLSegmentKind;
  label: string;
}

export interface SegmentedURLValue {
  text: string;
  segments: URLSegment[];
}

interface SegmentedURLProps extends SegmentedURLValue {
  copyLabel?: string;
  copyDisabled?: boolean;
  copySuccess?: string;
  onCopy?: () => void;
  layout?: "wrap" | "truncate";
}

const segmentClass = {
  service: "rounded-sm border border-border bg-muted px-0.5",
  route: "rounded-sm border border-primary/40 bg-primary/10 px-0.5",
  endpoint: "rounded-sm border border-border bg-accent/60 px-0.5",
} satisfies Record<URLSegmentKind, string>;

function checkedSegments(text: string, segments: URLSegment[]) {
  const sorted = [...segments].sort((a, b) => a.start - b.start);
  for (const [index, item] of sorted.entries()) {
    if (item.start < 0 || item.end <= item.start || item.end > text.length) throw new Error("invalid URL segment");
    if (index > 0 && sorted[index - 1]!.end > item.start) throw new Error("overlapping URL segment");
  }
  return sorted;
}

function renderContiguousParts(text: string, segments: URLSegment[], descriptionIDs: string[]) {
  const parts: React.ReactNode[] = [];
  let offset = 0;
  for (const [index, segment] of segments.entries()) {
    if (offset < segment.start) parts.push(text.slice(offset, segment.start));
    parts.push(<span key={`${segment.start}-${segment.end}-${segment.kind}`} className={segmentClass[segment.kind]} aria-describedby={descriptionIDs[index]}>{text.slice(segment.start, segment.end)}</span>);
    offset = segment.end;
  }
  if (offset < text.length) parts.push(text.slice(offset));
  return parts;
}

export function SegmentedURL({ text, segments, copyLabel, copyDisabled = false, copySuccess, onCopy, layout = "wrap" }: SegmentedURLProps) {
  const t = useTranslations("apiServices");
  const baseID = useId();
  const checked = checkedSegments(text, segments);
  const descriptionIDs = checked.map((_, index) => `${baseID}-segment-${index}`);
  const copy = () => {
    void copyTextWithFeedback(text, { success: copySuccess ?? t("urlCopied"), error: t("copyCommandFailed") });
    onCopy?.();
  };
  return <div className={cn("flex min-w-0 items-start gap-2", layout === "wrap" ? "flex-wrap" : "flex-nowrap items-center overflow-hidden")}><code data-testid="segmented-url-text" title={text} className={cn("min-w-0 font-mono text-xs", layout === "wrap" ? "whitespace-normal break-all" : "truncate whitespace-nowrap")}>{renderContiguousParts(text, checked, descriptionIDs)}</code>{checked.map((segment, index) => <span key={descriptionIDs[index]} id={descriptionIDs[index]} className="sr-only select-none">{segment.label}</span>)}{copyLabel ? <TooltipProvider><Tooltip><TooltipTrigger asChild><Button type="button" variant="ghost" size="icon-sm" className="shrink-0 max-sm:min-h-11 max-sm:min-w-11" aria-label={copyLabel} disabled={copyDisabled} onClick={copy}><Copy /></Button></TooltipTrigger><TooltipContent>{copyLabel}</TooltipContent></Tooltip></TooltipProvider> : null}</div>;
}
