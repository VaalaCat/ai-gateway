import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { ReactNode } from "react";
import { beforeEach, describe, expect, it, vi } from "vitest";

import type {
  LimiterBinding,
  LimiterChannelScope,
  LimiterKeyBy,
  LimiterTargetType,
} from "@/lib/types";
import { BindingEditor, validBindingTarget } from "./binding-editor";

if (typeof Element !== "undefined") {
  Element.prototype.hasPointerCapture ??= () => false;
  Element.prototype.setPointerCapture ??= () => {};
  Element.prototype.releasePointerCapture ??= () => {};
  Element.prototype.scrollIntoView ??= () => {};
}

const state = vi.hoisted(() => ({
  bindings: [] as LimiterBinding[],
  create: vi.fn(),
  remove: vi.fn(),
  multiPickerProps: null as Record<string, unknown> | null,
  pickerProps: null as Record<string, unknown> | null,
  serviceIDs: [] as string[],
}));

vi.mock("next-intl", () => ({
  useTranslations: () => (key: string) => key,
}));

vi.mock("sonner", () => ({
  toast: { success: vi.fn(), error: vi.fn() },
}));

vi.mock("@/lib/api/rate-limiters", () => ({
  useLimiterBindings: () => ({ data: state.bindings, isLoading: false }),
  useCreateLimiterBinding: () => ({ mutateAsync: state.create, isPending: false }),
  useDeleteLimiterBinding: () => ({ mutateAsync: state.remove, isPending: false }),
}));

vi.mock("@/components/business/entity-picker/entity-multi-picker", () => ({
  EntityMultiPicker: (props: { entity: string; disabled?: boolean; onChange: (values: string[]) => void }) => {
    state.multiPickerProps = props;
    return (
      <button
        type="button"
        data-testid={`multi-${props.entity}`}
        disabled={props.disabled}
        onClick={() => props.onChange([props.entity === "api-service" ? "19" : "23"])}
      >
        {props.entity}
      </button>
    );
  },
}));

vi.mock("@/components/business/entity-picker/entity-picker", () => ({
  EntityPicker: (props: { entity: string; disabled?: boolean; onChange: (value: string) => void }) => {
    state.pickerProps = props;
    return (
      <button
        type="button"
        data-testid={`picker-${props.entity}`}
        disabled={props.disabled}
        onClick={() => props.onChange(state.serviceIDs.shift() ?? "7")}
      >
        {props.entity}
      </button>
    );
  },
}));

vi.mock("@/components/business/entity-label", () => ({
  EntityLabel: ({ id }: { id: number }) => <span>{`entity-${id}`}</span>,
}));

function renderEditor(
  keyBy: LimiterKeyBy = "shared",
  channelScope: LimiterChannelScope = "",
) {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  const wrapper = ({ children }: { children: ReactNode }) => (
    <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>
  );
  return render(
    <BindingEditor limiterId={17} keyBy={keyBy} channelScope={channelScope} />,
    { wrapper },
  );
}

beforeEach(() => {
  state.bindings = [];
  state.create.mockReset().mockResolvedValue({});
  state.remove.mockReset().mockResolvedValue({});
  state.multiPickerProps = null;
  state.pickerProps = null;
  state.serviceIDs = ["7"];
});

describe("validBindingTarget", () => {
  it.each([
    ["shared", "api_service"],
    ["per_user", "api_route"],
    ["per_group", "api_upstream"],
  ] as const)("accepts %s limiter bindings for %s", (keyBy, targetType) => {
    expect(validBindingTarget(keyBy, "", targetType)).toBe(true);
  });

  it("clears a selected child target when its API service parent changes", async () => {
    state.serviceIDs = ["7", "8"];
    renderEditor();
    await userEvent.click(screen.getByRole("combobox"));
    await userEvent.click(await screen.findByRole("option", { name: "targetAPIRoute" }));
    await userEvent.click(screen.getByTestId("picker-api-service"));
    await userEvent.click(screen.getByTestId("multi-api-route"));

    expect(state.multiPickerProps).toMatchObject({ apiServiceId: 7, value: ["23"] });
    await userEvent.click(screen.getByTestId("picker-api-service"));
    expect(state.multiPickerProps).toMatchObject({ apiServiceId: 8, value: [] });
  });

  it.each([
    ["per_channel", "api_service"],
    ["per_channel_user", "api_route"],
    ["shared", "channel"],
  ] as const)("rejects incompatible %s binding for %s", (keyBy, targetType) => {
    expect(validBindingTarget(keyBy, "", targetType)).toBe(false);
  });

  it.each(["admin", "private", "all"] as const)(
    "rejects API targets in %s channel scope",
    (channelScope) => {
      expect(validBindingTarget("shared", channelScope, "api_service")).toBe(false);
      expect(validBindingTarget("per_user", channelScope, "api_route")).toBe(false);
      expect(validBindingTarget("per_group", channelScope, "api_upstream")).toBe(false);
    },
  );
});

