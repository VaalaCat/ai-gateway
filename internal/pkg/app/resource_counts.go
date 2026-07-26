package app

type ResourceCounts struct {
	LifecycleWorkers int64
	HTTPHandlers     int64
	AcceptedSockets  int64
	ControlSessions  int64
	ControlHandlers  int64
	ControlSockets   int64
	RelayCandidates  int64
	RelayActive      int64
	RelayDraining    int64
	RelayStreams     int64
	CacheLoads       int64
	CacheRefreshes   int64
	ReporterWorkers  int64
	Inflight         int64
	Timers           int64
	Transports       int64
	RelaySockets     int64

	DirectOutgoingActive     int64
	DirectOutgoingCandidates int64
	DirectOutgoingDraining   int64
	DirectIncomingActive     int64
	DirectIncomingCandidates int64
	DirectIncomingDraining   int64
	DirectStreams            int64
	DirectSockets            int64
}
