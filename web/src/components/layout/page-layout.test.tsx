import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";

import { PageLayout } from "./page-layout";

describe("PageLayout footer", () => {
  it("does not render an action surface or reserve footer space when omitted", () => {
    render(
      <PageLayout title="Settings">
        <div>last field</div>
      </PageLayout>,
    );

    expect(screen.queryByTestId("page-layout-footer")).not.toBeInTheDocument();
    expect(screen.getByTestId("page-layout-content")).not.toHaveClass("pb-24");
  });

  it("keeps footer actions associated with their form without duplicate content padding", async () => {
    const submit = vi.fn((event: React.FormEvent<HTMLFormElement>) => event.preventDefault());
    const user = userEvent.setup();

    render(
      <PageLayout
        title="Settings"
        footer={<Button type="submit" form="settings-form">Save</Button>}
      >
        <form id="settings-form" onSubmit={submit}>
          <input aria-label="last field" />
        </form>
      </PageLayout>,
    );

    const content = screen.getByTestId("page-layout-content");
    const footer = screen.getByTestId("page-layout-footer");
    expect(content).not.toHaveClass("pb-24");
    expect(content.compareDocumentPosition(footer) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
    await user.click(screen.getByRole("button", { name: "Save" }));
    expect(submit).toHaveBeenCalledOnce();
  });

  it("uses a contained sticky surface without negative margins on desktop", () => {
    render(
      <PageLayout title="Settings" maxWidth="3xl" footer={<Button>Save</Button>}>
        <div />
      </PageLayout>,
    );

    const footer = screen.getByTestId("page-layout-footer");
    expect(footer).toHaveClass("sticky", "bottom-[var(--dashboard-bottom-nav-offset)]", "z-20", "max-w-3xl", "md:justify-end");
    expect(footer.className).not.toMatch(/-m[bx]-/);
  });

  it("wraps multiple mobile actions and accounts for safe area plus the navigation offset", () => {
    render(
      <PageLayout
        title="Settings"
        footer={<><Button>Cancel</Button><Button>Previous</Button><Button>Save</Button></>}
      >
        <div />
      </PageLayout>,
    );

    const footer = screen.getByTestId("page-layout-footer");
    expect(footer).toHaveClass("flex-wrap", "pb-[max(env(safe-area-inset-bottom),0.75rem)]");
    expect(footer).toHaveClass("max-sm:[&>[data-slot=button]]:flex-1");
  });
});

describe("PageLayout header composition", () => {
  it("delegates one accessible page heading without metadata in its name", () => {
    render(
      <PageLayout
        backAction={<Button>Back</Button>}
        title="Weather"
        description="Current gateway weather"
        metadata={<Badge>enabled</Badge>}
        actions={<Button>Refresh</Button>}
      >
        <div>content</div>
      </PageLayout>,
    );

    expect(screen.getAllByRole("heading", { level: 1 })).toHaveLength(1);
    expect(screen.getByRole("heading", { level: 1 })).toHaveAccessibleName(
      "Weather",
    );
    expect(screen.getByRole("button", { name: "Back" })).toBeInTheDocument();
    expect(screen.getByText("Current gateway weather")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Refresh" })).toBeInTheDocument();
  });
});
