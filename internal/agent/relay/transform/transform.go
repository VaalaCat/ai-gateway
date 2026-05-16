package transform

import "github.com/VaalaCat/ai-gateway/internal/agent/relay/codec"

// 集中注册所有 IR transformer。
// 注册顺序 == 执行顺序。不要分散到各 transformer 文件的 init()，
// 否则 Go 同包 init 顺序按文件名字母序，会让执行顺序绑死到 OS 文件命名。
//
// 顺序依据：
//  1. system_prompt_injector  — 在最前，给后续 transformer 看到完整消息列表
//  2. role_mapping            — 在 system 消息已注入后改写 role
//  3. thinking_passthrough    — openai_chat 出站 + cfg.SendBackThinking=true
//  4. thinking_strip          — openai_chat 出站 + cfg.SendBackThinking=false
//     （3/4 互斥，按 cfg 二选一生效）
func init() {
	codec.RegisterIRTransformer(SystemPromptInjector{})
	codec.RegisterIRTransformer(RoleMappingTransformer{})
	codec.RegisterIRTransformer(ThinkingPassthroughTransformer{})
	codec.RegisterIRTransformer(ThinkingStripTransformer{})
}
