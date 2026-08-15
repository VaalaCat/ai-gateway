import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import type { APIService } from "@/lib/api/api-services";

import { useServiceColumns } from "./service-columns";

vi.mock("next-intl", () => ({ useTranslations: () => (key: string) => key }));

const service: APIService = { id: 7, slug: "weather", name: "Weather", description: "Forecast", price_per_call: 1, status: 1 };

function ServiceActions({ includeActions = true, canManage = true }: { includeActions?: boolean; canManage?: boolean }) {
  const actions = useServiceColumns({ includeActions, canManage: () => canManage, onToggleStatus: vi.fn(), onDelete: vi.fn() })
    .find((column) => column.id === "actions")?.cell;
  return <>{typeof actions === "function" ? actions({ row: { original: service } } as never) : null}</>;
}

function ServicePrice() {
  const cell = useServiceColumns({ includeActions: false, canManage: () => false, onToggleStatus: vi.fn(), onDelete: vi.fn() })
    .find((column) => "accessorKey" in column && column.accessorKey === "price_per_call")?.cell;
  const pricedService = { ...service, price_per_call: 100000 };
  return <>{typeof cell === "function" ? cell({ row: { original: pricedService } } as never) : null}</>;
}

describe("useServiceColumns", () => {
  it("formats the internal quota price as money", () => {
    render(<ServicePrice />);
    expect(screen.getByText("$ 1.00")).toBeInTheDocument();
    expect(screen.queryByText("100000")).not.toBeInTheDocument();
  });

  it("keeps View details in the Service three-dot menu", async () => {
    const user = userEvent.setup();
    render(<ServiceActions />);

    await user.click(screen.getByRole("button", { name: "actions" }));
    expect(screen.getByRole("menuitem", { name: "viewServiceDetails" })).toHaveAttribute("href", "/api-services/detail?id=7");
  });

  it("keeps the read-only row menu and hides every mutation item", async () => {
    const user = userEvent.setup();
    render(<ServiceActions includeActions={false} canManage={false} />);

    await user.click(screen.getByRole("button", { name: "actions" }));
    expect(screen.getByRole("menuitem", { name: "viewServiceDetails" })).toHaveAttribute("href", "/api-services/detail?id=7");
    expect(screen.queryByRole("menuitem", { name: "editService" })).not.toBeInTheDocument();
    expect(screen.queryByRole("menuitem", { name: "disableService" })).not.toBeInTheDocument();
    expect(screen.queryByRole("menuitem", { name: "deleteService" })).not.toBeInTheDocument();
  });
});
