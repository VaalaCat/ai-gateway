package events

import (
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
)

// Entity 常量。
const (
	EntityToken               = "token"
	EntityChannel             = "channel"
	EntityModel               = "model"
	EntityModelV1             = "model_config" // legacy full-sync entity name
	EntitySetting             = "setting"
	EntityAgent               = "agent"
	EntityAgentRoute          = "agent_route"
	EntityRequestLimiter      = "request_limiter"
	EntityLimiterBinding      = "limiter_binding"
	EntityModelRouting        = "model_routing"
	EntityUserRoutings        = "user_routings"
	EntityTokenRoutings       = "token_routings"
	EntitySync                = "sync"
	EntityUserGroup           = "user_group"
	EntityUser                = "user"
	EntityAPIService          = "api_service"
	EntityAPIRoute            = "api_route"
	EntityAPIUpstream         = "api_upstream"
	EntityAPIRole             = "api_role"
	EntityUserGroupAPIRoleSet = "user_group_api_role_set"
	EntityUserAPIRoleSet      = "user_api_role_set"
	EntityTokenAPIRoleSet     = "token_api_role_set"

	EntityPrivateChannel      = "private_channel"
	EntityPrivateChannelShare = "private_channel_share"

	EntityScript = "script"
)

// CRUD action 常量。
const (
	ActionCreate     = "create"
	ActionUpdate     = "update"
	ActionDelete     = "delete"
	ActionInvalidate = "invalidate"
)

const (
	topicTokenCreate = "token.create"
	topicTokenUpdate = "token.update"
	topicTokenDelete = "token.delete"

	topicChannelCreate = "channel.create"
	topicChannelUpdate = "channel.update"
	topicChannelDelete = "channel.delete"

	topicModelCreate = "model.create"
	topicModelUpdate = "model.update"
	topicModelDelete = "model.delete"

	topicAgentCreate = "agent.create"
	topicAgentUpdate = "agent.update"
	topicAgentDelete = "agent.delete"

	topicAgentRevoked    = "agent.revoked"
	topicAgentRegistered = "agent.registered"

	topicUsageReported  = "usage.reported"
	topicUsageCompleted = "usage.completed"

	topicUserQuotaDepleted = "user.quota_depleted"
	topicUserQuotaSync     = "user.quota_synced"

	topicAgentRouteCreate = "agent_route.create"
	topicAgentRouteUpdate = "agent_route.update"
	topicAgentRouteDelete = "agent_route.delete"

	topicRequestLimiterCreate = "request_limiter.create"
	topicRequestLimiterUpdate = "request_limiter.update"
	topicRequestLimiterDelete = "request_limiter.delete"
	topicLimiterBindingCreate = "limiter_binding.create"
	topicLimiterBindingUpdate = "limiter_binding.update"
	topicLimiterBindingDelete = "limiter_binding.delete"

	topicModelRoutingCreate = "model_routing.create"
	topicModelRoutingUpdate = "model_routing.update"
	topicModelRoutingDelete = "model_routing.delete"

	topicSettingUpdate = "setting.update"

	topicSyncFullSyncRequested = "sync.full_sync_requested"

	topicUserGroupCreate = "user_group.create"
	topicUserGroupUpdate = "user_group.update"
	topicUserGroupDelete = "user_group.delete"
	topicUserSyncUpdate  = "user.sync_update"
	topicUserSyncDelete  = "user.sync_delete"

	topicScriptCreate = "script.create"
	topicScriptUpdate = "script.update"
	topicScriptDelete = "script.delete"

	topicAPIServiceCreate         = "api_service.create"
	topicAPIServiceUpdate         = "api_service.update"
	topicAPIServiceDelete         = "api_service.delete"
	topicAPIRouteCreate           = "api_route.create"
	topicAPIRouteUpdate           = "api_route.update"
	topicAPIRouteDelete           = "api_route.delete"
	topicAPIUpstreamCreate        = "api_upstream.create"
	topicAPIUpstreamUpdate        = "api_upstream.update"
	topicAPIUpstreamDelete        = "api_upstream.delete"
	topicAPIRoleCreate            = "api_role.create"
	topicAPIRoleUpdate            = "api_role.update"
	topicAPIRoleDelete            = "api_role.delete"
	topicUserAPIRolesSynced       = "user.api_roles_synced"
	topicUserGroupAPIRolesSynced  = "user_group.api_roles_synced"
	topicUserGroupAPIRolesDeleted = "user_group.api_roles_deleted"
	topicTokenAPIRolesSynced      = "token.api_roles_synced"
)

