import { StrictMode } from "react";
import { act, fireEvent, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { hydrateRoot } from "react-dom/client";
import { renderToString } from "react-dom/server";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import type { APICatalogRoute, APICatalogService } from "@/lib/api/api-access";
import { renderWithQueryClient } from "@/test/render";

import APICatalogPage, { useBrowserOrigin } from "./page";

const navigation = vi.hoisted(() => {
  let query = "";
  let history = [""];
  let index = 0;
  const listeners = new Set<() => void>();
  const emit = () => listeners.forEach((listener) => listener());
  const searchOf = (href: string) => new URL(href, "http://local").search.slice(1);
  const replace = vi.fn((href: string) => {
    queueMicrotask(() => {
      query = searchOf(href);
      history[index] = query;
      emit();
    });
  });

  return {
    router: { replace },
    replace,
    subscribe(listener: () => void) {
      listeners.add(listener);
      return () => listeners.delete(listener);
    },
    snapshot: () => query,
    reset(next = "") {
      query = next;
      history = [next];
      index = 0;
      listeners.clear();
      replace.mockClear();
    },
    visit(next: string) {
      history = [...history.slice(0, index + 1), next];
      index += 1;
      query = next;
      emit();
    },
    back() {
      if (index === 0) return;
      index -= 1;
      query = history[index];
      emit();
    },
    forward() {
      if (index >= history.length - 1) return;
      index += 1;
      query = history[index];
      emit();
    },
  };
});

vi.mock("next-intl", () => ({
  useTranslations: () => (key: string, values?: Record<string, unknown>) => values?.count === undefined ? key : `${key}:${values.count}`,
}));
vi.mock("next/navigation", async () => {
  const { useMemo, useSyncExternalStore } = await import("react");
  return {
    usePathname: () => "/api-catalog",
    useRouter: () => navigation.router,
    useSearchParams: () => {
      const query = useSyncExternalStore(navigation.subscribe, navigation.snapshot, navigation.snapshot);
      return useMemo(() => new URLSearchParams(query), [query]);
    },
  };
});
const emptyExample = { method: "GET", subpath: "", query: "", headers: {}, body: "" };
const forecastExample = { method: "POST", subpath: "/cities/Paris", query: "unit=c", headers: { "Content-Type": "application/json" }, body: "{\"days\":3}" };
const weather: APICatalogService = { id: 7, slug: "weather", name: "Weather", description: "Forecast" };
const maps: APICatalogService = { id: 8, slug: "maps", name: "Maps", description: "Directions" };
const forecast: APICatalogRoute = { id: 9, api_service_id: 7, slug: "forecast", protocols: ["http"], allowed_methods: ["GET", "POST"], websocket_subprotocols: [], example_request: forecastExample };
const radar: APICatalogRoute = { id: 10, api_service_id: 7, slug: "radar", protocols: ["websocket"], allowed_methods: ["GET"], websocket_subprotocols: [], example_request: emptyExample };
const geocode: APICatalogRoute = { id: 11, api_service_id: 8, slug: "geocode", protocols: ["http"], allowed_methods: ["POST"], websocket_subprotocols: [], example_request: emptyExample };
const traffic: APICatalogRoute = { id: 13, api_service_id: 8, slug: "traffic", protocols: ["websocket"], allowed_methods: ["GET"], websocket_subprotocols: [], example_request: emptyExample };

function page<T>(data: T[], current: number, total: number) {
  return { data, total, page: current, page_size: 50 };
}

function json(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "content-type": "application/json" },
  });
}

function deferred<T>() {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>((done) => { resolve = done; });
  return { promise, resolve };
}

function jwt(userID: number, role = 1) {
  const payload = btoa(JSON.stringify({ user_id: userID, username: `viewer-${userID}`, role, exp: Math.floor(Date.now() / 1000) + 3600 }))
    .replaceAll("+", "-").replaceAll("/", "_").replaceAll("=", "");
  return `e30.${payload}.signature`;
}

type ResponseFactory = () => Promise<Response>;

class CatalogTransport {
  private readonly factories = new Map<string, ResponseFactory[]>();
  readonly fetch = vi.fn((input: string | URL | Request) => {
    const url = new URL(String(input), "http://local");
    const key = `${url.pathname}${url.search}`;
    const factories = this.factories.get(key);
    if (factories?.length) {
      const factory = factories.length > 1 ? factories.shift()! : factories[0];
      return factory();
    }
    if (url.pathname === "/api/api-catalog/effective") {
      return Promise.resolve(json({ scope: "routes", route_ids: [] }));
    }
    if (url.pathname === "/api/api-catalog/openapi") {
      return Promise.resolve(json({ document: { paths: {} } }));
    }
    return Promise.reject(new Error(`Unexpected request: ${key}`));
  });

  respond(path: string, body: unknown, status = 200) {
    this.factories.set(path, [() => Promise.resolve(json(body, status))]);
  }

  sequence(path: string, ...factories: ResponseFactory[]) {
    this.factories.set(path, factories);
  }

  calls(path: string) {
    return this.fetch.mock.calls.filter(([input]) => {
      const url = new URL(String(input), "http://local");
      return `${url.pathname}${url.search}` === path;
    }).length;
  }
}

const servicePath = (current = 1) => `/api/api-catalog/services?page=${current}&page_size=50`;
const routePath = (serviceID: number, current = 1) => `/api/api-catalog/routes?service_id=${serviceID}&page=${current}&page_size=50`;
const tokenServicePath = (tokenID: number, current = 1) => `/api/api-catalog/services?page=${current}&page_size=50&token_id=${tokenID}`;
const tokenRoutePath = (tokenID: number, serviceID: number, current = 1) => `/api/api-catalog/routes?service_id=${serviceID}&page=${current}&page_size=50&token_id=${tokenID}`;
const serviceSearchPath = (search: string, current = 1) => `/api/api-catalog/services?page=${current}&page_size=50&search=${encodeURIComponent(search)}`;
const routeSearchPath = (serviceID: number, search: string, current = 1) => `/api/api-catalog/routes?service_id=${serviceID}&page=${current}&page_size=50&search=${encodeURIComponent(search)}`;
const openAPIPath = (serviceID: number, tokenID?: number) => `/api/api-catalog/openapi?service_id=${serviceID}${tokenID === undefined ? "" : `&token_id=${tokenID}`}`;
const ownerValidationPath = (routeID = 9, tokenID = 5) => `/api/tokens?usable_only=true&user_id=1&api_service_id=7&api_route_id=${routeID}&token_id=${tokenID}&page=1&page_size=1`;
const globalValidationPath = (routeID = 9, tokenID = 5) => `/api/tokens?usable_only=true&api_service_id=7&api_route_id=${routeID}&token_id=${tokenID}&page=1&page_size=1`;

let transport: CatalogTransport;

function seedSingleService() {
  transport.respond(servicePath(), page([weather], 1, 1));
  transport.respond(routePath(7), page([forecast], 1, 1));
  transport.respond(tokenServicePath(5), page([weather], 1, 1));
  transport.respond(tokenRoutePath(5, 7), page([forecast], 1, 1));
}

function renderPage(query = "", strict = false) {
  navigation.reset(query);
  return renderWithQueryClient(strict ? <StrictMode><APICatalogPage /></StrictMode> : <APICatalogPage />);
}

function currentButton(name: string) {
  return screen.getAllByRole("button", { name }).find((button) => button.getAttribute("aria-current") === "true");
}

async function expectQuery(query: string) {
  await waitFor(() => expect(navigation.snapshot()).toBe(query));
}

