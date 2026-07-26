package app

import (
	"gorm.io/gorm"
)

// Application 是整个应用的顶层容器接口
// 统一管理 Master 和 Agent 侧的所有组件，通过 Get/Set 方法实现依赖注入
// 除 LogDB 支持后台 connector 原子替换外，其余依赖仅在初始化阶段 Set。
type Application interface {
	GetCoreDB() *gorm.DB
	SetCoreDB(*gorm.DB)
	GetLogDB() *gorm.DB
	SetLogDB(*gorm.DB)
	GetDatabaseLayoutMode() DatabaseLayoutMode
	SetDatabaseLayoutMode(DatabaseLayoutMode)
	GetMasterServer() MasterServer
	SetMasterServer(MasterServer)
	GetHub() Hub
	SetHub(Hub)
	GetPublisher() Publisher
	SetPublisher(Publisher)
	GetSettler() Settler
	SetSettler(Settler)
	GetQuotaChecker() QuotaChecker
	SetQuotaChecker(QuotaChecker)
	GetAgentServer() AgentServer
	SetAgentServer(AgentServer)
	GetStore() Store
	SetStore(Store)
	GetSyncer() Syncer
	SetSyncer(Syncer)
	GetWSBridge() WSBridge
	SetWSBridge(WSBridge)
	GetRelayHandler() RelayHandler
	SetRelayHandler(RelayHandler)
	GetReporter() Reporter
	SetReporter(Reporter)
	GetEventBus() EventBus
	SetEventBus(EventBus)
	GetWSClient() WSClient
	SetWSClient(WSClient)
}
