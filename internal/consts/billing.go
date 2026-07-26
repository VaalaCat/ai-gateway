package consts

// SettingKeyRebuildSliceSleepMs controls the pause (milliseconds) the billing
// rebuild runner takes between per-(date,hour) slices. Master-only setting
// (not synced to agent) read by internal/master/server.go when wiring
// billing.RebuildRunner. Default 1000ms lets background rebuild replay
// trickle rather than competing with peak DB I/O; 0 disables the pause.
const SettingKeyRebuildSliceSleepMs = "billing.rebuild_slice_sleep_ms"
