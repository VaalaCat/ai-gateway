import { useQuery } from "@tanstack/react-query";

export class AvailableModelsError extends Error {
  constructor(public code: "unauthorized" | "generic") {
    super(code);
    this.name = "AvailableModelsError";
  }
}

interface ModelsResponse {
  object: string;
  data: Array<{ id: string; object: string; created: number; owned_by: string }>;
}

export function useAvailableModels(tokenKey: string) {
  return useQuery({
    queryKey: ["available-models", tokenKey],
    queryFn: async (): Promise<string[]> => {
      const res = await fetch("/v1/models", {
        headers: { Authorization: `Bearer ${tokenKey}` },
      });
      if (res.status === 401) throw new AvailableModelsError("unauthorized");
      if (!res.ok) throw new AvailableModelsError("generic");
      const body = (await res.json()) as ModelsResponse;
      return (body.data ?? []).map((m) => m.id);
    },
    enabled: !!tokenKey,
    staleTime: 30_000,
    retry: false,
  });
}
