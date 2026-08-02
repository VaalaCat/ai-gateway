import { describe, expect, it } from "vitest";

import { emptyForm } from "../types";
import { serializeChannelLimitForPayload } from "../utils";
import {
  buildAdminCreatePayload,
  buildAdminUpdatePayload,
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
