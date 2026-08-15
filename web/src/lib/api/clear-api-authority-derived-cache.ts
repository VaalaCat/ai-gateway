import type { QueryClient } from "@tanstack/react-query";

// Reset removes permission-sensitive data immediately; active observers then refetch.
export function clearAPIAuthorityDerivedCache(queryClient: QueryClient) {
  void queryClient.resetQueries({ queryKey: ["api-catalog"] });
  void queryClient.resetQueries({ queryKey: ["tokens", "usable-for-api-route"] });
}
