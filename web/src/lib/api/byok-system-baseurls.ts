import { useQuery } from "@tanstack/react-query";
import { api } from "./client";

export interface BYOKSystemBaseURLs {
  urls: string[];
}

export function useBYOKSystemBaseURLs() {
  return useQuery({
    queryKey: ["byok-system-baseurls"],
    queryFn: () => api.get<BYOKSystemBaseURLs>("/admin/byok-system-baseurls"),
  });
}

// BYOKBaseURLUsage is the admin endpoint response for /admin/private-channels/baseurl/usage.
// Used to preview how many existing private channels reference a BaseURL prefix
// before the admin removes that prefix from byok_base_url_allowlist. The
// `channels` array is capped server-side at 50 entries; `count` is unbounded.
export interface BYOKBaseURLUsage {
  count: number;
  channels: Array<{ owner_id: number; channel_name: string }>;
}

// useBaseURLUsage fetches usage info for a BaseURL prefix. Pass null to disable
// the query (no fetch will fire). The hook intentionally uses a per-prefix
// queryKey so toggling between candidate prefixes invalidates correctly; we
// don't cache across opens of the same dialog because the admin may have edited
// channels in another tab between opens.
export function useBaseURLUsage(prefix: string | null) {
  return useQuery({
    queryKey: ["byok-baseurl-usage", prefix],
    queryFn: () =>
      api.get<BYOKBaseURLUsage>(
        `/admin/private-channels/baseurl/usage?prefix=${encodeURIComponent(prefix!)}`,
      ),
    enabled: !!prefix,
    staleTime: 0,
    gcTime: 0,
  });
}
