import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";

import { ScriptForm } from "./script-form";

const { createScript, push, state, updateScript } = vi.hoisted(() => ({
  createScript: vi.fn(),
  push: vi.fn(),
  state: { existing: undefined as Record<string, unknown> | undefined },
  updateScript: vi.fn(),
}));

const selectedByEntity: Record<string, string[]> = {
  channel: ["1"],
  "byok-channel": ["2"],
  "user-group": ["3"],
  user: ["4"],
};

vi.mock("next/navigation", () => ({ useRouter: () => ({ push }) }));
vi.mock("next-intl", () => ({ useTranslations: () => (key: string) => key }));
vi.mock("sonner", () => ({ toast: { error: vi.fn(), success: vi.fn() } }));
vi.mock("@/lib/api/scripts", () => ({
  useScript: () => ({ data: state.existing }),
  useCreateScript: () => ({ isPending: false, mutateAsync: createScript }),
  useUpdateScript: () => ({ isPending: false, mutateAsync: updateScript }),
}));
vi.mock("@/components/business/entity-picker/entity-multi-picker", () => ({
  EntityMultiPicker: ({
    entity,
    onChange,
    scope,
    value,
  }: {
    entity: string;
    onChange: (value: string[]) => void;
    scope?: string;
    value: string[];
  }) => (
    <button
      type="button"
      data-entity={entity}
      data-scope={scope}
      data-value={value.join(",")}
      onClick={() => onChange(selectedByEntity[entity])}
    >
      {entity}
    </button>
  ),
}));
vi.mock("@/components/business/model-selector", () => ({
  ModelSelector: ({ onChange, value }: { onChange: (value: string[]) => void; value: string[] }) => (
    <button type="button" data-entity="model" data-value={value.join(",")} onClick={() => onChange(["gpt-5"])}>
      model
    </button>
  ),
}));
vi.mock("@/components/script/code-editor", () => ({
  CodeEditor: () => null,
}));
vi.mock("@/components/script/api-reference", () => ({
  ScriptApiReference: () => null,
}));
vi.mock("@/components/business/field-tip", () => ({ FieldTip: () => null }));

describe("ScriptForm scope", () => {
  beforeEach(() => {
    createScript.mockReset();
    createScript.mockResolvedValue({});
    updateScript.mockReset();
    updateScript.mockResolvedValue({});
    push.mockReset();
    state.existing = undefined;
  });

  it("submits all five OR scope fields", async () => {
    const user = userEvent.setup();
    render(<ScriptForm mode={{ kind: "create" }} />);

    await user.type(screen.getByPlaceholderText("trim-temperature"), "scope-test");
    for (const entity of ["channel", "byok-channel", "user-group", "user", "model"]) {
      await user.click(screen.getByRole("button", { name: entity }));
    }
    await user.click(screen.getByRole("button", { name: "save" }));

    await waitFor(() => expect(createScript).toHaveBeenCalledWith(expect.objectContaining({
      scope: {
        channel_ids: [1],
        private_channel_ids: [2],
        model_names: ["gpt-5"],
        group_ids: [3],
        user_ids: [4],
      },
    })));
    expect(screen.getByRole("button", { name: "byok-channel" })).toHaveAttribute("data-scope", "all");
  });

  it("hydrates all five fields exactly once", async () => {
    state.existing = {
      id: 7,
      name: "existing",
      code: "function onRequest() {}",
      enabled: true,
      priority: 8,
      scope: {
        channel_ids: [11],
        private_channel_ids: [12],
        model_names: ["qwen"],
        group_ids: [13],
        user_ids: [14],
      },
    };
    const view = render(<ScriptForm mode={{ kind: "edit", id: 7 }} />);

    await waitFor(() => expect(screen.getByRole("button", { name: "user" })).toHaveAttribute("data-value", "14"));
    expect(screen.getByRole("button", { name: "channel" })).toHaveAttribute("data-value", "11");
    expect(screen.getByRole("button", { name: "byok-channel" })).toHaveAttribute("data-value", "12");
    expect(screen.getByRole("button", { name: "user-group" })).toHaveAttribute("data-value", "13");
    expect(screen.getByRole("button", { name: "model" })).toHaveAttribute("data-value", "qwen");

    state.existing = {
      ...state.existing,
      scope: {
        channel_ids: [21],
        private_channel_ids: [22],
        model_names: ["changed"],
        group_ids: [23],
        user_ids: [24],
      },
    };
    view.rerender(<ScriptForm mode={{ kind: "edit", id: 7 }} />);

    expect(screen.getByRole("button", { name: "channel" })).toHaveAttribute("data-value", "11");
    expect(screen.getByRole("button", { name: "user" })).toHaveAttribute("data-value", "14");
  });

  it("submits empty arrays for a global scope", async () => {
    const user = userEvent.setup();
    render(<ScriptForm mode={{ kind: "create" }} />);

    await user.type(screen.getByPlaceholderText("trim-temperature"), "global-test");
    await user.click(screen.getByRole("button", { name: "save" }));

    await waitFor(() => expect(createScript).toHaveBeenCalledWith(expect.objectContaining({
      scope: {
        channel_ids: [],
        private_channel_ids: [],
        model_names: [],
        group_ids: [],
        user_ids: [],
      },
    })));
  });
});
