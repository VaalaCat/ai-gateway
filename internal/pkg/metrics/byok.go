// Package metrics 暴露 BYOK 相关 prometheus 指标。
//
// 这些 metric 在包 init 阶段注册到 prometheus.DefaultRegisterer，
// 业务代码直接 .Inc()/.Set() 即可，无需关心注册路径。Task 8 临时 stub
// (interface{ Inc() }) 已被替换为真实 prometheus.Counter；调用点
// (e.g. byokcrypto.SanitizeDecryptErr) 因 prometheus.Counter 也实现 Inc()，
// 不需修改 import。
//
// 命名遵循 prometheus 约定：
//   - _total 后缀仅用于 Counter 单调累加值
//   - GaugeVec 用于按 label 维度 set 的瞬时快照（如 owner→count）
//
// 见 spec §4.4 + §6.2。
package metrics

import "github.com/prometheus/client_golang/prometheus"

var (
	// BYOKPrivateChannelCount 按 owner 维度 set 当前 PrivateChannel 行数。
	// 由 PrivateChannel mutation 路径增量维护（Create 后 +1，Delete 后 -1
	// 重新 CountByOwner 再 Set），用于观测 BYOKMaxChannels 配额接近情况。
	BYOKPrivateChannelCount = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "byok_private_channel_count",
			Help: "Number of BYOK private channels per owner (set by mutation path).",
		},
		[]string{"owner_id"},
	)

	// BYOKVisibleSetCacheSize 是 agent 端 visible private channel LRU 当前块数。
	// 在 LRU 写入/失效后 Set(len)；用于观测 cache 容量利用率。
	BYOKVisibleSetCacheSize = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Name: "byok_visible_set_cache_size",
			Help: "Current entry count in agent-side visible private-channel LRU.",
		},
	)

	// BYOKVisibleSetCacheHit 在 agent 端从 LRU 命中 visible private channel 块时 +1。
	BYOKVisibleSetCacheHit = prometheus.NewCounter(
		prometheus.CounterOpts{
			Name: "byok_visible_set_cache_hit_total",
			Help: "Agent-side visible private-channel cache hits.",
		},
	)

	// BYOKVisibleSetCacheMiss 在 agent 端未命中、走 loader 拉取时 +1。
	BYOKVisibleSetCacheMiss = prometheus.NewCounter(
		prometheus.CounterOpts{
			Name: "byok_visible_set_cache_miss_total",
			Help: "Agent-side visible private-channel cache misses.",
		},
	)

	// BYOKRequestTotal 在每条 BYOK relay usage 结算完成时按 (owner_type, model) +1。
	// owner_type 必为 "private"（admin 走非 BYOK 路径不入此 metric）。
	BYOKRequestTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "byok_request_total",
			Help: "Total BYOK relay requests settled, labelled by owner_type and model.",
		},
		[]string{"owner_type", "model"},
	)

	// BYOKDecryptFailureTotal 由 byokcrypto.SanitizeDecryptErr 每次 sanitize 时 +1。
	// 替换 Task 8 临时 stub（interface{ Inc() } / noopCounter{}）。
	// 调用点 sanitize.go 用 .Inc() 调，prometheus.Counter 也实现该方法，签名兼容。
	BYOKDecryptFailureTotal = prometheus.NewCounter(
		prometheus.CounterOpts{
			Name: "byok_decrypt_failure_total",
			Help: "Number of BYOK ciphertext decrypt failures detected via SanitizeDecryptErr.",
		},
	)
)

func init() {
	prometheus.MustRegister(
		BYOKPrivateChannelCount,
		BYOKVisibleSetCacheSize,
		BYOKVisibleSetCacheHit,
		BYOKVisibleSetCacheMiss,
		BYOKRequestTotal,
		BYOKDecryptFailureTotal,
	)
}
