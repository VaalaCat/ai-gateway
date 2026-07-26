package models

// RequestLog deliberately shares UsageLog's complete field and tag contract.
// Keeping it as a defined type avoids two independently evolving log schemas.
type RequestLog UsageLog

func (RequestLog) TableName() string { return "request_logs" }
