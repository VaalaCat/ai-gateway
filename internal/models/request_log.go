package models

// RequestLog is the current persistent LLM request-log schema. It deliberately
// shares UsageLog's complete field and tag contract so the split log database
// does not fork the LLM shape while UsageLog remains legacy compatibility data.
type RequestLog UsageLog

func (RequestLog) TableName() string { return "request_logs" }
