package dao

import "fmt"

// --- user-scoped composites ---

type Query interface {
	User() UserQuery
	UsageLog() UsageLogQuery
}

type Mutation interface {
	User() UserMutation
}

func NewQuery(ctx UserContext) Query {
	uc := ctx.(*userContextImpl)
	return &compositeQuery{ctx: uc}
}

func NewMutation(ctx UserContext) Mutation {
	uc := ctx.(*userContextImpl)
	return &compositeMutation{ctx: uc}
}

type compositeQuery struct{ ctx *userContextImpl }

func (q *compositeQuery) User() UserQuery         { return &userQuery{ctx: q.ctx} }
func (q *compositeQuery) UsageLog() UsageLogQuery { return &usageLogQuery{ctx: q.ctx} }

type compositeMutation struct{ ctx *userContextImpl }

func (m *compositeMutation) User() UserMutation { return &userMutation{ctx: m.ctx} }

// --- admin-scoped composites ---

type AdminQuery interface {
	User() AdminUserQuery
	Token() AdminTokenQuery
	Channel() AdminChannelQuery
	Agent() AdminAgentQuery
	ModelConfig() AdminModelConfigQuery
	UsageLog() AdminUsageLogQuery
	Billing() AdminBillingQuery
	Setting() AdminSettingQuery
	EnrollmentToken() AdminEnrollmentTokenQuery
	Stats() AdminStatsQuery
	AgentRoute() AdminAgentRouteQuery
	TokenTemplate() AdminTokenTemplateQuery
	UserGroup() AdminUserGroupQuery
	OAuthProvider() AdminOAuthProviderQuery
	OAuthIdentity() AdminOAuthIdentityQuery
	ModelRouting() AdminModelRoutingQuery
	PrivateChannel() AdminPrivateChannelQuery
	PrivateChannelShare() AdminPrivateChannelShareQuery
}

type AdminMutation interface {
	User() AdminUserMutation
	Token() AdminTokenMutation
	Channel() AdminChannelMutation
	Agent() AdminAgentMutation
	ModelConfig() AdminModelConfigMutation
	UsageLog() AdminUsageLogMutation
	Billing() AdminBillingMutation
	Setting() AdminSettingMutation
	EnrollmentToken() AdminEnrollmentTokenMutation
	AgentRoute() AdminAgentRouteMutation
	TokenTemplate() AdminTokenTemplateMutation
	UserGroup() AdminUserGroupMutation
	OAuthProvider() AdminOAuthProviderMutation
	OAuthIdentity() AdminOAuthIdentityMutation
	ModelRouting() AdminModelRoutingMutation
	PrivateChannel() AdminPrivateChannelMutation
	PrivateChannelShare() AdminPrivateChannelShareMutation
}

// getBaseContext extracts *baseContext from any Context implementation.
func getBaseContext(ctx Context) *baseContext {
	switch c := ctx.(type) {
	case *baseContext:
		return c
	case *userContextImpl:
		return &c.baseContext
	default:
		panic(fmt.Sprintf("dao: unexpected context type %T", ctx))
	}
}

func NewAdminQuery(ctx Context) AdminQuery {
	return &compositeAdminQuery{ctx: getBaseContext(ctx)}
}

func NewAdminMutation(ctx Context) AdminMutation {
	return &compositeAdminMutation{ctx: getBaseContext(ctx)}
}

type compositeAdminQuery struct{ ctx *baseContext }

