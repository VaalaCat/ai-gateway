import { fireEvent, render, screen, within } from "@testing-library/react";
import { beforeEach, expect, it, vi } from "vitest";

import SystemMaintenancePage from "./page";

const mocks = vi.hoisted(() => ({
  refetchStats: vi.fn(),
  updateSettings: vi.fn(),
  toastSuccess: vi.fn(),
  toastError: vi.fn(),
  settings: {} as Record<string, string>,
}));

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
      tables: [
        { database: "core", name: "history_cursors", count: 10 },
        { database: "log", name: "history_cursors", count: 20 },
      ],
    },
    refetch: mocks.refetchStats,
    isLoading: false,
  }),
  useSettings: () => ({ data: { settings: mocks.settings } }),
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

vi.mock("@/components/system/data-cleanup-stats", () => ({
  DataCleanupStats: ({ tables }: { tables: unknown[] }) => (
    <div>dataCleanupStats:{tables.length}</div>
  ),
}));

function panelForTab(name: string) {
  const tab = screen.getByRole("tab", { name });
  const panelID = tab.getAttribute("aria-controls");
  const panel = panelID ? document.getElementById(panelID) : null;
  expect(panel).not.toBeNull();
  return panel as HTMLElement;
}

function billingLogRetentionInput() {
  const panel = within(panelForTab("tabs.policyBilling"));
  const label = panel.getByText("billingLogRetentionDays");
  const input = label.parentElement?.querySelector("input");
  expect(input).not.toBeNull();
  return input as HTMLInputElement;
}

function marketplaceMinSamplesInput() {
  const panel = within(panelForTab("tabs.policyBilling"));
  const label = panel.getByText("modelMarketplaceMinSamples");
  const input = label.parentElement?.querySelector("input");
  expect(input).not.toBeNull();
  return input as HTMLInputElement;
}

function marketplaceEnabledSwitch() {
  const panel = within(panelForTab("tabs.policyBilling"));
  const label = panel.getByText("modelMarketplaceEnabled");
  const row = label.parentElement?.parentElement;
  const control = row?.querySelector('[role="switch"]');
  expect(control).not.toBeNull();
  return control as HTMLButtonElement;
}

beforeEach(() => {
  navigation.pathname = "/system";
  navigation.query = "";
  navigation.replace.mockReset();
  navigation.replace.mockImplementation((url: string) => {
    navigation.query = url.split("?")[1] ?? "";
  });
  mocks.refetchStats.mockReset();
  mocks.updateSettings.mockReset();
  mocks.toastSuccess.mockReset();
  mocks.toastError.mockReset();
  mocks.settings = {};
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
  expect(maintenance.getByText("dataCleanupStats:2")).toBeInTheDocument();
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

it("saves a valid billing log retention draft", () => {
  mocks.settings = { "billing.log_retention_days": "14" };
  render(<SystemMaintenancePage />);

  const input = billingLogRetentionInput();
  expect(input).toHaveValue(14);
  fireEvent.change(input, { target: { value: "30" } });
  fireEvent.click(
    within(panelForTab("tabs.policyBilling")).getByRole("button", {
      name: "saveSettings",
    }),
  );

  expect(mocks.updateSettings).toHaveBeenCalledWith(
    { settings: { "billing.log_retention_days": "30" } },
    expect.objectContaining({
      onSuccess: expect.any(Function),
      onError: expect.any(Function),
    }),
  );
});

it.each(["", "0", "366", "1.5"])(
  "rejects invalid billing log retention value %j",
  (value) => {
    render(<SystemMaintenancePage />);

    fireEvent.change(billingLogRetentionInput(), { target: { value } });
    fireEvent.click(
      within(panelForTab("tabs.policyBilling")).getByRole("button", {
        name: "saveSettings",
      }),
    );

    expect(mocks.updateSettings).not.toHaveBeenCalled();
    expect(mocks.toastError).toHaveBeenCalledWith(
      "billingLogRetentionDaysRangeError",
    );
  },
);

it("loads the persisted billing log retention value and resets its draft after save", () => {
  mocks.settings = { "billing.log_retention_days": "45" };
  mocks.updateSettings.mockImplementation((payload, options) => {
    const response = {
      settings: { ...mocks.settings, ...payload.settings },
    };
    mocks.settings = response.settings;
    options.onSuccess(response);
  });
  render(<SystemMaintenancePage />);

  const input = billingLogRetentionInput();
  expect(input).toHaveValue(45);
  fireEvent.change(input, { target: { value: "90" } });
  fireEvent.click(
    within(panelForTab("tabs.policyBilling")).getByRole("button", {
      name: "saveSettings",
    }),
  );

  expect(input).toHaveValue(90);
  expect(mocks.toastSuccess).toHaveBeenCalledWith("settingsSaved");
});

it("shows disabled marketplace and twenty samples by default", () => {
  render(<SystemMaintenancePage />);

  expect(marketplaceEnabledSwitch()).toHaveAttribute("data-state", "unchecked");
  expect(marketplaceMinSamplesInput()).toHaveValue(20);
});

it("saves marketplace enablement and minimum samples together", () => {
  render(<SystemMaintenancePage />);

  fireEvent.click(marketplaceEnabledSwitch());
  fireEvent.change(marketplaceMinSamplesInput(), { target: { value: "50" } });
  fireEvent.click(
    within(panelForTab("tabs.policyBilling")).getByRole("button", {
      name: "saveSettings",
    }),
  );

  expect(mocks.updateSettings).toHaveBeenCalledWith(
    {
      settings: {
        model_marketplace_enabled: "true",
        model_marketplace_min_samples: "50",
      },
    },
    expect.objectContaining({
      onSuccess: expect.any(Function),
      onError: expect.any(Function),
    }),
  );
});

it.each(["", "0", "100001", "1.5"])(
  "rejects invalid marketplace minimum sample value %j",
  (value) => {
    render(<SystemMaintenancePage />);
    fireEvent.change(marketplaceMinSamplesInput(), { target: { value } });
    fireEvent.click(
      within(panelForTab("tabs.policyBilling")).getByRole("button", {
        name: "saveSettings",
      }),
    );

    expect(mocks.updateSettings).not.toHaveBeenCalled();
    expect(mocks.toastError).toHaveBeenCalledWith(
      "modelMarketplaceMinSamplesRangeError",
    );
  },
);

it.each(["1", "100000"])(
  "accepts marketplace minimum sample boundary %s",
  (value) => {
    render(<SystemMaintenancePage />);
    fireEvent.change(marketplaceMinSamplesInput(), { target: { value } });
    fireEvent.click(
      within(panelForTab("tabs.policyBilling")).getByRole("button", {
        name: "saveSettings",
      }),
    );

    expect(mocks.updateSettings).toHaveBeenCalledWith(
      { settings: { model_marketplace_min_samples: value } },
      expect.any(Object),
    );
  },
);
