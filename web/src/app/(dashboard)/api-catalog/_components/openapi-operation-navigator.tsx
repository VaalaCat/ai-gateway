"use client";

import { useTranslations } from "next-intl";

import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Empty, EmptyHeader, EmptyTitle } from "@/components/ui/empty";
import type { OpenAPIOperation } from "./openapi-operation-selection";

export interface OpenAPIOperationNavigatorProps {
  operations: OpenAPIOperation[];
  selected?: OpenAPIOperation;
  onSelect: (operation: OpenAPIOperation) => void;
}

export function OpenAPIOperationNavigator({ operations, selected, onSelect }: OpenAPIOperationNavigatorProps) {
  const t = useTranslations("apiCatalog");
  const grouped = new Map<string, OpenAPIOperation[]>();
  for (const operation of operations) {
    const pathOperations = grouped.get(operation.path);
    if (pathOperations) {
      pathOperations.push(operation);
    } else {
      grouped.set(operation.path, [operation]);
    }
  }

  if (operations.length === 0) {
    return <Empty className="min-h-28"><EmptyHeader><EmptyTitle>{t("emptyOpenAPIOperations")}</EmptyTitle></EmptyHeader></Empty>;
  }

  return (
    <nav data-testid="openapi-operation-navigator" className="flex min-w-0 flex-col gap-3" aria-label={t("operations")}>
      <div className="flex items-center justify-between gap-2">
        <h2 className="text-sm font-semibold tracking-tight">{t("operations")}</h2>
        <span className="text-xs tabular-nums text-muted-foreground">{operations.length}</span>
      </div>
      {[...grouped.entries()].map(([path, pathOperations]) => (
        <section key={path} className="flex min-w-0 flex-col gap-1.5" aria-label={path}>
          <p className="truncate font-mono text-xs text-muted-foreground">{path}</p>
          <div className="flex flex-col gap-1">
            {pathOperations.map((operation) => {
              const isSelected = selected?.routeID === operation.routeID
                && selected.path === operation.path && selected.method === operation.method;
              return (
                <Button
                  key={`${operation.routeID}:${operation.path}:${operation.method}`}
                  type="button"
                  variant={isSelected ? "secondary" : "ghost"}
                  aria-label={`${operation.method} ${operation.path}`}
                  aria-current={isSelected ? "true" : undefined}
                  className="h-auto min-w-0 justify-start px-2.5 py-2 text-left"
                  onClick={() => onSelect(operation)}
                >
                  <span className="flex min-w-0 items-center gap-2">
                    <Badge variant="outline" className="shrink-0 font-mono">{operation.method}</Badge>
                    <span className="truncate font-mono text-xs">{operation.path}</span>
                  </span>
                </Button>
              );
            })}
          </div>
        </section>
      ))}
    </nav>
  );
}
