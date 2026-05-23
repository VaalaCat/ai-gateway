import { tokenAdapter } from "./adapters/token";
import { userAdapter } from "./adapters/user";
import { userGroupAdapter } from "./adapters/user-group";
import { byokChannelAdapter } from "./adapters/byok-channel";
import { channelAdapter } from "./adapters/channel";
import { modelAdapter } from "./adapters/model";
import { agentAdapter } from "./adapters/agent";

export const ENTITY_ADAPTERS = {
  token: tokenAdapter,
  user: userAdapter,
  "user-group": userGroupAdapter,
  "byok-channel": byokChannelAdapter,
  channel: channelAdapter,
  model: modelAdapter,
  agent: agentAdapter,
} as const;

export type EntityName = keyof typeof ENTITY_ADAPTERS;
