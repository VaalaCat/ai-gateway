"use client";

import { useState } from "react";
import { useTranslations } from "next-intl";
import { ChevronDown, ChevronRight } from "lucide-react";
import { Badge } from "@/components/ui/badge";
import { ScopeBadge } from "./scope-badge";
import type { RoutingPreviewNode } from "@/lib/types";

export interface PriorityCascadeProps {
  members: RoutingPreviewNode[];
  depth?: number;
}

// PriorityCascade 把 routing 成员按 priority 降序"阶梯"式渲染：
//   - 每个 priority 一张卡片，左侧色块 + 数字徽章一眼看清层级
//   - 卡片内同 priority 成员按 weight 加权显示分配
//   - 成员若本身是 routing，可下钻；默认展开一层，深层折叠
export function PriorityCascade({ members, depth = 0 }: PriorityCascadeProps) {
  if (members.length === 0) return null;
  const layers = groupByPriority(members);
  return (
    <div className="space-y-2.5">
      {layers.map((layer, idx) => (
        <LayerCard
          key={layer.priority}
          priority={layer.priority}
          members={layer.members}
          isPrimary={idx === 0}
          fallbackIndex={idx}
          depth={depth}
        />
      ))}
    </div>
  );
}

interface LayerCardProps {
  priority: number;
  members: RoutingPreviewNode[];
  isPrimary: boolean;
  fallbackIndex: number;
  depth: number;
}

function LayerCard({ priority, members, isPrimary, fallbackIndex, depth }: LayerCardProps) {
  const t = useTranslations("modelRoutings.preview");
  const totalWeight = members.reduce((s, m) => s + Math.max(1, m.weight), 0);
  const items = members.map((m) => ({
    node: m,
    pct: (Math.max(1, m.weight) / totalWeight) * 100,
  }));
  return (
    <section
      className={[
        "rounded-lg border bg-card",
        "border-l-[3px]",
        isPrimary ? "border-l-primary" : "border-l-muted-foreground/30",
      ].join(" ")}
    >
      <header className="flex items-center gap-2.5 px-3 pt-2.5">
        <PriorityBadge value={priority} isPrimary={isPrimary} />
        <div className="flex flex-col min-w-0 leading-tight">
          <span className="text-sm font-medium">
            {isPrimary ? t("layerPrimary") : t("layerFallback", { n: fallbackIndex })}
          </span>
          <span className="text-xs text-muted-foreground">
            {t("membersCount", { n: members.length })}
          </span>
        </div>
      </header>
      <ul className="px-3 pb-2.5 pt-2 space-y-1">
        {items.map(({ node, pct }) => (
          <MemberRow key={node.ref} node={node} pct={pct} depth={depth} />
        ))}
      </ul>
    </section>
  );
}

function PriorityBadge({ value, isPrimary }: { value: number; isPrimary: boolean }) {
  return (
    <div
      className={[
        "size-9 shrink-0 rounded-md border flex items-center justify-center",
        isPrimary
          ? "bg-primary/10 border-primary/20 text-primary"
          : "bg-muted border-muted-foreground/10 text-muted-foreground",
      ].join(" ")}
      aria-label={`priority ${value}`}
    >
      <span className="text-sm font-semibold tabular-nums leading-none">{value}</span>
    </div>
  );
}

interface MemberRowProps {
  node: RoutingPreviewNode;
  pct: number;
  depth: number;
}

function MemberRow({ node, pct, depth }: MemberRowProps) {
  const t = useTranslations("modelRoutings.preview");
  const isRouting = node.kind === "routing";
  const isInvalid = node.kind === "invalid" || !!node.error;
  const hasChildren = isRouting && !!node.children?.length;
  const [open, setOpen] = useState(depth < 1);

  return (
    <li>
      <div className="py-0.5">
        <div className="flex items-center gap-1.5 min-w-0">
          {hasChildren ? (
            <button
              type="button"
              onClick={() => setOpen(!open)}
              className="text-muted-foreground/60 hover:text-muted-foreground shrink-0"
              aria-label={open ? t("collapse") : t("expand")}
            >
              {open ? (
                <ChevronDown className="size-3.5" />
              ) : (
                <ChevronRight className="size-3.5" />
              )}
            </button>
          ) : (
            <span className="w-3.5 shrink-0" />
          )}
          <span
            className={[
              "truncate text-sm flex-1 min-w-0",
              isInvalid ? "text-muted-foreground/40 line-through" : "",
            ].join(" ")}
            title={node.ref}
          >
            {node.ref}
          </span>
          {isRouting && node.scope && <ScopeBadge scope={node.scope} />}
          <ErrorBadge error={node.error} />
          <span
            className={[
              "text-xs tabular-nums tracking-tight shrink-0",
              isInvalid ? "text-muted-foreground/40 line-through" : "text-muted-foreground",
            ].join(" ")}
          >
            {pct.toFixed(1)}%
          </span>
        </div>
        <div className="mt-1 ml-5 h-1 bg-muted rounded-full overflow-hidden">
          <div
            className={[
              "h-full rounded-full transition-all duration-300",
              isInvalid ? "bg-muted-foreground/30" : "bg-primary",
            ].join(" ")}
            style={{ width: `${pct}%` }}
          />
        </div>
      </div>
      {hasChildren && open && (
        <div className="mt-1 ml-1.5 pl-3 border-l border-dashed border-muted-foreground/20">
          <PriorityCascade members={node.children!} depth={depth + 1} />
        </div>
      )}
    </li>
  );
}

// 错误态 → badge 配置表（避免主流程长 if-else 链）
const ERROR_BADGE: Record<
  NonNullable<RoutingPreviewNode["error"]>,
  { i18nKey: string; variant: "outline" | "destructive" }
> = {
  not_found: { i18nKey: "unavailable", variant: "outline" },
  disabled: { i18nKey: "disabled", variant: "outline" },
  cycle: { i18nKey: "cycle", variant: "destructive" },
  max_depth: { i18nKey: "maxDepth", variant: "destructive" },
};

function ErrorBadge({ error }: { error: RoutingPreviewNode["error"] }) {
  const t = useTranslations("modelRoutings.preview");
  if (!error) return null;
  const cfg = ERROR_BADGE[error];
  return (
    <Badge variant={cfg.variant} className="text-2xs shrink-0">
      {t(cfg.i18nKey)}
    </Badge>
  );
}

interface PriorityLayer {
  priority: number;
  members: RoutingPreviewNode[];
}

function groupByPriority(members: RoutingPreviewNode[]): PriorityLayer[] {
  const map = new Map<number, RoutingPreviewNode[]>();
  for (const m of members) {
    const arr = map.get(m.priority) ?? [];
    arr.push(m);
    map.set(m.priority, arr);
  }
  return Array.from(map.entries())
    .sort(([a], [b]) => b - a)
    .map(([priority, members]) => ({ priority, members }));
}
