package events

import (
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
)

// Entity 常量。
const (
	EntityToken      = "token"
	EntityChannel    = "channel"
	EntityModel      = "model"
	EntityModelV1    = "model_config" // legacy full-sync entity name
	EntitySetting    = "setting"
	EntityAgent      = "agent"
	EntityAgentRoute    = "agent_route"
	EntityModelRouting  = "model_routing"
	EntityUserRoutings  = "user_routings"
	EntitySync          = "sync"
	EntityUserGroup  = "user_group"
	EntityUser       = "user"

	EntityPrivateChannel      = "private_channel"
	EntityPrivateChannelShare = "private_channel_share"
)

// CRUD action 常量。
const (
	ActionCreate = "create"
	ActionUpdate = "update"
	ActionDelete = "delete"
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

	topicAgentRouteCreate = "agent_route.create"
	topicAgentRouteUpdate = "agent_route.update"
	topicAgentRouteDelete = "agent_route.delete"

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

	patternModelRoutingAll     = "model_routing.*"
	patternSyncModelRoutingAll = "sync.model_routing.*"

	patternSyncUserGroupAll = "sync.user_group.*"
	patternSyncUserAll      = "sync.user.*"
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
)

func SyncPushTopic(entity, action string) Topic[protocol.SyncPushParams] {
	return newTopic[protocol.SyncPushParams](syncTopic(entity, action))
}
