package consts

// RPC/WebSocket 方法名常量。
const (
	RPCSyncFullSync           = "sync.fullSync"
	RPCSyncGetVersion         = "sync.getVersion"
	RPCSyncPush               = "sync.push"
	RPCSyncRequestFullSync    = "sync.requestFullSync"
	RPCSyncForceFullSync      = "sync.forceFullSync"
	RPCSyncAutoAddrUpdate     = "sync.autoAddrUpdate"
	RPCSyncFetchEntity        = "sync.fetchEntity"
	RPCAgentHeartbeat         = "agent.heartbeat"
	RPCAgentCheckConnectivity = "agent.checkConnectivity"
	RPCChannelTest            = "channel.test"
	RPCChannelFetchModels     = "channel.fetchModels"
	RPCReportUsage            = "report.usage"
)
