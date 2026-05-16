package codec

import "sync"

// IRTransformer 在 EncodeRequest 之前对 IR Request 做 in-place 改写。
// 用于"按 channel/model 配置改写 IR"类需求（系统提示注入、角色映射、
// DeepSeek thinking 字段兜底等）。
//
// 实现方约定：
//   - 必须幂等：同一 (req, cfg) 反复调用结果不变
//   - 必须只读 cfg
//   - 必须不返回错误：transformer 不阻塞请求路径，遇到非法配置 noop 并打 warn
type IRTransformer interface {
	// Name 用于日志 / 重复注册检测，必须全局唯一。
	Name() string

	// AppliesTo 决定该 transformer 是否对当前出站协议生效。
	AppliesTo(p Protocol) bool

	// Transform in-place 改写 req。不动 cfg。
	Transform(req *Request, cfg *ChannelConfig)
}

var (
	irTransformerMu       sync.RWMutex
	irTransformers        []IRTransformer
	irTransformerNameSeen = map[string]bool{}
)

// RegisterIRTransformer 在 init 时注册 transformer。
// 注册顺序 == 执行顺序。重复注册同名 transformer panic（fail-loud）。
func RegisterIRTransformer(t IRTransformer) {
	if t == nil {
		panic("codec: RegisterIRTransformer called with nil transformer")
	}
	irTransformerMu.Lock()
	defer irTransformerMu.Unlock()
	if irTransformerNameSeen[t.Name()] {
		panic("codec: duplicate IRTransformer registered: " + t.Name())
	}
	irTransformerNameSeen[t.Name()] = true
	irTransformers = append(irTransformers, t)
}

// ApplyIRTransformers 由 native.go 在调用 outboundCodec.EncodeRequest 之前调用。
// 按注册顺序对所有 AppliesTo(p) == true 的 transformer 链式应用。
func ApplyIRTransformers(p Protocol, req *Request, cfg *ChannelConfig) {
	irTransformerMu.RLock()
	snapshot := make([]IRTransformer, len(irTransformers))
	copy(snapshot, irTransformers)
	irTransformerMu.RUnlock()

	for _, t := range snapshot {
		if t.AppliesTo(p) {
			t.Transform(req, cfg)
		}
	}
}

// resetIRTransformers 仅供测试使用，清空注册表。
func resetIRTransformers() {
	irTransformerMu.Lock()
	defer irTransformerMu.Unlock()
	irTransformers = nil
	irTransformerNameSeen = map[string]bool{}
}
