import { tokenAdapter } from "./adapters/token";
import { usableTokenAdapter } from "./adapters/usable-token";
import { userAdapter } from "./adapters/user";
import { userGroupAdapter } from "./adapters/user-group";
import { byokChannelAdapter } from "./adapters/byok-channel";
import { channelAdapter } from "./adapters/channel";
import { modelAdapter } from "./adapters/model";
import { agentAdapter } from "./adapters/agent";
import { tokenTemplateAdapter } from "./adapters/token-template";
import { apiServiceAdapter } from "./adapters/api-service";
import { apiBackendAdapter } from "./adapters/api-backend";
import { apiRouteAdapter } from "./adapters/api-route";
import { apiUpstreamAdapter } from "./adapters/api-upstream";
import { apiRoleAdapter } from "./adapters/api-role";
import { apiAccessTokenAdapter } from "./adapters/api-access-token";

export const ENTITY_ADAPTERS = {
  token: tokenAdapter,
  "usable-token": usableTokenAdapter,
  user: userAdapter,
  "user-group": userGroupAdapter,
  "byok-channel": byokChannelAdapter,
  channel: channelAdapter,
  model: modelAdapter,
  agent: agentAdapter,
  "token-template": tokenTemplateAdapter,
	"api-service": apiServiceAdapter,
	"api-backend": apiBackendAdapter,
  "api-route": apiRouteAdapter,
  "api-upstream": apiUpstreamAdapter,
  "api-role": apiRoleAdapter,
  "api-access-token": apiAccessTokenAdapter,
} as const;

export type EntityName = keyof typeof ENTITY_ADAPTERS;
