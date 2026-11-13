import { act, render, screen, within, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { NextIntlClientProvider } from "next-intl";
import { beforeEach, describe, expect, it, vi } from "vitest";
import messages from "@/i18n/zh.json";
import type { AllInflightResponse, GlobalInflightRow } from "@/lib/types";
import { InflightRequests } from "./inflight-requests";

const notices = vi.hoisted(() => ({ success: vi.fn(), error: vi.fn(), info: vi.fn(), warning: vi.fn() }));
vi.mock("@/components/business/model-provider-logo", () => ({ ModelProviderLogo: () => null }));
vi.mock("sonner", () => ({ toast: notices }));
vi.mock("@/lib/utils/clipboard", () => ({ copyTextWithFeedback: vi.fn().mockResolvedValue(true) }));
const row = (id = 1): GlobalInflightRow => ({
  id, agent_id: 1, agent_name: "Local node", req_id: `request-${id}`, stage: "client_encode", elapsed_ms: 120000, queued_ms: 0, queued_reason: "",
  view: { user_id: 21, channel_id: 11, private_channel_id: 7, model_name: `Model ${id}`, is_stream: true },
  current_attempt: { seq: 1, channel_id: 7, channel_name: "Private current", source: "private", real_model: "upstream" },
  progress: { client_body_bytes: 1024, client_idle_ms: 0, upstream_body_bytes: null, upstream_idle_ms: null },
});
const response = (requests: GlobalInflightRow[], successful = [1]): AllInflightResponse => ({ requests, successful_agent_ids: successful, failed_agents: [] });

function setup(initial = response([row()])) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  const onRefresh = vi.fn();
  const props = { title: "在途", data: initial, dataUpdatedAt: 1000, isFetching: false, paused: false, onPausedChange: vi.fn(), onRefresh };
  const wrapper = ({ children }: { children: React.ReactNode }) => <QueryClientProvider client={client}><NextIntlClientProvider locale="zh" messages={messages}>{children}</NextIntlClientProvider></QueryClientProvider>;
  const rendered = render(<InflightRequests {...props} />, { wrapper });
  return { ...rendered, props, onRefresh, update: (next: Partial<typeof props> & { error?: unknown }) => rendered.rerender(<InflightRequests {...props} {...next} />) };
}

const entityResponse = (url: string) => url.includes("/users/")
  ? { id: Number(url.split("/").pop()), username: url.endsWith("/42") ? "Bob" : "Alice", role: 1, status: 1, quota: 0 }
  : url.includes("/channels/") ? { id: 7, name: "Renamed channel", status: 1 } : { data: [], total: 0 };
const interruptCalls = () => vi.mocked(fetch).mock.calls.filter(([url]) => String(url).endsWith("/inflight/interrupt"));

beforeEach(() => {
  vi.clearAllMocks();
  vi.stubGlobal("fetch", vi.fn(async (url: string) => new Response(JSON.stringify(entityResponse(url)), { status: 200 })));
});

