import { QueryClientProvider } from "@tanstack/react-query";
import { act, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeAll, beforeEach, describe, expect, it, vi } from "vitest";

import type { APIRoute } from "@/lib/api/api-services";
import type { Token } from "@/lib/types";
import { createTestQueryClient } from "@/test/render";

import { CopyInvocationCommand } from "./copy-invocation-command";

beforeAll(() => {
  Element.prototype.hasPointerCapture ??= () => false;
  Element.prototype.setPointerCapture ??= () => {};
  Element.prototype.releasePointerCapture ??= () => {};
  Element.prototype.scrollIntoView ??= () => {};
});

const route: APIRoute = {
  id: 9,
  api_service_id: 7,
  backend_id: 17,
  slug: "forecast",
  protocols: ["http"],
  allowed_methods: ["GET"],
  upstream_path: "/forecast",
  forward_subpath: true,
  example_request: { method: "GET", subpath: "today", query: "unit=c", headers: {}, body: "" },
  status: 1,
};

const token: Token = {
  id: 5,
  user_id: 1,
  key: "sk-production-secret",
  name: "Production Token",
  status: 1,
  expired_at: 0,
  models: "",
  trace_enabled: false,
  trace_mode: "full",
  api_role_mode: "explicit",
  created_at: 1,
  updated_at: 1,
};

const clipboard = vi.hoisted(() => ({ copy: vi.fn() }));
const picker = vi.hoisted(() => ({ props: undefined as Record<string, unknown> | undefined }));

vi.mock("next-intl", () => ({ useTranslations: () => (key: string) => key }));
vi.mock("@/lib/utils/clipboard", () => ({ copyTextWithFeedback: clipboard.copy }));
vi.mock("@/components/business/entity-picker/entity-picker", () => ({
  EntityPicker: (props: Record<string, unknown>) => {
    picker.props = props;
    return <button type="button" onClick={() => (props.onChange as (value: string) => void)("5")}>chooseToken</button>;
  },
}));

function jwt(userID: number) {
  const payload = btoa(JSON.stringify({ user_id: userID, username: `viewer-${userID}`, role: 1, exp: Math.floor(Date.now() / 1000) + 3600 }))
    .replaceAll("+", "-").replaceAll("/", "_").replaceAll("=", "");
  return `e30.${payload}.signature`;
}

function setViewer(userID: number) {
  window.localStorage.setItem("token", jwt(userID));
  window.dispatchEvent(new StorageEvent("storage", { key: "token" }));
}

function rememberedKey(userID: number, serviceID: number, routeID: number) {
  return `aigw:api-invocation-token-id:${userID}:${serviceID}:${routeID}`;
}

function json(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), { status, headers: { "content-type": "application/json", "x-server-time-ms": String(Date.now()) } });
}

function scopedPath(routeID: number, tokenID = 5, serviceID = 7) {
  return `/api/tokens?usable_only=true&api_service_id=${serviceID}&api_route_id=${routeID}&token_id=${tokenID}&page=1&page_size=1`;
}

function renderCommand(activeRoute = route, serviceID = 7) {
  const queryClient = createTestQueryClient();
  const view = render(
    <QueryClientProvider client={queryClient}>
      <CopyInvocationCommand origin="https://gateway.example" serviceId={serviceID} serviceSlug="weather" route={activeRoute} />
    </QueryClientProvider>,
  );
  return { ...view, queryClient };
}

