package models

// RequestTrace deliberately shares UsageLogTrace's complete field and tag contract.
type RequestTrace UsageLogTrace

func (RequestTrace) TableName() string { return "request_traces" }
