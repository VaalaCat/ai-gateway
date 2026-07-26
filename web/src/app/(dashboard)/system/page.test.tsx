import { fireEvent, render, screen, within } from "@testing-library/react";
import { beforeEach, expect, it, vi } from "vitest";

import SystemMaintenancePage from "./page";

const mocks = vi.hoisted(() => ({
  refetchStats: vi.fn(),
  refetchPreview: vi.fn(),
  cleanup: vi.fn(),
  updateSettings: vi.fn(),
  toastSuccess: vi.fn(),
  toastError: vi.fn(),
}));

vi.mock("next-intl", () => ({
  useTranslations: () => (key: string) => key,
}));

vi.mock("sonner", () => ({
  toast: { success: mocks.toastSuccess, error: mocks.toastError },
}));

vi.mock("@/lib/api/system", () => ({
  useSystemStats: () => ({
    data: {
      system: {
        version: "test",
        go_version: "go1.test",
        uptime_sec: 60,
        online_agents: 2,
        memory_alloc: 1024,
        memory_sys: 2048,
        num_gc: 3,
        num_goroutine: 4,
      },
      tables: [{ name: "billing_logs", count: 10 }],
    },
    refetch: mocks.refetchStats,
    isLoading: false,
  }),
  useCleanupPreview: () => ({
    data: { total: 10, to_delete: 2, cutoff_unix: 1_700_000_000 },
    dataUpdatedAt: Date.now(),
    isFetching: false,
    refetch: mocks.refetchPreview,
  }),
  useCleanup: () => ({ mutate: mocks.cleanup }),
  useSettings: () => ({ data: { settings: {} } }),
  useUpdateSettings: () => ({
    mutate: mocks.updateSettings,
    isPending: false,
  }),
}));

vi.mock("@/components/system/agent-relay-settings", () => ({
  AgentRelaySettings: () => <div>agentRelaySettings</div>,
}));

vi.mock("@/components/system/byok-settings", () => ({
  BYOKSettingsCard: () => <div>byokSettings</div>,
}));

vi.mock("@/components/system/log-storage-status", () => ({
  LogStorageStatus: () => <div>logStorageStatus</div>,
}));

function panelForTab(name: string) {
  const tab = screen.getByRole("tab", { name });
  const panelID = tab.getAttribute("aria-controls");
  const panel = panelID ? document.getElementById(panelID) : null;
  expect(panel).not.toBeNull();
  return panel as HTMLElement;
}

beforeEach(() => {
  mocks.refetchStats.mockReset();
  mocks.refetchPreview.mockReset();
  mocks.cleanup.mockReset();
  mocks.updateSettings.mockReset();
  mocks.toastSuccess.mockReset();
  mocks.toastError.mockReset();
});

it("assigns the key fields to all five maintenance panels", () => {
  render(<SystemMaintenancePage />);

  expect(
    within(panelForTab("tabs.overview")).getByText("systemInfo"),
  ).toBeInTheDocument();

  const requestPath = within(panelForTab("tabs.requestPath"));
  expect(requestPath.getByText("agentRelaySettings")).toBeInTheDocument();
  expect(requestPath.getByText("resilienceDefaults")).toBeInTheDocument();
  expect(requestPath.getByText("secRateLimiter")).toBeInTheDocument();
  expect(requestPath.getByText("secAffinity")).toBeInTheDocument();
  expect(requestPath.getByText("secNetwork")).toBeInTheDocument();
  expect(requestPath.getByText("secImageInline")).toBeInTheDocument();
  expect(requestPath.queryByText("secQuotaGate")).not.toBeInTheDocument();

  const policyBilling = within(panelForTab("tabs.policyBilling"));
  expect(policyBilling.getByText("secQuotaGate")).toBeInTheDocument();
  expect(policyBilling.getByText("pricingSyncSettings")).toBeInTheDocument();
  expect(policyBilling.getByText("secBillingRebuild")).toBeInTheDocument();
  expect(policyBilling.getByText("secTrace")).toBeInTheDocument();
  expect(policyBilling.getByText("secRegistration")).toBeInTheDocument();
  expect(policyBilling.getByText("secTokenPermissions")).toBeInTheDocument();
  expect(policyBilling.getByText("secInvite")).toBeInTheDocument();
  expect(policyBilling.queryByText("secNetwork")).not.toBeInTheDocument();

  expect(
    within(panelForTab("tabs.byok")).getByText("byokSettings"),
  ).toBeInTheDocument();
  const maintenance = within(panelForTab("tabs.dataMaintenance"));
  expect(maintenance.getByText("logStorageStatus")).toBeInTheDocument();
  expect(maintenance.getByText("databaseStats")).toBeInTheDocument();
  expect(maintenance.getByText("dataCleanup")).toBeInTheDocument();
});

