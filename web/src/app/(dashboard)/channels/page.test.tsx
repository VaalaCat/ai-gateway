import { act, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";

import type { Channel } from "@/lib/types";
import ChannelsPage from "./page";

const channelOne: Channel = {
  id: 1,
  name: "Internal Channel",
  public_display_name: "Public Channel",
  type: 1,
  key: "key-1",
  base_url: "https://example.com",
  models: "gpt-5",
  model_mapping: "",
  weight: 1,
  priority: 0,
  status: 1,
  setting: "",
  organization: "",
  api_version: "",
  tag: "",
  remark: "",
  test_model: "",
  auto_ban: 0,
  status_code_mapping: "",
  param_override: "",
  header_override: "",
  other_settings: "",
  supported_api_types: "",
  endpoints: "",
  passthrough_enabled: false,
  use_legacy_adaptor: false,
  system_prompt: "",
  proxy_url: "",
  role_mapping: "",
  created_at: 1,
  updated_at: 1,
};

const channelTwo: Channel = { ...channelOne, id: 2, name: "Internal Channel 2", public_display_name: "", status: 0 };
const channelThree: Channel = {
  ...channelTwo,
  id: 3,
  name: "Internal Channel 3",
  auto_ban_state: { tripped: true, reason: "consecutive_errors", tripped_at: 1_723_456_789 },
};

const state = vi.hoisted(() => ({
  channels: [] as Channel[],
  isLoading: false,
  isFetching: false,
  updateHookCalls: 0,
  inlineUpdate: { mutateAsync: vi.fn(), isPending: false },
  quickStatusUpdate: { mutateAsync: vi.fn(), isPending: false },
  toast: { success: vi.fn(), error: vi.fn() },
  router: { push: vi.fn() },
  autoBanBadgeChannels: [] as Channel[],
}));

vi.mock("next-intl", () => ({
  useTranslations: () => (key: string, values?: Record<string, string | number>) =>
    values?.count === undefined ? key : `${key} ${values.count}`,
}));
vi.mock("next/navigation", () => ({
  useRouter: () => ({ ...state.router, replace: vi.fn() }),
  usePathname: () => "/channels",
  useSearchParams: () => new URLSearchParams(),
}));
vi.mock("sonner", () => ({ toast: state.toast }));
vi.mock("@/lib/api/channels", () => ({
  useChannels: () => ({
    data: { data: state.channels, total: state.channels.length },
    isLoading: state.isLoading,
    isFetching: state.isFetching,
    refetch: vi.fn(),
  }),
  useChannelTypes: () => ({ data: [] }),
  useUpdateChannel: () => {
    const mutation = state.updateHookCalls % 2 === 0 ? state.inlineUpdate : state.quickStatusUpdate;
    state.updateHookCalls += 1;
    return mutation;
  },
  useDeleteChannel: () => ({ mutateAsync: vi.fn() }),
  useTestChannel: () => ({ mutateAsync: vi.fn() }),
}));
vi.mock("@/components/data-table/data-table", () => ({
  DataTable: ({ columns, data, toolbar, onPaginationChange, rowSelection, onRowSelectionChange }: {
    columns: Array<{ id?: string; accessorKey?: string; cell?: (props: { row: { original: Channel } }) => React.ReactNode }>;
    data: Channel[];
    toolbar: React.ReactNode;
    onPaginationChange?: (page: number, pageSize: number) => void;
    rowSelection?: Record<string, boolean>;
    onRowSelectionChange?: (next: Record<string, boolean>) => void;
  }) => {
    const name = columns.find((column) => column.accessorKey === "name");
    const status = columns.find((column) => column.accessorKey === "status");
    const actions = columns.find((column) => column.id === "actions");
    return (
      <section>
        {toolbar}
        <button type="button" onClick={() => onRowSelectionChange?.(Object.fromEntries(data.map(({ id }) => [id, true])))}>
          select-current-page
        </button>
        <button type="button" onClick={() => onRowSelectionChange?.({ 1: true })}>select-channel-1</button>
        <button type="button" onClick={() => onRowSelectionChange?.({ 2: true })}>select-channel-2</button>
        <button type="button" onClick={() => onPaginationChange?.(2, 10)}>next-page</button>
        <output data-testid="selection">{Object.keys(rowSelection ?? {}).filter((id) => rowSelection?.[id]).join(",")}</output>
        {data.map((channel) => (
          <div key={channel.id} data-testid={`channel-${channel.id}`}>
            {name?.cell?.({ row: { original: channel } })}
            {status?.cell?.({ row: { original: channel } })}
            {actions?.cell?.({ row: { original: channel } })}
          </div>
        ))}
      </section>
    );
  },
}));
vi.mock("@/components/data-table/filterable-toolbar", () => ({
  FilterableToolbar: ({ primaryAction, onChange }: { primaryAction: React.ReactNode; onChange: (next: { search: string }) => void }) => (
    <div>
      <button type="button" onClick={() => onChange({ search: "edge" })}>change-filter</button>
      {primaryAction}
    </div>
  ),
}));
vi.mock("@/components/channel/channel-batch-edit-dialog", () => ({
  ChannelBatchEditDialog: ({ ids, open, onSucceeded }: { ids: number[]; open: boolean; onSucceeded: (response: { updated_count: number; updated_ids: number[] }) => void }) => open ? (
    <div data-testid="batch-dialog">
      {ids.join(",")}
      <button type="button" onClick={() => onSucceeded({ updated_count: ids.length, updated_ids: ids })}>batch-succeeded</button>
      <button type="button" onClick={() => onSucceeded({ updated_count: 1, updated_ids: [1] })}>old-batch-1-succeeded</button>
      <button type="button">batch-failed</button>
    </div>
  ) : null,
}));
vi.mock("@/components/business/status-badge", () => ({ StatusBadge: ({ status }: { status: number }) => <span data-testid={`status-${status}`}>{status}</span> }));
vi.mock("@/components/business/channel-limit-badge", () => ({ ChannelLimitBadge: () => null }));
vi.mock("@/components/business/channel-auto-ban-badge", () => ({
  ChannelAutoBanBadge: ({ channel }: { channel: Channel }) => {
    state.autoBanBadgeChannels.push(channel);
    return <span data-testid={`auto-ban-${channel.id}`}>auto-ban</span>;
  },
}));
vi.mock("@/components/business/channel-billing-badge", () => ({ ChannelBillingBadge: () => null }));
vi.mock("@/components/business/delete-confirm", () => ({ DeleteConfirm: () => null }));
vi.mock("@/components/business/channel-transfer-dialogs", () => ({ ChannelExportDialog: () => null, ChannelImportDialog: () => null }));
vi.mock("@/components/business/expanded-models-view", () => ({ ExpandedModelsView: () => null }));
vi.mock("@/components/business/model-name", () => ({ ModelName: ({ name }: { name: string }) => <span>{name}</span> }));
vi.mock("@/components/business/inline-edit", () => ({ InlineEdit: () => null }));
vi.mock("@/components/business/channel-test-dialog", () => ({ ChannelTestDialog: () => null }));

beforeEach(() => {
  state.channels = [channelOne, channelTwo];
  state.autoBanBadgeChannels = [];
  state.isLoading = false;
  state.isFetching = false;
  state.updateHookCalls = 0;
  state.inlineUpdate.mutateAsync.mockReset();
  state.inlineUpdate.mutateAsync.mockResolvedValue(channelOne);
  state.inlineUpdate.isPending = false;
  state.quickStatusUpdate.mutateAsync.mockReset();
  state.quickStatusUpdate.mutateAsync.mockResolvedValue(channelOne);
  state.quickStatusUpdate.isPending = false;
  state.toast.success.mockReset();
  state.toast.error.mockReset();
  state.router.push.mockReset();
});

describe("ChannelsPage batch actions and quick status", () => {
  it("passes enabled, manual-disabled, and error-disabled rows through one wrapping status cell", () => {
    state.channels = [channelOne, channelTwo, channelThree];

    render(<ChannelsPage />);

    expect(state.autoBanBadgeChannels.map(({ id }) => id)).toEqual([1, 2, 3]);
    expect(screen.getByTestId("auto-ban-3").parentElement).toHaveClass("flex-wrap");
  });

  it("keeps the batch bar hidden without a selected row", () => {
    render(<ChannelsPage />);

    expect(screen.queryByRole("button", { name: "batchEdit" })).not.toBeInTheDocument();
  });

  it("shows the selected current-page count and opens the batch dialog", async () => {
    const user = userEvent.setup();
    render(<ChannelsPage />);

    await user.click(screen.getByRole("button", { name: "select-current-page" }));
    expect(screen.getByRole("button", { name: "batchEdit" })).toBeInTheDocument();
    expect(screen.getByText("selectedChannels 2")).toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: "batchEdit" }));
    expect(screen.getByTestId("batch-dialog")).toHaveTextContent("1,2");
  });

  it("clears selection immediately from the explicit shadcn action", async () => {
    const user = userEvent.setup();
    render(<ChannelsPage />);

    await user.click(screen.getByRole("button", { name: "select-current-page" }));
    await user.click(screen.getByRole("button", { name: "clearSelection" }));

    expect(screen.getByTestId("selection")).toBeEmptyDOMElement();
    expect(screen.queryByRole("button", { name: "batchEdit" })).not.toBeInTheDocument();
  });

  it("selects every row on the rendered page without inventing off-page ids", async () => {
    const user = userEvent.setup();
    render(<ChannelsPage />);

    await user.click(screen.getByRole("button", { name: "select-current-page" }));

    expect(screen.getByTestId("selection")).toHaveTextContent("1,2");
    expect(screen.getByTestId("selection")).not.toHaveTextContent("3");
  });

  it("clears selection when pagination changes", async () => {
    const user = userEvent.setup();
    render(<ChannelsPage />);

    await user.click(screen.getByRole("button", { name: "select-current-page" }));
    await user.click(screen.getByRole("button", { name: "next-page" }));

    expect(screen.getByTestId("selection")).toBeEmptyDOMElement();
    expect(screen.queryByRole("button", { name: "batchEdit" })).not.toBeInTheDocument();
  });

  it("clears selection when filters change", async () => {
    const user = userEvent.setup();
    render(<ChannelsPage />);

    await user.click(screen.getByRole("button", { name: "select-current-page" }));
    await user.click(screen.getByRole("button", { name: "change-filter" }));

    expect(screen.getByTestId("selection")).toBeEmptyDOMElement();
  });

  it("clears selection only after the batch dialog reports success", async () => {
    const user = userEvent.setup();
    render(<ChannelsPage />);

    await user.click(screen.getByRole("button", { name: "select-current-page" }));
    await user.click(screen.getByRole("button", { name: "batchEdit" }));
    await user.click(screen.getByRole("button", { name: "batch-succeeded" }));

    expect(screen.getByTestId("selection")).toBeEmptyDOMElement();
  });

  it("keeps selection when the batch dialog fails without reporting success", async () => {
    const user = userEvent.setup();
    render(<ChannelsPage />);

    await user.click(screen.getByRole("button", { name: "select-current-page" }));
    await user.click(screen.getByRole("button", { name: "batchEdit" }));
    await user.click(screen.getByRole("button", { name: "batch-failed" }));

    expect(screen.getByTestId("selection")).toHaveTextContent("1,2");
    expect(screen.getByRole("button", { name: "batchEdit" })).toBeInTheDocument();
  });

  it("removes only the IDs reported by an older successful batch session", async () => {
    const user = userEvent.setup();
    render(<ChannelsPage />);

    await user.click(screen.getByRole("button", { name: "select-channel-1" }));
    await user.click(screen.getByRole("button", { name: "batchEdit" }));
    await user.click(screen.getByRole("button", { name: "select-channel-2" }));
    await user.click(screen.getByRole("button", { name: "old-batch-1-succeeded" }));

    expect(screen.getByTestId("selection")).toHaveTextContent("2");
    expect(screen.getByRole("button", { name: "batchEdit" })).toBeInTheDocument();
  });

  it("clears all selection after an external refresh removes one selected ID", async () => {
    const user = userEvent.setup();
    const view = render(<ChannelsPage />);
    await user.click(screen.getByRole("button", { name: "select-current-page" }));

    state.channels = [channelTwo];
    view.rerender(<ChannelsPage />);

    expect(screen.getByTestId("selection")).toBeEmptyDOMElement();
  });

  it("keeps selection during refresh and clears it only after the missing-ID result settles", async () => {
    const user = userEvent.setup();
    const view = render(<ChannelsPage />);
    await user.click(screen.getByRole("button", { name: "select-channel-1" }));

    state.channels = [];
    state.isFetching = true;
    view.rerender(<ChannelsPage />);
    expect(screen.getByTestId("selection")).toHaveTextContent("1");

    state.isFetching = false;
    view.rerender(<ChannelsPage />);
    expect(screen.getByTestId("selection")).toBeEmptyDOMElement();
  });

  it("preserves selection when every selected ID remains on the refreshed page", async () => {
    const user = userEvent.setup();
    const view = render(<ChannelsPage />);
    await user.click(screen.getByRole("button", { name: "select-current-page" }));

    state.channels = [{ ...channelOne, tag: "fresh" }, { ...channelTwo, tag: "fresh" }];
    view.rerender(<ChannelsPage />);

    expect(screen.getByTestId("selection")).toHaveTextContent("1,2");
  });

  it("offers disable for an enabled channel and sends a non-optimistic status update", async () => {
    const user = userEvent.setup();
    render(<ChannelsPage />);

    await user.click(screen.getAllByRole("button", { name: "actions" })[0]);
    await user.click(screen.getByRole("menuitem", { name: "disableChannel" }));

    expect(state.quickStatusUpdate.mutateAsync).toHaveBeenCalledWith({ id: 1, status: 0 });
    expect(state.inlineUpdate.mutateAsync).not.toHaveBeenCalled();
    expect(screen.getByTestId("status-1")).toBeInTheDocument();
  });

  it("keeps the row pending until refreshed server state reverses the next action", async () => {
    let resolve!: (channel: Channel) => void;
    state.quickStatusUpdate.mutateAsync.mockImplementationOnce(() => new Promise((done) => { resolve = done; }));
    const user = userEvent.setup();
    const view = render(<ChannelsPage />);

    await user.click(screen.getAllByRole("button", { name: "actions" })[0]);
    await user.click(screen.getByRole("menuitem", { name: "disableChannel" }));
    await user.click(screen.getAllByRole("button", { name: "actions" })[0]);
    expect(screen.getByRole("menuitem", { name: "channelStatusUpdating" })).toHaveAttribute("data-disabled");

    await user.keyboard("{Escape}");
    const updated = { ...channelOne, status: 0 };
    state.channels = [updated, channelTwo];
    await act(async () => resolve(updated));
    view.rerender(<ChannelsPage />);
    await user.click(screen.getAllByRole("button", { name: "actions" })[0]);

    expect(screen.getByRole("menuitem", { name: "enableChannel" })).not.toHaveAttribute("data-disabled");
  });

  it("clears a selected filtered row after quick toggle refresh removes it", async () => {
    let resolve!: (channel: Channel) => void;
    state.quickStatusUpdate.mutateAsync.mockImplementationOnce(() => new Promise((done) => { resolve = done; }));
    const user = userEvent.setup();
    const view = render(<ChannelsPage />);
    await user.click(screen.getByRole("button", { name: "select-channel-1" }));
    await user.click(screen.getAllByRole("button", { name: "actions" })[0]);
    await user.click(screen.getByRole("menuitem", { name: "disableChannel" }));

    const updated = { ...channelOne, status: 0 };
    state.channels = [channelTwo];
    await act(async () => resolve(updated));
    view.rerender(<ChannelsPage />);

    expect(screen.getByTestId("selection")).toBeEmptyDOMElement();
  });

  it("offers enable for a disabled channel", async () => {
    const user = userEvent.setup();
    render(<ChannelsPage />);

    await user.click(screen.getAllByRole("button", { name: "actions" })[1]);

    expect(screen.getByRole("menuitem", { name: "enableChannel" })).toBeInTheDocument();
  });

  it("keeps each concurrently pending row disabled until its own out-of-order completion", async () => {
    const pending = new Map<number, { resolve: (channel: Channel) => void; reject: (error: Error) => void }>();
    state.quickStatusUpdate.mutateAsync.mockImplementation(({ id }: { id: number }) => new Promise<Channel>((resolve, reject) => {
      pending.set(id, { resolve, reject });
    }));
    const user = userEvent.setup();
    render(<ChannelsPage />);

    await user.click(screen.getAllByRole("button", { name: "actions" })[0]);
    await user.click(screen.getByRole("menuitem", { name: "disableChannel" }));
    await user.click(screen.getAllByRole("button", { name: "actions" })[1]);
    await user.click(screen.getByRole("menuitem", { name: "enableChannel" }));

    await user.click(screen.getAllByRole("button", { name: "actions" })[0]);
    expect(screen.getByRole("menuitem", { name: "channelStatusUpdating" })).toHaveAttribute("data-disabled");
    await user.keyboard("{Escape}");
    await user.click(screen.getAllByRole("button", { name: "actions" })[1]);
    expect(screen.getByRole("menuitem", { name: "channelStatusUpdating" })).toHaveAttribute("data-disabled");
    expect(state.quickStatusUpdate.mutateAsync).toHaveBeenCalledTimes(2);

    await user.keyboard("{Escape}");
    await act(async () => pending.get(2)?.resolve(channelTwo));
    await user.click(screen.getAllByRole("button", { name: "actions" })[0]);
    expect(screen.getByRole("menuitem", { name: "channelStatusUpdating" })).toHaveAttribute("data-disabled");
    await user.keyboard("{Escape}");
    await act(async () => pending.get(1)?.reject(new Error("network unavailable")));
    await user.click(screen.getAllByRole("button", { name: "actions" })[0]);
    expect(screen.getByRole("menuitem", { name: "disableChannel" })).not.toHaveAttribute("data-disabled");
    expect(state.quickStatusUpdate.mutateAsync).toHaveBeenCalledTimes(2);
  });

  it("keeps the displayed status after a failed quick toggle and reports the error", async () => {
    state.quickStatusUpdate.mutateAsync.mockRejectedValueOnce(new Error("network unavailable"));
    const user = userEvent.setup();
    render(<ChannelsPage />);

    await user.click(screen.getAllByRole("button", { name: "actions" })[0]);
    await user.click(screen.getByRole("menuitem", { name: "disableChannel" }));

    expect(await screen.findByTestId("status-1")).toBeInTheDocument();
    expect(state.toast.error).toHaveBeenCalledWith("network unavailable");
  });

  it("renders a public display name under its internal name without an empty placeholder", () => {
    render(<ChannelsPage />);

    const publicName = screen.getByText("Public Channel");
    expect(publicName.parentElement).toHaveTextContent("Internal Channel");
    expect(screen.getByTestId("channel-2")).not.toHaveTextContent("publicDisplayNameNotSet");
  });
});
