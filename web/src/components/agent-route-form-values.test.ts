import { describe, expect, it } from "vitest";

import {
  buildAgentRoutePayload as build,
  createAgentRouteFormValues as initial,
} from "./agent-route-form-values";

describe("createAgentRouteFormValues", () => {
  it("creates blank token-to-agent defaults for a new rule", () => {
    expect(initial(null)).toEqual({
      sourceType: "token",
      sourceId: "",
      model: "",
      targetType: "agent_id",
      targetValue: "",
    });
  });

  it("maps every editable field from an existing tag rule", () => {
    expect(initial({
      source_type: "channel",
      source_id: 9,
      model: "gpt-4.1",
      agent_id: "",
      agent_tag: "canary",
    })).toEqual({
      sourceType: "channel",
      sourceId: "9",
      model: "gpt-4.1",
      targetType: "agent_tag",
      targetValue: "canary",
    });
  });
});

describe("buildAgentRoutePayload", () => {
  it("trims create values and explicitly clears agent_tag for an agent target", () => {
    expect(build({
      sourceType: "token",
      sourceId: " 17 ",
      model: " gpt-4.1 ",
      targetType: "agent_id",
      targetValue: " agent-east ",
    })).toEqual({
      ok: true,
      payload: {
        source_type: "token",
        source_id: 17,
        model: "gpt-4.1",
        agent_id: "agent-east",
        agent_tag: "",
      },
    });
  });

  it("supports an empty model and explicitly clears agent_id for a tag target", () => {
    expect(build({
      sourceType: "channel",
      sourceId: "9",
      model: "   ",
      targetType: "agent_tag",
      targetValue: " canary ",
    })).toEqual({
      ok: true,
      payload: {
        source_type: "channel",
        source_id: 9,
        model: "",
        agent_id: "",
        agent_tag: "canary",
      },
    });
  });

  it.each(["", "0", "-1", "1.5", "abc"])(
    "rejects invalid source id %j",
    (sourceId) => {
      expect(build({
        sourceType: "token",
        sourceId,
        model: "",
        targetType: "agent_id",
        targetValue: "agent-east",
      })).toEqual({ ok: false, field: "source_id" });
    },
  );

  it("rejects a blank target", () => {
    expect(build({
      sourceType: "token",
      sourceId: "17",
      model: "",
      targetType: "agent_tag",
      targetValue: "   ",
    })).toEqual({ ok: false, field: "target_value" });
  });
});
