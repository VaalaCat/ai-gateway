import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";

import { ApiError } from "@/lib/api/client";
import OpenAPIImportPage from "./import/page";

const state = vi.hoisted(() => ({
  preview: vi.fn(),
  importDocument: vi.fn(),
  push: vi.fn(),
  capability: { data: { generic_api: { services: true, service_actions: { create: true, manage_all: true, manage_ids: [] as number[] } } } } as Record<string, unknown>,
}));

vi.mock("next-intl", () => ({ useTranslations: () => (key: string) => key }));
vi.mock("next/navigation", () => ({ useRouter: () => ({ push: state.push }) }));
vi.mock("@/lib/auth", () => ({ useAuth: () => ({ user: { user_id: 1 } }) }));
vi.mock("@/lib/api/capabilities", async (importOriginal) => ({
  ...(await importOriginal<typeof import("@/lib/api/capabilities")>()),
  useCapabilities: () => state.capability,
}));
vi.mock("@/lib/api/api-services", () => ({
  useOpenAPIPreview: () => ({ mutateAsync: state.preview, isPending: false }),
  useImportOpenAPI: () => ({ mutateAsync: state.importDocument, isPending: false }),
}));

const preview = {
  service: { slug: "weather", name: "Weather", description: "Forecast API" },
  servers: [
    { index: 0, url: "https://one.example.test", description: "Primary" },
    { index: 1, url: "https://two.example.test", description: "Failover" },
  ],
  routes: [
    { slug: "", display_name: "根路由", upstream_path: "", allowed_methods: ["GET"], paths: ["/health"], public_paths: { "/health": "/health" }, path_count: 1, operation_count: 1 },
  ],
  problems: [{ path: "$.paths", code: "unsupported", message: "Ignored callback" }],
};

function selectJSON(file = new File(["{\"openapi\":\"3.1.0\"}"], "weather.json", { type: "application/json" })) {
  return userEvent.setup().upload(screen.getByLabelText("openAPIFile"), file);
}

function deferred<T>() {
  let resolve!: (value: T) => void;
  let reject!: (reason?: unknown) => void;
  const promise = new Promise<T>((onResolve, onReject) => { resolve = onResolve; reject = onReject; });
  return { promise, resolve, reject };
}

function deferredFile(name: string) {
  const reading = deferred<string>();
  const file = new File(["ignored"], name, { type: "application/json" });
  vi.spyOn(file, "text").mockReturnValue(reading.promise);
  return { file, reading };
}

function chooseFile(file: File) {
  fireEvent.change(screen.getByLabelText("openAPIFile"), { target: { files: [file] } });
}