const (
	patternTokenAll   = "token.*"
	patternChannelAll = "channel.*"
	patternModelAll   = "model.*"

	patternSyncTokenAll       = "sync.token.*"
	patternSyncChannelAll     = "sync.channel.*"
	patternSyncModelAll       = "sync.model.*"
	patternSyncModelConfigAll = "sync.model_config.*"
	patternSyncSettingAll     = "sync.setting.*"
	patternAgentAll           = "agent.*"
	patternSyncAgentAll       = "sync.agent.*"
	patternAgentRouteAll      = "agent_route.*"
	patternSyncAgentRouteAll  = "sync.agent_route.*"

	patternSyncRequestLimiterAll = "sync.request_limiter.*"
	patternSyncLimiterBindingAll = "sync.limiter_binding.*"

	patternModelRoutingAll     = "model_routing.*"
	patternSyncModelRoutingAll = "sync.model_routing.*"

	patternSyncUserGroupAll = "sync.user_group.*"
	patternSyncUserAll      = "sync.user.*"

	patternSyncPrivateChannelAll = "sync.private_channel.*"

	patternSyncScriptAll = "sync.script.*"

	patternSyncAPIServiceAll          = "sync.api_service.*"
	patternSyncAPIRouteAll            = "sync.api_route.*"
	patternSyncAPIUpstreamAll         = "sync.api_upstream.*"
	patternSyncAPIRoleAll             = "sync.api_role.*"
	patternSyncUserAPIRoleSetAll      = "sync.user_api_role_set.*"
	patternSyncUserGroupAPIRoleSetAll = "sync.user_group_api_role_set.*"
	patternSyncTokenAPIRoleSetAll     = "sync.token_api_role_set.*"
)

func entityTopic(entity, action string) string {
	return entity + "." + action
}

func syncTopic(entity, action string) string {
	return EntitySync + "." + entity + "." + action
}

func DynamicTopic[T any](entity, action string) Topic[T] {
	return newTopic[T](entityTopic(entity, action))
}

