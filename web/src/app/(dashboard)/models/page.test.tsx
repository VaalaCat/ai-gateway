import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { beforeEach, expect, it, vi } from "vitest";

import type { ModelConfig } from "@/lib/types";
import ModelsPage from "./page";

const state = vi.hoisted(() => ({
  models: [] as ModelConfig[],
  update: vi.fn(),
  toastSuccess: vi.fn(),
  toastError: vi.fn(),
  sync: vi.fn(),
}));

vi.mock("next-intl", () => ({
  useTranslations: () => (key: string) => key,
}));
vi.mock("next/navigation", () => ({
  useRouter: () => ({ push: vi.fn(), replace: vi.fn() }),
  usePathname: () => "/models",
  useSearchParams: () => new URLSearchParams(),
}));
vi.mock("sonner", () => ({
  toast: { success: state.toastSuccess, error: state.toastError },
}));
vi.mock("@/hooks/use-mobile", () => ({ useIsMobile: () => false }));
vi.mock("@/components/data-table/data-table", () => ({
  DataTable: ({ columns, data, toolbar }: {
    columns: Array<{
      id?: string;
      cell?: (props: { row: { original: ModelConfig } }) => React.ReactNode;
    }>;
    data: ModelConfig[];
    toolbar: React.ReactNode;
  }) => {
    const actions = columns.find((column) => column.id === "actions");
    return (
      <section>
        {toolbar}
        {data.map((model) => (
          <div key={model.id}>
            <span>{model.model_name}</span>
            {actions?.cell?.({ row: { original: model } })}
          </div>
        ))}
      </section>
    );
  },
}));
vi.mock("@/components/data-table/filterable-toolbar", () => ({
  FilterableToolbar: ({ primaryAction }: { primaryAction: React.ReactNode }) => (
    <div>{primaryAction}</div>
  ),
}));
vi.mock("@/components/ui/dropdown-menu", () => ({
  DropdownMenu: ({ children }: { children: React.ReactNode }) => <div>{children}</div>,
  DropdownMenuTrigger: ({ children }: { children: React.ReactNode }) => <>{children}</>,
  DropdownMenuContent: ({ children }: { children: React.ReactNode }) => <div>{children}</div>,
  DropdownMenuItem: ({ children, onClick }: { children: React.ReactNode; onClick?: () => void }) => (
    <button type="button" onClick={onClick}>{children}</button>
  ),
}));
vi.mock("@/components/business/status-badge", () => ({ StatusBadge: () => null }));
vi.mock("@/components/business/status-select", () => ({
  StatusSelect: ({ value, onChange }: { value: string; onChange: (value: string) => void }) => (
    <button type="button" onClick={() => onChange(value === "1" ? "0" : "1")}>status:{value}</button>
  ),
}));
vi.mock("@/components/business/delete-confirm", () => ({ DeleteConfirm: () => null }));
vi.mock("@/components/business/date-cell", () => ({ DateCell: () => null }));
vi.mock("@/lib/api/models", () => ({
  useModels: () => ({ data: { data: state.models, total: state.models.length }, isLoading: false }),
  useUpdateModel: () => ({ mutateAsync: state.update, isPending: false }),
  useDeleteModel: () => ({ mutateAsync: vi.fn(), isPending: false }),
  useSyncModels: () => ({ mutateAsync: state.sync, isPending: false }),
}));

function model(overrides: Record<string, unknown> = {}): ModelConfig {
  return {
    id: 1,
    model_name: "gpt-test",
    input_price: 1,
    output_price: 2,
    cache_read_price: 0,
    cache_write_price: 0,
    status: 1,
    created_at: 1,
    updated_at: 1,
    synced_metadata: {
      display_name: "Synced name",
      description: "Synced description",
      provider: "openai",
      input_modalities: ["text", "image"],
      output_modalities: ["text"],
      context_length: 128000,
      max_output_tokens: 16384,
      supported_parameters: ["temperature"],
      tool_calling: true,
      structured_output: true,
      reasoning: true,
      prompt_cache: true,
    },
    metadata_override: {},
    ...overrides,
  } as ModelConfig;
}

function openEditDialog() {
  render(<ModelsPage />);
  fireEvent.click(screen.getByRole("button", { name: "edit" }));
}

beforeEach(() => {
  state.models = [model()];
  state.update.mockReset();
  state.update.mockResolvedValue(model());
  state.toastSuccess.mockReset();
  state.toastError.mockReset();
  state.sync.mockReset();
  state.sync.mockResolvedValue({ created: 0, removed: 0, metadata_updated: 0 });
});

it("recognizes explicit false zero and empty-list overrides as present", () => {
  state.models = [model({
    metadata_override: {
      tool_calling: false,
      context_length: 0,
      input_modalities: [],
    },
  })];
  openEditDialog();

  expect(screen.getByRole("checkbox", { name: "overrideToolCalling" })).toBeChecked();
  expect(screen.getByRole("switch", { name: "metadataToolCalling" })).not.toBeChecked();
  expect(screen.getByRole("checkbox", { name: "overrideContextLength" })).toBeChecked();
  expect(screen.getByLabelText("metadataContextLength")).toHaveValue(0);
  expect(screen.getByRole("checkbox", { name: "overrideInputModalities" })).toBeChecked();
  expect(screen.getByLabelText("metadataInputModalities")).toHaveValue("");
});

