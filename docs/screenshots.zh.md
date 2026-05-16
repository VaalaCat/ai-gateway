# 界面截图

Web UI 全部页面的展示。所有截图都使用预置 demo 数据 — 每个 `demo-*` 名称（渠道、令牌、用户）都是占位符，并非真实生产数据。

> English: [screenshots.md](screenshots.md)

---

## 总览

### 仪表盘 / Dashboard

一屏查看用户数、令牌数、渠道数、模型数、节点数、累计请求数与累计费用。

![仪表盘](images/zh/dashboard.png)

---

## 用户与访问

### 用户管理 / Users

管理管理员与终端用户账号，分配配额与所属用户组。

![用户管理](images/zh/users.png)

### 用户组 / User Groups

用户组级别的渠道与模型 allow-list，新用户自动继承所在组的策略。

![用户组](images/zh/groups.png)

### 令牌模板 / Token Templates

创建 API 令牌的可复用预设，统一模型列表与过期策略。

![令牌模板](images/zh/token-templates.png)

### OAuth 提供商 / OAuth Providers

注册 OIDC / OAuth2 身份源（GitHub、Google、自建 IdP），实现单点登录。

![OAuth 提供商](images/zh/oauth-providers.png)

### 个人中心 / Profile

终端用户的个人信息、配额使用情况、OAuth 身份绑定。

![个人中心](images/zh/profile.png)

---

## 渠道与模型

### 渠道管理 / Channels

配置上游 AI 服务渠道。支持 50+ 提供商（OpenAI、Anthropic、Gemini、DeepSeek、Ollama 等），按权重 / 优先级负载均衡。

![渠道管理](images/zh/channels.png)

### 模型配置 / Models

模型级别的定价（输入 / 输出 / 缓存读 / 缓存写）。

![模型配置](images/zh/models.png)

### 节点管理 / Agents

数据平面工作节点 — 单内置 agent 或多个分布式 agent（通过 enrollment token 注册）。

![节点管理](images/zh/agents.png)

### 路由规则 / Agent Routes

将特定渠道或路由固定到特定 agent（例如 EU 流量只走 EU 区域 agent）。

![路由规则](images/zh/agent-routes.png)

### 模型路由 / Model Routings

将多个上游 channel-model 聚合为一个对外模型名，按优先级与权重做负载均衡。

![模型路由](images/zh/model-routings.png)

---

## 令牌与用量

### 令牌管理 / Tokens

用户级 API 密钥，可选模型 allow-list、渠道 allow-list、过期时间。

![令牌管理](images/zh/tokens.png)

### 用量日志 / Logs

按请求的审计日志，包含令牌 / 用户 / 渠道 / 模型 / 费用 / 耗时 / 状态，可下钻查看原始 request/response trace。

![用量日志](images/zh/logs.png)

### 计费 / Billing

按令牌与按渠道的日度汇总 — 总费用、请求数、成功率、token 用量。支持从原始日志重建汇总。

![计费](images/zh/billing.png)

---

## 工具

### 对话测试 / Playground

浏览器内对任意已配置模型发起对话测试。支持 Chat / JSON / SSE 视图以及自定义 system prompt。

![对话测试](images/zh/playground.png)

### 我的路由 / My Routings

用户私有的模型路由 — 每个用户可定义自己的路由池，不影响全局路由。

![我的路由](images/zh/profile-model-routings.png)

---

## 运维与监控

### 系统设置 / System

站点级设置：注册开关、品牌、功能开关。

![系统设置](images/zh/system.png)

### 缓存监控 / Cache

Agent 端令牌 / 用户 LRU 缓存的统计 — 命中率、容量、淘汰次数。

![缓存监控](images/zh/monitoring-cache.png)

---

## 身份认证

### 登录 / Login

用户名+密码登录，以及通过配置的 OAuth 提供商登录。

![登录](images/zh/login.png)

### 注册 / Register

自助注册（可在系统设置中关闭）。

![注册](images/zh/register.png)
