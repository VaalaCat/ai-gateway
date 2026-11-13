package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/master/connectivity"
	"github.com/VaalaCat/ai-gateway/internal/master/controlrpc"
	"go.uber.org/zap"
)

const (
	DiagnosticNodeTimeout      = 10 * time.Second
	DiagnosticAggregateTimeout = 15 * time.Second
)

// DiagnosticError 将诊断失败分类为安全响应，并记录服务端原因。
func DiagnosticError(logger *zap.Logger, agentID, method string, started time.Time, err error) *APIError {
	// 1. 按错误类型选择公开分类。
	result := &APIError{Status: http.StatusBadGateway, Code: "agent_operation_failed", Message: "agent operation failed", Cause: err}
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		result.Status, result.Code, result.Message = http.StatusGatewayTimeout, "rpc_timeout", "the agent diagnostic request timed out"
	case errors.Is(err, context.Canceled):
		result.Status, result.Code, result.Message = http.StatusRequestTimeout, "request_canceled", "the diagnostic request was canceled"
	case errors.Is(err, connectivity.ErrConnectionGenerationChanged):
		result.Status, result.Code, result.Message = http.StatusConflict, "connection_generation_changed", "connection_generation_changed"
	case errors.Is(err, controlrpc.ErrDisconnected):
		result.Status, result.Code, result.Message = http.StatusServiceUnavailable, "control_disconnected", "control_disconnected"
	case errors.Is(err, controlrpc.ErrSendFailed):
		result.Code, result.Message = "send_failed", "could not send the agent diagnostic request"
	case errors.Is(err, controlrpc.ErrInvalidResponse):
		result.Code, result.Message = "invalid_agent_response", "invalid agent response"
	}
	// 2. 记录节点、方法、失败类型和耗时，不记录响应正文。
	if logger != nil {
		logger.Warn("agent diagnostic failed", zap.String("agent_id", agentID), zap.String("method", method), zap.String("code", result.Code), zap.Int64("elapsed_ms", time.Since(started).Milliseconds()), zap.String("error_type", fmt.Sprintf("%T", err)), zap.String("reason", result.Message))
	}
	return result
}
