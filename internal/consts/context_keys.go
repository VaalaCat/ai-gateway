package consts

// Gin context key 常量，用于 middleware 写入和 handler 读取。
const (
	CtxKeyUserInfo      = "user_info"
	CtxKeyRequestScope  = "request_scope"
	CtxKeyTraceRecorder = "trace_recorder"
)
