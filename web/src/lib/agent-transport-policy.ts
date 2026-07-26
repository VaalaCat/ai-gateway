const agentTransportPolicyReasonLabelKeys = {
  source_direct_outbound_disabled: "sourceDirectOutboundDisabled",
  target_direct_inbound_disabled: "targetDirectInboundDisabled",
  source_relay_outbound_disabled: "sourceRelayOutboundDisabled",
  target_relay_inbound_disabled: "targetRelayInboundDisabled",
  relay_connection_disabled: "relayConnectionDisabled",
} as const;

type AgentTransportPolicyReasonCode = keyof typeof agentTransportPolicyReasonLabelKeys;
export type AgentTransportPolicyReasonLabelKey =
  (typeof agentTransportPolicyReasonLabelKeys)[AgentTransportPolicyReasonCode];

function isAgentTransportPolicyReasonCode(
  reasonCode: string,
): reasonCode is AgentTransportPolicyReasonCode {
  return Object.prototype.hasOwnProperty.call(
    agentTransportPolicyReasonLabelKeys,
    reasonCode,
  );
}

export function agentTransportPolicyReasonLabelKey(
  reasonCode?: string,
): AgentTransportPolicyReasonLabelKey | undefined {
  return reasonCode && isAgentTransportPolicyReasonCode(reasonCode)
    ? agentTransportPolicyReasonLabelKeys[reasonCode]
    : undefined;
}
