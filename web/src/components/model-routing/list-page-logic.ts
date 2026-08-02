import type { FilterValues } from "@/components/data-table/filter-spec";
import type { ModelRoutingOwner } from "@/lib/api/model-routings";
import type { ModelRouting } from "@/lib/types";

export function normalizeModelRoutingFilterChange(
  next: Partial<FilterValues>,
): Partial<FilterValues> {
  if (!("scope" in next)) return next;
  return { ...next, user_id: "", token_id: "" };
}

export function modelRoutingOwner(routing: ModelRouting): ModelRoutingOwner {
  return routing.scope === "token"
    ? { kind: "token", tokenId: routing.token_id }
    : { kind: "scope" };
}

export function buildModelRoutingEditHref(
  baseHref: string,
  routing: ModelRouting,
): string {
  const href = `${baseHref}/edit?id=${routing.id}`;
  return routing.scope === "token"
    ? `${href}&token_id=${routing.token_id}`
    : href;
}
