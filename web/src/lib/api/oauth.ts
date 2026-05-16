import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { api } from "@/lib/api/client";
import type { PublicProvider, OAuthProvider, OAuthIdentityItem } from "@/lib/types-oauth";

export const oauthApi = {
  publicProviders: () => api.get<PublicProvider[]>("/oauth/providers"),
  myIdentities: () => api.get<OAuthIdentityItem[]>("/oauth/identities"),
  unlink: (id: number) => api.delete<{ status: string }>(`/oauth/identities/${id}`),
  issueLinkTicket: () => api.post<{ ticket: string }>("/oauth/link-ticket", {}),
  bind: (ticket: string, username: string, password: string) =>
    api.post<{ token: string }>("/oauth/bind", { ticket, username, password }),
  register: (ticket: string) =>
    api.post<{ token: string }>("/oauth/register", { ticket }),
};

// ===== TanStack Query hooks =====

export function usePublicOAuthProviders() {
  return useQuery({
    queryKey: ["oauth-public-providers"],
    queryFn: () => api.get<PublicProvider[]>("/oauth/providers"),
  });
}

export function useOAuthProviders() {
  return useQuery({
    queryKey: ["oauth-providers"],
    queryFn: () => api.get<OAuthProvider[]>("/admin/oauth-providers"),
  });
}

export function useCreateOAuthProvider() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (body: Partial<OAuthProvider>) =>
      api.post<OAuthProvider>("/admin/oauth-providers", body),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["oauth-providers"] }),
  });
}

export function useUpdateOAuthProvider() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ id, ...patch }: { id: number } & Partial<OAuthProvider>) =>
      api.put<OAuthProvider>(`/admin/oauth-providers/${id}`, patch),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["oauth-providers"] }),
  });
}

export function useDeleteOAuthProvider() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: number) =>
      api.delete<{ status: string }>(`/admin/oauth-providers/${id}`),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["oauth-providers"] }),
  });
}

export function useMyIdentities() {
  return useQuery({
    queryKey: ["oauth-identities"],
    queryFn: () => api.get<OAuthIdentityItem[]>("/oauth/identities"),
  });
}

export function useDeleteIdentity() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: number) =>
      api.delete<{ status: string }>(`/oauth/identities/${id}`),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["oauth-identities"] }),
  });
}

export function useIssueLinkTicket() {
  return useMutation({
    mutationFn: () => api.post<{ ticket: string }>("/oauth/link-ticket", {}),
  });
}