var (
	TokenCreateTopic = newTopic[models.Token](topicTokenCreate)
	TokenUpdateTopic = newTopic[models.Token](topicTokenUpdate)
	TokenDeleteTopic = newTopic[models.Token](topicTokenDelete)

	ChannelCreateTopic = newTopic[models.Channel](topicChannelCreate)
	ChannelUpdateTopic = newTopic[models.Channel](topicChannelUpdate)
	ChannelDeleteTopic = newTopic[models.Channel](topicChannelDelete)

	ModelCreateTopic = newTopic[models.ModelConfig](topicModelCreate)
	ModelUpdateTopic = newTopic[models.ModelConfig](topicModelUpdate)
	ModelDeleteTopic = newTopic[models.ModelConfig](topicModelDelete)

	AgentRevokedTopic    = newTopic[models.Agent](topicAgentRevoked)
	AgentRegisteredTopic = newTopic[models.Agent](topicAgentRegistered)

	UsageReportedTopic  = newTopic[protocol.UsageReport](topicUsageReported)
	UsageCompletedTopic = newTopic[protocol.UsageLogEntry](topicUsageCompleted)

	UserQuotaDepletedTopic = newTopic[models.User](topicUserQuotaDepleted)
	UserQuotaSyncTopic     = newTopic[protocol.UserQuotaSync](topicUserQuotaSync)

	SettingUpdateTopic = newTopic[models.Setting](topicSettingUpdate)

	SyncFullSyncRequestedTopic = newTopic[struct{}](topicSyncFullSyncRequested)

	TokenAllPattern   = newPattern[models.Token](patternTokenAll)
	ChannelAllPattern = newPattern[models.Channel](patternChannelAll)
	ModelAllPattern   = newPattern[models.ModelConfig](patternModelAll)

	SyncTokenAllPattern       = newPattern[protocol.SyncPushParams](patternSyncTokenAll)
	SyncChannelAllPattern     = newPattern[protocol.SyncPushParams](patternSyncChannelAll)
	SyncModelAllPattern       = newPattern[protocol.SyncPushParams](patternSyncModelAll)
	SyncModelConfigAllPattern = newPattern[protocol.SyncPushParams](patternSyncModelConfigAll)
	SyncSettingAllPattern     = newPattern[protocol.SyncPushParams](patternSyncSettingAll)

	AgentCreateTopic    = newTopic[models.Agent](topicAgentCreate)
	AgentUpdateTopic    = newTopic[models.Agent](topicAgentUpdate)
	AgentDeleteTopic    = newTopic[models.Agent](topicAgentDelete)
	AgentAllPattern     = newPattern[models.Agent](patternAgentAll)
	SyncAgentAllPattern = newPattern[protocol.SyncPushParams](patternSyncAgentAll)

	AgentRouteCreateTopic    = newTopic[models.AgentRoute](topicAgentRouteCreate)
	AgentRouteUpdateTopic    = newTopic[models.AgentRoute](topicAgentRouteUpdate)
	AgentRouteDeleteTopic    = newTopic[models.AgentRoute](topicAgentRouteDelete)
	AgentRouteAllPattern     = newPattern[models.AgentRoute](patternAgentRouteAll)
	SyncAgentRouteAllPattern = newPattern[protocol.SyncPushParams](patternSyncAgentRouteAll)

	RequestLimiterCreateTopic    = newTopic[models.RequestLimiter](topicRequestLimiterCreate)
	RequestLimiterUpdateTopic    = newTopic[models.RequestLimiter](topicRequestLimiterUpdate)
	RequestLimiterDeleteTopic    = newTopic[models.RequestLimiter](topicRequestLimiterDelete)
	LimiterBindingCreateTopic    = newTopic[models.LimiterBinding](topicLimiterBindingCreate)
	LimiterBindingUpdateTopic    = newTopic[models.LimiterBinding](topicLimiterBindingUpdate)
	LimiterBindingDeleteTopic    = newTopic[models.LimiterBinding](topicLimiterBindingDelete)
	SyncRequestLimiterAllPattern = newPattern[protocol.SyncPushParams](patternSyncRequestLimiterAll)
	SyncLimiterBindingAllPattern = newPattern[protocol.SyncPushParams](patternSyncLimiterBindingAll)

	ModelRoutingCreateTopic    = newTopic[models.ModelRouting](topicModelRoutingCreate)
	ModelRoutingUpdateTopic    = newTopic[models.ModelRouting](topicModelRoutingUpdate)
	ModelRoutingDeleteTopic    = newTopic[models.ModelRouting](topicModelRoutingDelete)
	ModelRoutingAllPattern     = newPattern[models.ModelRouting](patternModelRoutingAll)
	SyncModelRoutingAllPattern = newPattern[protocol.SyncPushParams](patternSyncModelRoutingAll)

	UserGroupCreateTopic = newTopic[models.UserGroup](topicUserGroupCreate)
	UserGroupUpdateTopic = newTopic[models.UserGroup](topicUserGroupUpdate)
	UserGroupDeleteTopic = newTopic[models.UserGroup](topicUserGroupDelete)

	UserSyncUpdateTopic = newTopic[protocol.SyncedUser](topicUserSyncUpdate)
	UserSyncDeleteTopic = newTopic[protocol.SyncedUser](topicUserSyncDelete)

	SyncUserGroupAllPattern = newPattern[protocol.SyncPushParams](patternSyncUserGroupAll)
	SyncUserAllPattern      = newPattern[protocol.SyncPushParams](patternSyncUserAll)

	SyncPrivateChannelAllPattern = newPattern[protocol.SyncPushParams](patternSyncPrivateChannelAll)

	PrivateChannelInvalidateTopic = newTopic[protocol.PrivateChannelInvalidatePayload](entityTopic(EntityPrivateChannel, ActionInvalidate))

	ScriptCreateTopic = newTopic[models.AdminScript](topicScriptCreate)
	ScriptUpdateTopic = newTopic[models.AdminScript](topicScriptUpdate)
	ScriptDeleteTopic = newTopic[models.AdminScript](topicScriptDelete)

	SyncScriptAllPattern = newPattern[protocol.SyncPushParams](patternSyncScriptAll)

	SyncAPIServiceAllPattern          = newPattern[protocol.SyncPushParams](patternSyncAPIServiceAll)
	SyncAPIRouteAllPattern            = newPattern[protocol.SyncPushParams](patternSyncAPIRouteAll)
	SyncAPIUpstreamAllPattern         = newPattern[protocol.SyncPushParams](patternSyncAPIUpstreamAll)
	SyncAPIRoleAllPattern             = newPattern[protocol.SyncPushParams](patternSyncAPIRoleAll)
	SyncUserAPIRoleSetAllPattern      = newPattern[protocol.SyncPushParams](patternSyncUserAPIRoleSetAll)
	SyncUserGroupAPIRoleSetAllPattern = newPattern[protocol.SyncPushParams](patternSyncUserGroupAPIRoleSetAll)
	SyncTokenAPIRoleSetAllPattern     = newPattern[protocol.SyncPushParams](patternSyncTokenAPIRoleSetAll)

	APIServiceCreateTopic  = newTopic[protocol.SyncedAPIService](topicAPIServiceCreate)
	APIServiceUpdateTopic  = newTopic[protocol.SyncedAPIService](topicAPIServiceUpdate)
	APIServiceDeleteTopic  = newTopic[protocol.SyncedAPIService](topicAPIServiceDelete)
	APIRouteCreateTopic    = newTopic[protocol.SyncedAPIRoute](topicAPIRouteCreate)
	APIRouteUpdateTopic    = newTopic[protocol.SyncedAPIRoute](topicAPIRouteUpdate)
	APIRouteDeleteTopic    = newTopic[protocol.SyncedAPIRoute](topicAPIRouteDelete)
	APIUpstreamCreateTopic = newTopic[protocol.SyncedAPIUpstream](topicAPIUpstreamCreate)
	APIUpstreamUpdateTopic = newTopic[protocol.SyncedAPIUpstream](topicAPIUpstreamUpdate)
	APIUpstreamDeleteTopic = newTopic[protocol.SyncedAPIUpstream](topicAPIUpstreamDelete)
	APIRoleCreateTopic     = newTopic[protocol.SyncedAPIRole](topicAPIRoleCreate)
	APIRoleUpdateTopic     = newTopic[protocol.SyncedAPIRole](topicAPIRoleUpdate)
	APIRoleDeleteTopic     = newTopic[protocol.SyncedAPIRole](topicAPIRoleDelete)

	UserAPIRolesSyncedTopic       = newTopic[protocol.APIRoleSetInvalidate](topicUserAPIRolesSynced)
	UserGroupAPIRolesSyncedTopic  = newTopic[protocol.APIRoleSetFetchResult](topicUserGroupAPIRolesSynced)
	UserGroupAPIRolesDeletedTopic = newTopic[protocol.APIRoleSetFetchResult](topicUserGroupAPIRolesDeleted)
	TokenAPIRolesSyncedTopic      = newTopic[protocol.APIRoleSetInvalidate](topicTokenAPIRolesSynced)
)

func SyncPushTopic(entity, action string) Topic[protocol.SyncPushParams] {
	return newTopic[protocol.SyncPushParams](syncTopic(entity, action))
}