describe("CopyInvocationCommand", () => {
  beforeEach(() => {
    window.localStorage.clear();
    setViewer(1);
    clipboard.copy.mockReset();
    clipboard.copy.mockResolvedValue(true);
    picker.props = undefined;
    vi.stubGlobal("fetch", vi.fn());
  });

  afterEach(() => { vi.unstubAllGlobals(); vi.restoreAllMocks(); });

  it("opens a searchable usable-token picker before the first copy", async () => {
    const user = userEvent.setup();
    renderCommand();

    await user.click(screen.getByRole("button", { name: "copyCommand" }));

    expect(screen.getByRole("dialog")).toBeInTheDocument();
    expect(picker.props).toMatchObject({ entity: "usable-token", apiServiceId: 7, apiRouteId: 9, value: "" });
    expect(screen.getByRole("button", { name: "copyTemplateCommand" })).toBeInTheDocument();
  });

  it("validates the selected Token and remembers it only after copying", async () => {
    vi.mocked(fetch).mockImplementation((input) => {
      if (String(input) === scopedPath(9)) return Promise.resolve(json({ data: [token], total: 1, page: 1, page_size: 1 }));
      throw new Error(`unexpected request: ${String(input)}`);
    });
    const user = userEvent.setup();
    renderCommand();

    await user.click(screen.getByRole("button", { name: "copyCommand" }));
    await user.click(screen.getByRole("button", { name: "chooseToken" }));
    await user.click(await screen.findByRole("button", { name: "copyAndRememberToken" }));

    await waitFor(() => expect(clipboard.copy).toHaveBeenCalledOnce());
    expect(String(clipboard.copy.mock.calls[0]?.[0])).toContain("Authorization: Bearer sk-production-secret");
    expect(window.localStorage.getItem(rememberedKey(1, 7, 9))).toBe("5");
    expect(screen.queryByText(/sk-production-secret/)).not.toBeInTheDocument();
  });

  it("copies immediately from a still-usable remembered Token with the same primary label", async () => {
    window.localStorage.setItem(rememberedKey(1, 7, 9), "5");
    vi.mocked(fetch).mockResolvedValue(json({ data: [token], total: 1, page: 1, page_size: 1 }));
    const user = userEvent.setup();
    renderCommand();

    await screen.findByRole("button", { name: "copyCommand" });
    await user.click(screen.getByRole("button", { name: "copyCommand" }));

    await waitFor(() => expect(clipboard.copy).toHaveBeenCalledOnce());
    expect(String(clipboard.copy.mock.calls[0]?.[0])).toContain("sk-production-secret");
  });

  it("forgets an unavailable remembered Token and returns to the picker without copying", async () => {
    window.localStorage.setItem(rememberedKey(1, 7, 9), "5");
    vi.mocked(fetch).mockResolvedValue(json({ data: [], total: 0, page: 1, page_size: 1 }));
    const user = userEvent.setup();
    renderCommand();

    expect(await screen.findByText("invocationTokenNoLongerAllowed")).toBeInTheDocument();
    expect(window.localStorage.getItem(rememberedKey(1, 7, 9))).toBeNull();
    await user.click(screen.getByRole("button", { name: "copyCommand" }));
    expect(screen.getByRole("dialog")).toBeInTheDocument();
    expect(clipboard.copy).not.toHaveBeenCalled();
  });

  it("uses a single protocol choice for a dual-protocol Route", async () => {
    const dualRoute: APIRoute = { ...route, protocols: ["http", "websocket"] };
    const user = userEvent.setup();
    renderCommand(dualRoute);

    await user.click(screen.getByRole("button", { name: "copyCommand" }));

    expect(screen.getByRole("group", { name: "invocationProtocol" })).toBeInTheDocument();
    await user.click(screen.getByRole("radio", { name: "websocketProtocol" }));
    await user.click(screen.getByRole("button", { name: "copyTemplateCommand" }));
    expect(clipboard.copy).toHaveBeenCalledWith(expect.stringMatching(/^websocat .*wss:\/\/gateway\.example/), expect.anything());
  });

  it("does not show a protocol switch when a Route supports only WebSocket", async () => {
    const user = userEvent.setup();
    renderCommand({ ...route, protocols: ["websocket"] });

    await user.click(screen.getByRole("button", { name: "copyCommand" }));

    expect(screen.queryByRole("group", { name: "invocationProtocol" })).not.toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "copyTemplateCommand" }));
    expect(clipboard.copy).toHaveBeenCalledWith(expect.stringMatching(/^websocat .*wss:\/\/gateway\.example/), expect.anything());
  });

  it("shows a usable-token permission error without treating it as an empty catalog", async () => {
    vi.mocked(fetch).mockResolvedValue(json({ error: "scope unavailable" }, 500));
    const user = userEvent.setup();
    renderCommand();

    await user.click(screen.getByRole("button", { name: "copyCommand" }));
    await user.click(screen.getByRole("button", { name: "chooseToken" }));

    expect(await screen.findByText("invocationTokenValidationFailed")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "copyAndRememberToken" })).toBeDisabled();
    expect(clipboard.copy).not.toHaveBeenCalled();
  });

  it("degrades a protected localStorage read to an unremembered Token without crashing", async () => {
    const originalGetItem = Storage.prototype.getItem;
    vi.spyOn(Storage.prototype, "getItem").mockImplementation(function (this: Storage, key: string) {
      if (key.startsWith("aigw:api-invocation-token-id:")) throw new DOMException("blocked", "SecurityError");
      return originalGetItem.call(this, key);
    });
    const user = userEvent.setup();

    renderCommand();
    await user.click(screen.getByRole("button", { name: "copyCommand" }));

    expect(screen.getByRole("dialog")).toBeInTheDocument();
    expect(clipboard.copy).not.toHaveBeenCalled();
  });

  it("keeps a successful copy when persisting the remembered Token exceeds storage quota", async () => {
    vi.mocked(fetch).mockResolvedValue(json({ data: [token], total: 1, page: 1, page_size: 1 }));
    const originalSetItem = Storage.prototype.setItem;
    vi.spyOn(Storage.prototype, "setItem").mockImplementation(function (this: Storage, key: string, value: string) {
      if (key === rememberedKey(1, 7, 9)) throw new DOMException("quota", "QuotaExceededError");
      return originalSetItem.call(this, key, value);
    });
    const user = userEvent.setup();
    renderCommand();

    await user.click(screen.getByRole("button", { name: "copyCommand" }));
    await user.click(screen.getByRole("button", { name: "chooseToken" }));
    await user.click(await screen.findByRole("button", { name: "copyAndRememberToken" }));

    await waitFor(() => expect(clipboard.copy).toHaveBeenCalledOnce());
    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
  });

  it("fails closed when remembered Token cleanup cannot remove protected storage", async () => {
    window.localStorage.setItem(rememberedKey(1, 7, 9), "5");
    vi.mocked(fetch).mockResolvedValue(json({ data: [], total: 0, page: 1, page_size: 1 }));
    const originalRemoveItem = Storage.prototype.removeItem;
    vi.spyOn(Storage.prototype, "removeItem").mockImplementation(function (this: Storage, key: string) {
      if (key === rememberedKey(1, 7, 9)) throw new DOMException("blocked", "SecurityError");
      return originalRemoveItem.call(this, key);
    });
    const user = userEvent.setup();
    renderCommand();

    expect(await screen.findByText("invocationTokenNoLongerAllowed")).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "copyCommand" }));
    expect(screen.getByRole("dialog")).toBeInTheDocument();
    expect(clipboard.copy).not.toHaveBeenCalled();
  });

  it("does not reuse an old viewer's remembered Token after the authenticated user changes", async () => {
    window.localStorage.setItem(rememberedKey(1, 7, 9), "5");
    vi.mocked(fetch).mockResolvedValue(json({ data: [token], total: 1, page: 1, page_size: 1 }));
    const user = userEvent.setup();
    renderCommand();
    await waitFor(() => expect(fetch).toHaveBeenCalledWith(scopedPath(9), expect.anything()));

    setViewer(2);
    await user.click(screen.getByRole("button", { name: "copyCommand" }));

    expect(screen.getByRole("dialog")).toBeInTheDocument();
    expect(clipboard.copy).not.toHaveBeenCalled();
  });

  it("does not reuse a selected candidate after the authenticated viewer changes", async () => {
    vi.mocked(fetch).mockResolvedValue(json({ data: [token], total: 1, page: 1, page_size: 1 }));
    const user = userEvent.setup();
    renderCommand();

    await user.click(screen.getByRole("button", { name: "copyCommand" }));
    await user.click(screen.getByRole("button", { name: "chooseToken" }));
    expect(await screen.findByRole("button", { name: "copyAndRememberToken" })).toBeEnabled();

    setViewer(2);

    await waitFor(() => expect(screen.getByRole("button", { name: "copyAndRememberToken" })).toBeDisabled());
    expect(clipboard.copy).not.toHaveBeenCalled();
  });

  it.each([
    ["different Service", 8, { ...route, api_service_id: 8 }, 8, 9],
    ["different Route", 7, { ...route, id: 10, slug: "history" }, 7, 10],
  ] as const)("does not reuse a remembered Token for a %s scope", async (_name, serviceID, scopedRoute, expectedServiceID, expectedRouteID) => {
    window.localStorage.setItem(rememberedKey(1, 7, 9), "5");
    const user = userEvent.setup();
    renderCommand(scopedRoute, serviceID);

    await user.click(screen.getByRole("button", { name: "copyCommand" }));

    expect(screen.getByRole("dialog")).toBeInTheDocument();
    expect(picker.props).toMatchObject({ apiServiceId: expectedServiceID, apiRouteId: expectedRouteID, value: "" });
    expect(clipboard.copy).not.toHaveBeenCalled();
  });

  it.each([
    ["miss", () => Promise.resolve(json({ data: [], total: 0, page: 1, page_size: 1 })), "invocationTokenNoLongerAllowed"],
    ["RBAC error", () => Promise.resolve(json({ error: "scope unavailable" }, 500)), "invocationTokenValidationFailed"],
  ] as const)("clears and disables a remembered Token after a scoped %s", async (_name, response, message) => {
    window.localStorage.setItem(rememberedKey(1, 7, 9), "5");
    vi.mocked(fetch).mockImplementation((input) => String(input) === scopedPath(9) ? response() : Promise.reject(new Error(`unexpected request: ${String(input)}`)));
    const user = userEvent.setup();
    renderCommand();

    expect(await screen.findByText(message)).toBeInTheDocument();
    expect(window.localStorage.getItem(rememberedKey(1, 7, 9))).toBeNull();
    await user.click(screen.getByRole("button", { name: "copyCommand" }));
    expect(screen.getByRole("dialog")).toBeInTheDocument();
    expect(clipboard.copy).not.toHaveBeenCalled();
  });

  it("fails closed when a cached usable Token later fails background validation", async () => {
    window.localStorage.setItem(rememberedKey(1, 7, 9), "5");
    vi.mocked(fetch)
      .mockResolvedValueOnce(json({ data: [token], total: 1, page: 1, page_size: 1 }))
      .mockResolvedValueOnce(json({ error: "scope unavailable" }, 500));
    const user = userEvent.setup();
    const { queryClient } = renderCommand();
    await waitFor(() => expect(fetch).toHaveBeenCalledOnce());

    await act(async () => {
      await queryClient.refetchQueries({ queryKey: ["tokens", "usable-for-api-route", 1, 7, 9, 5] });
    });

    expect(await screen.findByText("invocationTokenValidationFailed")).toBeInTheDocument();
    expect(window.localStorage.getItem(rememberedKey(1, 7, 9))).toBeNull();
    await user.click(screen.getByRole("button", { name: "copyCommand" }));
    expect(screen.getByRole("dialog")).toBeInTheDocument();
    expect(clipboard.copy).not.toHaveBeenCalled();
  });

  it("does not copy while remembered Token validation is still pending", async () => {
    window.localStorage.setItem(rememberedKey(1, 7, 9), "5");
    vi.mocked(fetch).mockImplementation(() => new Promise(() => {}));
    const user = userEvent.setup();
    renderCommand();

    expect(await screen.findByText("invocationTokenChecking")).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "copyCommand" }));

    expect(screen.getByRole("dialog")).toBeInTheDocument();
    expect(clipboard.copy).not.toHaveBeenCalled();
  });
});
