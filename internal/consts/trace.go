package consts

// TraceBufferHardLimitMultiple 是单个 Recorder buffer 的硬上限相对 maxBodySize 的倍数。
// 默认 maxBodySize=64KB → 单 buffer 硬上限 ≈ 1920 KB。
const TraceBufferHardLimitMultiple = 30
