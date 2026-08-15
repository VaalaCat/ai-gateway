import { render, screen } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

import EditAPIRolePage from "./page";

const state = vi.hoisted(() => ({
  searchParams: new URLSearchParams(),
  suspendRoleForm: false,
  pendingRoleForm: new Promise<never>(() => undefined),
}));

vi.mock("next-intl", () => ({ useTranslations: () => (key: string) => key }));
vi.mock("next/navigation", () => ({ useSearchParams: () => state.searchParams }));
vi.mock("../../_components/role-form", () => ({
  RoleForm: ({ mode }: { mode: { kind: string; id?: number } }) => {
    if (state.suspendRoleForm) throw state.pendingRoleForm;
    return <h1>{`${mode.kind}:${mode.id ?? ""}`}</h1>;
  },
  RoleFormPageSkeleton: ({ mode }: { mode: string }) => <h1>{`loading:${mode}`}</h1>,
}));

describe("EditAPIRolePage", () => {
  beforeEach(() => {
    state.searchParams = new URLSearchParams();
    state.suspendRoleForm = false;
  });

  it.each(["", "id=0", "id=-1", "id=1.5", "id=NaN"])("rejects invalid role id search params: %s", (query) => {
    state.searchParams = new URLSearchParams(query);
    render(<EditAPIRolePage />);
    expect(screen.getByText("roleNotFound")).toBeInTheDocument();
    expect(screen.queryByText(/^edit:/)).not.toBeInTheDocument();
  });

  it("passes a valid search-param role id into edit mode", () => {
    state.searchParams = new URLSearchParams("id=7");
    state.suspendRoleForm = true;
    const view = render(<EditAPIRolePage />);

    expect(screen.getByRole("heading", { level: 1 })).toHaveTextContent("loading:edit");

    state.suspendRoleForm = false;
    view.rerender(<EditAPIRolePage />);
    expect(screen.getByText("edit:7")).toBeInTheDocument();
    expect(screen.getAllByRole("heading", { level: 1 })).toHaveLength(1);
  });
});