describe("in-flight request interaction", () => {
  it("keeps one inline detail fresh, retains unknown-node data, and disables an explicitly departed request", async () => {
    const user = userEvent.setup();
    const view = setup();
    expect(await screen.findByText("Alice")).toBeInTheDocument();
    expect(screen.getByText("#21")).toBeInTheDocument();
    expect(screen.getByText("Private current")).toBeInTheDocument();
    expect(screen.getByText("#7")).toBeInTheDocument();
    expect(screen.queryByText("#0")).not.toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "展开请求 request-1" }));
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
    const next = { ...row(), progress: { ...row().progress!, client_body_bytes: 4096 } };
    view.update({ data: response([next]), dataUpdatedAt: 2000 });
    expect(within(screen.getByTestId("inflight-detail")).getByText("4.0 KB")).toBeInTheDocument();
    view.update({ data: response([], []), dataUpdatedAt: 3000 });
    expect(screen.getByRole("button", { name: "中断" })).toBeDisabled();
    expect(screen.queryByText("已离开在途列表")).not.toBeInTheDocument();
    view.update({ data: response([]), dataUpdatedAt: 4000 });
    expect(screen.getByText("已离开在途列表")).toBeInTheDocument();
    expect(within(screen.getByTestId("inflight-detail")).getByText("4.0 KB")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "中断" })).toBeDisabled();
  });

  it("does not expand when copying or selecting text; selecting another row replaces the detail", async () => {
    const user = userEvent.setup();
    const view = setup(response([row(), row(2)]));
    await user.click(screen.getByRole("button", { name: "列设置" }));
    await user.click(screen.getByRole("checkbox", { name: "请求 ID" }));
    await user.keyboard("{Escape}");
    await user.click(screen.getAllByRole("button", { name: "复制" })[0]);
    expect(screen.queryByTestId("inflight-detail")).not.toBeInTheDocument();
    await user.hover((await screen.findAllByText("Alice"))[0]);
    await screen.findByRole("button", { name: "复制 ID" });
    view.update({ data: response([{ ...row(), elapsed_ms: 125000 }, row(2)]), dataUpdatedAt: 2000 });
    await user.click(screen.getByRole("button", { name: "复制 ID" }));
    expect(screen.queryByTestId("inflight-detail")).not.toBeInTheDocument();
    await user.unhover(screen.getAllByText("Alice")[0]);
    const selection = vi.spyOn(window, "getSelection").mockReturnValue({ toString: () => "selected" } as Selection);
    await user.click(screen.getByText("Model 1"));
    expect(screen.queryByTestId("inflight-detail")).not.toBeInTheDocument();
    selection.mockRestore();
    await user.click(screen.getByText("Model 1"));
    await user.click(screen.getByText("Model 2"));
    expect(screen.getAllByTestId("inflight-detail")).toHaveLength(1);
    expect(screen.getByRole("button", { name: "收起请求 request-2" })).toHaveAttribute("aria-expanded", "true");
  });


  it("filters locally by identity and channel, clears search, and retains an already open detail", async () => {
    const user = userEvent.setup();
    setup(response([row(), { ...row(2), view: { ...row(2).view, user_id: 42 } }]));
    const search = screen.getByRole("searchbox");
    await user.type(search, "UID 42");
    expect(screen.queryByText("Model 1")).not.toBeInTheDocument();
    expect(screen.getByText("Model 2")).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "清除" }));
    expect(screen.getByText("Model 1")).toBeInTheDocument();
    await user.type(search, "Private current");
    expect(screen.getAllByRole("button", { name: /展开请求/ })).toHaveLength(2);
    await user.clear(search);
    await user.type(search, "no-match");
    expect(screen.getByText("没有匹配的请求")).toBeInTheDocument();
    await user.clear(search);
    await user.click(screen.getByText("Model 1"));
    await user.type(search, "no-match");
    expect(screen.getByText("没有匹配的请求，保留当前展开的详情。")).toBeInTheDocument();
    expect(screen.getByTestId("inflight-detail")).toBeInTheDocument();
  });


  it("keeps the animated dots and selects the same single inline detail as the table", async () => {
    const user = userEvent.setup();
    const scroll = vi.fn();
    const original = Object.getOwnPropertyDescriptor(HTMLElement.prototype, "scrollIntoView");
    Object.defineProperty(HTMLElement.prototype, "scrollIntoView", { configurable: true, value: scroll });
    try {
      setup(response([row(), row(2)]));
      await user.click(screen.getByRole("button", { name: "Model 1 client_encode" }));
      await waitFor(() => expect(scroll).toHaveBeenCalled());
      expect(screen.getByRole("button", { name: "收起请求 request-1" })).toHaveAttribute("aria-expanded", "true");
      await user.click(screen.getByRole("button", { name: "Model 2 client_encode" }));
      expect(screen.getAllByTestId("inflight-detail")).toHaveLength(1);
      expect(screen.getByRole("button", { name: "Model 2 client_encode" })).toHaveAttribute("aria-pressed", "true");
      expect(screen.getByRole("button", { name: "展开请求 request-1" })).toHaveAttribute("aria-expanded", "false");
    } finally {
      if (original) Object.defineProperty(HTMLElement.prototype, "scrollIntoView", original);
      else Reflect.deleteProperty(HTMLElement.prototype, "scrollIntoView");
    }
  });

  it.each([
    { result: false, notice: "info", message: "请求已不在途或无法中断" },
    { result: true, notice: "success", message: "已发送取消信号，等待请求退出" },
    { result: "timeout", notice: "warning", message: "中断结果未知，正在刷新；未自动重发" },
  ])("confirms once and reports $result without claiming completion", async ({ result, notice, message }) => {
    let finish!: () => void;
    vi.mocked(fetch).mockImplementation((url) => String(url).endsWith("/inflight/interrupt") ? new Promise<Response>((resolve, reject) => {
      finish = () => result === "timeout" ? reject(new DOMException("timed out", "TimeoutError")) : resolve(new Response(JSON.stringify({ interrupted: result }), { status: 200 }));
    }) : Promise.resolve(new Response(JSON.stringify(entityResponse(String(url))))));
    const user = userEvent.setup();
    const { onRefresh, update } = setup();
    await user.click(screen.getByRole("button", { name: "中断" }));
    expect(interruptCalls()).toHaveLength(0);
    expect(screen.getByRole("alertdialog")).toHaveTextContent("#21");
    expect(screen.getByRole("alertdialog")).toHaveTextContent("Private current");
    expect(screen.getByRole("alertdialog")).toHaveTextContent("#7");
    await user.click(within(screen.getByRole("alertdialog")).getByRole("button", { name: "中断" }));
    await waitFor(() => expect(interruptCalls()).toHaveLength(1));
    expect(within(screen.getByRole("alertdialog")).getByRole("button", { name: "发送中…" })).toBeDisabled();
    if (result === "timeout") update({ data: response([row()]), dataUpdatedAt: Date.now() - 1 });
    await act(async () => finish());
    await waitFor(() => expect(notices[notice as keyof typeof notices]).toHaveBeenCalledWith(message));
    expect(interruptCalls()).toHaveLength(1);
    expect(onRefresh).toHaveBeenCalledTimes(1);
    expect(screen.queryByRole("alertdialog")).not.toBeInTheDocument();
    if (result === true) expect(screen.getByRole("button", { name: "取消中" })).toBeDisabled();
    if (result === "timeout") {
      expect(screen.getByRole("button", { name: "待确认" })).toBeDisabled();
      update({ error: new Error("refresh failed") });
      expect(screen.getByRole("button", { name: "待确认" })).toBeDisabled();
      update({ data: response([row()]), dataUpdatedAt: Date.now() + 1 });
      expect(screen.getByRole("button", { name: "中断" })).toBeEnabled();
    }
  });
});
