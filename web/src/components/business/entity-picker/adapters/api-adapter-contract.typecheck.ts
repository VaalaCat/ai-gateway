import { useAPIServices, type APIService } from "@/lib/api/api-services";
import type { EntityAdapter, EntityListParams } from "../types";

/**
 * Type-level boundary: a picker list may consume only its data/loading/error/retry state,
 * while API hooks retain their full React Query result type internally.
 */
const useServiceListContract: EntityAdapter<APIService>["useList"] = (
  { search, page_size, enabled = true }: EntityListParams,
) => useAPIServices(
  { search, page_size },
  { enabled },
);

void useServiceListContract;
