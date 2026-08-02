import type { ReactNode } from "react";
import { QueryClientProvider } from "@tanstack/react-query";
import { act, render, screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

import type { CapabilitiesResponse } from "@/lib/api/capabilities";
import { createTestQueryClient } from "@/test/render";
import { AppSidebar } from "./sidebar";

const { apiGet } = vi.hoisted(() => ({ apiGet: vi.fn() }));

vi.mock("next/link", () => ({
  default: ({ href, children }: { href: string; children: React.ReactNode }) => (
    <a href={href}>{children}</a>
  ),
}));
vi.mock("next/navigation", () => ({ usePathname: () => "/dashboard" }));
vi.mock("next-intl", () => ({ useTranslations: () => (key: string) => key }));
vi.mock("@/lib/api/client", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/lib/api/client")>();
  return { ...actual, api: { get: apiGet } };
});
vi.mock("@/lib/api/system", () => ({ usePublicConfig: () => ({ data: {} }) }));
vi.mock("@/hooks/use-sidebar-section", () => ({ useSidebarSection: () => [true, vi.fn()] }));
vi.mock("@/components/ui/sidebar", () => ({
  Sidebar: ({ children }: { children: React.ReactNode }) => <nav>{children}</nav>,
  SidebarContent: ({ children }: { children: React.ReactNode }) => <div>{children}</div>,
  SidebarFooter: ({ children }: { children: React.ReactNode }) => <div>{children}</div>,
  SidebarGroup: ({ children }: { children: React.ReactNode }) => <div>{children}</div>,
  SidebarGroupContent: ({ children }: { children: React.ReactNode }) => <div>{children}</div>,
  SidebarGroupLabel: ({ children }: { children: React.ReactNode }) => <div>{children}</div>,
  SidebarHeader: ({ children }: { children: React.ReactNode }) => <div>{children}</div>,
  SidebarMenu: ({ children }: { children: React.ReactNode }) => <ul>{children}</ul>,
  SidebarMenuButton: ({ children }: { children: React.ReactNode }) => <>{children}</>,
  SidebarMenuItem: ({ children }: { children: React.ReactNode }) => <li>{children}</li>,
  SidebarSeparator: () => null,
}));
vi.mock("@/components/ui/collapsible", () => ({
  Collapsible: ({ children }: { children: React.ReactNode }) => <>{children}</>,
  CollapsibleContent: ({ children }: { children: React.ReactNode }) => <>{children}</>,
  CollapsibleTrigger: ({ children }: { children: React.ReactNode }) => <>{children}</>,
}));

function authToken(userId: number, role: number) {
  const payload = btoa(JSON.stringify({
    user_id: userId,
    username: `user-${userId}`,
    role,
    exp: Math.floor(Date.now() / 1000) + 3_600,
  })).replace(/=/g, "").replace(/\+/g, "-").replace(/\//g, "_");
  return `header.${payload}.signature`;
}

function capabilities(modelMarketplace: boolean | undefined): CapabilitiesResponse {
  return {
    token: { can_edit_model_whitelist: false },
    model_marketplace: modelMarketplace,
  };
}

function renderSidebar() {
  const queryClient = createTestQueryClient();
  const wrapper = ({ children }: { children: ReactNode }) => (
    <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>
  );
  return { queryClient, ...render(<AppSidebar />, { wrapper }) };
}

describe("model marketplace navigation", () => {
  beforeEach(() => {
    window.localStorage.clear();
    apiGet.mockReset();
  });

  it("fails closed when an ordinary user has no marketplace capability", async () => {
    window.localStorage.setItem("token", authToken(8, 1));
    apiGet.mockResolvedValueOnce(capabilities(undefined));
    renderSidebar();

    await waitFor(() => expect(apiGet).toHaveBeenCalledWith("/capabilities"));
    expect(screen.queryByRole("link", { name: "modelMarketplace" })).not.toBeInTheDocument();
  });

  it("shows the marketplace when an ordinary user has the optional capability", async () => {
    window.localStorage.setItem("token", authToken(8, 1));
    apiGet.mockResolvedValueOnce(capabilities(true));
    renderSidebar();

    expect(await screen.findByRole("link", { name: "modelMarketplace" })).toHaveAttribute(
      "href",
      "/model-marketplace",
    );
  });

  it("keeps exactly one marketplace entry for administrators regardless of the capability", () => {
    window.localStorage.setItem("token", authToken(7, 2));
    apiGet.mockResolvedValueOnce(capabilities(false));
    renderSidebar();

    expect(screen.getAllByRole("link", { name: "modelMarketplace" })).toHaveLength(1);
  });

  it("does not expose admin A capability to ordinary B during an auth storage switch", async () => {
    let resolveBob!: (value: CapabilitiesResponse) => void;
    const bobCapabilities = new Promise<CapabilitiesResponse>((resolve) => { resolveBob = resolve; });
    const aliceToken = authToken(7, 2);
    const bobToken = authToken(8, 1);
    window.localStorage.setItem("token", aliceToken);
    apiGet
      .mockResolvedValueOnce(capabilities(true))
      .mockReturnValueOnce(bobCapabilities);
    const { queryClient } = renderSidebar();

    expect(await screen.findByRole("link", { name: "modelMarketplace" })).toBeInTheDocument();
    act(() => {
      window.localStorage.setItem("token", bobToken);
      window.dispatchEvent(new StorageEvent("storage", {
        key: "token",
        oldValue: aliceToken,
        newValue: bobToken,
      }));
    });

    expect(screen.queryByRole("link", { name: "modelMarketplace" })).not.toBeInTheDocument();
    expect(queryClient.getQueryData(["capabilities", { viewerId: 8 }])).toBeUndefined();
    resolveBob(capabilities(false));
    await waitFor(() => expect(apiGet).toHaveBeenCalledTimes(2));
    expect(screen.queryByRole("link", { name: "modelMarketplace" })).not.toBeInTheDocument();
  });
});
