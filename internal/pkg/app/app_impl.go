package app

import (
	"sync/atomic"

	"gorm.io/gorm"
)

type application struct {
	coreDB       *gorm.DB
	logDB        atomic.Pointer[gorm.DB]
	layoutMode   atomic.Uint32
	masterServer MasterServer
	agentServer  AgentServer
	hub          Hub
	publisher    Publisher
	settler      Settler
	quotaChecker QuotaChecker
	store        Store
	syncer       Syncer
	wsBridge     WSBridge
	relayHandler RelayHandler
	reporter     Reporter
	eventBus     EventBus
	wsClient     WSClient
}

var _ Application = (*application)(nil)

func NewApplication() Application {
	return &application{}
}

func (a *application) GetCoreDB() *gorm.DB   { return a.coreDB }
func (a *application) SetCoreDB(db *gorm.DB) { a.coreDB = db }
func (a *application) GetLogDB() *gorm.DB    { return a.logDB.Load() }
func (a *application) SetLogDB(db *gorm.DB)  { a.logDB.Store(db) }
func (a *application) GetDatabaseLayoutMode() DatabaseLayoutMode {
	return DatabaseLayoutMode(a.layoutMode.Load())
}
func (a *application) SetDatabaseLayoutMode(mode DatabaseLayoutMode) {
	if err := mode.Validate(); err != nil {
		panic(err)
	}
	a.layoutMode.Store(uint32(mode))
}
func (a *application) GetMasterServer() MasterServer  { return a.masterServer }
func (a *application) SetMasterServer(s MasterServer) { a.masterServer = s }
func (a *application) GetHub() Hub                    { return a.hub }
func (a *application) SetHub(h Hub)                   { a.hub = h }
func (a *application) GetPublisher() Publisher        { return a.publisher }
func (a *application) SetPublisher(p Publisher)       { a.publisher = p }
func (a *application) GetSettler() Settler            { return a.settler }
func (a *application) SetSettler(s Settler)           { a.settler = s }
func (a *application) GetQuotaChecker() QuotaChecker  { return a.quotaChecker }
func (a *application) SetQuotaChecker(q QuotaChecker) { a.quotaChecker = q }
func (a *application) GetAgentServer() AgentServer    { return a.agentServer }
func (a *application) SetAgentServer(s AgentServer)   { a.agentServer = s }
func (a *application) GetStore() Store                { return a.store }
func (a *application) SetStore(s Store)               { a.store = s }
func (a *application) GetSyncer() Syncer              { return a.syncer }
func (a *application) SetSyncer(s Syncer)             { a.syncer = s }
func (a *application) GetWSBridge() WSBridge          { return a.wsBridge }
func (a *application) SetWSBridge(b WSBridge)         { a.wsBridge = b }
func (a *application) GetRelayHandler() RelayHandler  { return a.relayHandler }
func (a *application) SetRelayHandler(h RelayHandler) { a.relayHandler = h }
func (a *application) GetReporter() Reporter          { return a.reporter }
func (a *application) SetReporter(r Reporter)         { a.reporter = r }
func (a *application) GetEventBus() EventBus          { return a.eventBus }
func (a *application) SetEventBus(b EventBus)         { a.eventBus = b }
func (a *application) GetWSClient() WSClient          { return a.wsClient }
func (a *application) SetWSClient(c WSClient)         { a.wsClient = c }
