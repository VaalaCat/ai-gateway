import { describe, expect, it } from "vitest";

import type { ModelRouting } from "@/lib/types";
import {
  buildModelRoutingEditHref,
  modelRoutingOwner,
  normalizeModelRoutingFilterChange,
} from "./list-page-logic";

function routing(overrides: Partial<ModelRouting>): ModelRouting {
  return {
    id: 11,
    name: "smart",
    scope: "global",
    user_id: 0,
    token_id: 0,
    members: [],
    enabled: true,
    remark: "",
    created_at: 0,
    updated_at: 0,
    ...overrides,
  };
}

describe("model routing list page logic", () => {
  it("clears both owner filters atomically when scope changes", () => {
    expect(normalizeModelRoutingFilterChange({ scope: "token" })).toEqual({
      scope: "token",
      user_id: "",
      token_id: "",
    });
  });

  it("maps only token rows to a token subresource owner", () => {
    expect(modelRoutingOwner(routing({ scope: "token", token_id: 23 }))).toEqual({
      kind: "token",
      tokenId: 23,
    });
    expect(modelRoutingOwner(routing({ scope: "user", user_id: 7 }))).toEqual({
      kind: "scope",
    });
  });

  it("adds token_id only to token row edit links", () => {
    expect(buildModelRoutingEditHref("/model-routings", routing({ scope: "token", token_id: 23 })))
      .toBe("/model-routings/edit?id=11&token_id=23");
    expect(buildModelRoutingEditHref("/model-routings", routing({ scope: "global" })))
      .toBe("/model-routings/edit?id=11");
  });
});