describe("BindingEditor API targets", () => {
  it("shows all API targets for an API-only limiter", async () => {
    renderEditor("shared", "");

    await userEvent.click(screen.getByRole("combobox"));

    expect(await screen.findByRole("option", { name: "targetAPIService" })).toBeInTheDocument();
    expect(screen.getByRole("option", { name: "targetAPIRoute" })).toBeInTheDocument();
    expect(screen.getByRole("option", { name: "targetAPIUpstream" })).toBeInTheDocument();
  });

  it("hides all API targets for an LLM channel scope", async () => {
    renderEditor("shared", "admin");

    await userEvent.click(screen.getByRole("combobox"));

    expect(await screen.findByRole("option", { name: "targetGlobal" })).toBeInTheDocument();
    expect(screen.queryByRole("option", { name: "targetAPIService" })).not.toBeInTheDocument();
    expect(screen.queryByRole("option", { name: "targetAPIRoute" })).not.toBeInTheDocument();
    expect(screen.queryByRole("option", { name: "targetAPIUpstream" })).not.toBeInTheDocument();
  });

  it("updates target options immediately when the draft scope changes", async () => {
    const view = renderEditor("shared", "");

    await userEvent.click(screen.getByRole("combobox"));
    expect(await screen.findByRole("option", { name: "targetAPIService" })).toBeInTheDocument();

    view.rerender(
      <BindingEditor limiterId={17} keyBy="shared" channelScope="admin" />,
    );

    await waitFor(() => {
      expect(screen.queryByRole("option", { name: "targetAPIService" })).not.toBeInTheDocument();
    });
    expect(screen.getByRole("option", { name: "targetGlobal" })).toBeInTheDocument();
  });

  it("uses the service multi-picker directly for API service bindings", async () => {
    renderEditor();

    await userEvent.click(screen.getByRole("combobox"));
    await userEvent.click(await screen.findByRole("option", { name: "targetAPIService" }));
    expect(screen.getByTestId("multi-api-service")).toBeInTheDocument();
    await userEvent.click(screen.getByTestId("multi-api-service"));
    await userEvent.click(screen.getByRole("button", { name: "addBinding" }));

    await waitFor(() => expect(state.create).toHaveBeenCalledWith({
      limiter_id: 17,
      target_type: "api_service",
      target_id: 19,
    }));
  });

  it.each([
    ["api_route", "targetAPIRoute", "api-route"],
    ["api_upstream", "targetAPIUpstream", "api-upstream"],
  ] as const)("requires a service parent before selecting %s targets", async (targetType, option, entity) => {
    renderEditor();

    await userEvent.click(screen.getByRole("combobox"));
    await userEvent.click(await screen.findByRole("option", { name: option }));

    expect(screen.getByTestId("picker-api-service")).toBeInTheDocument();
    expect(screen.getByTestId(`multi-${entity}`)).toBeDisabled();
    await userEvent.click(screen.getByTestId("picker-api-service"));
    expect(screen.getByTestId(`multi-${entity}`)).not.toBeDisabled();
    expect(state.multiPickerProps).toMatchObject({ entity, apiServiceId: 7 });

    await userEvent.click(screen.getByTestId(`multi-${entity}`));
    await userEvent.click(screen.getByRole("button", { name: "addBinding" }));
    await waitFor(() => expect(state.create).toHaveBeenCalledWith({
      limiter_id: 17,
      target_type: targetType as LimiterTargetType,
      target_id: 23,
    }));
  });

  it("shows the concrete API target ID instead of labeling it as global", () => {
    state.bindings = [{
      id: 31,
      limiter_id: 17,
      target_type: "api_service",
      target_id: 19,
      enabled: true,
      created_at: 1,
    }];

    renderEditor();

    expect(screen.getByText("entity-19")).toBeInTheDocument();
    expect(screen.queryByText("targetGlobalLabel")).not.toBeInTheDocument();
  });
});
