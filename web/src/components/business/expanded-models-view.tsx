"use client";

import { useState } from "react";
import { ChevronRight } from "lucide-react";
import { useTranslations } from "next-intl";
import { Badge } from "@/components/ui/badge";
import { getProviderIconKey } from "@/lib/constants";
import { ProviderAvatar } from "@/components/business/provider-avatar";
import { Button } from "@/components/ui/button";

interface ModelGroup {
  provider: string | null;
  displayName: string;
  models: string[];
}

interface ExpandedModelsViewProps {
  groups: ModelGroup[];
  totalCount: number;
  onChipClick?: (model: string) => void;
  hideHeader?: boolean;
}

export function ExpandedModelsView({ groups, totalCount, onChipClick, hideHeader }: ExpandedModelsViewProps) {
  const t = useTranslations("channels");
  const [expanded, setExpanded] = useState<Set<string>>(new Set());

  const toggleGroup = (key: string) => {
    const next = new Set(expanded);
    if (next.has(key)) next.delete(key);
    else next.add(key);
    setExpanded(next);
  };

  const expandAll = () => setExpanded(new Set(groups.map(g => g.provider ?? "_other")));
  const collapseAll = () => setExpanded(new Set());

  return (
    <div>
      {!hideHeader && (
        <div className="flex items-center justify-between mb-2">
          <h4 className="text-sm font-medium">
            {t("models")} ({totalCount})
          </h4>
          <div className="flex gap-1">
            <Button variant="ghost" size="sm" className="h-6 text-xs" onClick={expandAll}>
              {t("expandAll")}
            </Button>
            <Button variant="ghost" size="sm" className="h-6 text-xs" onClick={collapseAll}>
              {t("collapseAll")}
            </Button>
          </div>
        </div>
      )}
      <div className="space-y-1">
        {groups.map((group) => {
          const key = group.provider ?? "_other";
          const isExpanded = expanded.has(key);
          return (
            <div key={key}>
              <button
                type="button"
                className="flex w-full items-center gap-2 rounded-md px-2 py-1 text-sm hover:bg-accent"
                onClick={() => toggleGroup(key)}
              >
                <ChevronRight className={`size-3.5 shrink-0 transition-transform ${isExpanded ? "rotate-90" : ""}`} />
                {group.provider && getProviderIconKey(group.provider) && <ProviderAvatar provider={getProviderIconKey(group.provider)!} size={14} />}
                <span className="font-medium">{group.displayName}</span>
                <span className="text-xs text-muted-foreground">({group.models.length})</span>
              </button>
              {isExpanded && (
                <div className="ml-6 flex flex-wrap gap-1 py-1">
                  {group.models.map((m) => (
                    onChipClick ? (
                      <button
                        key={m}
                        type="button"
                        onClick={(e) => {
                          e.stopPropagation();
                          onChipClick(m);
                        }}
                        className="cursor-pointer transition-opacity rounded-md hover:opacity-80 active:scale-95 outline-none focus-visible:ring-[3px] focus-visible:ring-ring/50"
                      >
                        <Badge variant="secondary" className="text-xs font-mono px-1.5 py-0">
                          {m}
                        </Badge>
                      </button>
                    ) : (
                      <Badge key={m} variant="secondary" className="text-xs font-mono px-1.5 py-0">
                        {m}
                      </Badge>
                    )
                  ))}
                </div>
              )}
            </div>
          );
        })}
      </div>
    </div>
  );
}
