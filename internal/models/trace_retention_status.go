package models

// TraceRetentionStatus describes how much LLM trace data survived capture and
// asynchronous upload. It lives on the request log so a stripped trace can
// still explain its own absence.
type TraceRetentionStatus string

const (
	TraceRetentionFull          TraceRetentionStatus = "full"
	TraceRetentionHeadersOnly   TraceRetentionStatus = "headers_only"
	TraceRetentionBodyTruncated TraceRetentionStatus = "body_truncated"
	TraceRetentionBodyTrimmed   TraceRetentionStatus = "body_trimmed"
	TraceRetentionStripped      TraceRetentionStatus = "trace_stripped"
	TraceRetentionBillingOnly   TraceRetentionStatus = "billing_only"
	TraceRetentionDisabled      TraceRetentionStatus = "disabled"
)