describe("OpenAPI import page", () => {
  beforeEach(() => {
    state.preview.mockReset();
    state.preview.mockResolvedValue(preview);
    state.importDocument.mockReset();
    state.importDocument.mockResolvedValue({ kind: "imported", service_id: 42, backend_id: 43, upstream_id: 44, route_ids: [45] });
    state.push.mockReset();
    state.capability = { data: { generic_api: { services: true, service_actions: { create: true, manage_all: true, manage_ids: [] } } } };
  });

  it("accepts only JSON files and reports an invalid selection without previewing", async () => {
    render(<OpenAPIImportPage />);

    fireEvent.change(screen.getByLabelText("openAPIFile"), { target: { files: [new File(["x"], "weather.yaml", { type: "application/yaml" })] } });

    expect(await screen.findByRole("alert")).toHaveTextContent("openAPIFileJSONOnly");
    expect(state.preview).not.toHaveBeenCalled();
  });

  it("fails closed when the administrator cannot create API services", () => {
    state.capability = { data: { generic_api: { services: true, service_actions: { create: false, manage_all: true, manage_ids: [] } } } };
    render(<OpenAPIImportPage />);
    expect(screen.getByText("permissionDenied")).toBeVisible();
    expect(screen.queryByLabelText("openAPIFile")).not.toBeInTheDocument();
  });

  it("previews a JSON document without creating resources, displays root routes, and requires one server", async () => {
    render(<OpenAPIImportPage />);

    await selectJSON();
    await userEvent.setup().click(screen.getByRole("button", { name: "previewOpenAPI" }));

    expect(await screen.findByText("rootRoute")).toBeVisible();
    expect(screen.getByText("https://one.example.test")).toBeVisible();
    expect(screen.getByText("/v1/api/weather/health")).toBeVisible();
    expect(screen.getByText("Ignored callback")).toBeVisible();
    expect(state.importDocument).not.toHaveBeenCalled();
    expect(screen.getByRole("button", { name: "confirmImport" })).toBeDisabled();

    await userEvent.setup().click(screen.getByRole("radio", { name: /https:\/\/one\.example\.test/i }));
    expect(screen.getByRole("button", { name: "confirmImport" })).toBeEnabled();
  });

  it("keeps the file draft and preview visible when the import fails", async () => {
    state.importDocument.mockRejectedValueOnce(new Error("gateway unavailable"));
    render(<OpenAPIImportPage />);

    await selectJSON();
    await userEvent.setup().click(screen.getByRole("button", { name: "previewOpenAPI" }));
    await screen.findByText("rootRoute");
    await userEvent.setup().click(screen.getByRole("radio", { name: /https:\/\/one\.example\.test/i }));
    await userEvent.setup().click(screen.getByRole("button", { name: "confirmImport" }));

    expect(await screen.findByText("gateway unavailable")).toBeVisible();
    expect(screen.getByText("weather.json")).toBeVisible();
    expect(screen.getByText("rootRoute")).toBeVisible();
  });

  it("invalidates an older preview when a different valid JSON file is selected", async () => {
    render(<OpenAPIImportPage />);

    await selectJSON();
    await userEvent.setup().click(screen.getByRole("button", { name: "previewOpenAPI" }));
    await screen.findByText("rootRoute");
    await selectJSON(new File(["{\"openapi\":\"3.1.0\",\"info\":{\"title\":\"Other\"}}"], "other.json", { type: "application/json" }));

    expect(screen.queryByText("rootRoute")).not.toBeInTheDocument();
    expect(screen.getByText("other.json")).toBeVisible();
    expect(screen.getByRole("button", { name: "confirmImport" })).toBeDisabled();
  });

  it("keeps the newest file when A and B reads finish out of order", async () => {
    const a = deferredFile("a.json");
    const b = deferredFile("b.json");
    render(<OpenAPIImportPage />);

    chooseFile(a.file);
    chooseFile(b.file);
    await act(async () => b.reading.resolve('{"openapi":"3.1.0","info":{"title":"B"}}'));
    expect(await screen.findByText("b.json")).toBeVisible();
    await act(async () => a.reading.resolve('{"openapi":"3.1.0","info":{"title":"A"}}'));

    expect(screen.getByText("b.json")).toBeVisible();
    expect(screen.queryByText("a.json")).not.toBeInTheDocument();
  });

  it("cannot preview A while the newly selected B file is still being read", async () => {
    const b = deferredFile("b.json");
    render(<OpenAPIImportPage />);

    await selectJSON(new File(["{}"], "a.json", { type: "application/json" }));
    chooseFile(b.file);

    const previewButton = screen.getByRole("button", { name: "previewOpenAPI" });
    expect(previewButton).toBeDisabled();
    fireEvent.click(previewButton);
    expect(state.preview).not.toHaveBeenCalled();
    expect(screen.queryByText("a.json")).not.toBeInTheDocument();
  });

  it("does not restore A as previewable state when reading B fails", async () => {
    const b = deferredFile("b.json");
    render(<OpenAPIImportPage />);

    await selectJSON(new File(["{}"], "a.json", { type: "application/json" }));
    chooseFile(b.file);
    await act(async () => b.reading.reject(new Error("B read failed")));

    expect(await screen.findByText("openAPIFileInvalid")).toBeVisible();
    expect(screen.getByRole("button", { name: "previewOpenAPI" })).toBeDisabled();
    expect(screen.queryByText("a.json")).not.toBeInTheDocument();
    expect(state.preview).not.toHaveBeenCalled();
  });

  it("ignores an A preview success that arrives after B is selected", async () => {
    const aPreview = deferred<typeof preview>();
    state.preview.mockReturnValueOnce(aPreview.promise);
    render(<OpenAPIImportPage />);

    await selectJSON(new File(["{}"], "a.json", { type: "application/json" }));
    await userEvent.setup().click(screen.getByRole("button", { name: "previewOpenAPI" }));
    await selectJSON(new File(["{}"], "b.json", { type: "application/json" }));
    await act(async () => aPreview.resolve(preview));

    expect(screen.getByText("b.json")).toBeVisible();
    expect(screen.queryByText("rootRoute")).not.toBeInTheDocument();
  });

  it("ignores an old preview failure after B has a successful preview", async () => {
    const aPreview = deferred<typeof preview>();
    state.preview.mockReset();
    state.preview.mockReturnValueOnce(aPreview.promise).mockResolvedValueOnce(preview);
    render(<OpenAPIImportPage />);

    await selectJSON(new File(["{}"], "a.json", { type: "application/json" }));
    await userEvent.setup().click(screen.getByRole("button", { name: "previewOpenAPI" }));
    await selectJSON(new File(["{}"], "b.json", { type: "application/json" }));
    await userEvent.setup().click(screen.getByRole("button", { name: "previewOpenAPI" }));
    expect(await screen.findByText("rootRoute")).toBeVisible();
    await act(async () => aPreview.reject(new Error("stale A preview failed")));

    expect(screen.getByText("rootRoute")).toBeVisible();
    expect(screen.queryByText("stale A preview failed")).not.toBeInTheDocument();
  });

  it("invalidates the current draft as soon as re-preview starts and keeps confirmation blocked after failure", async () => {
    const secondPreview = deferred<typeof preview>();
    state.preview.mockResolvedValueOnce(preview).mockReturnValueOnce(secondPreview.promise);
    render(<OpenAPIImportPage />);

    await selectJSON();
    await userEvent.setup().click(screen.getByRole("button", { name: "previewOpenAPI" }));
    await screen.findByText("rootRoute");
    await userEvent.setup().click(screen.getByRole("radio", { name: /https:\/\/one\.example\.test/i }));
    expect(screen.getByRole("button", { name: "confirmImport" })).toBeEnabled();

    await userEvent.setup().click(screen.getByRole("button", { name: "previewOpenAPI" }));
    expect(screen.getByRole("button", { name: "confirmImport" })).toBeDisabled();
    await act(async () => secondPreview.reject(new Error("latest preview failed")));

    expect(await screen.findByText("latest preview failed")).toBeVisible();
    expect(screen.getByRole("button", { name: "confirmImport" })).toBeDisabled();
    expect(state.importDocument).not.toHaveBeenCalled();
  });

  it("requires and submits an upstream Base URL when the document has no servers", async () => {
    state.preview.mockResolvedValueOnce({ ...preview, servers: [], problems: [] });
    render(<OpenAPIImportPage />);

    await selectJSON();
    await userEvent.setup().click(screen.getByRole("button", { name: "previewOpenAPI" }));
    const input = await screen.findByLabelText("upstreamBaseURL");
    expect(screen.getByRole("button", { name: "confirmImport" })).toBeDisabled();
    await userEvent.setup().type(input, "https://upstream.example.test");
    expect(screen.getByRole("button", { name: "confirmImport" })).toBeEnabled();
    await userEvent.setup().click(screen.getByRole("button", { name: "confirmImport" }));

    expect(state.importDocument).toHaveBeenCalledWith(expect.objectContaining({
      upstream: expect.objectContaining({ base_url: "https://upstream.example.test" }),
    }));
    expect(state.importDocument.mock.calls[0]?.[0]).not.toHaveProperty("selected_server");
  });

  it("freezes the whole draft during import, submits once, then restores the same draft after failure", async () => {
    const pending = deferred<never>();
    state.importDocument.mockReturnValue(pending.promise);
    render(<OpenAPIImportPage />);

    await selectJSON();
    await userEvent.setup().click(screen.getByRole("button", { name: "previewOpenAPI" }));
    await screen.findByText("rootRoute");
    await userEvent.setup().click(screen.getByRole("radio", { name: /https:\/\/one\.example\.test/i }));
    const confirm = screen.getByRole("button", { name: "confirmImport" });
    await userEvent.setup().click(confirm);
    await userEvent.setup().click(confirm);

    expect(state.importDocument).toHaveBeenCalledTimes(1);
    expect(screen.getByLabelText("openAPIFile")).toBeDisabled();
    expect(screen.getByRole("button", { name: "previewOpenAPI" })).toBeDisabled();
    expect(screen.getAllByRole("radio").every((radio) => radio.hasAttribute("disabled"))).toBe(true);
    expect(screen.getByRole("button", { name: "cancel" })).toBeDisabled();

    await act(async () => pending.reject(new Error("network import failed")));
    expect(await screen.findByText("network import failed")).toBeVisible();
    expect(screen.getByText("weather.json")).toBeVisible();
    expect(screen.getByText("rootRoute")).toBeVisible();
    expect(screen.getByRole("radio", { name: /https:\/\/one\.example\.test/i })).toBeChecked();
    expect(screen.getByLabelText("openAPIFile")).toBeEnabled();
  });

  it("localizes stable and unknown API errors without exposing server wording", async () => {
    state.preview.mockRejectedValueOnce(new ApiError(500, "untrusted preview wording", { code: "future_code" }));
    render(<OpenAPIImportPage />);
    await selectJSON();
    await userEvent.setup().click(screen.getByRole("button", { name: "previewOpenAPI" }));
    expect(await screen.findByText("openAPIPreviewFailed")).toBeVisible();
    expect(screen.queryByText("untrusted preview wording")).not.toBeInTheDocument();

    state.preview.mockResolvedValueOnce(preview);
    await userEvent.setup().click(screen.getByRole("button", { name: "previewOpenAPI" }));
    await screen.findByText("rootRoute");
    await userEvent.setup().click(screen.getByRole("radio", { name: /https:\/\/one\.example\.test/i }));
    state.importDocument.mockRejectedValueOnce(new ApiError(409, "untrusted conflict wording", { code: "service_slug_conflict" }));
    await userEvent.setup().click(screen.getByRole("button", { name: "confirmImport" }));
    expect(await screen.findByText("openAPIServiceSlugConflict")).toBeVisible();
    expect(screen.queryByText("untrusted conflict wording")).not.toBeInTheDocument();
  });

  it("treats a committed import as final, navigates once, and cannot submit it again", async () => {
    state.importDocument.mockResolvedValueOnce({ kind: "committed-with-sync-failure", service_id: 42 });
    render(<OpenAPIImportPage />);

    await selectJSON();
    await userEvent.setup().click(screen.getByRole("button", { name: "previewOpenAPI" }));
    await screen.findByText("rootRoute");
    await userEvent.setup().click(screen.getByRole("radio", { name: /https:\/\/one\.example\.test/i }));
    const confirm = screen.getByRole("button", { name: "confirmImport" });
    await userEvent.setup().click(confirm);

    await waitFor(() => expect(state.push).toHaveBeenCalledWith("/api-services/detail?id=42"));
    expect(confirm).toBeDisabled();
    fireEvent.click(confirm);
    expect(state.importDocument).toHaveBeenCalledTimes(1);
  });

  it("keeps the workspace locked after a normal imported outcome while navigation has not unmounted it", async () => {
    const imported = deferred<{ kind: "imported"; service_id: number; backend_id: number; upstream_id: number; route_ids: number[] }>();
    state.importDocument.mockReturnValueOnce(imported.promise);
    render(<OpenAPIImportPage />);

    await selectJSON();
    await userEvent.setup().click(screen.getByRole("button", { name: "previewOpenAPI" }));
    await screen.findByText("rootRoute");
    await userEvent.setup().click(screen.getByRole("radio", { name: /https:\/\/one\.example\.test/i }));
    const confirm = screen.getByRole("button", { name: "confirmImport" });
    await userEvent.setup().click(confirm);

    expect(screen.getByLabelText("openAPIFile")).toBeDisabled();
    expect(confirm).toBeDisabled();
    await act(async () => imported.resolve({
      kind: "imported",
      service_id: 42,
      backend_id: 43,
      upstream_id: 44,
      route_ids: [45],
    }));
    await waitFor(() => expect(state.push).toHaveBeenCalledWith("/api-services/detail?id=42"));
    expect(screen.getByLabelText("openAPIFile")).toBeDisabled();
    expect(screen.getByRole("button", { name: "cancel" })).toBeDisabled();
    expect(confirm).toBeDisabled();
    fireEvent.click(confirm);
    expect(state.importDocument).toHaveBeenCalledTimes(1);
  });

  it("keeps the committed lock when navigation throws after a normal import resolves", async () => {
    state.push.mockImplementationOnce(() => { throw new Error("navigation failed"); });
    render(<OpenAPIImportPage />);

    await selectJSON();
    await userEvent.setup().click(screen.getByRole("button", { name: "previewOpenAPI" }));
    await screen.findByText("rootRoute");
    await userEvent.setup().click(screen.getByRole("radio", { name: /https:\/\/one\.example\.test/i }));
    const confirm = screen.getByRole("button", { name: "confirmImport" });
    await userEvent.setup().click(confirm);

    expect(await screen.findByText("navigation failed")).toBeVisible();
    expect(screen.getByLabelText("openAPIFile")).toBeDisabled();
    expect(screen.getByRole("button", { name: "cancel" })).toBeDisabled();
    expect(confirm).toBeDisabled();
    fireEvent.click(confirm);
    expect(state.importDocument).toHaveBeenCalledTimes(1);
  });

  it("uses the generic import fallback when sync failure has no valid committed service id", async () => {
    state.importDocument.mockRejectedValueOnce(new ApiError(500, "untrusted sync wording", {
      code: "sync_publish_failed",
      details: { service_id: 0 },
    }));
    render(<OpenAPIImportPage />);

    await selectJSON();
    await userEvent.setup().click(screen.getByRole("button", { name: "previewOpenAPI" }));
    await screen.findByText("rootRoute");
    await userEvent.setup().click(screen.getByRole("radio", { name: /https:\/\/one\.example\.test/i }));
    await userEvent.setup().click(screen.getByRole("button", { name: "confirmImport" }));

    expect(await screen.findByText("openAPIImportFailed", { selector: "[data-slot='alert-description']" })).toBeVisible();
    expect(screen.queryByText("openAPISyncPublishFailed")).not.toBeInTheDocument();
    expect(screen.queryByText("untrusted sync wording")).not.toBeInTheDocument();
  });

  it("navigates to the static service detail URL after import", async () => {
    render(<OpenAPIImportPage />);

    await selectJSON();
    await userEvent.setup().click(screen.getByRole("button", { name: "previewOpenAPI" }));
    await screen.findByText("rootRoute");
    await userEvent.setup().click(screen.getByRole("radio", { name: /https:\/\/one\.example\.test/i }));
    await userEvent.setup().click(screen.getByRole("button", { name: "confirmImport" }));

    await waitFor(() => expect(state.importDocument).toHaveBeenCalledWith(expect.objectContaining({ slug: "weather", selected_server: 0 })));
    expect(state.push).toHaveBeenCalledWith("/api-services/detail?id=42");
  });
});
