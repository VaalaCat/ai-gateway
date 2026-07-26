import { readFileSync } from "node:fs";

const keys = [
  "agents.transportPolicy.title",
  "agents.transportPolicy.inbound",
  "agents.transportPolicy.outbound",
  "agents.transportPolicy.direct",
  "agents.transportPolicy.relay",
  "agents.transportPolicy.direct_inbound_enabled",
  "agents.transportPolicy.direct_outbound_enabled",
  "agents.transportPolicy.relay_inbound_enabled",
  "agents.transportPolicy.relay_outbound_enabled",
  "agents.transportPolicy.configured",
  "agents.transportPolicy.effective",
  "agents.transportPolicy.on",
  "agents.transportPolicy.off",
  "agents.connection.sourceDirectOutboundDisabled",
  "agents.connection.targetDirectInboundDisabled",
  "agents.connection.sourceRelayOutboundDisabled",
  "agents.connection.targetRelayInboundDisabled",
  "agents.connection.relayConnectionDisabled",
];

function lookup(messages, key) {
  return key.split(".").reduce((value, segment) => value?.[segment], messages);
}

for (const locale of ["zh", "en"]) {
  const messages = JSON.parse(readFileSync(new URL(`../src/i18n/${locale}.json`, import.meta.url), "utf8"));
  for (const key of keys) {
    const value = lookup(messages, key);
    if (typeof value !== "string" || value.trim() === "") {
      throw new Error(`${locale}: missing non-empty translation for ${key}`);
    }
  }
}

console.log(`Agent transport i18n check passed (${keys.length} keys, zh/en).`);