it("preserves a request-path draft after switching to policy and back", () => {
  render(<SystemMaintenancePage />);

  fireEvent.click(screen.getByRole("tab", { name: "tabs.requestPath" }));
  const proxyInput = within(
    panelForTab("tabs.requestPath"),
  ).getByPlaceholderText("proxyUrlPlaceholder");
  fireEvent.change(proxyInput, { target: { value: "http://proxy.test:8080" } });

  fireEvent.click(screen.getByRole("tab", { name: "tabs.policyBilling" }));
  fireEvent.click(screen.getByRole("tab", { name: "tabs.requestPath" }));

  expect(proxyInput).toHaveValue("http://proxy.test:8080");
});

it("saves a request-path draft from the policy tab with the existing payload shape", () => {
  render(<SystemMaintenancePage />);

  const proxyInput = within(
    panelForTab("tabs.requestPath"),
  ).getByPlaceholderText("proxyUrlPlaceholder");
  fireEvent.change(proxyInput, { target: { value: "http://proxy.test:8080" } });
  fireEvent.click(screen.getByRole("tab", { name: "tabs.policyBilling" }));
  fireEvent.click(
    within(panelForTab("tabs.policyBilling")).getByRole("button", {
      name: "saveSettings",
    }),
  );

  expect(mocks.updateSettings).toHaveBeenCalledTimes(1);
  expect(mocks.updateSettings).toHaveBeenCalledWith(
    { settings: { proxy_url: "http://proxy.test:8080" } },
    expect.objectContaining({
      onSuccess: expect.any(Function),
      onError: expect.any(Function),
    }),
  );
});

it("keeps the cross-tab draft when saving settings fails", () => {
  mocks.updateSettings.mockImplementation((_payload, options) => {
    options.onError();
  });
  render(<SystemMaintenancePage />);

  const requestPath = panelForTab("tabs.requestPath");
  const proxyInput = within(requestPath).getByPlaceholderText(
    "proxyUrlPlaceholder",
  );
  fireEvent.change(proxyInput, { target: { value: "http://retry.test:8080" } });
  fireEvent.click(screen.getByRole("tab", { name: "tabs.policyBilling" }));
  fireEvent.click(
    within(panelForTab("tabs.policyBilling")).getByRole("button", {
      name: "saveSettings",
    }),
  );

  expect(mocks.toastError).toHaveBeenCalledWith("settingsSaveFailed");
  fireEvent.click(screen.getByRole("tab", { name: "tabs.requestPath" }));
  expect(proxyInput).toHaveValue("http://retry.test:8080");
});

it("keeps an open cleanup confirmation dialog mounted when switching tabs", () => {
  render(<SystemMaintenancePage />);

  const overviewTab = screen.getByRole("tab", { name: "tabs.overview" });
  fireEvent.click(screen.getByRole("tab", { name: "tabs.dataMaintenance" }));
  const maintenance = within(panelForTab("tabs.dataMaintenance"));
  fireEvent.click(maintenance.getByRole("button", { name: "preview" }));
  fireEvent.click(maintenance.getByRole("button", { name: "executeCleanup" }));
  expect(screen.getByRole("alertdialog")).toBeInTheDocument();

  fireEvent.click(overviewTab);

  expect(screen.getByRole("alertdialog")).toBeInTheDocument();
});
