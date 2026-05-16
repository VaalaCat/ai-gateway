package models

// AgentRoute 定义 Agent 路由规则。
// 管理员可为 Token 或 Channel 配置请求应转发到哪个 Agent 节点/组。
type AgentRoute struct {
	ID         uint   `json:"id" gorm:"primaryKey"`
	SourceType string `json:"source_type" gorm:"size:32;index:idx_agent_route_source;uniqueIndex:uk_agent_route"`
	SourceID   uint   `json:"source_id" gorm:"index:idx_agent_route_source;uniqueIndex:uk_agent_route"`
	Model      string `json:"model" gorm:"size:256;uniqueIndex:uk_agent_route;default:''"`
	AgentID    string `json:"agent_id" gorm:"size:128"`
	AgentTag   string `json:"agent_tag" gorm:"size:128"`
	Priority   int    `json:"priority" gorm:"index"`
	CreatedAt  int64  `json:"created_at" gorm:"autoCreateTime"`
	UpdatedAt  int64  `json:"updated_at" gorm:"autoUpdateTime"`
}

// CalcPriority 根据 SourceType 和 Model 计算优先级。
func (r *AgentRoute) CalcPriority() int {
	switch r.SourceType {
	case "token":
		if r.Model != "" {
			return 100
		}
		return 90
	case "channel":
		if r.Model != "" {
			return 80
		}
		return 70
	default:
		return 0
	}
}
