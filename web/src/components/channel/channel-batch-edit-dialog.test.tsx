import { act, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { useState } from "react";
import { beforeEach, describe, expect, it, vi } from "vitest";

import { ChannelBatchEditDialog, normalizeBatchEditIDs } from "./channel-batch-edit-dialog";

const mutation = vi.hoisted(() => ({
  mutateAsync: vi.fn(),
  isPending: false,
}));

vi.mock("@/lib/api/channels", () => ({
  useBatchEditChannels: () => mutation,
}));
vi.mock("next-intl", () => ({
  useTranslations: () => (key: string, values?: Record<string, string | number>) => {
    const messages: Record<string, string> = {
      publicDisplayName: "公开名称",
      tag: "渠道标签",
      free: "免费渠道",
      priority: "优先级",
      batchEditFieldAffinity: "亲和性",
      batchEditFieldStatus: "状态",
      modelMapping: "模型映射",
      roleMapping: "角色映射",
      usageLimit: "用量限制",
      resilienceOverride: "弹性策略",
      sectionEndpoints: "端点配置",
      passthroughEnabled: "透传请求",
      fieldSystemPromptInInput: "系统提示注入",
      otherSettings: "其他设置",
      apiTypeChatCompletion: "Chat Completions",
      batchEditUpdateField: "修改此字段：{field}",
      batchEditApply: "应用到 {count} 个渠道",
      batchEditApplying: "正在应用…",
      batchEditFailed: "批量编辑失败",
      batchEditInvalidId: "渠道 ID 必须是正整数",
      batchEditMaxIds: "一次最多编辑 {limit} 个渠道",
      publicDisplayNameTooLong: "公开名称最多 64 个字符",
      publicDisplayNameControlCharacters: "公开名称不能包含控制字符",
      publicDisplayNameAutoPreview: "留空将恢复安全自动名称",
      noBatchFieldSelected: "请至少选择一个要修改的字段",
    };
    return (messages[key] ?? key).replace(/\{(\w+)\}/g, (_, name) => String(values?.[name] ?? `{${name}}`));
  },
}));
vi.mock("sonner", () => ({
  toast: { error: vi.fn(), success: vi.fn() },
}));

describe("ChannelBatchEditDialog", () => {
  beforeEach(() => {
    mutation.mutateAsync.mockReset();
    mutation.isPending = false;
  });

  it("submits an explicitly selected false boolean", async () => {
    mutation.mutateAsync.mockResolvedValueOnce({ updated_count: 2, updated_ids: [1, 2] });
    const user = userEvent.setup();
    render(<ChannelBatchEditDialog ids={[1, 2]} open onOpenChange={vi.fn()} onSucceeded={vi.fn()} />);

    await user.click(screen.getByLabelText("修改此字段：免费渠道"));
    await user.click(screen.getByRole("switch", { name: "免费渠道" }));
    await user.click(screen.getByRole("button", { name: "应用到 2 个渠道" }));

    expect(mutation.mutateAsync).toHaveBeenCalledWith({ ids: [1, 2], fields: { free: false } });
  });

  it("normalizes selected target ids into deterministic unique positive ids", async () => {
    mutation.mutateAsync.mockResolvedValueOnce({ updated_count: 2, updated_ids: [1, 2] });
    const user = userEvent.setup();
    render(<ChannelBatchEditDialog ids={[2, 1, 2]} open onOpenChange={vi.fn()} onSucceeded={vi.fn()} />);

    await user.click(screen.getByLabelText("修改此字段：渠道标签"));
    await user.click(screen.getByRole("button", { name: "应用到 2 个渠道" }));

    expect(mutation.mutateAsync).toHaveBeenCalledWith({ ids: [1, 2], fields: { tag: "" } });
  });

  it.each([
    [[0], "渠道 ID 必须是正整数"],
    [[-1], "渠道 ID 必须是正整数"],
    [[1.5], "渠道 ID 必须是正整数"],
    [Array.from({ length: 501 }, (_, index) => index + 1), "一次最多编辑 500 个渠道"],
  ] as const)("rejects invalid batch ids %p without calling the mutation", async (ids, message) => {
    const user = userEvent.setup();
    render(<ChannelBatchEditDialog ids={[...ids]} open onOpenChange={vi.fn()} onSucceeded={vi.fn()} />);

    await user.click(screen.getByLabelText("修改此字段：渠道标签"));
    await user.click(screen.getByRole("button", { name: `应用到 ${ids.length} 个渠道` }));

    expect(await screen.findByRole("alert")).toHaveTextContent(message);
    expect(mutation.mutateAsync).not.toHaveBeenCalled();
  });

  it("accepts exactly 500 normalized target ids", () => {
    expect(normalizeBatchEditIDs(Array.from({ length: 500 }, (_, index) => index + 1))).toEqual({
      ids: Array.from({ length: 500 }, (_, index) => index + 1),
    });
  });

  it.each([
    ["优先级", "0", { priority: 0 }],
    ["渠道标签", "", { tag: "" }],
  ] as const)("submits the selected %s zero or empty value", async (label, value, expected) => {
    mutation.mutateAsync.mockResolvedValueOnce({ updated_count: 2, updated_ids: [1, 2] });
    const user = userEvent.setup();
    render(<ChannelBatchEditDialog ids={[1, 2]} open onOpenChange={vi.fn()} onSucceeded={vi.fn()} />);

    await user.click(screen.getByLabelText(`修改此字段：${label}`));
    const input = screen.getByRole(label === "优先级" ? "spinbutton" : "textbox", { name: label });
    await user.clear(input);
    if (value) await user.type(input, value);
    await user.click(screen.getByRole("button", { name: "应用到 2 个渠道" }));

    expect(mutation.mutateAsync).toHaveBeenCalledWith({ ids: [1, 2], fields: expected });
  });

  it("submits an explicitly selected empty object", async () => {
    mutation.mutateAsync.mockResolvedValueOnce({ updated_count: 2, updated_ids: [1, 2] });
    const user = userEvent.setup();
    render(<ChannelBatchEditDialog ids={[1, 2]} open onOpenChange={vi.fn()} onSucceeded={vi.fn()} />);

    await user.click(screen.getByLabelText("修改此字段：亲和性"));
    await user.click(screen.getByRole("button", { name: "应用到 2 个渠道" }));

    expect(mutation.mutateAsync).toHaveBeenCalledWith({ ids: [1, 2], fields: { affinity: {} } });
  });

  it("trims a valid public display name before submitting it", async () => {
    mutation.mutateAsync.mockResolvedValueOnce({ updated_count: 2, updated_ids: [1, 2] });
    const user = userEvent.setup();
    render(<ChannelBatchEditDialog ids={[1, 2]} open onOpenChange={vi.fn()} onSucceeded={vi.fn()} />);

    await user.click(screen.getByLabelText("修改此字段：公开名称"));
    await user.type(screen.getByRole("textbox", { name: "公开名称" }), "  Public name  ");
    await user.click(screen.getByRole("button", { name: "应用到 2 个渠道" }));

    expect(mutation.mutateAsync).toHaveBeenCalledWith({
      ids: [1, 2],
      fields: { public_display_name: "Public name" },
    });
  });

  it("submits a public display name containing exactly 64 emoji", async () => {
    mutation.mutateAsync.mockResolvedValueOnce({ updated_count: 2, updated_ids: [1, 2] });
    const value = "😀".repeat(64);
    const user = userEvent.setup();
    render(<ChannelBatchEditDialog ids={[1, 2]} open onOpenChange={vi.fn()} onSucceeded={vi.fn()} />);

    await user.click(screen.getByLabelText("修改此字段：公开名称"));
    const input = screen.getByRole("textbox", { name: "公开名称" });
    fireEvent.change(input, { target: { value } });

    expect(input).toHaveValue(value);
    await user.click(screen.getByRole("button", { name: "应用到 2 个渠道" }));

    expect(mutation.mutateAsync).toHaveBeenCalledWith({
      ids: [1, 2],
      fields: { public_display_name: value },
    });
  });

  it("rejects a public display name containing 65 emoji without calling the mutation", async () => {
    const user = userEvent.setup();
    render(<ChannelBatchEditDialog ids={[1, 2]} open onOpenChange={vi.fn()} onSucceeded={vi.fn()} />);

    await user.click(screen.getByLabelText("修改此字段：公开名称"));
    fireEvent.change(screen.getByRole("textbox", { name: "公开名称" }), {
      target: { value: "😀".repeat(65) },
    });
    await user.click(screen.getByRole("button", { name: "应用到 2 个渠道" }));

    expect(await screen.findByRole("alert")).toHaveTextContent("公开名称最多 64 个字符");
    expect(mutation.mutateAsync).not.toHaveBeenCalled();
  });

  it.each([
    ["x".repeat(65), "公开名称最多 64 个字符"],
    ["invalid\u0001name", "公开名称不能包含控制字符"],
  ])("rejects invalid public display name %p", async (value, message) => {
    const user = userEvent.setup();
    render(<ChannelBatchEditDialog ids={[1, 2]} open onOpenChange={vi.fn()} onSucceeded={vi.fn()} />);

    await user.click(screen.getByLabelText("修改此字段：公开名称"));
    fireEvent.change(screen.getByRole("textbox", { name: "公开名称" }), { target: { value } });
    await user.click(screen.getByRole("button", { name: "应用到 2 个渠道" }));

    expect(await screen.findByRole("alert")).toHaveTextContent(message);
    expect(mutation.mutateAsync).not.toHaveBeenCalled();
  });

  it("keeps a trimmed empty public display name as an explicit clear", async () => {
    mutation.mutateAsync.mockResolvedValueOnce({ updated_count: 2, updated_ids: [1, 2] });
    const user = userEvent.setup();
    render(<ChannelBatchEditDialog ids={[1, 2]} open onOpenChange={vi.fn()} onSucceeded={vi.fn()} />);

    await user.click(screen.getByLabelText("修改此字段：公开名称"));
    await user.type(screen.getByRole("textbox", { name: "公开名称" }), "   ");
    await user.click(screen.getByRole("button", { name: "应用到 2 个渠道" }));

    expect(mutation.mutateAsync).toHaveBeenCalledWith({
      ids: [1, 2],
      fields: { public_display_name: "" },
    });
  });

  it("disables submission when no field is selected", () => {
    render(<ChannelBatchEditDialog ids={[1, 2]} open onOpenChange={vi.fn()} onSucceeded={vi.fn()} />);

    expect(screen.getByRole("button", { name: "应用到 2 个渠道" })).toBeDisabled();
    expect(screen.getByText("请至少选择一个要修改的字段")).toBeVisible();
    expect(mutation.mutateAsync).not.toHaveBeenCalled();
  });

  it("explains that an empty public display name restores the safe automatic name", async () => {
    const user = userEvent.setup();
    render(<ChannelBatchEditDialog ids={[1, 2]} open onOpenChange={vi.fn()} onSucceeded={vi.fn()} />);

    await user.click(screen.getByLabelText("修改此字段：公开名称"));

    expect(screen.getByText("留空将恢复安全自动名称")).toBeVisible();
  });

  it("disables submission when there are no target channels", async () => {
    const user = userEvent.setup();
    render(<ChannelBatchEditDialog ids={[]} open onOpenChange={vi.fn()} onSucceeded={vi.fn()} />);

    await user.click(screen.getByLabelText("修改此字段：渠道标签"));
    expect(screen.getByRole("button", { name: "应用到 0 个渠道" })).toBeDisabled();
    expect(mutation.mutateAsync).not.toHaveBeenCalled();
  });

  it("keeps the selected draft after a failed submit", async () => {
    mutation.mutateAsync.mockRejectedValueOnce(new Error("network unavailable"));
    const user = userEvent.setup();
    const onSucceeded = vi.fn();
    render(<ChannelBatchEditDialog ids={[1, 2]} open onOpenChange={vi.fn()} onSucceeded={onSucceeded} />);

    await user.click(screen.getByLabelText("修改此字段：渠道标签"));
    const input = screen.getByRole("textbox", { name: "渠道标签" });
    await user.type(input, "edge");
    await user.click(screen.getByRole("button", { name: "应用到 2 个渠道" }));

    expect(await screen.findByRole("alert")).toHaveTextContent("network unavailable");
    expect(input).toHaveValue("edge");
    expect(screen.getByLabelText("修改此字段：渠道标签")).toBeChecked();
    expect(onSucceeded).not.toHaveBeenCalled();
  });

  it("uses the fallback error and preserves the draft for a non-Error rejection", async () => {
    mutation.mutateAsync.mockRejectedValueOnce("unavailable");
    const user = userEvent.setup();
    render(<ChannelBatchEditDialog ids={[1, 2]} open onOpenChange={vi.fn()} onSucceeded={vi.fn()} />);

    await user.click(screen.getByLabelText("修改此字段：渠道标签"));
    await user.type(screen.getByRole("textbox", { name: "渠道标签" }), "edge");
    await user.click(screen.getByRole("button", { name: "应用到 2 个渠道" }));

    expect(await screen.findByRole("alert")).toHaveTextContent("批量编辑失败");
    expect(screen.getByRole("textbox", { name: "渠道标签" })).toHaveValue("edge");
  });

  it("allows retrying the same preserved draft after a failed request", async () => {
    mutation.mutateAsync
      .mockRejectedValueOnce(new Error("unavailable"))
      .mockResolvedValueOnce({ updated_count: 2, updated_ids: [1, 2] });
    const user = userEvent.setup();
    render(<ChannelBatchEditDialog ids={[1, 2]} open onOpenChange={vi.fn()} onSucceeded={vi.fn()} />);

    await user.click(screen.getByLabelText("修改此字段：渠道标签"));
    await user.type(screen.getByRole("textbox", { name: "渠道标签" }), "edge");
    await user.click(screen.getByRole("button", { name: "应用到 2 个渠道" }));
    await screen.findByRole("alert");
    await user.click(screen.getByRole("button", { name: "应用到 2 个渠道" }));

    expect(mutation.mutateAsync).toHaveBeenLastCalledWith({ ids: [1, 2], fields: { tag: "edge" } });
    expect(mutation.mutateAsync).toHaveBeenCalledTimes(2);
  });

  it("does not submit twice while the first request is pending", async () => {
    let resolve!: (value: { updated_count: number; updated_ids: number[] }) => void;
    mutation.mutateAsync.mockImplementationOnce(() => new Promise((done) => { resolve = done; }));
    const user = userEvent.setup();
    render(<ChannelBatchEditDialog ids={[1, 2]} open onOpenChange={vi.fn()} onSucceeded={vi.fn()} />);

    await user.click(screen.getByLabelText("修改此字段：渠道标签"));
    await user.click(screen.getByRole("button", { name: "应用到 2 个渠道" }));
    await user.click(screen.getByRole("button", { name: "正在应用…" }));

    expect(mutation.mutateAsync).toHaveBeenCalledTimes(1);
    await act(async () => resolve({ updated_count: 2, updated_ids: [1, 2] }));
  });

  it("keeps a reopened different-ID session intact when an old request succeeds", async () => {
    let resolveOld!: (value: { updated_count: number; updated_ids: number[] }) => void;
    mutation.mutateAsync.mockImplementationOnce(() => new Promise((done) => { resolveOld = done; }));
    const onSucceeded = vi.fn();
    const user = userEvent.setup();
    render(<SessionDialogHarness onSucceeded={onSucceeded} />);

    await user.click(screen.getByLabelText("修改此字段：渠道标签"));
    fireEvent.change(screen.getByRole("textbox", { name: "渠道标签" }), {
      target: { value: "old draft" },
    });
    await user.click(screen.getByRole("button", { name: "应用到 1 个渠道" }));
    await user.click(screen.getByRole("button", { name: "Close" }));
    await user.click(screen.getByRole("button", { name: "用渠道 2 重新打开" }));
    await user.click(screen.getByLabelText("修改此字段：渠道标签"));
    fireEvent.change(screen.getByRole("textbox", { name: "渠道标签" }), {
      target: { value: "new draft" },
    });

    await act(async () => resolveOld({ updated_count: 1, updated_ids: [1] }));

    await waitFor(() => {
      expect(onSucceeded).toHaveBeenCalledWith({ updated_count: 1, updated_ids: [1] });
      expect(screen.getByRole("dialog")).toBeInTheDocument();
      expect(screen.getByRole("textbox", { name: "渠道标签" })).toHaveValue("new draft");
      expect(screen.getByLabelText("修改此字段：渠道标签")).toBeChecked();
    });
  });

  it("does not write an old request failure into a reopened different-ID session", async () => {
    let rejectOld!: (error: Error) => void;
    mutation.mutateAsync.mockImplementationOnce(() => new Promise((_, reject) => { rejectOld = reject; }));
    const user = userEvent.setup();
    render(<SessionDialogHarness onSucceeded={vi.fn()} />);

    await user.click(screen.getByLabelText("修改此字段：渠道标签"));
    await user.click(screen.getByRole("button", { name: "应用到 1 个渠道" }));
    await user.click(screen.getByRole("button", { name: "Close" }));
    await user.click(screen.getByRole("button", { name: "用渠道 2 重新打开" }));
    await user.click(screen.getByLabelText("修改此字段：渠道标签"));
    fireEvent.change(screen.getByRole("textbox", { name: "渠道标签" }), {
      target: { value: "new draft" },
    });

    await act(async () => rejectOld(new Error("old request failed")));

    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
    expect(screen.getByRole("dialog")).toBeInTheDocument();
    expect(screen.getByRole("textbox", { name: "渠道标签" })).toHaveValue("new draft");
  });

  it("submits the normalized single id and resets on a later reopen after success", async () => {
    mutation.mutateAsync.mockResolvedValueOnce({ updated_count: 1, updated_ids: [1] });
    const onSucceeded = vi.fn();
    const user = userEvent.setup();
    render(<DialogHarness ids={[1]} onSucceeded={onSucceeded} />);

    await user.click(screen.getByLabelText("修改此字段：渠道标签"));
    await user.type(screen.getByRole("textbox", { name: "渠道标签" }), "edge");
    await user.click(screen.getByRole("button", { name: "应用到 1 个渠道" }));

    await act(async () => undefined);
    expect(mutation.mutateAsync).toHaveBeenCalledWith({ ids: [1], fields: { tag: "edge" } });
    expect(onSucceeded).toHaveBeenCalledWith({ updated_count: 1, updated_ids: [1] });
    await user.click(screen.getByRole("button", { name: "重新打开" }));
    expect(screen.getByLabelText("修改此字段：渠道标签")).not.toBeChecked();
    expect(screen.getByRole("textbox", { name: "渠道标签" })).toHaveValue("");
  });

  it("resets the draft after a manual close and later reopen", async () => {
    const user = userEvent.setup();
    render(<DialogHarness ids={[1, 2]} onSucceeded={vi.fn()} />);

    await user.click(screen.getByLabelText("修改此字段：渠道标签"));
    await user.type(screen.getByRole("textbox", { name: "渠道标签" }), "edge");
    await user.click(screen.getByRole("button", { name: "Close" }));
    await user.click(screen.getByRole("button", { name: "重新打开" }));

    expect(screen.getByLabelText("修改此字段：渠道标签")).not.toBeChecked();
    expect(screen.getByRole("textbox", { name: "渠道标签" })).toHaveValue("");
  });

  it("gives status and every complex editor an accessible batch-field group name", async () => {
    const user = userEvent.setup();
    render(<ChannelBatchEditDialog ids={[1, 2]} open onOpenChange={vi.fn()} onSucceeded={vi.fn()} />);

    await user.click(screen.getByLabelText("修改此字段：状态"));
    expect(screen.getByRole("combobox", { name: "状态" })).toBeInTheDocument();
    for (const label of ["模型映射", "角色映射", "用量限制", "亲和性", "弹性策略", "端点配置"]) {
      expect(screen.getByRole("group", { name: label })).toBeInTheDocument();
    }
  });

  it("renders only endpoint controls inside the endpoints field and serializes only endpoints", async () => {
    mutation.mutateAsync.mockResolvedValueOnce({ updated_count: 2, updated_ids: [1, 2] });
    const user = userEvent.setup();
    render(<ChannelBatchEditDialog ids={[1, 2]} open onOpenChange={vi.fn()} onSucceeded={vi.fn()} />);

    await user.click(screen.getByLabelText("修改此字段：端点配置"));
    const endpointsField = screen.getByRole("group", { name: "端点配置" });
    expect(within(endpointsField).queryAllByRole("switch")).toHaveLength(0);
    expect(within(endpointsField).queryByText("protocolOverride")).not.toBeInTheDocument();
    expect(within(endpointsField).queryByText("sectionProtocolBehavior")).not.toBeInTheDocument();
    await user.click(within(endpointsField).getByText("Chat Completions"));
    await user.click(screen.getByRole("button", { name: "应用到 2 个渠道" }));

    expect(mutation.mutateAsync).toHaveBeenCalledWith({
      ids: [1, 2],
      fields: { endpoints: "{\"chat_completions\":\"/v1/chat/completions\"}" },
    });
  });

  it("does not validate empty endpoints until the endpoints field is selected", async () => {
    const user = userEvent.setup();
    render(<ChannelBatchEditDialog ids={[1, 2]} open onOpenChange={vi.fn()} onSucceeded={vi.fn()} />);

    expect(screen.queryByText("encodeEndpointsRequired")).not.toBeInTheDocument();
    await user.click(screen.getByLabelText("修改此字段：端点配置"));
    expect(screen.getByText("encodeEndpointsRequired")).toBeVisible();
  });

  it("uses a single-column mobile grid, two desktop columns, a scroll body, and a fixed footer", () => {
    render(<ChannelBatchEditDialog ids={[1, 2]} open onOpenChange={vi.fn()} onSucceeded={vi.fn()} />);

    const dialog = screen.getByRole("dialog");
    expect(dialog).toHaveClass("grid-rows-[auto_minmax(0,1fr)_auto]", "overflow-hidden");
    const body = dialog.querySelector('[data-slot="channel-batch-edit-body"]');
    const footer = dialog.querySelector('[data-slot="dialog-footer"]');
    const groups = dialog.querySelectorAll('[data-slot="field-set"] > [data-slot="field-group"]');
    expect(body).toHaveClass("min-h-0", "overflow-y-auto");
    expect(footer).toHaveClass("border-t", "bg-background");
    expect(groups.length).toBeGreaterThan(0);
    for (const group of groups) {
      expect(group).toHaveClass("grid-cols-1", "sm:grid-cols-2");
    }
    expect(dialog.querySelector('[data-batch-field="endpoints"]')).toHaveClass("sm:col-span-2");
    expect(screen.getByRole("spinbutton", { name: "优先级" })).toHaveClass("font-mono", "tabular-nums");
  });
});

function DialogHarness({ ids, onSucceeded }: { ids: number[]; onSucceeded: (response: { updated_count: number; updated_ids: number[] }) => void }) {
  const [open, setOpen] = useState(true);
  return (
    <>
      <button type="button" onClick={() => setOpen(true)}>重新打开</button>
      <ChannelBatchEditDialog ids={ids} open={open} onOpenChange={setOpen} onSucceeded={onSucceeded} />
    </>
  );
}

function SessionDialogHarness({ onSucceeded }: { onSucceeded: (response: { updated_count: number; updated_ids: number[] }) => void }) {
  const [ids, setIDs] = useState([1]);
  const [open, setOpen] = useState(true);
  return (
    <>
      <button type="button" onClick={() => { setIDs([2]); setOpen(true); }}>用渠道 2 重新打开</button>
      <ChannelBatchEditDialog ids={ids} open={open} onOpenChange={setOpen} onSucceeded={onSucceeded} />
    </>
  );
}