it("submits an explicitly opted-in false override", async () => {
  openEditDialog();

  fireEvent.click(screen.getByRole("checkbox", { name: "overrideReasoning" }));
  const reasoning = screen.getByRole("switch", { name: "metadataReasoning" });
  expect(reasoning).toBeChecked();
  fireEvent.click(reasoning);
  fireEvent.click(screen.getByRole("button", { name: "save" }));

  await waitFor(() => expect(state.update).toHaveBeenCalled());
  expect(state.update).toHaveBeenCalledWith(expect.objectContaining({
    id: 1,
    metadata_override: { reasoning: false },
  }));
  expect(state.update.mock.calls[0][0]).not.toHaveProperty("synced_metadata");
});

it("removes an unchecked field from metadata_override", async () => {
  state.models = [model({ metadata_override: { tool_calling: false, context_length: 42 } })];
  openEditDialog();

  fireEvent.click(screen.getByRole("checkbox", { name: "overrideToolCalling" }));
  fireEvent.click(screen.getByRole("button", { name: "save" }));

  await waitFor(() => expect(state.update).toHaveBeenCalled());
  expect(state.update.mock.calls[0][0].metadata_override).toEqual({ context_length: 42 });
});

it("shows synced bool number and list values when overrides are disabled", async () => {
  state.models = [model({
    metadata_override: {
      tool_calling: false,
      context_length: 42,
      input_modalities: ["audio"],
    },
  })];
  openEditDialog();

  fireEvent.click(screen.getByRole("checkbox", { name: "overrideToolCalling" }));
  fireEvent.click(screen.getByRole("checkbox", { name: "overrideContextLength" }));
  fireEvent.click(screen.getByRole("checkbox", { name: "overrideInputModalities" }));

  expect(screen.getByRole("switch", { name: "metadataToolCalling" })).toBeDisabled();
  expect(screen.getByRole("switch", { name: "metadataToolCalling" })).toBeChecked();
  expect(screen.getByLabelText("metadataContextLength")).toBeDisabled();
  expect(screen.getByLabelText("metadataContextLength")).toHaveValue(128000);
  expect(screen.getByLabelText("metadataInputModalities")).toBeDisabled();
  expect(screen.getByLabelText("metadataInputModalities")).toHaveValue("text, image");

  fireEvent.click(screen.getByRole("button", { name: "save" }));
  await waitFor(() => expect(state.update).toHaveBeenCalled());
  expect(state.update.mock.calls[0][0].metadata_override).toEqual({});
});

it("restores bool number and list override drafts when overrides are re-enabled", () => {
  state.models = [model({
    metadata_override: {
      tool_calling: false,
      context_length: 42,
      input_modalities: ["audio"],
    },
  })];
  openEditDialog();

  for (const name of ["overrideToolCalling", "overrideContextLength", "overrideInputModalities"]) {
    fireEvent.click(screen.getByRole("checkbox", { name }));
    fireEvent.click(screen.getByRole("checkbox", { name }));
  }

  expect(screen.getByRole("switch", { name: "metadataToolCalling" })).not.toBeChecked();
  expect(screen.getByLabelText("metadataContextLength")).toHaveValue(42);
  expect(screen.getByLabelText("metadataInputModalities")).toHaveValue("audio");
});

it("keeps the dialog open and reports a failed metadata update", async () => {
  state.update.mockRejectedValueOnce(new Error("update failed"));
  openEditDialog();
  fireEvent.click(screen.getByRole("checkbox", { name: "overridePromptCache" }));
  fireEvent.click(screen.getByRole("button", { name: "save" }));

  await waitFor(() => expect(state.toastError).toHaveBeenCalled());
  expect(screen.getByRole("dialog")).toBeInTheDocument();
});

it("reports a metadata source failure instead of claiming full sync success", async () => {
  state.sync.mockResolvedValueOnce({
    created: 1,
    removed: 0,
    metadata_updated: 0,
    metadata_source_error: "models.dev unavailable",
  });
  render(<ModelsPage />);

  fireEvent.click(screen.getByRole("button", { name: "syncFromChannels" }));

  await waitFor(() => expect(state.toastError).toHaveBeenCalledWith(
    "syncMetadataFailed",
    { description: "models.dev unavailable" },
  ));
  expect(state.toastSuccess).not.toHaveBeenCalled();
});

it("uses the shared page header without changing the model toolbar action", () => {
  render(<ModelsPage />);

  expect(screen.getAllByRole("heading", { level: 1 })).toHaveLength(1);
  expect(screen.getByRole("heading", { level: 1 })).toHaveClass("tracking-tight");
  expect(screen.getByTestId("page-header")).toBeInTheDocument();
  expect(screen.getByRole("button", { name: "syncFromChannels" })).toBeInTheDocument();
});
