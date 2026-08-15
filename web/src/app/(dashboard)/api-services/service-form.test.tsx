import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeAll, beforeEach, describe, expect, it, vi } from "vitest";

import { APIServiceForm } from "./_components/service-form";

const navigation = vi.hoisted(() => ({ push: vi.fn() }));
const mutations = vi.hoisted(() => ({ create: vi.fn(), update: vi.fn() }));
const state = vi.hoisted(() => ({ query: {} as Record<string, unknown> }));
const scrolling = vi.hoisted(() => ({ scrollIntoView: vi.fn() }));
const notifications = vi.hoisted(() => ({ success: vi.fn() }));

beforeAll(() => { Element.prototype.scrollIntoView = scrolling.scrollIntoView; });

vi.mock("next-intl", () => ({ useTranslations: () => (key: string) => key }));
vi.mock("next/navigation", () => ({ useRouter: () => navigation }));
vi.mock("sonner", () => ({ toast: notifications }));
vi.mock("@/lib/api/api-services", async (importOriginal) => ({
  ...(await importOriginal<typeof import("@/lib/api/api-services")>()),
  useAPIService: () => state.query,
  useCreateAPIService: () => ({ mutateAsync: mutations.create, isPending: false }),
  useUpdateAPIService: () => ({ mutateAsync: mutations.update, isPending: false }),
}));

describe("APIServiceForm", () => {
  beforeEach(() => {
    state.query = {};
    navigation.push.mockReset();
    mutations.create.mockReset();
    mutations.update.mockReset();
    scrolling.scrollIntoView.mockReset();
    notifications.success.mockReset();
  });

  it("creates a service and opens its static detail page", async () => {
    mutations.create.mockResolvedValue({ id: 23 });
    const user = userEvent.setup();
    render(<APIServiceForm mode={{ kind: "create" }} />);
    const save = screen.getByRole("button", { name: "save" });
    expect(save).toHaveAttribute("form", "api-service-form");
    expect(save.closest("[data-slot=page-layout-footer]")).toBeInTheDocument();
    await user.type(screen.getByLabelText("name"), "Weather");
    await user.type(screen.getByLabelText("slug"), "weather");
    await user.type(screen.getByLabelText("pricePerCall"), "100000");
    expect(await screen.findByRole("tooltip")).toHaveTextContent("$ 1.00");
    await user.click(save);
    expect(mutations.create).toHaveBeenCalledWith({ name: "Weather", slug: "weather", description: "", price_per_call: 100000, status: 1 });
    expect(notifications.success).toHaveBeenCalledWith("success");
    expect(navigation.push).toHaveBeenCalledWith("/api-services/detail?id=23");
    expect(notifications.success.mock.invocationCallOrder[0]).toBeLessThan(navigation.push.mock.invocationCallOrder[0]);
  });

  it("updates one public slug input and one selectable Base URL result", async () => {
    const user = userEvent.setup();
    render(<APIServiceForm mode={{ kind: "create" }} />);

    await user.type(screen.getByLabelText("slug"), "weather");

    expect(screen.getByTestId("segmented-url-text")).toHaveTextContent("/v1/api/weather");
    expect(screen.getAllByLabelText("slug")).toHaveLength(1);
    const copy = screen.getByRole("button", { name: "copyBaseUrl" });
    expect(copy).toBeInTheDocument();
    expect(screen.getByTestId("service-base-url-result")).toHaveClass("[&_[data-slot=button]]:size-11");
  });

  it("shows edit not found for a 404 response", () => {
    state.query = { error: { status: 404 }, isLoading: false };
    render(<APIServiceForm mode={{ kind: "edit", id: 7 }} />);
    expect(screen.getByText("serviceNotFound")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "save" })).not.toBeInTheDocument();
  });

  it.each(["", "-1", "1.5", "not-a-number"])("rejects invalid integer price %j without discarding input", async (price) => {
    const user = userEvent.setup();
    render(<APIServiceForm mode={{ kind: "create" }} />);
    await user.type(screen.getByLabelText("name"), "Weather");
    await user.type(screen.getByLabelText("slug"), "weather");
    if (price) await user.type(screen.getByLabelText("pricePerCall"), price);
    await user.click(screen.getByRole("button", { name: "save" }));
    expect(mutations.create).not.toHaveBeenCalled();
    const alert = screen.getByRole("alert");
    expect(alert).toHaveTextContent("invalidPrice");
    await waitFor(() => expect(alert).toHaveFocus());
    expect(scrolling.scrollIntoView).toHaveBeenCalledWith({ block: "nearest" });
    expect(screen.getByLabelText("name")).toHaveValue("Weather");
  });

  it("refocuses the summary when the same validation error occurs again", async () => {
    const user = userEvent.setup();
    render(<APIServiceForm mode={{ kind: "create" }} />);
    await user.type(screen.getByLabelText("name"), "Weather");
    await user.type(screen.getByLabelText("slug"), "weather");
    const save = screen.getByRole("button", { name: "save" });

    await user.click(save);
    const alert = screen.getByRole("alert");
    await waitFor(() => expect(alert).toHaveFocus());
    expect(scrolling.scrollIntoView).toHaveBeenCalledTimes(1);

    save.focus();
    expect(save).toHaveFocus();
    await user.click(save);

    await waitFor(() => expect(alert).toHaveFocus());
    expect(scrolling.scrollIntoView).toHaveBeenCalledTimes(2);
  });

  it("keeps form values and shows the mutation error", async () => {
    mutations.create.mockRejectedValue(new Error("server rejected"));
    const user = userEvent.setup();
    render(<APIServiceForm mode={{ kind: "create" }} />);
    await user.type(screen.getByLabelText("name"), "Weather");
    await user.type(screen.getByLabelText("slug"), "weather");
    await user.type(screen.getByLabelText("pricePerCall"), "12");
    await user.click(screen.getByRole("button", { name: "save" }));
    expect(screen.getByRole("alert")).toHaveTextContent("server rejected");
    expect(screen.getByLabelText("name")).toHaveValue("Weather");
  });

  it("does not carry a draft across edit identities on the same path", async () => {
    state.query = { data: { id: 7, name: "Service A", slug: "a", description: "", price_per_call: 1, status: 1 }, isLoading: false };
    const user = userEvent.setup();
    const { rerender } = render(<APIServiceForm mode={{ kind: "edit", id: 7 }} />);
    await user.clear(screen.getByLabelText("name"));
    await user.type(screen.getByLabelText("name"), "A draft");

    state.query = { data: { id: 8, name: "Service B", slug: "b", description: "", price_per_call: 2, status: 1 }, isLoading: false };
    rerender(<APIServiceForm mode={{ kind: "edit", id: 8 }} />);

    expect(screen.getByLabelText("name")).toHaveValue("Service B");
  });
});