describe("APICatalogPage", () => {
  beforeEach(() => {
    transport = new CatalogTransport();
    navigation.reset();
    window.localStorage.clear();
    window.localStorage.setItem("token", jwt(1, 2));
    window.dispatchEvent(new StorageEvent("storage", { key: "token" }));
    vi.stubGlobal("fetch", transport.fetch);
  });

  afterEach(() => vi.unstubAllGlobals());

  it("hydrates the browser origin without changing the server-rendered first frame", async () => {
    function OriginProbe() {
      return <span>{useBrowserOrigin()}</span>;
    }
    const container = document.createElement("div");
    container.innerHTML = renderToString(<OriginProbe />);
    const consoleError = vi.spyOn(console, "error").mockImplementation(() => {});

    expect(container).toHaveTextContent("");
    const root = hydrateRoot(container, <OriginProbe />);
    await waitFor(() => expect(container).toHaveTextContent(window.location.origin));

    expect(consoleError.mock.calls.flat().join(" ")).not.toMatch(/hydration|did not match|server rendered html/i);
    act(() => root.unmount());
    consoleError.mockRestore();
  });

  it("requires a Token before an ordinary user can load the catalog", async () => {
    // behavior change: a regular user's empty Token scope never falls back to the full catalog.
    window.localStorage.setItem("token", jwt(1, 1));
    window.dispatchEvent(new StorageEvent("storage", { key: "token" }));

    renderPage();

    expect(await screen.findByTestId("catalog-token-scope")).toHaveTextContent("selectTokenForCatalog");
    expect(screen.getByRole("combobox", { name: "tokenLabel" })).toBeInTheDocument();
    expect(transport.fetch.mock.calls.some(([input]) => String(input).includes("/api/api-catalog/"))).toBe(false);
  });

  it("forgets an unavailable remembered Token instead of falling back to the full catalog", async () => {
    // behavior change: token_not_available returns the ordinary user to required scope, never admin-all scope.
    window.localStorage.setItem("token", jwt(1, 1));
    window.localStorage.setItem("aigw:api-catalog-token-id:1", "5");
    window.dispatchEvent(new StorageEvent("storage", { key: "token" }));
    transport.respond(tokenServicePath(5), { code: "token_not_available", error: "Token unavailable" }, 404);

    renderPage("service_id=7&route_id=9&protocol=http");

    await waitFor(() => expect(window.localStorage.getItem("aigw:api-catalog-token-id:1")).toBeNull());
    expect(screen.getByTestId("catalog-token-scope")).toHaveTextContent("selectTokenForCatalog");
    expect(screen.getByText("tokenNotAvailable")).toBeInTheDocument();
    expect(transport.calls(servicePath())).toBe(0);
  });

  it("returns an administrator to admin-all after a remembered Token becomes unavailable", async () => {
    // behavior change: clearing an administrator's failed Token restores only the admin-all catalog scope.
    window.localStorage.setItem("token", jwt(1, 2));
    window.localStorage.setItem("aigw:api-catalog-token-id:1", "5");
    window.dispatchEvent(new StorageEvent("storage", { key: "token" }));
    transport.respond(tokenServicePath(5), { code: "token_not_available", error: "Token unavailable" }, 404);
    transport.respond(servicePath(), page([weather], 1, 1));
    transport.respond(routePath(7), page([forecast], 1, 1));

    renderPage("service_id=7&route_id=9&protocol=http");

    await waitFor(() => expect(window.localStorage.getItem("aigw:api-catalog-token-id:1")).toBeNull());
    expect(screen.getByTestId("catalog-token-scope")).toHaveTextContent("adminAllAPIs");
    await waitFor(() => expect(transport.calls(servicePath())).toBe(1));
  });

  it("does not reuse catalog cache or local workspace when two viewers remember the same Token ID", async () => {
    // behavior change: viewer identity owns both React Query data and local pagination/search/known/draft state.
    const viewerBServices = deferred<Response>();
    const viewerBToken = {
      id: 5, user_id: 2, key: "sk-viewer-b", name: "Viewer B Token", status: 1,
      expired_at: 0, models: "", trace_enabled: false, trace_mode: "full", api_role_mode: "explicit",
      created_at: 1, updated_at: 1,
    };
    window.localStorage.setItem("token", jwt(1));
    window.localStorage.setItem("aigw:api-catalog-token-id:1", "5");
    window.localStorage.setItem("aigw:api-catalog-token-id:2", "5");
    window.dispatchEvent(new StorageEvent("storage", { key: "token" }));
    transport.sequence(
      tokenServicePath(5),
      () => Promise.resolve(json(page([weather], 1, 2))),
      () => viewerBServices.promise,
    );
    transport.respond(tokenServicePath(5, 2), page([maps], 2, 2));
    transport.respond("/api/api-catalog/services?page=1&page_size=50&search=Weather&token_id=5", page([weather], 1, 1));
    transport.respond(tokenRoutePath(5, 7), page([forecast], 1, 1));
    transport.respond(globalValidationPath(9, 5), page([{ ...viewerBToken, id: 5, user_id: 1, name: "Viewer A Token" }], 1, 1));
    transport.respond("/api/tokens?page_size=50&usable_only=true&user_id=2", page([viewerBToken], 1, 1));
    const user = userEvent.setup();
    renderPage("service_id=7&route_id=9&protocol=http");

    await screen.findByRole("button", { name: "Weather" });
    await user.click(screen.getAllByRole("button", { name: "loadMoreServices" })[0]);
    await waitFor(() => expect(transport.calls(tokenServicePath(5, 2))).toBe(1));
    await user.type(screen.getByRole("textbox", { name: "serviceLabel" }), "Weather");
    await user.clear(await screen.findByRole("textbox", { name: "method" }));
    await user.type(screen.getByRole("textbox", { name: "method" }), "PATCH");
    window.localStorage.setItem("token", jwt(2));
    window.dispatchEvent(new StorageEvent("storage", { key: "token" }));

    await waitFor(() => expect(transport.calls(tokenServicePath(5))).toBe(2));
    expect(window.localStorage.getItem("aigw:api-catalog-token-id:1")).toBe("5");
    expect(window.localStorage.getItem("aigw:api-catalog-token-id:2")).toBe("5");
    expect(screen.queryByRole("button", { name: "Weather" })).not.toBeInTheDocument();
    expect(screen.queryByTestId("invocation-workbench")).not.toBeInTheDocument();
    await expectQuery("");

    transport.respond(tokenRoutePath(5, 8), page([geocode], 1, 1));
    viewerBServices.resolve(json(page([maps], 1, 1)));
    expect(await screen.findByRole("button", { name: "Maps" })).toBeInTheDocument();
    expect(screen.getByRole("textbox", { name: "serviceLabel" })).toHaveValue("");
    expect(await screen.findByRole("textbox", { name: "method" })).toHaveValue("GET");

    await user.click(screen.getByRole("combobox", { name: "tokenLabel" }));
    expect(await screen.findByRole("option", { name: /Viewer B Token/ })).toBeInTheDocument();
    expect(screen.queryByRole("option", { name: /Viewer A Token/ })).not.toBeInTheDocument();
    expect(transport.calls("/api/tokens?page_size=50&usable_only=true&user_id=2")).toBe(1);
  });

  it("fails closed when a warm Token-scoped Route refresh returns 503", async () => {
    // behavior change: an access failure hides cached catalog content and its callable Token action.
    const usableToken = {
      id: 5, user_id: 1, key: "sk-production-secret", name: "Production Token", status: 1,
      expired_at: 0, models: "", trace_enabled: false, trace_mode: "full", api_role_mode: "explicit",
      created_at: 1, updated_at: 1,
    };
    window.localStorage.setItem("token", jwt(1, 1));
    window.localStorage.setItem("aigw:api-catalog-token-id:1", "5");
    window.dispatchEvent(new StorageEvent("storage", { key: "token" }));
    transport.respond(tokenServicePath(5), page([weather], 1, 1));
    transport.sequence(
      tokenRoutePath(5, 7),
      () => Promise.resolve(json(page([forecast], 1, 1))),
      () => Promise.resolve(json({ code: "catalog_access_unavailable", error: "catalog unavailable" }, 503)),
    );
    transport.respond(globalValidationPath(), page([usableToken], 1, 1));
    const view = renderPage("service_id=7&route_id=9&protocol=http");

    await waitFor(() => expect(screen.getByRole("button", { name: "copyCommand" })).toBeEnabled());
    void view.queryClient.invalidateQueries({ queryKey: ["api-catalog", "routes", 1, ["token", 5], 7] });

    await waitFor(() => expect(transport.calls(tokenRoutePath(5, 7))).toBe(2));
    expect(await screen.findByRole("alert")).toHaveTextContent("catalogAccessUnavailable");
    expect(screen.queryByTestId("invocation-workbench")).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "copyCommand" })).not.toBeInTheDocument();
    expect(transport.calls(servicePath())).toBe(0);
  });

  it("forgets a remembered Token when its Route request says token_not_available", async () => {
    // behavior change: downstream Token failures clear the same viewer-scoped catalog Token.
    window.localStorage.setItem("token", jwt(1, 1));
    window.localStorage.setItem("aigw:api-catalog-token-id:1", "5");
    window.dispatchEvent(new StorageEvent("storage", { key: "token" }));
    transport.respond(tokenServicePath(5), page([weather], 1, 1));
    transport.respond(tokenRoutePath(5, 7), { code: "token_not_available", error: "Token unavailable" }, 404);

    renderPage("service_id=7&route_id=9&protocol=http");

    await waitFor(() => expect(window.localStorage.getItem("aigw:api-catalog-token-id:1")).toBeNull());
    expect(screen.getByTestId("catalog-token-scope")).toHaveTextContent("selectTokenForCatalog");
    expect(screen.getByText("tokenNotAvailable")).toBeInTheDocument();
  });

  it("clears one Token scope once when Strict Mode repeats the failure effect", async () => {
    // behavior change: repeated development effects must not clear the same selected Token twice.
    const storageKey = "aigw:api-catalog-token-id:1";
    window.localStorage.setItem("token", jwt(1, 1));
    window.localStorage.setItem(storageKey, "5");
    window.dispatchEvent(new StorageEvent("storage", { key: "token" }));
    transport.respond(tokenServicePath(5), page([weather], 1, 1));
    transport.respond(tokenRoutePath(5, 7), { code: "token_not_available", error: "Token unavailable" }, 404);
    const removeItem = vi.spyOn(Storage.prototype, "removeItem");

    renderPage("service_id=7&route_id=9&protocol=http", true);

    await waitFor(() => expect(window.localStorage.getItem(storageKey)).toBeNull());
    expect(removeItem.mock.calls.filter(([key]) => key === storageKey)).toHaveLength(1);
  });

  it("uses an administrator's selected external Token for both catalog scope and Route validation", async () => {
    // behavior change: administrators browse all Tokens first, then invoke with that Token's actual key.
    const externalToken = {
      id: 17, user_id: 99, key: "sk-alice-secret", name: "Alice production", owner_username: "alice",
      status: 1, expired_at: 0, models: "", trace_enabled: false, trace_mode: "full", api_role_mode: "explicit",
      created_at: 1, updated_at: 1,
    };
    seedSingleService();
    transport.respond("/api/tokens?page_size=50&usable_only=true", page([externalToken], 1, 1));
    transport.respond(tokenServicePath(17), page([weather], 1, 1));
    transport.respond(tokenRoutePath(17, 7), page([forecast], 1, 1));
    transport.respond(globalValidationPath(9, 17), page([externalToken], 1, 1));
    const user = userEvent.setup();
    renderPage("service_id=7&route_id=9&protocol=http");

    await user.click(await screen.findByRole("combobox", { name: "tokenLabel" }));
    await user.click(await screen.findByRole("option", { name: /Alice production.*alice/ }));

    await waitFor(() => expect(transport.calls(tokenServicePath(17))).toBe(1));
    await waitFor(() => expect(transport.calls(globalValidationPath(9, 17))).toBe(1));
    expect(transport.calls(ownerValidationPath(9, 17))).toBe(0);
    expect(await screen.findByRole("button", { name: "copyCommand" })).toBeEnabled();
  });

  it("selects the first Service and Route into the URL while preserving unrelated query", async () => {
    seedSingleService();
    renderPage("view=compact");

    await expectQuery("view=compact&service_id=7&route_id=9&protocol=http");
    expect(currentButton("Weather")).toBeInTheDocument();
    expect(currentButton("forecast")).toBeInTheDocument();
  });

  it("loads later Service and Route pages before accepting a deep link", async () => {
    transport.respond(servicePath(1), page([weather], 1, 2));
    transport.respond(servicePath(2), page([maps], 2, 2));
    transport.respond(routePath(8, 1), page([geocode], 1, 2));
    transport.respond(routePath(8, 2), page([traffic], 2, 2));

    renderPage("service_id=8&route_id=13&protocol=websocket");

    await waitFor(() => expect(currentButton("Maps")).toBeInTheDocument());
    await waitFor(() => expect(currentButton("traffic")).toBeInTheDocument());
    expect(navigation.snapshot()).toBe("service_id=8&route_id=13&protocol=websocket");
    expect(transport.calls(servicePath(2))).toBe(1);
    expect(transport.calls(routePath(7))).toBe(0);
    expect(transport.calls(routePath(8, 2))).toBe(1);
  });

  it("falls back only after Service and Route pagination is exhausted", async () => {
    transport.respond(servicePath(1), page([weather], 1, 2));
    transport.respond(servicePath(2), page([maps], 2, 2));
    transport.respond(routePath(7, 1), page([forecast], 1, 2));
    transport.respond(routePath(7, 2), page([radar], 2, 2));

    renderPage("service_id=999&route_id=998&protocol=websocket");

    await expectQuery("service_id=7&route_id=9&protocol=http");
    expect(transport.calls(servicePath(2))).toBe(1);
    expect(transport.calls(routePath(7, 2))).toBe(1);
  });

  it("normalizes a loaded Service after Service append fails and still completes its Route URL", async () => {
    transport.respond(servicePath(1), page([weather, maps], 1, 3));
    transport.respond(servicePath(2), { error: "service append failed" }, 503);
    transport.respond(routePath(7), page([forecast], 1, 1));
    transport.respond(routePath(8), page([geocode], 1, 1));
    const user = userEvent.setup();
    renderPage("service_id=7&route_id=9&protocol=http");
    await waitFor(() => expect(currentButton("Weather")).toBeInTheDocument());

    await user.click(screen.getAllByRole("button", { name: "loadMoreServices" })[0]);
    expect((await screen.findAllByRole("alert"))[0]).toHaveTextContent("loadFailed");
    await user.click(screen.getByRole("button", { name: "Maps" }));

    await expectQuery("service_id=8&route_id=11&protocol=http");
    expect(currentButton("geocode")).toBeInTheDocument();
  });

  it("normalizes a loaded Route after append failure when forward restores an unsupported protocol", async () => {
    transport.respond(servicePath(), page([weather], 1, 1));
    transport.respond(routePath(7, 1), page([forecast], 1, 2));
    transport.respond(routePath(7, 2), { error: "route append failed" }, 503);
    const user = userEvent.setup();
    renderPage("service_id=7&route_id=9&protocol=http");
    await waitFor(() => expect(currentButton("forecast")).toBeInTheDocument());

    await user.click(screen.getAllByRole("button", { name: "loadMoreRoutes" })[0]);
    expect(await screen.findByRole("alert")).toHaveTextContent("routesLoadFailed");
    act(() => navigation.visit("service_id=7&route_id=9&protocol=websocket"));
    act(() => navigation.back());
    act(() => navigation.forward());

    await expectQuery("service_id=7&route_id=9&protocol=http");
  });

  it("keeps Route page buckets for A page two, B page two, then back to A", async () => {
    const backgroundRefetch = deferred<Response>();
    transport.respond(servicePath(), page([weather, maps], 1, 2));
    transport.respond(routePath(7, 1), page([forecast], 1, 2));
    transport.sequence(
      routePath(7, 2),
      () => Promise.resolve(json(page([radar], 2, 2))),
      () => backgroundRefetch.promise,
    );
    transport.respond(routePath(8, 1), page([geocode], 1, 2));
    transport.respond(routePath(8, 2), page([traffic], 2, 2));
    renderPage("service_id=7&route_id=10&protocol=websocket");
    await waitFor(() => expect(currentButton("radar")).toBeInTheDocument());

    act(() => navigation.visit("service_id=8&route_id=13&protocol=websocket"));
    await waitFor(() => expect(currentButton("traffic")).toBeInTheDocument());
    act(() => navigation.back());

    await waitFor(() => expect(currentButton("radar")).toBeInTheDocument());
    expect(screen.getByRole("button", { name: "forecast" })).toBeInTheDocument();
    expect(transport.calls(routePath(7, 2))).toBe(2);
  });

  it("ignores a late Route page from the previously selected Service", async () => {
    const oldRoutePage = deferred<Response>();
    transport.respond(servicePath(), page([weather, maps], 1, 2));
    transport.respond(routePath(7, 1), page([forecast], 1, 2));
    transport.sequence(routePath(7, 2), () => oldRoutePage.promise);
    transport.respond(routePath(8), page([geocode], 1, 1));
    renderPage("service_id=7&route_id=10&protocol=websocket");
    await waitFor(() => expect(transport.calls(routePath(7, 2))).toBe(1));

    act(() => navigation.visit("service_id=8&route_id=11&protocol=http"));
    await waitFor(() => expect(currentButton("geocode")).toBeInTheDocument());
    oldRoutePage.resolve(json(page([radar], 2, 2)));

    await waitFor(() => expect(navigation.snapshot()).toBe("service_id=8&route_id=11&protocol=http"));
    expect(currentButton("geocode")).toBeInTheDocument();
    expect(screen.queryByText("radar")).not.toBeInTheDocument();
  });

  it("merges rapid protocol and Service patches before async navigation commits", async () => {
    const dualProtocolForecast = { ...forecast, protocols: ["http", "websocket"] as const };
    transport.respond(servicePath(), page([weather, maps], 1, 2));
    transport.respond(routePath(7), page([dualProtocolForecast], 1, 1));
    transport.respond(routePath(8), page([geocode], 1, 1));
    renderPage("view=compact&service_id=7&route_id=9&protocol=http");
    await screen.findByRole("radio", { name: "websocketProtocol" });

    fireEvent.click(screen.getByRole("radio", { name: "websocketProtocol" }));
    fireEvent.click(screen.getByRole("button", { name: "Maps" }));

    expect(navigation.replace).toHaveBeenNthCalledWith(2, "/api-catalog?view=compact&service_id=8");
    await expectQuery("view=compact&service_id=8&route_id=11&protocol=http");
  });

  it("does not request downstream data for Service ID zero and clears stale selection after empty success", async () => {
    const services = deferred<Response>();
    transport.sequence(servicePath(), () => services.promise);
    renderPage("service_id=0&route_id=9&protocol=http");

    await waitFor(() => expect(transport.calls(servicePath())).toBe(1));
    expect(transport.fetch.mock.calls.some(([input]) => String(input).includes("/api-catalog/routes"))).toBe(false);
    expect(transport.fetch.mock.calls.some(([input]) => String(input).includes("/api-catalog/effective"))).toBe(false);
    services.resolve(json(page([], 1, 0)));
    await expectQuery("");
    expect(transport.fetch.mock.calls.some(([input]) => String(input).includes("/api-catalog/routes"))).toBe(false);
    expect(transport.fetch.mock.calls.some(([input]) => String(input).includes("/api-catalog/effective"))).toBe(false);
  });

  it.each([
    { role: 2, label: "administrator" },
    { role: 1, label: "ordinary user" },
  ])("starts the $label catalog directly in the remembered Token scope", async ({ role }) => {
    window.localStorage.setItem("token", jwt(1, role));
    window.localStorage.setItem("aigw:api-catalog-token-id:1", "17");
    window.dispatchEvent(new StorageEvent("storage", { key: "token" }));
    transport.respond(tokenServicePath(17), page([weather], 1, 1));
    transport.respond(tokenRoutePath(17, 7), page([forecast], 1, 1));
    renderPage("service_id=7&route_id=9&protocol=http");

    await waitFor(() => expect(transport.calls(tokenServicePath(17))).toBe(1));
    const catalogRequests = transport.fetch.mock.calls
      .map(([input]) => new URL(String(input), "http://local"))
      .filter((url) => url.pathname.startsWith("/api/api-catalog/"));
    expect(catalogRequests[0]?.searchParams.get("token_id")).toBe("17");
    expect(transport.calls(servicePath())).toBe(0);
  });

  it("switches catalog state to the new Token scope before either deferred response can render", async () => {
    const tokenAService = deferred<Response>();
    const tokenARoute = deferred<Response>();
    const tokenBService = deferred<Response>();
    const tokenA = { id: 17, user_id: 1, name: "Token A", owner_username: "viewer-1" };
    const tokenB = { id: 18, user_id: 1, name: "Token B", owner_username: "viewer-1" };
    window.localStorage.setItem("token", jwt(1));
    window.localStorage.setItem("aigw:api-catalog-token-id:1", "17");
    window.dispatchEvent(new StorageEvent("storage", { key: "token" }));
    transport.sequence(tokenServicePath(17), () => tokenAService.promise);
    transport.sequence(tokenRoutePath(17, 7), () => tokenARoute.promise);
    transport.sequence(tokenServicePath(18), () => tokenBService.promise);
    transport.respond("/api/tokens?page_size=50&usable_only=true&user_id=1", page([tokenA, tokenB], 1, 2));
    const user = userEvent.setup();
    renderPage("service_id=7&route_id=9&protocol=http");

    await waitFor(() => expect(transport.calls(tokenServicePath(17))).toBe(1));
    tokenAService.resolve(json(page([weather], 1, 1)));
    await waitFor(() => expect(transport.calls(tokenRoutePath(17, 7))).toBe(1));
    await user.click(screen.getByRole("combobox", { name: "tokenLabel" }));
    await user.click(await screen.findByRole("option", { name: /Token B/ }));

    await waitFor(() => expect(transport.calls(tokenServicePath(18))).toBe(1));
    await expectQuery("");
    expect(screen.queryByText("Weather")).not.toBeInTheDocument();
    expect(screen.queryByText("forecast")).not.toBeInTheDocument();
    expect(screen.queryByTestId("invocation-workbench")).not.toBeInTheDocument();

    tokenBService.resolve(json(page([maps], 1, 1)));
    transport.respond(tokenRoutePath(18, 8), page([geocode], 1, 1));
    expect(await screen.findByRole("button", { name: "Maps" })).toBeInTheDocument();
    tokenARoute.resolve(json(page([forecast], 1, 1)));

    await waitFor(() => expect(currentButton("Maps")).toBeInTheDocument());
    expect(screen.queryByText("Weather")).not.toBeInTheDocument();
    expect(transport.calls(tokenRoutePath(17, 7))).toBe(1);
  });

  it("does not let a stale Route continuation prevent the new Token scope from loading page two", async () => {
    const continuations: Array<() => void> = [];
    const realQueueMicrotask = globalThis.queueMicrotask;
    const tokenB = {
      id: 18, user_id: 1, key: "sk-token-b", name: "Token B", status: 1,
      expired_at: 0, models: "", trace_enabled: false, trace_mode: "full", api_role_mode: "explicit",
      created_at: 1, updated_at: 1,
    };
    window.localStorage.setItem("token", jwt(1));
    window.localStorage.setItem("aigw:api-catalog-token-id:1", "17");
    window.dispatchEvent(new StorageEvent("storage", { key: "token" }));
    transport.respond(tokenServicePath(17), page([weather], 1, 1));
    transport.respond(tokenRoutePath(17, 7), page([forecast], 1, 2));
    transport.respond(tokenServicePath(18), page([weather], 1, 1));
    transport.respond(tokenRoutePath(18, 7), page([forecast], 1, 2));
    transport.respond(tokenRoutePath(18, 7, 2), page([radar], 2, 2));
    transport.respond(globalValidationPath(10, 18), page([tokenB], 1, 1));
    transport.respond(globalValidationPath(9, 17), page([{ ...tokenB, id: 17, key: "sk-token-a" }], 1, 1));
    renderPage("service_id=7&route_id=9&protocol=http");

    expect(await screen.findByRole("button", { name: "forecast" })).toHaveAttribute("aria-current", "true");
    vi.stubGlobal("queueMicrotask", (continuation: () => void) => continuations.push(continuation));
    navigation.visit("service_id=7&route_id=10&protocol=websocket");
    await waitFor(() => expect(continuations).toHaveLength(1));
    vi.stubGlobal("queueMicrotask", realQueueMicrotask);
    window.localStorage.setItem("aigw:api-catalog-token-id:1", "18");
    window.dispatchEvent(new StorageEvent("storage", { key: "aigw:api-catalog-token-id:1" }));

    await waitFor(() => expect(transport.calls(tokenServicePath(18))).toBe(1));
    act(() => continuations.shift()?.());
    navigation.visit("service_id=7&route_id=10&protocol=websocket");

    await waitFor(() => expect(transport.calls(tokenRoutePath(18, 7, 2))).toBe(1));
    expect(await screen.findByRole("button", { name: "radar" })).toHaveAttribute("aria-current", "true");
  });

  it("starts Token A from page one after returning from Token B", async () => {
    // behavior change: a Token scope visit never revives its previous page-two catalog state.
    const tokenA = { id: 17, user_id: 1, name: "Token A", owner_username: "viewer-1" };
    const tokenB = { id: 18, user_id: 1, name: "Token B", owner_username: "viewer-1" };
    const trafficService = { ...maps, id: 19, slug: "traffic", name: "Traffic" };
    window.localStorage.setItem("token", jwt(1));
    window.localStorage.setItem("aigw:api-catalog-token-id:1", "17");
    window.dispatchEvent(new StorageEvent("storage", { key: "token" }));
    transport.respond(tokenServicePath(17), page([weather], 1, 2));
    transport.respond(tokenServicePath(17, 2), page([trafficService], 2, 2));
    transport.respond(tokenRoutePath(17, 7), page([forecast], 1, 1));
    transport.respond(tokenServicePath(18), page([maps], 1, 1));
    transport.respond(tokenRoutePath(18, 8), page([geocode], 1, 1));
    transport.respond(globalValidationPath(9, 17), page([tokenA], 1, 1));
    transport.respond(globalValidationPath(11, 18), page([tokenB], 1, 1));
    const user = userEvent.setup();
    renderPage("service_id=7&route_id=9&protocol=http");

    await screen.findByRole("button", { name: "Weather" });
    await user.click((await screen.findAllByRole("button", { name: "loadMoreServices" }))[0]);
    expect(await screen.findByRole("button", { name: "Traffic" })).toBeInTheDocument();
    window.localStorage.setItem("aigw:api-catalog-token-id:1", "18");
    window.dispatchEvent(new StorageEvent("storage", { key: "aigw:api-catalog-token-id:1" }));
    expect(await screen.findByRole("button", { name: "Maps" })).toBeInTheDocument();
    window.localStorage.setItem("aigw:api-catalog-token-id:1", "17");
    window.dispatchEvent(new StorageEvent("storage", { key: "aigw:api-catalog-token-id:1" }));

    expect(await screen.findByRole("button", { name: "Weather" })).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Traffic" })).not.toBeInTheDocument();
    expect(transport.calls(tokenServicePath(17, 2))).toBe(1);
  });

  it("keeps Token A page two after a same-value storage notification", async () => {
    // behavior change: remembering the selected Token again does not start a new scope visit.
    const tokenA = { id: 17, user_id: 1, name: "Token A", owner_username: "viewer-1" };
    const trafficService = { ...maps, id: 19, slug: "traffic", name: "Traffic" };
    window.localStorage.setItem("token", jwt(1));
    window.localStorage.setItem("aigw:api-catalog-token-id:1", "17");
    window.dispatchEvent(new StorageEvent("storage", { key: "token" }));
    transport.respond(tokenServicePath(17), page([weather], 1, 2));
    transport.respond(tokenServicePath(17, 2), page([trafficService], 2, 2));
    transport.respond(tokenRoutePath(17, 7), page([forecast], 1, 1));
    transport.respond(globalValidationPath(9, 17), page([tokenA], 1, 1));
    const user = userEvent.setup();
    renderPage("service_id=7&route_id=9&protocol=http");

    await screen.findByRole("button", { name: "Weather" });
    await user.click((await screen.findAllByRole("button", { name: "loadMoreServices" }))[0]);
    expect(await screen.findByRole("button", { name: "Traffic" })).toBeInTheDocument();
    act(() => window.dispatchEvent(new StorageEvent("storage", { key: "aigw:api-catalog-token-id:1" })));

    expect(screen.getByRole("button", { name: "Traffic" })).toBeInTheDocument();
    expect(transport.calls(tokenServicePath(17, 2))).toBe(1);
  });

  it("renders the Token scope before the two mobile catalog navigators", async () => {
    window.localStorage.setItem("token", jwt(1, 2));
    window.dispatchEvent(new StorageEvent("storage", { key: "token" }));
    seedSingleService();
    renderPage();

    await expectQuery("service_id=7&route_id=9&protocol=http");
    expect(screen.getByTestId("catalog-token-scope")).toHaveTextContent("adminAllAPIs");
    expect(screen.getByRole("combobox", { name: "tokenLabel" })).toBeInTheDocument();
    const mobile = screen.getByTestId("catalog-mobile-toolbar");
    expect(mobile).toHaveClass("lg:hidden", "flex-col");
    expect(within(mobile).getByRole("combobox", { name: "mobileServicePicker" })).toHaveTextContent("Weather");
    expect(within(mobile).getByRole("combobox", { name: "mobileRoutePicker" })).toHaveTextContent("forecast");
    expect(within(mobile).queryByRole("combobox", { name: "mobileTokenPicker" })).not.toBeInTheDocument();
    expect(screen.getByTestId("catalog-desktop-service-navigation")).toHaveClass("hidden", "lg:block");
    expect(screen.getByTestId("catalog-main")).toHaveClass("min-w-0");
  });

  it("renders Service, operation navigation, document, and invocation in the normal desktop and mobile page flow", async () => {
    seedSingleService();
    transport.respond(openAPIPath(7), {
      document: {
        paths: {
          "/forecast/{city}": {
            "x-ai-gateway-route-slug": "forecast",
            get: { summary: "City forecast", parameters: [{ name: "city", in: "path", required: true, example: "Paris" }], responses: { "200": { description: "Forecast" } } },
          },
        },
      },
    });

    renderPage("service_id=7&route_id=9&path=%2Fforecast%2F%7Bcity%7D&method=get");

    expect(await screen.findByTestId("catalog-openapi-workbench")).toBeInTheDocument();
    expect(screen.getByTestId("catalog-desktop-service-navigation")).toContainElement(currentButton("Weather")!);
    expect(screen.getByTestId("catalog-desktop-operation-navigation")).toHaveClass("hidden", "lg:block");
    expect(screen.getAllByRole("button", { name: "GET /forecast/{city}" })).toHaveLength(2);
    expect(screen.getByTestId("openapi-operation-document")).toHaveTextContent("City forecast");
    expect(screen.getByTestId("openapi-invocation-workbench")).toBeInTheDocument();
    expect(document.querySelector('[role="dialog"]')).toBeNull();
  });

  it.each([
    ["loading", 0, "openAPILoading"],
    ["500 error", 500, "openAPILoadFailed"],
    ["503 error", 503, "openAPILoadFailed"],
    ["empty", 200, "openAPIEmpty"],
  ])("keeps legacy Route navigation and invocation reachable for OpenAPI %s", async (state, status, message) => {
    seedSingleService();
    if (state === "loading") {
      transport.sequence(openAPIPath(7), () => new Promise<Response>(() => {}));
    } else if (status === 200) {
      transport.respond(openAPIPath(7), { document: { paths: {} } });
    } else {
      transport.respond(openAPIPath(7), { code: "catalog_access_unavailable", error: "OpenAPI unavailable" }, status);
    }

    renderPage("service_id=7&route_id=9&protocol=http");

    expect(await screen.findByTestId("invocation-workbench")).toBeInTheDocument();
    expect(screen.getByTestId("catalog-mobile-toolbar")).toBeInTheDocument();
    expect(within(screen.getByTestId("catalog-main")).getByRole("button", { name: "forecast" })).toHaveAttribute("aria-current", "true");
    expect(screen.getByText(message)).toBeInTheDocument();
    if (state.endsWith("error")) expect(screen.getByRole("button", { name: "retry" })).toBeInTheDocument();
  });

  it("automatically loads the 51st Route identity before preserving an OpenAPI deep link", async () => {
    const firstFifty = Array.from({ length: 50 }, (_, index): APICatalogRoute => ({
      ...forecast,
      id: index + 1,
      slug: `route-${index + 1}`,
    }));
    const route51: APICatalogRoute = { ...forecast, id: 51, slug: "route-51" };
    transport.respond(servicePath(), page([weather], 1, 1));
    transport.respond(routePath(7), page(firstFifty, 1, 51));
    transport.respond(routePath(7, 2), page([route51], 2, 51));
    transport.respond(openAPIPath(7), {
      document: {
        paths: {
          "/route-51/items": {
            "x-ai-gateway-route-slug": "route-51",
            get: { summary: "Operation 51", responses: { "200": { description: "ok" } } },
          },
        },
      },
    });

    renderPage("service_id=7&route_id=51&path=%2Froute-51%2Fitems&method=get");

    await waitFor(() => expect(transport.calls(routePath(7, 2))).toBe(1));
    expect(await screen.findByTestId("catalog-openapi-workbench")).toHaveTextContent("Operation 51");
    expect(screen.getAllByRole("button", { name: "GET /route-51/items" })[0]).toHaveAttribute("aria-current", "true");
    await expectQuery("service_id=7&route_id=51&path=%2Froute-51%2Fitems&method=get");
  });

  it("keeps legacy invocation visible and reports documented Route identities still missing after pagination is exhausted", async () => {
    seedSingleService();
    transport.respond(openAPIPath(7), {
      document: {
        paths: {
          "/missing/items": {
            "x-ai-gateway-route-slug": "missing",
            get: { responses: { "200": { description: "ok" } } },
          },
        },
      },
    });

    renderPage("service_id=7&route_id=9&protocol=http");

    expect(await screen.findByText("openAPIRoutesUnresolved")).toBeInTheDocument();
    expect(screen.getByTestId("invocation-workbench")).toBeInTheDocument();
    expect(within(screen.getByTestId("catalog-main")).getByRole("button", { name: "forecast" })).toHaveAttribute("aria-current", "true");
  });

  it("stops OpenAPI Route identity pagination when a non-empty stale page adds no Route ID", async () => {
    transport.respond(servicePath(), page([weather], 1, 1));
    transport.respond(routePath(7), page([forecast], 1, 100));
    transport.respond(routePath(7, 2), page([forecast], 2, 100));
    transport.respond(openAPIPath(7), {
      document: { paths: { "/missing": { "x-ai-gateway-route-slug": "missing", get: { responses: {} } } } },
    });

    renderPage("service_id=7&route_id=9&protocol=http");

    expect(await screen.findByText("openAPIRoutesUnresolved")).toBeInTheDocument();
    expect(transport.calls(routePath(7, 2))).toBe(1);
    expect(transport.calls(routePath(7, 3))).toBe(0);
    expect(screen.getByTestId("invocation-workbench")).toBeInTheDocument();
  });

  it("returns to legacy Route invocation when a warm OpenAPI refresh fails", async () => {
    seedSingleService();
    transport.sequence(
      openAPIPath(7),
      () => Promise.resolve(json({ document: { paths: { "/forecast": { "x-ai-gateway-route-slug": "forecast", get: { responses: {} } } } } })),
      () => Promise.resolve(json({ code: "catalog_access_unavailable", error: "OpenAPI unavailable" }, 503)),
    );
    const view = renderPage("service_id=7&route_id=9&path=%2Fforecast&method=get");
    expect(await screen.findByTestId("catalog-openapi-workbench")).toBeInTheDocument();

    await view.queryClient.invalidateQueries({ queryKey: ["api-catalog", "openapi"] });

    expect(await screen.findByText("openAPILoadFailed")).toBeInTheDocument();
    expect(screen.getByTestId("invocation-workbench")).toBeInTheDocument();
    expect(screen.queryByTestId("catalog-openapi-workbench")).toBeNull();
  });

  it("forgets a remembered Token when only OpenAPI reports token_not_available", async () => {
    const usableToken = {
      id: 5, user_id: 1, key: "sk-production-secret", name: "Production Token", status: 1,
      expired_at: 0, models: "", trace_enabled: false, trace_mode: "full", api_role_mode: "explicit",
      created_at: 1, updated_at: 1,
    };
    window.localStorage.setItem("token", jwt(1));
    window.localStorage.setItem("aigw:api-catalog-token-id:1", "5");
    window.dispatchEvent(new StorageEvent("storage", { key: "token" }));
    seedSingleService();
    transport.respond(globalValidationPath(), page([usableToken], 1, 1));
    transport.respond(openAPIPath(7, 5), { code: "token_not_available", error: "Token unavailable" }, 404);

    renderPage("service_id=7&route_id=9&protocol=http");

    await waitFor(() => expect(window.localStorage.getItem("aigw:api-catalog-token-id:1")).toBeNull());
    expect(await screen.findByTestId("catalog-token-scope")).toHaveTextContent("selectTokenForCatalog");
    expect(screen.queryByTestId("invocation-workbench")).not.toBeInTheDocument();
  });

  it("keeps a remembered Token and legacy invocation when only OpenAPI returns 503", async () => {
    const usableToken = {
      id: 5, user_id: 1, key: "sk-production-secret", name: "Production Token", status: 1,
      expired_at: 0, models: "", trace_enabled: false, trace_mode: "full", api_role_mode: "explicit",
      created_at: 1, updated_at: 1,
    };
    window.localStorage.setItem("token", jwt(1));
    window.localStorage.setItem("aigw:api-catalog-token-id:1", "5");
    window.dispatchEvent(new StorageEvent("storage", { key: "token" }));
    seedSingleService();
    transport.respond(globalValidationPath(), page([usableToken], 1, 1));
    transport.respond(openAPIPath(7, 5), { code: "catalog_access_unavailable", error: "OpenAPI unavailable" }, 503);

    renderPage("service_id=7&route_id=9&protocol=http");

    expect(await screen.findByText("openAPILoadFailed")).toBeInTheDocument();
    expect(screen.getByTestId("invocation-workbench")).toBeInTheDocument();
    expect(window.localStorage.getItem("aigw:api-catalog-token-id:1")).toBe("5");
  });

  it("searches unloaded Services on the server without letting candidates rewrite the deep link", async () => {
    transport.respond(servicePath(), page([weather], 1, 2));
    transport.respond(routePath(7), page([forecast], 1, 1));
    transport.respond(serviceSearchPath("Maps"), page([maps], 1, 1));
    transport.respond(routePath(8), page([geocode], 1, 1));
    const user = userEvent.setup();
    renderPage("service_id=7&route_id=9&protocol=http");
    await waitFor(() => expect(currentButton("Weather")).toBeInTheDocument());

    await user.type(screen.getByRole("textbox", { name: "serviceLabel" }), "Maps");

    await waitFor(() => expect(transport.calls(serviceSearchPath("Maps"))).toBe(1));
    expect(navigation.snapshot()).toBe("service_id=7&route_id=9&protocol=http");
    expect(transport.calls(servicePath(2))).toBe(0);
    await user.click(await screen.findByRole("button", { name: "Maps" }));

    await expectQuery("service_id=8&route_id=11&protocol=http");
    expect(transport.calls(servicePath(2))).toBe(0);
  });

  it("searches Routes only inside the selected Service and preserves selection on search failure", async () => {
    transport.respond(servicePath(), page([weather, maps], 1, 2));
    transport.respond(routePath(7), page([forecast], 1, 2));
    transport.respond(routePath(8), page([geocode], 1, 1));
    transport.respond(routeSearchPath(7, "radar"), { error: "search unavailable" }, 503);
    const user = userEvent.setup();
    renderPage("service_id=7&route_id=9&protocol=http");
    await waitFor(() => expect(currentButton("forecast")).toBeInTheDocument());

    await user.type(screen.getByRole("textbox", { name: "routeLabel" }), "radar");

    await waitFor(() => expect(transport.calls(routeSearchPath(7, "radar"))).toBe(1));
    expect(transport.calls(routeSearchPath(8, "radar"))).toBe(0);
    expect(transport.calls(routePath(7, 2))).toBe(0);
    expect(navigation.snapshot()).toBe("service_id=7&route_id=9&protocol=http");
    expect(currentButton("forecast")).toBeInTheDocument();
    expect(screen.getByTestId("invocation-workbench")).toBeInTheDocument();
    expect(await screen.findByText("errorState")).toBeInTheDocument();
  });

  it("selects a remotely searched Route without loading unrelated or authoritative next pages", async () => {
    transport.respond(servicePath(), page([weather, maps], 1, 2));
    transport.respond(routePath(7), page([forecast], 1, 2));
    transport.respond(routePath(8), page([geocode], 1, 1));
    transport.respond(routeSearchPath(7, "radar"), page([radar], 1, 1));
    const user = userEvent.setup();
    renderPage("service_id=7&route_id=9&protocol=http");
    await waitFor(() => expect(currentButton("forecast")).toBeInTheDocument());

    await user.type(screen.getByRole("textbox", { name: "routeLabel" }), "radar");
    await user.click(await screen.findByRole("button", { name: "radar" }));

    await expectQuery("service_id=7&route_id=10&protocol=websocket");
    expect(transport.calls(routePath(7, 2))).toBe(0);
    expect(transport.calls(routeSearchPath(8, "radar"))).toBe(0);
  });

  it("keeps PageHeader visible and does not rewrite the URL on first-page failure", async () => {
    transport.respond(servicePath(), { error: "catalog unavailable" }, 503);
    renderPage("service_id=999&route_id=998&protocol=websocket");

    expect(await screen.findByRole("heading", { name: "title" })).toBeInTheDocument();
    expect(await screen.findByRole("alert")).toHaveTextContent("catalogAccessUnavailable");
    expect(navigation.snapshot()).toBe("service_id=999&route_id=998&protocol=websocket");
    expect(navigation.replace).not.toHaveBeenCalled();
  });

  it("isolates Route and effective-access failures from neighboring content", async () => {
    transport.respond(servicePath(), page([weather], 1, 1));
    transport.respond(routePath(7), { error: "routes unavailable" }, 503);
    renderPage("service_id=7");

    expect(await screen.findByRole("heading", { name: "Weather" })).toBeInTheDocument();
    expect(await screen.findByRole("alert")).toHaveTextContent("routesLoadFailed");
    expect(navigation.snapshot()).toBe("service_id=7");
  });

  it("keeps Route browsing visible when only effective access fails", async () => {
    seedSingleService();
    transport.respond("/api/api-catalog/effective?service_id=7", { error: "effective unavailable" }, 503);
    renderPage("service_id=7&route_id=9&protocol=http");

    expect(await screen.findByTestId("invocation-workbench")).toHaveAttribute("data-effective-access", "unknown");
    expect(currentButton("forecast")).toBeInTheDocument();
    expect(screen.queryByText("tokenUnavailable")).not.toBeInTheDocument();
    expect(await screen.findByText("accessLoadFailed")).toBeInTheDocument();
  });

  it("rebuilds every request field for a new Route and again when browser back returns", async () => {
    const routeB: APICatalogRoute = {
      ...forecast,
      id: 10,
      slug: "radar",
      allowed_methods: ["PUT"],
      example_request: {
        method: "PUT",
        subpath: "/stations/Berlin",
        query: "layer=rain",
        headers: { Accept: "application/geo+json" },
        body: "{\"zoom\":4}",
      },
    };
    transport.respond(servicePath(), page([weather], 1, 1));
    transport.respond(routePath(7), page([forecast, routeB], 1, 2));
    const user = userEvent.setup();
    renderPage("service_id=7&route_id=9&protocol=http");

    const method = await screen.findByRole("textbox", { name: "method" });
    await user.clear(method);
    await user.type(method, "PATCH");
    await user.clear(screen.getByRole("textbox", { name: "subpath" }));
    await user.type(screen.getByRole("textbox", { name: "subpath" }), "/edited");
    await user.clear(screen.getByRole("textbox", { name: "query" }));
    await user.type(screen.getByRole("textbox", { name: "query" }), "draft=1");
    await user.clear(screen.getByRole("textbox", { name: "headerValue 1" }));
    await user.type(screen.getByRole("textbox", { name: "headerValue 1" }), "text/plain");
    await user.clear(screen.getByRole("textbox", { name: "body" }));
    await user.type(screen.getByRole("textbox", { name: "body" }), "edited");

    act(() => navigation.visit("service_id=7&route_id=10&protocol=http"));
    await waitFor(() => expect(screen.getByRole("textbox", { name: "method" })).toHaveValue("PUT"));
    expect(screen.getByRole("textbox", { name: "subpath" })).toHaveValue("/stations/Berlin");
    expect(screen.getByRole("textbox", { name: "query" })).toHaveValue("layer=rain");
    expect(screen.getByRole("textbox", { name: "headerName 1" })).toHaveValue("Accept");
    expect(screen.getByRole("textbox", { name: "headerValue 1" })).toHaveValue("application/geo+json");
    expect(screen.getByRole("textbox", { name: "body" })).toHaveValue("{\"zoom\":4}");

    act(() => navigation.back());
    await waitFor(() => expect(screen.getByRole("textbox", { name: "method" })).toHaveValue("POST"));
    expect(screen.getByRole("textbox", { name: "subpath" })).toHaveValue("/cities/Paris");
    expect(screen.getByRole("textbox", { name: "query" })).toHaveValue("unit=c");
    expect(screen.getByRole("textbox", { name: "headerName 1" })).toHaveValue("Content-Type");
    expect(screen.getByRole("textbox", { name: "headerValue 1" })).toHaveValue("application/json");
    expect(screen.getByRole("textbox", { name: "body" })).toHaveValue("{\"days\":3}");
  });

  it("does not overwrite an edited draft when the same Route refetches", async () => {
    transport.respond(servicePath(), page([weather], 1, 1));
    transport.sequence(
      routePath(7),
      () => Promise.resolve(json(page([forecast], 1, 1))),
      () => Promise.resolve(json(page([{
        ...forecast,
        example_request: { ...forecastExample, method: "DELETE" },
      }], 1, 1))),
    );
    const user = userEvent.setup();
    const view = renderPage("service_id=7&route_id=9&protocol=http");
    const method = await screen.findByRole("textbox", { name: "method" });
    await user.clear(method);
    await user.type(method, "PATCH");

    await view.queryClient.invalidateQueries({ queryKey: ["api-catalog", "routes", 1, ["admin-all", 0], 7] });

    await waitFor(() => expect(transport.calls(routePath(7))).toBe(2));
    expect(screen.getByRole("textbox", { name: "method" })).toHaveValue("PATCH");
  });

  it("keeps catalog query failures out of Token validation messaging", async () => {
    transport.respond(servicePath(), { error: "catalog unavailable" }, 503);
    renderPage("service_id=999&route_id=998&protocol=websocket");

    expect(await screen.findByRole("alert")).toHaveTextContent("catalogAccessUnavailable");
    expect(screen.queryByText("invocationTokenValidationFailed")).not.toBeInTheDocument();
    expect(screen.queryByText("invocationTokenNoLongerAllowed")).not.toBeInTheDocument();
  });

  it("chooses and validates the user-level Token without exposing its key", async () => {
    const usableToken = {
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
    window.localStorage.setItem("token", jwt(1));
    window.localStorage.setItem("aigw:api-catalog-token-id:1", "5");
    window.dispatchEvent(new StorageEvent("storage", { key: "token" }));
    seedSingleService();
    transport.respond("/api/api-catalog/effective?service_id=7", { scope: "routes", route_ids: [] });
    transport.respond(
      "/api/tokens?page_size=50&usable_only=true&user_id=1",
      page([usableToken], 1, 1),
    );
    transport.respond(
      globalValidationPath(),
      page([usableToken], 1, 1),
    );
    const user = userEvent.setup();
    renderPage("service_id=7&route_id=9&protocol=http");

    await user.click(await screen.findByRole("button", { name: "changeToken" }));
    await user.click(await screen.findByRole("option", { name: "Production Token" }));

    expect(await screen.findByRole("button", { name: "copyCommand" })).toBeEnabled();
    expect(screen.getByTestId("invocation-command-preview")).not.toHaveTextContent("sk-production-secret");
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
  });

  it("fails closed while a cached usable Token is revalidated in the background", async () => {
    const usableToken = {
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
    const backgroundValidation = deferred<Response>();
    const validationPath = globalValidationPath();
    window.localStorage.setItem("token", jwt(1));
    window.localStorage.setItem("aigw:api-catalog-token-id:1", "5");
    window.dispatchEvent(new StorageEvent("storage", { key: "token" }));
    seedSingleService();
    transport.respond(
      "/api/tokens?page_size=50&usable_only=true&user_id=1",
      page([usableToken], 1, 1),
    );
    transport.sequence(
      validationPath,
      () => Promise.resolve(json(page([usableToken], 1, 1))),
      () => backgroundValidation.promise,
    );
    const user = userEvent.setup();
    const view = renderPage("service_id=7&route_id=9&protocol=http");

    await user.click(await screen.findByRole("button", { name: "changeToken" }));
    await user.click(await screen.findByRole("option", { name: "Production Token" }));
    expect(await screen.findByRole("button", { name: "copyCommand" })).toBeEnabled();

    void view.queryClient.invalidateQueries({
      queryKey: ["tokens", "usable-for-api-route"],
    });
    await waitFor(() => expect(transport.calls(validationPath)).toBe(2));

    expect(screen.getByRole("button", { name: "copyCommand" })).toBeDisabled();
    expect(screen.getByRole("button", { name: "copyTemplate" })).toBeEnabled();
    expect(screen.getByText("tokenChecking")).toBeInTheDocument();
  });

  it("revalidates a remembered viewer Token for each Route without reusing the previous secret", async () => {
    const routeB = { ...radar, id: 10 };
    const usableToken = {
      id: 5, user_id: 1, key: "sk-production-secret", name: "Production Token", status: 1,
      expired_at: 0, models: "", trace_enabled: false, trace_mode: "full", api_role_mode: "explicit",
      created_at: 1, updated_at: 1,
    };
    const routeBValidation = deferred<Response>();
    window.localStorage.setItem("token", jwt(1));
    window.localStorage.setItem("aigw:api-catalog-token-id:1", "5");
    window.dispatchEvent(new StorageEvent("storage", { key: "token" }));
    transport.respond(tokenServicePath(5), page([weather], 1, 1));
    transport.respond(tokenRoutePath(5, 7), page([forecast, routeB], 1, 2));
    transport.respond(
      globalValidationPath(),
      page([usableToken], 1, 1),
    );
    transport.sequence(
      globalValidationPath(10),
      () => routeBValidation.promise,
    );
    const user = userEvent.setup();
    renderPage("service_id=7&route_id=9&protocol=http");
    await waitFor(() => expect(transport.calls(tokenServicePath(5))).toBe(1));
    await waitFor(() => expect(screen.getByRole("button", { name: "copyCommand" })).toBeEnabled());

    await user.click(screen.getByRole("button", { name: "radar" }));

    await waitFor(() => expect(screen.getByText("tokenChecking")).toBeInTheDocument());
    expect(screen.getByRole("button", { name: "copyCommand" })).toBeDisabled();
    routeBValidation.resolve(json(page([], 1, 0)));
    await waitFor(() => expect(window.localStorage.getItem("aigw:api-catalog-token-id:1")).toBeNull());
    expect(await screen.findByTestId("catalog-token-scope")).toHaveTextContent("selectTokenForCatalog");
    expect(screen.queryByRole("button", { name: "copyCommand" })).not.toBeInTheDocument();
  });

  it("keeps the selected Service and Route when Token validation errors", async () => {
    const usableToken = {
      id: 5, user_id: 1, key: "sk-production-secret", name: "Production Token", status: 1,
      expired_at: 0, models: "", trace_enabled: false, trace_mode: "full", api_role_mode: "explicit",
      created_at: 1, updated_at: 1,
    };
    window.localStorage.setItem("token", jwt(1, 2));
    window.dispatchEvent(new StorageEvent("storage", { key: "token" }));
    seedSingleService();
    transport.respond(
      "/api/tokens?page_size=50&usable_only=true",
      page([usableToken], 1, 1),
    );
    transport.respond(
      globalValidationPath(),
      { error: "validation unavailable" },
      503,
    );
    const user = userEvent.setup();
    renderPage("service_id=7&route_id=9&protocol=http");

    await user.click(await screen.findByRole("button", { name: "changeToken" }));
    await user.click(await screen.findByRole("option", { name: "Production Token" }));

    expect(await screen.findByText("tokenValidationFailed")).toBeInTheDocument();
    expect(currentButton("Weather")).toBeInTheDocument();
    expect(currentButton("forecast")).toBeInTheDocument();
    expect(navigation.snapshot()).toBe("service_id=7&route_id=9&protocol=http");
  });

  it("uses separate empty states and never renders a Service or Route preview overlay", async () => {
    transport.respond(servicePath(), page([], 1, 0));
    const emptyCatalog = renderPage();
    expect(await screen.findByText("emptyTitle")).toBeInTheDocument();
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
    emptyCatalog.unmount();

    transport = new CatalogTransport();
    vi.stubGlobal("fetch", transport.fetch);
    transport.respond(servicePath(), page([weather], 1, 1));
    transport.respond(routePath(7), page([], 1, 0));
    renderPage("service_id=7");
    expect(await screen.findByText("emptyRoutes")).toBeInTheDocument();
    expect(screen.queryByText("emptyTitle")).not.toBeInTheDocument();
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
    expect(screen.queryByRole("complementary", { name: /sheet/i })).not.toBeInTheDocument();
  });

  it("starts an administrator Token picker with every user's usable Tokens", async () => {
    window.localStorage.setItem("token", jwt(1, 2));
    window.dispatchEvent(new StorageEvent("storage", { key: "token" }));
    seedSingleService();
    transport.respond("/api/tokens?page_size=50&usable_only=true", page([], 1, 0));
    const user = userEvent.setup();
    renderPage("service_id=7&route_id=9&protocol=http");

    await user.click(await screen.findByRole("combobox", { name: "tokenLabel" }));

    expect(transport.calls("/api/tokens?page_size=50&usable_only=true")).toBe(1);
  });

  it("uses a remembered administrator Token even when its owner is another user", async () => {
    const foreignToken = {
      id: 5, user_id: 2, key: "sk-foreign-secret", name: "Foreign Token", status: 1,
      expired_at: 0, models: "", trace_enabled: false, trace_mode: "full", api_role_mode: "explicit",
      created_at: 1, updated_at: 1,
    };
    window.localStorage.setItem("token", jwt(1, 2));
    window.localStorage.setItem("aigw:api-catalog-token-id:1", "5");
    window.dispatchEvent(new StorageEvent("storage", { key: "token" }));
    seedSingleService();
    transport.respond(globalValidationPath(), page([foreignToken], 1, 1));
    renderPage("service_id=7&route_id=9&protocol=http");

    await waitFor(() => expect(screen.getByRole("button", { name: "copyCommand" })).toBeEnabled());
    expect(window.localStorage.getItem("aigw:api-catalog-token-id:1")).toBe("5");
    expect(screen.queryByText("sk-foreign-secret")).not.toBeInTheDocument();
  });

  it("keeps a remembered controlled Token cleared after the real Catalog picker clear action", async () => {
    const usableToken = {
      id: 5, user_id: 1, key: "sk-production-secret", name: "Production Token", status: 1,
      expired_at: 0, models: "", trace_enabled: false, trace_mode: "full", api_role_mode: "explicit",
      created_at: 1, updated_at: 1,
    };
    window.localStorage.setItem("token", jwt(1));
    window.localStorage.setItem("aigw:api-catalog-token-id:1", "5");
    window.dispatchEvent(new StorageEvent("storage", { key: "token" }));
    seedSingleService();
    transport.respond(globalValidationPath(), page([usableToken], 1, 1));
    const user = userEvent.setup();
    renderPage("service_id=7&route_id=9&protocol=http");
    await waitFor(() => expect(screen.getByRole("button", { name: "copyCommand" })).toBeEnabled());

    await user.click(within(screen.getByTestId("catalog-token-scope")).getByRole("button", { name: "clear" }));

    await waitFor(() => expect(window.localStorage.getItem("aigw:api-catalog-token-id:1")).toBeNull());
    await act(async () => { await Promise.resolve(); });
    await waitFor(() => expect(screen.queryByRole("button", { name: "copyCommand" })).not.toBeInTheDocument());
    expect(screen.queryByRole("button", { name: "copyTemplate" })).not.toBeInTheDocument();
    expect(within(screen.getByTestId("catalog-token-scope")).queryByRole("button", { name: "clear" })).not.toBeInTheDocument();
  });
});
