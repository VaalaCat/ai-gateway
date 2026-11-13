import { describe, expect, it } from "vitest";

import { emptyForm } from "../types";
import { mapChannelToForm, serializeChannelLimitForPayload } from "../utils";
import type { Channel } from "@/lib/types";
import type { BYOKChannelDetail } from "@/lib/api/byok-channels";
import {
  buildAdminCreatePayload,
  buildAdminUpdatePayload,
  adminChannelAdapter,
} from "./admin";
import { byokChannelAdapter } from "./byok";

describe("admin channel payloads", () => {
  it("keeps the required internal name in a create payload", () => {
    expect(buildAdminCreatePayload({ ...emptyForm, name: "primary" })).toMatchObject({
      name: "primary",
    });
  });

  it("omits the read-only internal name from an update payload", () => {
    const payload = buildAdminUpdatePayload(
      { ...emptyForm, name: "renamed", tag: "edge" },
      emptyForm,
    );

    expect(payload.fields).toMatchObject({ tag: "edge" });
    expect(payload.fields).not.toHaveProperty("name");
  });

  it("keeps an explicitly cleared public display name in an update payload", () => {
    const payload = buildAdminUpdatePayload(
      { ...emptyForm, public_display_name: "" },
      { ...emptyForm, public_display_name: "Visible name" },
    );

    expect(payload.fields).toHaveProperty("public_display_name", "");
  });

  it("hides the platform-only public display name from BYOK forms", () => {
    expect(byokChannelAdapter.hiddenFields?.has("public_display_name")).toBe(true);
  });

  it("uses the shared limit serializer to remove unfinished rules and non-positive cutoff", () => {
    const raw = JSON.stringify({
      disable_at: 0,
      rules: [
        { metric: "calls", window: "daily", threshold: 0 },
        { metric: "cost", window: "monthly", threshold: 12, cost_basis: "raw" },
      ],
    });
    const expected = {
      rules: [{ metric: "cost", window: "monthly", threshold: 12, cost_basis: "raw" }],
    };

    expect(serializeChannelLimitForPayload(raw)).toEqual(expected);
    expect(buildAdminCreatePayload({ ...emptyForm, limit: raw }).limit).toEqual(expected);
  });

  it("keeps an explicit empty limit object so a partial update can clear it", () => {
    expect(serializeChannelLimitForPayload(JSON.stringify({
      disable_at: -1,
      rules: [{ metric: "calls", window: "daily", threshold: -1 }],
    }))).toEqual({});
  });
});

describe("upstream authentication payloads", () => {
  const auth = {
    default: { header: "Authorization", value: "Bearer ${CHANNEL_KEY}" },
    protocols: { messages: { header: "X-Api-Key", value: "${CHANNEL_KEY}" } },
  };

  it("keeps admin edit, copy, save, and reopen independent of the source map", () => {
    const channel = { name: "primary", auth } as Channel;
    const form = mapChannelToForm(channel);
    const saved = buildAdminUpdatePayload(form, form).fields;
    expect(saved.auth).toEqual(auth);
    expect(mapChannelToForm(saved as unknown as Channel).auth).toBe(form.auth);
    const copy = adminChannelAdapter.mapEntityToCopyForm!(channel);
    expect(buildAdminCreatePayload(copy).auth).toEqual(auth);
    expect(buildAdminCreatePayload(form).auth).not.toBe(auth);
    expect(buildAdminUpdatePayload({ ...form, auth: "" }, form).fields.auth).toEqual({});
  });

  it("keeps BYOK create, edit, clear and reopen without enabling ordinary header overrides", () => {
    const form = byokChannelAdapter.mapEntityToForm({ name: "private", auth } as BYOKChannelDetail);
    expect(byokChannelAdapter.buildCreatePayload(form)).toMatchObject({ auth });
    const initial = { ...form, auth: "" };
    const saved = byokChannelAdapter.buildUpdatePayload(form, initial).fields;
    expect(saved.auth).toEqual(auth);
    expect(byokChannelAdapter.mapEntityToForm(saved as unknown as BYOKChannelDetail).auth).toBe(form.auth);
    expect(byokChannelAdapter.buildUpdatePayload(form, form).fields).not.toHaveProperty("auth");
    expect(byokChannelAdapter.buildUpdatePayload({ ...form, auth: "" }, form).fields.auth).toEqual({});
    expect(byokChannelAdapter.hiddenFields?.has("header_override")).toBe(true);
    expect(byokChannelAdapter.buildCreatePayload(form)).not.toHaveProperty("header_override");
  });

  it("leaves old channels with no auth on the default behavior", () => {
    expect(buildAdminCreatePayload(emptyForm).auth).toEqual({});
    expect(byokChannelAdapter.buildCreatePayload(emptyForm)).toMatchObject({ auth: {} });
    expect(mapChannelToForm({} as Channel).auth).toBe("");
  });
});

describe("Gemini channel round trips", () => {
  const endpoints = JSON.stringify({ generate_content: "/custom/models/{model}:{action}?extra=1" });
  const auth = { protocols: { generate_content: { header: "x-goog-api-key", value: "${CHANNEL_KEY}" } } };
  const otherSettings = { model_protocol_override: [{ model: "custom-.*", overrides: { openai_chat: "gemini" } }] };

  it("preserves endpoint, auth and inbound conversion rules for admin save and reopen", () => {
    const form = mapChannelToForm({ name: "gemini", endpoints, auth, other_settings: JSON.stringify(otherSettings) } as Channel);
    const saved = buildAdminUpdatePayload(form, form).fields;
    expect(saved).toMatchObject({ endpoints, auth });
    expect(JSON.parse(saved.other_settings as string)).toEqual(otherSettings);
    expect(mapChannelToForm(saved as unknown as Channel).endpoints).toBe(endpoints);
  });

  it("preserves the same Gemini settings for BYOK create and reopen", () => {
    const form = byokChannelAdapter.mapEntityToForm({ name: "gemini", endpoints, auth, other_settings: JSON.stringify(otherSettings) } as BYOKChannelDetail);
    const saved = byokChannelAdapter.buildCreatePayload(form) as Partial<BYOKChannelDetail>;
    expect(saved).toMatchObject({ endpoints, auth });
    expect(JSON.parse(saved.other_settings!)).toEqual(otherSettings);
    expect(byokChannelAdapter.mapEntityToForm(saved as BYOKChannelDetail).endpoints).toBe(endpoints);
  });
});
