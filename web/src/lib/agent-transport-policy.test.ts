import { describe, expect, it } from "vitest";

import { agentTransportPolicyReasonLabelKey } from "./agent-transport-policy";

describe("agentTransportPolicyReasonLabelKey", () => {
  it.each([
    ["source_direct_outbound_disabled", "sourceDirectOutboundDisabled"],
    ["target_direct_inbound_disabled", "targetDirectInboundDisabled"],
    ["source_relay_outbound_disabled", "sourceRelayOutboundDisabled"],
    ["target_relay_inbound_disabled", "targetRelayInboundDisabled"],
    ["relay_connection_disabled", "relayConnectionDisabled"],
  ])("returns the translation key for %s", (reasonCode, expected) => {
    expect(agentTransportPolicyReasonLabelKey(reasonCode)).toBe(expected);
  });

  it("returns undefined for an unknown reason code", () => {
    expect(agentTransportPolicyReasonLabelKey("relay_timeout")).toBeUndefined();
  });

  it.each([undefined, ""])("returns undefined for the empty boundary %s", (reasonCode) => {
    expect(agentTransportPolicyReasonLabelKey(reasonCode)).toBeUndefined();
  });
});