func (q *compositeAdminQuery) User() AdminUserQuery       { return &adminUserQuery{ctx: q.ctx} }
func (q *compositeAdminQuery) Token() AdminTokenQuery     { return &adminTokenQuery{ctx: q.ctx} }
func (q *compositeAdminQuery) Channel() AdminChannelQuery { return &adminChannelQuery{ctx: q.ctx} }
func (q *compositeAdminQuery) Agent() AdminAgentQuery     { return &adminAgentQuery{ctx: q.ctx} }
func (q *compositeAdminQuery) Billing() AdminBillingQuery { return &adminBillingQuery{ctx: q.ctx} }
func (q *compositeAdminQuery) ModelConfig() AdminModelConfigQuery {
	return &adminModelConfigQuery{ctx: q.ctx}
}
func (q *compositeAdminQuery) UsageLog() AdminUsageLogQuery { return &adminUsageLogQuery{ctx: q.ctx} }
func (q *compositeAdminQuery) Setting() AdminSettingQuery   { return &adminSettingQuery{ctx: q.ctx} }
func (q *compositeAdminQuery) EnrollmentToken() AdminEnrollmentTokenQuery {
	return &adminEnrollmentTokenQuery{ctx: q.ctx}
}
func (q *compositeAdminQuery) Stats() AdminStatsQuery { return &adminStatsQuery{ctx: q.ctx} }
func (q *compositeAdminQuery) AgentRoute() AdminAgentRouteQuery {
	return &adminAgentRouteQuery{ctx: q.ctx}
}
func (q *compositeAdminQuery) TokenTemplate() AdminTokenTemplateQuery {
	return &adminTokenTemplateQuery{ctx: q.ctx}
}
func (q *compositeAdminQuery) UserGroup() AdminUserGroupQuery {
	return &adminUserGroupQuery{ctx: q.ctx}
}
func (q *compositeAdminQuery) OAuthProvider() AdminOAuthProviderQuery {
	return &adminOAuthProviderQuery{ctx: q.ctx}
}
func (q *compositeAdminQuery) OAuthIdentity() AdminOAuthIdentityQuery {
	return &adminOAuthIdentityQuery{ctx: q.ctx}
}
func (q *compositeAdminQuery) ModelRouting() AdminModelRoutingQuery {
	return &adminModelRoutingQuery{ctx: q.ctx}
}
func (q *compositeAdminQuery) PrivateChannel() AdminPrivateChannelQuery {
	return &adminPrivateChannelQuery{ctx: q.ctx}
}
func (q *compositeAdminQuery) PrivateChannelShare() AdminPrivateChannelShareQuery {
	return &adminPrivateChannelShareQuery{ctx: q.ctx}
}

type compositeAdminMutation struct{ ctx *baseContext }

func (m *compositeAdminMutation) User() AdminUserMutation   { return &adminUserMutation{ctx: m.ctx} }
func (m *compositeAdminMutation) Token() AdminTokenMutation { return &adminTokenMutation{ctx: m.ctx} }
func (m *compositeAdminMutation) Channel() AdminChannelMutation {
	return &adminChannelMutation{ctx: m.ctx}
}
func (m *compositeAdminMutation) Agent() AdminAgentMutation { return &adminAgentMutation{ctx: m.ctx} }
func (m *compositeAdminMutation) ModelConfig() AdminModelConfigMutation {
	return &adminModelConfigMutation{ctx: m.ctx}
}
func (m *compositeAdminMutation) UsageLog() AdminUsageLogMutation {
	return &adminUsageLogMutation{ctx: m.ctx}
}
func (m *compositeAdminMutation) Billing() AdminBillingMutation {
	return &adminBillingMutation{ctx: m.ctx}
}
func (m *compositeAdminMutation) Setting() AdminSettingMutation {
	return &adminSettingMutation{ctx: m.ctx}
}
func (m *compositeAdminMutation) EnrollmentToken() AdminEnrollmentTokenMutation {
	return &adminEnrollmentTokenMutation{ctx: m.ctx}
}
func (m *compositeAdminMutation) AgentRoute() AdminAgentRouteMutation {
	return &adminAgentRouteMutation{ctx: m.ctx}
}
func (m *compositeAdminMutation) TokenTemplate() AdminTokenTemplateMutation {
	return &adminTokenTemplateMutation{ctx: m.ctx}
}
func (m *compositeAdminMutation) UserGroup() AdminUserGroupMutation {
	return &adminUserGroupMutation{ctx: m.ctx}
}
func (m *compositeAdminMutation) OAuthProvider() AdminOAuthProviderMutation {
	return &adminOAuthProviderMutation{ctx: m.ctx}
}
func (m *compositeAdminMutation) OAuthIdentity() AdminOAuthIdentityMutation {
	return &adminOAuthIdentityMutation{ctx: m.ctx}
}
func (m *compositeAdminMutation) ModelRouting() AdminModelRoutingMutation {
	return &adminModelRoutingMutation{ctx: m.ctx}
}
func (m *compositeAdminMutation) PrivateChannel() AdminPrivateChannelMutation {
	return &adminPrivateChannelMutation{ctx: m.ctx}
}
func (m *compositeAdminMutation) PrivateChannelShare() AdminPrivateChannelShareMutation {
	return &adminPrivateChannelShareMutation{ctx: m.ctx}
}
