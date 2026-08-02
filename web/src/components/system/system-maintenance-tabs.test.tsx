import { fireEvent, render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, expect, it, vi } from "vitest";

import { SystemMaintenanceTabs } from "./system-maintenance-tabs";

const navigation = vi.hoisted(() => ({
  pathname: "/system",
  query: "",
  replace: vi.fn(),
}));

vi.mock("next/navigation", () => ({
  usePathname: () => navigation.pathname,
  useRouter: () => ({ replace: navigation.replace }),
  useSearchParams: () => new URLSearchParams(navigation.query),
}));

vi.mock("next-intl", () => ({
  useTranslations: () => (key: string) =>
    ({
      "tabs.label": "System maintenance sections",
      "tabs.overview": "Overview",
      "tabs.requestPath": "Request path",
      "tabs.policyBilling": "Policy & billing",
      "tabs.byok": "BYOK",
      "tabs.dataMaintenance": "Data maintenance",
    })[key] ?? key,
}));

function tabsContent() {
  return (
    <SystemMaintenanceTabs
      overview={<div>Overview content</div>}
      requestPath={<div>Request path content</div>}
      policyBilling={<div>Policy content</div>}
      byok={<input aria-label="BYOK draft" defaultValue="" />}
      dataMaintenance={<div>Data content</div>}
    />
  );
}

function renderTabs() {
  return render(tabsContent());
}

beforeEach(() => {
  navigation.pathname = "/system";
  navigation.query = "";
  navigation.replace.mockReset();
  navigation.replace.mockImplementation((url: string) => {
    navigation.query = url.split("?")[1] ?? "";
  });
  Object.defineProperty(HTMLElement.prototype, "scrollIntoView", {
    configurable: true,
    value: vi.fn(),
  });
});

it("renders the approved tab order with overview active by default", () => {
  renderTabs();

  const tabs = within(screen.getByRole("tablist")).getAllByRole("tab");
  expect(tabs.map((tab) => tab.textContent)).toEqual([
    "Overview",
    "Request path",
    "Policy & billing",
    "BYOK",
    "Data maintenance",
  ]);
  expect(tabs[0]).toHaveAttribute("data-state", "active");
  expect(
    tabs.slice(1).every((tab) => tab.getAttribute("data-state") === "inactive"),
  ).toBe(true);
});

it("uses a legal section query value and falls back to overview for an invalid one", () => {
  navigation.query = "section=data-maintenance";
  const { rerender } = renderTabs();
  expect(screen.getByRole("tab", { name: "Data maintenance" })).toHaveAttribute("data-state", "active");

  navigation.query = "section=unknown";
  rerender(tabsContent());
  expect(screen.getByRole("tab", { name: "Overview" })).toHaveAttribute("data-state", "active");
});

it("persists a tab switch while preserving unrelated query parameters", async () => {
  navigation.query = "model=gpt-5&view=compact";
  renderTabs();

  await userEvent.click(screen.getByRole("tab", { name: "Data maintenance" }));

  expect(navigation.replace).toHaveBeenCalledWith(
    "/system?model=gpt-5&view=compact&section=data-maintenance",
    { scroll: false },
  );
});

it("keeps the mobile tab strip inside its own horizontal scroll boundary", () => {
  renderTabs();

  const tablist = screen.getByRole("tablist");
  expect(tablist.parentElement).toHaveClass("min-w-0", "overflow-x-auto");
  expect(tablist).toHaveClass(
    "w-max",
    "min-w-full",
    "justify-start",
    "md:grid",
    "md:grid-cols-5",
  );
  for (const tab of within(tablist).getAllByRole("tab")) {
    expect(tab).toHaveClass("flex-none", "px-3", "md:flex-1");
  }
});

it("scrolls the focused mobile tab into view", () => {
  renderTabs();

  const tab = screen.getByRole("tab", { name: "Data maintenance" });
  const scrollIntoView = vi.fn();
  Object.defineProperty(tab, "scrollIntoView", { configurable: true, value: scrollIntoView });
  fireEvent.focus(tab);

  expect(scrollIntoView).toHaveBeenCalledWith({
    behavior: "smooth",
    block: "nearest",
    inline: "center",
  });
});

it("force-mounts every panel and preserves an unsaved BYOK draft across tab switches", async () => {
  const { rerender } = renderTabs();
  const user = userEvent.setup();

  for (const content of [
    "Overview content",
    "Request path content",
    "Policy content",
    "Data content",
  ]) {
    expect(screen.getByText(content)).toBeInTheDocument();
  }

  const panels = screen.getAllByRole("tabpanel", { hidden: true });
  expect(panels).toHaveLength(5);
  for (const panel of panels) {
    expect(panel).toHaveClass("min-w-0", "data-[state=inactive]:hidden");
  }

  await user.click(screen.getByRole("tab", { name: "BYOK" }));
  rerender(tabsContent());
  const draft = screen.getByRole("textbox", { name: "BYOK draft" });
  fireEvent.change(draft, { target: { value: "unsaved" } });
  await user.click(screen.getByRole("tab", { name: "Overview" }));
  rerender(tabsContent());
  await user.click(screen.getByRole("tab", { name: "BYOK" }));
  rerender(tabsContent());

  expect(screen.getByRole("textbox", { name: "BYOK draft" })).toHaveValue(
    "unsaved",
  );
});
