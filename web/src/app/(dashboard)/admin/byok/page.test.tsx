import { render, screen } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

import type { BYOKChannelDetail } from "@/lib/api/byok-channels";
import AdminBYOKPage from "./page";

const baseChannel: BYOKChannelDetail = {
  id: 11,
  owner_id: 7,
  name: "Enabled BYOK",
  type: 1,
  key_last4: "1234",
  base_url: "https://example.com",
  models: ["gpt-5"],
  model_mapping: {},
  weight: 1,
  priority: 0,
  status: 1,
  supported_api_types: "",
  endpoints: "",
  passthrough_enabled: false,
  use_legacy_adaptor: false,
  organization: "",
  api_version: "",
  system_prompt: "",
  system_prompt_in_input: false,
  role_mapping: "",
  param_override: "",
  setting: "",
  tag: "",
  remark: "",
  test_model: "",
  auto_ban: 1,
  status_code_mapping: "",
  other_settings: "",
  created_at: 1,
  updated_at: 1,
};

const rows: BYOKChannelDetail[] = [
  baseChannel,
  { ...baseChannel, id: 12, name: "Manual BYOK", status: 0 },
  {
    ...baseChannel,
    id: 13,
    name: "Error BYOK",
    status: 0,
    auto_ban_state: { tripped: true, reason: "consecutive_errors", tripped_at: 100 },
  },
];

const state = vi.hoisted(() => ({ autoBanBadgeIds: [] as number[] }));

vi.mock("next-intl", () => ({ useTranslations: () => (key: string) => key }));
vi.mock("next/navigation", () => ({
  useRouter: () => ({ push: vi.fn(), replace: vi.fn() }),
  usePathname: () => "/admin/byok",
  useSearchParams: () => new URLSearchParams(),
}));
vi.mock("sonner", () => ({ toast: { success: vi.fn(), error: vi.fn() } }));
vi.mock("@/lib/api/byok-channels", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/lib/api/byok-channels")>();
  return {
    ...actual,
    useAdminBYOKChannels: () => ({ data: { data: rows, total: rows.length }, isLoading: false }),
    useBYOKSupportedTypes: () => ({ data: { types: [] } }),
    useDisableBYOKChannel: () => ({ mutateAsync: vi.fn() }),
  };
});
vi.mock("@/components/data-table/data-table", () => ({
  DataTable: ({ columns, data }: {
    columns: Array<{ accessorKey?: string; cell?: (props: { row: { original: BYOKChannelDetail } }) => React.ReactNode }>;
    data: BYOKChannelDetail[];
  }) => {
    const status = columns.find((column) => column.accessorKey === "status");
    return data.map((channel) => (
      <div key={channel.id} data-testid={`admin-byok-${channel.id}`}>
        {status?.cell?.({ row: { original: channel } })}
      </div>
    ));
  },
}));
vi.mock("@/components/business/status-badge", () => ({
  StatusBadge: ({ status }: { status: number }) => <span>{`status-${status}`}</span>,
}));
vi.mock("@/components/business/channel-auto-ban-badge", () => ({
  ChannelAutoBanBadge: ({ channel }: { channel: BYOKChannelDetail }) => {
    state.autoBanBadgeIds.push(channel.id);
    return <span data-testid={`auto-ban-${channel.id}`}>auto-ban</span>;
  },
}));
vi.mock("@/components/business/delete-confirm", () => ({ DeleteConfirm: () => null }));
vi.mock("@/components/business/entity-label", () => ({ EntityLabel: () => null }));
vi.mock("@/components/business/expanded-models-view", () => ({ ExpandedModelsView: () => null }));
vi.mock("@/components/business/model-name", () => ({ ModelName: ({ name }: { name: string }) => <span>{name}</span> }));

beforeEach(() => {
  state.autoBanBadgeIds = [];
});

describe("AdminBYOKPage channel status", () => {
  it("uses the shared auto-disable badge for enabled, manual-disabled, and error-disabled rows", () => {
    render(<AdminBYOKPage />);

    expect(state.autoBanBadgeIds).toEqual([11, 12, 13]);
    expect(screen.getByTestId("auto-ban-13").parentElement).toHaveClass("flex-wrap");
  });
});
