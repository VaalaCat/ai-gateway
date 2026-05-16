// Package backend defines the Backend contract and the Dispatcher that
// routes each Attempt to one of native / passthrough / legacy implementations
// based on Attempt.Mode.
//
// 三个实现子包：
//   - backend/native       走 codec pipeline 的原生 relay
//   - backend/passthrough  只替换 model / auth / base URL，原样转发 body
//   - backend/legacy       走 new-api adaptor
//
// Dispatcher 在 Backends map 上做策略表派发，并在结果上叠加 FinalizeTokenCounts。
package backend

import "github.com/VaalaCat/ai-gateway/internal/agent/relay/state"

// Backend 是单次 attempt 的执行后端，按 mode 选择。
// 三个实现：native.Backend / passthrough.Backend / legacy.Backend，
// 各自在自己的子包内 export，外部只通过本接口调用。
//
// 入参 rctx 为整次请求级上下文；a 为本次尝试的 channel / 真实 model / mode。
// 返回的 AttemptResult 由 Dispatcher 进一步通过 FinalizeTokenCounts 调和 token 计数。
type Backend interface {
	Relay(rctx *state.RelayContext, a state.Attempt) state.AttemptResult
}
