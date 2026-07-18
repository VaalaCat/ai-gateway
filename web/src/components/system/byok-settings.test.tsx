import { fireEvent, render, screen } from "@testing-library/react";
import { beforeEach, expect, it, vi } from "vitest";

import { BYOKSettingsCard } from "./byok-settings";

const mocks = vi.hoisted(() => ({
  settings: { settings: {} as Record<string, string> },
  update: { mutateAsync: vi.fn(), isPending: false },
}));

vi.mock("next-intl", () => ({ useTranslations: () => (key: string) => key }));
vi.mock("@/lib/api/system", () => ({
  useSettings: () => ({ data: mocks.settings }),
  useUpdateSettings: () => mocks.update,
}));
vi.mock("@/lib/api/byok-system-baseurls", () => ({
  useBYOKSystemBaseURLs: () => ({ data: { urls: [] } }),
  useBaseURLUsage: () => ({ data: undefined, isLoading: false }),
}));

function setSettings(maxChannels: string) {
  mocks.settings = {
    settings: {
      byok_enabled: "true",
      byok_max_channels_per_user: maxChannels,
      byok_billing_mode: "free",
      byok_service_fee_ratio: "0.1",
      byok_base_url_allowlist: "[]",
    },
  };
}

beforeEach(() => {
  setSettings("20");
  mocks.update.mutateAsync.mockReset().mockResolvedValue({});
});

it("resets the draft immediately when the authoritative BYOK baseline changes", () => {
  const { rerender } = render(<BYOKSettingsCard />);
  const maxChannels = screen.getByRole("spinbutton");
  fireEvent.change(maxChannels, { target: { value: "99" } });
  expect(maxChannels).toHaveValue(99);

  setSettings("30");
  rerender(<BYOKSettingsCard />);

  expect(screen.getByRole("spinbutton")).toHaveValue(30);
});
