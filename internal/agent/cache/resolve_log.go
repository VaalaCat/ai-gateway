package cache

import (
	"context"
	"errors"

	"go.uber.org/zap"

	"github.com/VaalaCat/ai-gateway/internal/agent/cache/entitycache"
	"github.com/VaalaCat/ai-gateway/internal/pkg/ws"
)

// classifyResolveErr 把实体解析失败归类为稳定的可观测 reason。nil → ""。
// 当前分支控制面错误来源:连接死 → ws.ErrConnClosed;内部/调用方超时 →
// context.DeadlineExceeded;调用方取消 → context.Canceled;源端确认不存在 →
// entitycache.ErrNotFound。其余归 unknown。
func classifyResolveErr(err error) string {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, ws.ErrConnClosed):
		return "master_unreachable"
	case errors.Is(err, context.DeadlineExceeded):
		return "control_timeout"
	case errors.Is(err, context.Canceled):
		return "client_canceled"
	case errors.Is(err, entitycache.ErrNotFound):
		return "not_found"
	default:
		return "unknown"
	}
}

// logResolveDegrade 用统一 Store.logger 记录一次实体解析降级。
// not_found 属正常业务(用户没配该实体),降到 Debug 以免噪音;其余用 Warn。
func (s *Store) logResolveDegrade(entity, reason string, extra ...zap.Field) {
	fields := append([]zap.Field{
		zap.String("entity", entity),
		zap.String("reason", reason),
	}, extra...)
	if reason == "not_found" {
		s.logger.Debug("relay entity resolve degraded", fields...)
		return
	}
	s.logger.Warn("relay entity resolve degraded", fields...)
}
