import { useState } from "react";
import { fireEvent, render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";

import type { APICatalogRoute, APICatalogService } from "@/lib/api/api-access";

import { InvocationWorkbench, type InvocationWorkbenchProps } from "./invocation-workbench";
import type { RequestExampleDraft } from "./request-example-draft";

const clipboard = vi.hoisted(() => ({ copy: vi.fn() }));

vi.mock("next-intl", () => ({
  useTranslations: () => (key: string) => key,
}));
vi.mock("@/lib/utils/clipboard", () => ({ copyTextWithFeedback: clipboard.copy }));

const service: APICatalogService = {
  id: 7,
  slug: "weather",
  name: "Weather",
  description: "Forecast data",
};
const route: APICatalogRoute = {
  id: 9,
  api_service_id: 7,
  slug: "forecast",
  protocols: ["http", "websocket"],
  allowed_methods: ["GET", "POST"],
  websocket_subprotocols: ["weather.v1"],
  example_request: {
    method: "POST",
    subpath: "/cities/Paris",
    query: "unit=c&unit=f",
    headers: { "Content-Type": "application/json" },
    body: "{\"days\":3}",
  },
};
const initialDraft: RequestExampleDraft = { ...route.example_request, headers: { ...route.example_request.headers } };
const token = { id: 5, name: "Production Token", key: "sk-production-secret" };

function renderWorkbench(overrides: Partial<InvocationWorkbenchProps> = {}) {
  const props: InvocationWorkbenchProps = {
    origin: "https://gateway.example",
    service,
    route,
    protocol: "http",
    draft: initialDraft,
    tokenID: 0,
    tokenChecking: false,
    tokenFailure: null,
    effectiveAccess: "granted",
    onProtocolChange: vi.fn(),
    onDraftChange: vi.fn(),
    onChooseToken: vi.fn(),
    onTokenCommandCopied: vi.fn(),
    ...overrides,
  };
  return { ...render(<InvocationWorkbench {...props} />), props };
}

function ControlledWorkbench({
  initial = initialDraft,
  protocol = "http" as const,
}: {
  initial?: RequestExampleDraft;
  protocol?: "http" | "websocket";
}) {
  const [draft, setDraft] = useState(initial);
  return (
    <InvocationWorkbench
      origin="https://gateway.example"
      service={service}
      route={route}
      protocol={protocol}
      draft={draft}
      tokenID={0}
      tokenChecking={false}
      tokenFailure={null}
      effectiveAccess="granted"
      onProtocolChange={() => {}}
      onDraftChange={setDraft}
      onChooseToken={() => {}}
      onTokenCommandCopied={() => {}}
    />
  );
}

describe("InvocationWorkbench", () => {
  beforeEach(() => {
    clipboard.copy.mockReset();
    clipboard.copy.mockResolvedValue(true);
  });

  it("shows only the public Service, Route, and request line", () => {
    renderWorkbench();

    expect(screen.getByRole("heading", { name: /Weather.*forecast/i })).toBeInTheDocument();
    expect(screen.getByText("https://gateway.example/v1/api/weather/forecast/cities/Paris?unit=c&unit=f")).toBeInTheDocument();
    expect(screen.getAllByText("POST")).not.toHaveLength(0);
    expect(document.body).not.toHaveTextContent(/backend|upstream|credential|proxy_url|header_override/i);
  });

  it("updates the HTTP command preview from every editable request field", async () => {
    const user = userEvent.setup();
    render(<ControlledWorkbench />);

    await user.clear(screen.getByRole("textbox", { name: "method" }));
    await user.type(screen.getByRole("textbox", { name: "method" }), "PATCH");
    await user.clear(screen.getByRole("textbox", { name: "subpath" }));
    await user.type(screen.getByRole("textbox", { name: "subpath" }), "/alerts");
    await user.clear(screen.getByRole("textbox", { name: "query" }));
    await user.type(screen.getByRole("textbox", { name: "query" }), "kind=a&kind=b");
    await user.clear(screen.getByRole("textbox", { name: "headerValue 1" }));
    await user.type(screen.getByRole("textbox", { name: "headerValue 1" }), "text/plain");
    await user.clear(screen.getByRole("textbox", { name: "body" }));
    await user.type(screen.getByRole("textbox", { name: "body" }), "hello");

    const command = screen.getByTestId("invocation-command-preview");
    expect(command).toHaveTextContent("--request 'PATCH'");
    expect(command).toHaveTextContent("/alerts?kind=a&kind=b");
    expect(command).toHaveTextContent("Content-Type: text/plain");
    expect(command).toHaveTextContent("--data-raw 'hello'");
  });

  it("shows header and HTTP body editors only when populated, and never shows a WebSocket body editor", () => {
    const empty = { method: "GET", subpath: "", query: "", headers: {}, body: "" };
    const http = render(<ControlledWorkbench initial={empty} />);
    expect(screen.queryByRole("textbox", { name: /headerName/ })).not.toBeInTheDocument();
    expect(screen.queryByRole("textbox", { name: "body" })).not.toBeInTheDocument();
    http.unmount();

    render(<ControlledWorkbench protocol="websocket" />);
    expect(screen.getByRole("textbox", { name: /headerName/ })).toBeInTheDocument();
    expect(screen.queryByRole("textbox", { name: "body" })).not.toBeInTheDocument();
  });

  it("uses ToggleGroup only for a dual-protocol Route", () => {
    const dual = renderWorkbench();
    expect(screen.getByRole("group", { name: "invocationProtocol" })).toBeInTheDocument();
    dual.unmount();

    renderWorkbench({ route: { ...route, protocols: ["http"] } });
    expect(screen.queryByRole("group", { name: "invocationProtocol" })).not.toBeInTheDocument();
  });

  it("builds a websocat preview with a subprotocol and no body", () => {
    renderWorkbench({
      protocol: "websocket",
      draft: {
        ...initialDraft,
        headers: { "Sec-WebSocket-Protocol": "weather.v2" },
        body: "must-not-leak",
      },
    });

    const command = screen.getByTestId("invocation-command-preview");
    expect(command).toHaveTextContent(/^websocat/);
    expect(command).toHaveTextContent("--protocol 'weather.v2'");
    expect(command).not.toHaveTextContent("must-not-leak");
    expect(screen.getAllByText(/wss:\/\/gateway\.example\/v1\/api\/weather\/forecast/)).not.toHaveLength(0);
  });

  it("previews and copies an API_TOKEN template when no Token is selected", async () => {
    const user = userEvent.setup();
    renderWorkbench();

    expect(screen.getByTestId("invocation-command-preview")).toHaveTextContent("${API_TOKEN}");
    await user.click(screen.getByRole("button", { name: "copyTemplate" }));

    expect(clipboard.copy).toHaveBeenCalledWith(expect.stringContaining("${API_TOKEN}"), expect.anything());
  });

  it("copies a verified Token through the primary action and remembers only after clipboard success", async () => {
    const user = userEvent.setup();
    const { props } = renderWorkbench({ token, tokenID: 5 });

    const realAction = screen.getByRole("button", { name: "copyCommand" });
    expect(realAction).toBeEnabled();
    await user.click(realAction);

    expect(clipboard.copy).toHaveBeenCalledWith(expect.stringContaining("sk-production-secret"), expect.anything());
    expect(props.onTokenCommandCopied).toHaveBeenCalledOnce();
    expect(screen.getByTestId("invocation-command-preview")).not.toHaveTextContent("sk-production-secret");
  });

  it("does not remember a Token when clipboard copying fails", async () => {
    clipboard.copy.mockResolvedValue(false);
    const user = userEvent.setup();
    const { props } = renderWorkbench({ token, tokenID: 5 });

    await user.click(screen.getByRole("button", { name: "copyCommand" }));

    expect(props.onTokenCommandCopied).not.toHaveBeenCalled();
  });

  it.each(["not_granted", "unknown"] as const)(
    "lets Route-validated explicit Tokens copy when account effective access is %s",
    (effectiveAccess) => {
      renderWorkbench({ token, tokenID: 5, effectiveAccess });

      expect(screen.getByRole("button", { name: "copyCommand" })).toBeEnabled();
    },
  );

  it.each([
    { tokenChecking: true, tokenFailure: null },
    { tokenChecking: false, tokenFailure: "miss" as const },
    { tokenChecking: false, tokenFailure: "error" as const },
  ])("disables real-Token copying for unresolved validation while keeping the template action", async (state) => {
    const user = userEvent.setup();
    renderWorkbench({ token, tokenID: 5, ...state });

    expect(screen.getByRole("button", { name: "copyCommand" })).toBeDisabled();
    await user.click(screen.getByRole("button", { name: "copyTemplate" }));
    expect(clipboard.copy).toHaveBeenCalledWith(expect.stringContaining("${API_TOKEN}"), expect.anything());
  });

  it("keeps effective access auxiliary when Token validation misses", () => {
    renderWorkbench({ tokenID: 5, tokenFailure: "miss", effectiveAccess: "granted" });

    expect(screen.getByText("tokenUnavailable")).toBeInTheDocument();
    expect(screen.getByText("effectiveAccessHint")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "copyCommand" })).toBeDisabled();
  });

  it("rejects an Authorization header rename without changing the draft", async () => {
    const user = userEvent.setup();
    const onDraftChange = vi.fn();
    renderWorkbench({ onDraftChange });

    const name = screen.getByRole("textbox", { name: "headerName 1" });
    await user.clear(name);
    await user.type(name, "Authorization");

    expect(screen.getByText("unsafeExampleHeader")).toBeInTheDocument();
    expect(name).toHaveAttribute("aria-invalid", "true");
    expect(name).toHaveAttribute("aria-describedby", screen.getByText("unsafeExampleHeader").id);
    expect(onDraftChange).not.toHaveBeenCalledWith(expect.objectContaining({
      headers: expect.objectContaining({ Authorization: expect.anything() }),
    }));
  });

  it("keeps focus while renaming a header through multiple characters", async () => {
    const user = userEvent.setup();
    render(<ControlledWorkbench />);
    const name = screen.getByRole("textbox", { name: "headerName 1" });

    await user.clear(name);
    await user.type(name, "X-Weather-Mode");

    expect(screen.getByRole("textbox", { name: "headerName 1" })).toHaveValue("X-Weather-Mode");
    expect(screen.getByRole("textbox", { name: "headerName 1" })).toHaveFocus();
  });

  it("rejects duplicate header names without dropping either row", () => {
    render(<ControlledWorkbench initial={{
      ...initialDraft,
      headers: { "Content-Type": "application/json", "X-Test": "1" },
    }} />);
    const firstName = screen.getByRole("textbox", { name: "headerName 1" });

    fireEvent.change(firstName, { target: { value: "x-test" } });

    expect(screen.getByText("invalidExampleHeader")).toBeInTheDocument();
    expect(screen.getAllByRole("textbox", { name: /headerName/ })).toHaveLength(2);
    expect(screen.getByRole("textbox", { name: "headerName 2" })).toHaveValue("X-Test");
  });

  it("keeps command overflow contained and action buttons content-width on narrow layouts", () => {
    renderWorkbench();

    const preview = screen.getByTestId("invocation-command-preview");
    expect(preview).toHaveClass("min-w-0", "max-w-full", "overflow-auto", "whitespace-pre-wrap", "break-all");
    const actions = screen.getByTestId("invocation-actions");
    expect(actions).toHaveClass("flex-wrap");
    for (const button of within(actions).getAllByRole("button")) {
      expect(button).not.toHaveClass("w-full");
    }
  });
});
