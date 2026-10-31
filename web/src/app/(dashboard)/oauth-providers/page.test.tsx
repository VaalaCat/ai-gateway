import { render, screen } from "@testing-library/react";
import type { ReactNode } from "react";
import { expect, it, vi } from "vitest";

import type * as ClientModule from "@/lib/api/client";
import { queryClientWrapper } from "@/test/render";
import OAuthProvidersPage from "./page";

const mocks = vi.hoisted(() => ({ get: vi.fn() }));
vi.mock("@/lib/api/client", async (importOriginal) => {
  const actual = await importOriginal<typeof ClientModule>();
  return { ...actual, api: { ...actual.api, get: mocks.get } };
});
vi.mock("next-intl", () => ({ useTranslations: () => (key: string) => key }));
vi.mock("next/navigation", () => ({
  useRouter: () => ({ replace: vi.fn() }),
  usePathname: () => "/oauth-providers",
  useSearchParams: () => new URLSearchParams(),
}));
vi.mock("@/components/layout/page-layout", () => ({ PageLayout: ({ children }: { children: ReactNode }) => <main>{children}</main> }));
vi.mock("@/components/business/entity-picker/entity-picker", () => ({ EntityPicker: () => <div /> }));
vi.mock("@/components/business/entity-label", () => ({
  EntityLabel: ({ entity, id }: { entity: string; id: number }) => <span>{entity}:{id}</span>,
}));

it("shows each provider's configured registration group in the real provider table", async () => {
  mocks.get.mockResolvedValue([{
    id: 12, name: "demo", display_name: "Demo provider", protocol: "oidc", enabled: true,
    authorization_endpoint: "https://idp.example/a", token_endpoint: "https://idp.example/t",
    userinfo_endpoint: "https://idp.example/u", client_id: "client", default_group_id: 7,
    created_at: 1, updated_at: 2,
  }]);
  render(<OAuthProvidersPage />, { wrapper: queryClientWrapper() });
  expect(await screen.findByText("user-group:7")).toBeInTheDocument();
  expect(screen.getByRole("columnheader", { name: "defaultGroup" })).toBeInTheDocument();
  expect(mocks.get).toHaveBeenCalledWith("/admin/oauth-providers");
});
