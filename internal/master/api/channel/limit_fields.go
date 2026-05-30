package channel

import (
	"encoding/json"
	"fmt"

	"github.com/VaalaCat/ai-gateway/internal/models"
	"gorm.io/datatypes"
)

// sanitizeChannelLimitFields 处理 update Fields map 里与限额相关的字段:
//   - 丢弃客户端传来的 limit_state(系统所有,API 不接受写入)
//   - 校验 limit(若存在),并回填成强类型 JSONType 保证 GORM 正确序列化进 text 列
//   - 若显式改 status(手动操作),把 limit_state 清成空(Tripped=false),
//     使评估器把它当作手动启用/禁用(不再自动恢复)。
func sanitizeChannelLimitFields(updates map[string]any) error {
	delete(updates, "limit_state")

	if v, ok := updates["limit"]; ok && v != nil {
		b, err := json.Marshal(v)
		if err != nil {
			return fmt.Errorf("invalid limit: %w", err)
		}
		var cl models.ChannelLimit
		if err := json.Unmarshal(b, &cl); err != nil {
			return fmt.Errorf("invalid limit: %w", err)
		}
		if err := cl.Validate(); err != nil {
			return err
		}
		updates["limit"] = datatypes.NewJSONType(cl)
	}

	if _, ok := updates["status"]; ok {
		updates["limit_state"] = datatypes.NewJSONType(models.ChannelLimitState{})
	}
	return nil
}
