package sync

import (
	"bytes"
	"compress/gzip"
	"io"
	"net/http"

	"github.com/VaalaCat/ai-gateway/internal/pkg/events"
	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// usageIngestMaxBodyBytes 上限一次用量上报请求体的大小,防止单条恶意/异常
// 请求把内存/CPU 拖爆——10 MiB 对正常一批用量上报绰绰有余。
const usageIngestMaxBodyBytes = 10 << 20

// usageIngestMaxDecodedBytes 是 gzip 解压后的体积上限——压缩体受
// usageIngestMaxBodyBytes(10MiB)管,解压后必须另设上限防 zip bomb。
const usageIngestMaxDecodedBytes = 32 << 20

// HandleUsageHTTP 是数据面用量摄取端点(POST /api/agents/usage)。
// agent 头鉴权与 /ws/agent 同源;落库链复用 usage.reported 事件(Settler 幂等去重)。
func (h *Hub) HandleUsageHTTP(c *gin.Context) {
	agentID, ok := h.authenticateAgent(c.Request.Context(), c)
	if !ok {
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, usageIngestMaxBodyBytes)
	if c.GetHeader("Content-Encoding") == "gzip" {
		zr, err := gzip.NewReader(c.Request.Body)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid gzip body: " + err.Error()})
			return
		}
		defer zr.Close()
		// LimitReader 多给 1 字节探测超限:读满说明解压体 > 上限
		limited := io.LimitReader(zr, usageIngestMaxDecodedBytes+1)
		decoded, err := io.ReadAll(limited)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid gzip body: " + err.Error()})
			return
		}
		if len(decoded) > usageIngestMaxDecodedBytes {
			c.JSON(http.StatusBadRequest, gin.H{"error": "decompressed body exceeds limit"})
			return
		}
		c.Request.Body = io.NopCloser(bytes.NewReader(decoded))
		c.Request.Header.Del("Content-Encoding")
	}
	var report protocol.UsageReport
	if err := c.ShouldBindJSON(&report); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid usage report: " + err.Error()})
		return
	}
	// 用鉴权得到的 agentID 覆盖 body 里的值(而非只在为空时兜底):body 里的
	// agent_id 是不可信输入,不覆盖会让已认证 agent 冒领/误标到别的 agent 名下。
	report.AgentID = agentID

	if h.SettleUsage != nil {
		if err := h.SettleUsage(c.Request.Context(), agentID, report.Logs); err != nil {
			// ④ 诊断打点:结算失败不 ack,agent 会带着数据重试(request_id 去重保证幂等)
			h.Logger.Error("usage http ingest settle failed",
				zap.String("agent_id", agentID), zap.Int("batch_size", len(report.Logs)), zap.Error(err))
			c.JSON(http.StatusInternalServerError, gin.H{"error": "settle failed"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"accepted": len(report.Logs)})
		return
	}
	// 未接线兜底:异步 publish(老语义,ack≠持久化)
	// behavior change: propagate request cancellation into legacy settlement work.
	if err := events.PublishUsageReported(c.Request.Context(), h.Bus, report); err != nil {
		// ④ 诊断打点:摄取失败必须可见,agent 会重试
		h.Logger.Error("usage http ingest publish failed",
			zap.String("agent_id", agentID), zap.Int("batch_size", len(report.Logs)), zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "ingest failed"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"accepted": len(report.Logs)})
}
