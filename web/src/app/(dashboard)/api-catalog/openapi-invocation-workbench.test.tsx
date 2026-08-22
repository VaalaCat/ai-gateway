import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import {
  abortActiveOpenAPIRequest,
  OpenAPIInvocationWorkbench,
} from "./_components/openapi-invocation-workbench";
import type { OpenAPIOperation } from "./_components/openapi-operation-selection";
import { copyTextWithFeedback } from "@/lib/utils/clipboard";

vi.mock("next-intl", () => ({ useTranslations: () => (key: string) => key }));
vi.mock("@/lib/utils/clipboard", () => ({ copyTextWithFeedback: vi.fn().mockResolvedValue(true) }));

const operation: OpenAPIOperation = {
  routeID: 9, routeSlug: "users", path: "/users/{id}", method: "POST", pathItem: {
    parameters: [{ name: "id", in: "path", required: true, example: "u-1" }],
  }, operation: {
    security: [{ upstreamKey: [] }],
    parameters: [{ name: "include", in: "query", example: "teams" }, { name: "X-Trace", in: "header", example: "trace-1" }],
    requestBody: { content: { "application/json": { example: { enabled: true } } } },
  },
};

afterEach(() => {
  vi.unstubAllGlobals();
  vi.clearAllMocks();
});

beforeEach(() => {
  Element.prototype.hasPointerCapture ??= () => false;
  Element.prototype.setPointerCapture ??= () => {};
  Element.prototype.releasePointerCapture ??= () => {};
  HTMLElement.prototype.scrollIntoView = vi.fn();
});

function renderWorkbench(overrides: Partial<React.ComponentProps<typeof OpenAPIInvocationWorkbench>> = {}) {
  return render(<OpenAPIInvocationWorkbench
    scopeKey="token:5"
    origin="https://gateway.example"
    serviceSlug="user-api"
    operation={operation}
    route={{ allowed_methods: ["POST"] }}
    components={{}}
    token={{ id: 5, name: "Production", key: "sk-production" }}
    tokenChecking={false}
    tokenFailure={null}
    onChooseToken={() => {}}
    onTokenCommandCopied={() => {}}
    {...overrides}
  />);
}

