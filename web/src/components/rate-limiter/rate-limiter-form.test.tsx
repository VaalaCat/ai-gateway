import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";

import type { RequestLimiter } from "@/lib/types";
import { RateLimiterForm } from "./rate-limiter-form";

if (typeof Element !== "undefined") {
  Element.prototype.hasPointerCapture ??= () => false;
  Element.prototype.setPointerCapture ??= () => {};
  Element.prototype.releasePointerCapture ??= () => {};
  Element.prototype.scrollIntoView ??= () => {};
}

const { state } = vi.hoisted(() => ({
  state: {
    limiters: [] as RequestLimiter[],
    create: vi.fn(),
    update: vi.fn(),
    push: vi.fn(),
    bindingEditorProps: [] as Array<{
      keyBy: RequestLimiter["key_by"];
      channelScope: RequestLimiter["channel_scope"];
      policyDirty: boolean;
    }>,
  },
}));

vi.mock("next-intl", () => ({
  useTranslations: () => (key: string) => key,
}));

vi.mock("next/navigation", () => ({
  useRouter: () => ({ push: state.push }),
}));

vi.mock("@/lib/api/rate-limiters", () => ({
  useRateLimiters: () => ({ data: { data: state.limiters } }),
  useCreateRateLimiter: () => ({ mutateAsync: state.create, isPending: false }),
  useUpdateRateLimiter: () => ({ mutateAsync: state.update, isPending: false }),
}));

vi.mock("@/components/rate-limiter/binding-editor", () => ({
  BindingEditor: (props: {
    keyBy: RequestLimiter["key_by"];
    channelScope: RequestLimiter["channel_scope"];
    policyDirty: boolean;
  }) => {
    state.bindingEditorProps.push(props);
    return null;
  },
}));

function existingLimiter(channelScope: RequestLimiter["channel_scope"]): RequestLimiter {
  return {
    id: 17,
    name: "existing",
    enabled: true,
    metric: "concurrency",
    capacity: 10,
    window_ms: 0,
    key_by: "shared",
    channel_scope: channelScope,
    action: "reject",
    queue_size: 0,
    queue_time_ms: 0,
    priority: 0,
    created_at: 1,
    updated_at: 1,
  };
}

async function submitCreate(name: string) {
  await userEvent.type(screen.getByPlaceholderText("namePlaceholder"), name);
  await userEvent.click(screen.getByRole("button", { name: "save" }));
  await waitFor(() => expect(state.create).toHaveBeenCalledTimes(1));
}

describe("RateLimiterForm API-only scope", () => {
  beforeEach(() => {
    state.limiters = [];
    state.create.mockReset();
    state.create.mockResolvedValue({});
    state.update.mockReset();
    state.update.mockResolvedValue({});
    state.push.mockReset();
    state.bindingEditorProps = [];
  });

  it("maps the explicit API-only Select sentinel to an empty channel scope", async () => {
    render(<RateLimiterForm mode={{ kind: "create" }} />);

    const scopeSelect = screen.getAllByRole("combobox")[2];
    scopeSelect.focus();
    await userEvent.keyboard("{Enter}");
    await userEvent.click(await screen.findByRole("option", { name: "scopeAPIOnly" }));
    await submitCreate("api only");

    expect(state.create.mock.calls[0][0]).toMatchObject({
      key_by: "shared",
      channel_scope: "",
    });
  });

  it("keeps the existing LLM create default as admin when API-only is not selected", async () => {
    render(<RateLimiterForm mode={{ kind: "create" }} />);

    await submitCreate("llm default");

    expect(state.create.mock.calls[0][0]).toMatchObject({
      key_by: "shared",
      channel_scope: "admin",
    });
  });

  it("round-trips an existing empty scope through edit", async () => {
    state.limiters = [existingLimiter("")];
    render(<RateLimiterForm mode={{ kind: "edit", id: 17 }} />);

    await waitFor(() => expect(screen.getByPlaceholderText("namePlaceholder")).toHaveValue("existing"));
    await userEvent.click(screen.getByRole("button", { name: "save" }));
    await waitFor(() => expect(state.update).toHaveBeenCalledTimes(1));

    expect(state.update.mock.calls[0][0]).toMatchObject({
      id: 17,
      key_by: "shared",
      channel_scope: "",
    });
  });

  it("passes the draft scope and marks an unsaved API-only to LLM change dirty", async () => {
    state.limiters = [existingLimiter("")];
    render(<RateLimiterForm mode={{ kind: "edit", id: 17 }} />);

    await waitFor(() => {
      expect(state.bindingEditorProps.at(-1)).toMatchObject({
        keyBy: "shared",
        channelScope: "",
        policyDirty: false,
      });
    });

    await userEvent.click(screen.getByRole("combobox", { name: "channelScope" }));
    await userEvent.click(await screen.findByRole("option", { name: "scopeAdmin" }));

    await waitFor(() => {
      expect(state.bindingEditorProps.at(-1)).toMatchObject({
        keyBy: "shared",
        channelScope: "admin",
        policyDirty: true,
      });
    });
  });

  it("passes the draft scope and marks an unsaved LLM to API-only change dirty", async () => {
    state.limiters = [existingLimiter("admin")];
    render(<RateLimiterForm mode={{ kind: "edit", id: 17 }} />);

    await waitFor(() => {
      expect(state.bindingEditorProps.at(-1)).toMatchObject({
        keyBy: "shared",
        channelScope: "admin",
        policyDirty: false,
      });
    });

    await userEvent.click(screen.getByRole("combobox", { name: "channelScope" }));
    await userEvent.click(await screen.findByRole("option", { name: "scopeAPIOnly" }));

    await waitFor(() => {
      expect(state.bindingEditorProps.at(-1)).toMatchObject({
        keyBy: "shared",
        channelScope: "",
        policyDirty: true,
      });
    });
  });
});
