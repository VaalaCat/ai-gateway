import { render, screen } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

import type { UserGroup } from "@/lib/types";

import GroupDetailPage from "./page";

const group: UserGroup = {
  id: 7,
  name: "Platform users",
  description: "Controls access for the platform team.",
  status: 1,
  created_at: 1,
  updated_at: 1,
  user_count: 3,
  models: "",
};

const queryState = vi.hoisted(() => ({
  data: undefined as UserGroup | undefined,
  isLoading: false,
  isError: false,
}));

vi.mock("next/navigation", () => ({
  useSearchParams: () => new URLSearchParams("id=7"),
  useRouter: () => ({ push: vi.fn(), replace: vi.fn() }),
}));
vi.mock("next-intl", () => ({
  useTranslations: () => (key: string) => ({
    title: "User Groups",
    userCount: "Members",
    overviewTab: "Overview",
    membersTab: "Members",
    notFound: "User group not found",
    noData: "No data",
    back: "Back",
    id: "ID",
  } as Record<string, string>)[key] ?? key,
}));
vi.mock("@/lib/api/user-groups", () => ({
  DEFAULT_GROUP_ID: 1,
  useUserGroup: () => queryState,
}));
vi.mock("@/components/business/status-badge", () => ({
  StatusBadge: ({ status }: { status: number }) => <span>Status {status}</span>,
}));
vi.mock("./overview-tab", () => ({ OverviewTab: () => <div>Overview content</div> }));
vi.mock("./members-tab", () => ({ MembersTab: () => <div>Members content</div> }));

describe("GroupDetailPage", () => {
  beforeEach(() => {
    queryState.data = group;
    queryState.isLoading = false;
    queryState.isError = false;
  });

  it("puts the group ID and member count in ready-state header metadata", () => {
    render(<GroupDetailPage />);

    const header = screen.getByTestId("page-header");
    expect(header).toHaveTextContent("Platform users");
    expect(header).toHaveTextContent("7");
    expect(header).toHaveTextContent("Members: 3");
    expect(header).toHaveTextContent("Status 1");
  });

  it("keeps a stable no-data header after a completed empty query", () => {
    queryState.data = undefined;
    render(<GroupDetailPage />);

    expect(screen.getByTestId("page-header")).toHaveTextContent("User Groups");
    expect(screen.getByRole("alert")).toHaveTextContent("No data");
    expect(screen.queryByLabelText("loading")).not.toBeInTheDocument();
  });

  it("keeps a stable header when the group query fails", () => {
    queryState.data = undefined;
    queryState.isError = true;
    render(<GroupDetailPage />);

    expect(screen.getByTestId("page-header")).toHaveTextContent("User Groups");
    expect(screen.getByRole("alert")).toHaveTextContent("User group not found");
  });
});
