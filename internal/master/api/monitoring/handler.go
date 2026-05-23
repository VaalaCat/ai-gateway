package monitoring

// Handler 是 /v1/monitoring/* 端点的容器。
// 当前仅承载 Insights;后续若新增 monitoring 子端点 (alerts、heartbeat 等),
// 都挂在同一个 Handler 上,便于路由聚合。
type Handler struct{}
