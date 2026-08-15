import { render, screen } from "@testing-library/react";
import { expect, it, vi } from "vitest";

import DashboardLayout from "./layout";

vi.mock("next-intl", () => ({ useTranslations: () => (key: string) => key }));
vi.mock("@/components/layout/sidebar", () => ({ AppSidebar: () => <aside /> }));
vi.mock("@/components/layout/header", () => ({ AppHeader: () => <header /> }));
vi.mock("@/components/ui/sidebar", () => ({
  SidebarProvider: ({ children }: { children: React.ReactNode }) => <>{children}</>,
  SidebarInset: ({ children }: { children: React.ReactNode }) => <section>{children}</section>,
}));

const pending = new Promise<never>(() => undefined);

function PendingPage(): React.ReactNode {
  throw pending;
}

it("keeps exactly one page heading while a dashboard child is suspended", () => {
  render(
    <DashboardLayout>
      <PendingPage />
    </DashboardLayout>,
  );

  expect(screen.getAllByRole("heading", { level: 1 })).toHaveLength(1);
});