describe("OpenAPIInvocationWorkbench", () => {
  it("clears the active controller before aborting during unmount cleanup", () => {
    const controller = new AbortController();
    const active = { current: controller as AbortController | undefined };
    const observedDuringAbort = vi.fn(() => active.current);
    controller.signal.addEventListener("abort", observedDuringAbort);

    abortActiveOpenAPIRequest(active);

    expect(observedDuringAbort).toHaveReturnedWith(undefined);
    expect(active.current).toBeUndefined();
  });

  it("builds path, query, header, and JSON body inputs from the operation without exposing document security as credentials", () => {
    renderWorkbench();

    expect(screen.getByRole("textbox", { name: "id" })).toHaveValue("u-1");
    expect(screen.getByRole("textbox", { name: "include" })).toHaveValue("teams");
    expect(screen.getByRole("textbox", { name: "X-Trace" })).toHaveValue("trace-1");
    expect(screen.getByRole("textbox", { name: "body" })).toHaveValue(JSON.stringify({ enabled: true }, null, 2));
    expect(document.body).not.toHaveTextContent("upstreamKey");
  });

  it("sends through the public URL with the selected Token as the only Authorization header and renders the response in flow", async () => {
    const fetchMock = vi.fn().mockResolvedValue(new Response(JSON.stringify({ ok: true }), { status: 201, headers: { "content-type": "application/json" } }));
    vi.stubGlobal("fetch", fetchMock);
    const user = userEvent.setup();
    renderWorkbench();

    await user.click(screen.getByRole("button", { name: "sendOpenAPIRequest" }));

    await waitFor(() => expect(fetchMock).toHaveBeenCalledOnce());
    expect(fetchMock).toHaveBeenCalledWith("https://gateway.example/v1/api/user-api/users/u-1?include=teams", expect.objectContaining({
      method: "POST",
      headers: expect.objectContaining({ Authorization: "Bearer sk-production", "X-Trace": "trace-1" }),
    }));
    expect(screen.getByTestId("openapi-invocation-result")).toHaveTextContent('"ok": true');
  });

  it("keeps Send disabled without a verified Token and directs the user to choose one", async () => {
    const user = userEvent.setup();
    const onChooseToken = vi.fn();
    renderWorkbench({ token: undefined, onChooseToken });

    expect(screen.getByRole("button", { name: "sendOpenAPIRequest" })).toBeDisabled();
    await user.click(screen.getByRole("button", { name: "changeToken" }));
    expect(onChooseToken).toHaveBeenCalledOnce();
  });

  it("does not render an old request result after a scope change", async () => {
    let resolve!: (response: Response) => void;
    vi.stubGlobal("fetch", vi.fn(() => new Promise<Response>((done) => { resolve = done; })));
    const user = userEvent.setup();
    const view = renderWorkbench();

    await user.click(screen.getByRole("button", { name: "sendOpenAPIRequest" }));
    const signal = (vi.mocked(fetch).mock.calls[0]?.[1] as RequestInit).signal;
    view.rerender(<OpenAPIInvocationWorkbench scopeKey="token:6" origin="https://gateway.example" serviceSlug="user-api" operation={operation} route={{ allowed_methods: ["POST"] }} components={{}} token={{ id: 6, name: "Other", key: "sk-other" }} tokenChecking={false} tokenFailure={null} onChooseToken={() => {}} onTokenCommandCopied={() => {}} />);
    expect(signal?.aborted).toBe(true);
    resolve(new Response("old result", { status: 200 }));

    await waitFor(() => expect(screen.queryByTestId("openapi-invocation-result")).toBeNull());
  });

  it("deduplicates overridden parameters and blocks required path, query, header, and body values when empty", async () => {
    const user = userEvent.setup();
    renderWorkbench({
      operation: {
        ...operation,
        pathItem: { parameters: [{ name: "id", in: "path", required: true, example: "path" }, { name: "q", in: "query", example: "old" }] },
        operation: {
          parameters: [
            { name: "q", in: "query", required: true, example: "new" },
            { name: "X-Trace", in: "header", required: true, example: "trace" },
          ],
          requestBody: { required: true, content: { "application/json": { example: { ok: true } } } },
        },
      },
    });

    expect(screen.getAllByRole("textbox", { name: "q" })).toHaveLength(1);
    for (const name of ["id", "q", "X-Trace", "body"]) {
      await user.clear(screen.getByRole("textbox", { name }));
    }
    expect(screen.getAllByText("openAPIRequiredField")).toHaveLength(4);
    expect(screen.getByRole("button", { name: "sendOpenAPIRequest" })).toBeDisabled();
    expect(screen.getByRole("button", { name: "copyTemplate" })).toBeDisabled();
  });

  it("keeps unsupported parameter serialization visible and disables Send", () => {
    renderWorkbench({
      operation: {
        ...operation,
        pathItem: {},
        operation: { parameters: [{ name: "tags", in: "query", schema: { type: "array" } }] },
      },
    });

    expect(screen.getByRole("textbox", { name: "tags" })).toBeInTheDocument();
    expect(screen.getByText("openAPIInvocationUnsupported")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "sendOpenAPIRequest" })).toBeDisabled();
    expect(screen.getByRole("button", { name: "copyTemplate" })).toBeDisabled();
  });

  it("selects a deterministic request media type and uses the same URL, body, and Content-Type for fetch and curl", async () => {
    const fetchMock = vi.fn().mockResolvedValue(new Response("ok", { status: 200 }));
    vi.stubGlobal("fetch", fetchMock);
    const user = userEvent.setup();
    renderWorkbench({
      operation: {
        ...operation,
        operation: {
          requestBody: {
            content: {
              "text/plain": { example: "plain-body" },
              "application/json": { example: { json: true } },
            },
          },
        },
      },
    });

    expect(screen.getByRole("combobox", { name: "contentType" })).toHaveTextContent("application/json");
    await user.click(screen.getByRole("combobox", { name: "contentType" }));
    await user.click(screen.getByRole("option", { name: "text/plain" }));
    expect(screen.getByRole("textbox", { name: "body" })).toHaveValue("plain-body");
    expect(screen.getByText(/--header 'Content-Type: text\/plain'/)).toBeInTheDocument();
    expect(screen.getAllByText(/https:\/\/gateway\.example\/v1\/api\/user-api\/users\/u-1/)).toHaveLength(2);

    await user.click(screen.getByRole("button", { name: "sendOpenAPIRequest" }));
    expect(fetchMock).toHaveBeenCalledWith("https://gateway.example/v1/api/user-api/users/u-1", expect.objectContaining({
      body: "plain-body",
      headers: expect.objectContaining({ "Content-Type": "text/plain" }),
    }));
  });

  it.each([
    ["missing", "#/components/requestBodies/Missing", {}],
    ["external", "https://schemas.example/CreateUser.json", {}],
    ["recursive", "#/components/requestBodies/Loop", {
      requestBodies: { Loop: { $ref: "#/components/requestBodies/Loop" } },
    }],
  ])("disables Send and Copy for a %s requestBody ref", (_kind, reference, components) => {
    renderWorkbench({
      components,
      operation: {
        ...operation,
        operation: { requestBody: { $ref: reference } },
      },
    });

    expect(screen.getByText("openAPIInvocationUnsupported")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "sendOpenAPIRequest" })).toBeDisabled();
    expect(screen.getByRole("button", { name: "copyTemplate" })).toBeDisabled();
  });

  it.each([
    "application/x-www-form-urlencoded",
    "multipart/form-data",
    "application/octet-stream",
  ])("disables Send and Copy for unsupported %s request bodies", (contentType) => {
    renderWorkbench({
      operation: {
        ...operation,
        operation: { requestBody: { content: { [contentType]: { example: { unsafe: true } } } } },
      },
    });

    expect(screen.getByRole("textbox", { name: "body" })).toHaveValue(JSON.stringify({ unsafe: true }, null, 2));
    expect(screen.getByText("openAPIInvocationUnsupported")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "sendOpenAPIRequest" })).toBeDisabled();
    expect(screen.getByRole("button", { name: "copyTemplate" })).toBeDisabled();
  });

  it("sends +json bodies with the same Content-Type in fetch and curl", async () => {
    const fetchMock = vi.fn().mockResolvedValue(new Response("ok", { status: 200 }));
    vi.stubGlobal("fetch", fetchMock);
    const user = userEvent.setup();
    renderWorkbench({
      operation: {
        ...operation,
        operation: {
          requestBody: { content: { "application/problem+json": { example: { detail: "invalid" } } } },
        },
      },
    });

    expect(screen.getByText(/--header 'Content-Type: application\/problem\+json'/)).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "sendOpenAPIRequest" }));
    expect(fetchMock).toHaveBeenCalledWith(expect.any(String), expect.objectContaining({
      body: JSON.stringify({ detail: "invalid" }, null, 2),
      headers: expect.objectContaining({ "Content-Type": "application/problem+json" }),
    }));
  });

  it("shows Route policy drift while keeping the document operation visible and Send disabled", () => {
    renderWorkbench({ route: { allowed_methods: ["GET"] } });
    expect(screen.getByText("openAPIMethodMismatch")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "sendOpenAPIRequest" })).toBeDisabled();
    expect(screen.getByRole("button", { name: "copyTemplate" })).toBeDisabled();
  });

  it("fails closed for allowReserved query serialization instead of issuing an incorrectly encoded request", () => {
    renderWorkbench({
      operation: {
        ...operation,
        pathItem: {},
        operation: {
          parameters: [{ name: "redirect", in: "query", allowReserved: true, schema: { type: "string" }, example: "a/b?c" }],
        },
      },
    });

    expect(screen.getByRole("textbox", { name: "redirect" })).toHaveValue("a/b?c");
    expect(screen.getByText("openAPIInvocationUnsupported")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "sendOpenAPIRequest" })).toBeDisabled();
    expect(screen.getByRole("button", { name: "copyTemplate" })).toBeDisabled();
  });

  it.each([
    ["missing", "#/components/schemas/Missing"],
    ["external", "https://schemas.example/Scalar.json"],
  ])("disables Send and Copy for a %s parameter schema ref", (_kind, reference) => {
    renderWorkbench({
      operation: {
        ...operation,
        pathItem: {},
        operation: { parameters: [{ name: "value", in: "query", schema: { $ref: reference }, example: "unsafe" }] },
      },
    });

    expect(screen.getByRole("textbox", { name: "value" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "sendOpenAPIRequest" })).toBeDisabled();
    expect(screen.getByRole("button", { name: "copyTemplate" })).toBeDisabled();
  });

  it("copies the API_TOKEN template without remembering or exposing the selected Token", async () => {
    const onTokenCommandCopied = vi.fn();
    const user = userEvent.setup();
    renderWorkbench({ onTokenCommandCopied });

    await user.click(screen.getByRole("button", { name: "copyTemplate" }));

    expect(copyTextWithFeedback).toHaveBeenCalledWith(expect.stringContaining("${API_TOKEN}"), expect.any(Object));
    expect(copyTextWithFeedback).not.toHaveBeenCalledWith(expect.stringContaining("sk-production"), expect.any(Object));
    expect(onTokenCommandCopied).not.toHaveBeenCalled();
  });

  it("turns Send into Cancel and aborts the active stream without starting a second request", async () => {
    vi.stubGlobal("fetch", vi.fn((_url: string, init?: RequestInit) => new Promise<Response>((_resolve, reject) => {
      init?.signal?.addEventListener("abort", () => reject(new DOMException("aborted", "AbortError")));
    })));
    const user = userEvent.setup();
    renderWorkbench();

    await user.click(screen.getByRole("button", { name: "sendOpenAPIRequest" }));
    const cancel = screen.getByRole("button", { name: "cancelOpenAPIRequest" });
    await user.click(cancel);

    expect(fetch).toHaveBeenCalledOnce();
    expect((vi.mocked(fetch).mock.calls[0]?.[1] as RequestInit).signal?.aborted).toBe(true);
    await waitFor(() => expect(screen.getByRole("button", { name: "sendOpenAPIRequest" })).toBeEnabled());
    expect(screen.queryByText("openAPIRequestFailed")).toBeNull();
  });

  it("aborts an active request when the workbench unmounts", async () => {
    vi.stubGlobal("fetch", vi.fn(() => new Promise<Response>(() => {})));
    const user = userEvent.setup();
    const view = renderWorkbench();
    await user.click(screen.getByRole("button", { name: "sendOpenAPIRequest" }));
    const signal = (vi.mocked(fetch).mock.calls[0]?.[1] as RequestInit).signal;

    view.unmount();

    expect(signal?.aborted).toBe(true);
  });

  it("renders a network error in flow without leaving the workbench stuck in sending state", async () => {
    vi.stubGlobal("fetch", vi.fn().mockRejectedValue(new Error("network down")));
    const user = userEvent.setup();
    renderWorkbench();

    await user.click(screen.getByRole("button", { name: "sendOpenAPIRequest" }));

    expect(await screen.findByText("network down")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "sendOpenAPIRequest" })).toBeEnabled();
  });

  it("marks an oversized streamed response truncated in the normal result flow", async () => {
    const { OPENAPI_RESPONSE_BYTE_LIMIT } = await import("./_components/openapi-response-reader");
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(new Response(new Uint8Array(OPENAPI_RESPONSE_BYTE_LIMIT + 1).fill(97), { status: 200 })));
    const user = userEvent.setup();
    renderWorkbench();

    await user.click(screen.getByRole("button", { name: "sendOpenAPIRequest" }));

    expect(await screen.findByText("openAPIResponseTruncated")).toBeInTheDocument();
    expect(screen.getByTestId("openapi-invocation-result")).toHaveTextContent("a".repeat(100));
  });

  it("omits optional blank query and header values from URL, curl, and fetch", async () => {
    const fetchMock = vi.fn().mockResolvedValue(new Response("ok", { status: 200 }));
    vi.stubGlobal("fetch", fetchMock);
    const user = userEvent.setup();
    renderWorkbench();
    await user.clear(screen.getByRole("textbox", { name: "include" }));
    await user.clear(screen.getByRole("textbox", { name: "X-Trace" }));

    await user.click(screen.getByRole("button", { name: "sendOpenAPIRequest" }));

    expect(fetchMock).toHaveBeenCalledWith("https://gateway.example/v1/api/user-api/users/u-1", expect.objectContaining({
      headers: expect.not.objectContaining({ "X-Trace": expect.anything() }),
    }));
    expect(screen.getByText(/^curl /)).not.toHaveTextContent("X-Trace");
  });
});
