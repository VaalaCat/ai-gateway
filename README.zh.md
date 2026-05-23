> 博客地址：https://vaala.cat/posts/vibe-ai-gateway-oss/

# AI Gateway

为分布式而生的 AI API 网关，采用控制平面（master）+ 数据平面（agent）分离架构，提供 OpenAI/Claude 兼容的 `/v1/*` 转发能力，内置管理 API 与 Web UI，单二进制分布式部署。

[English](README.md)

## 核心能力

- **控制平面管理** — 用户（组）、令牌、渠道、模型、Agent 管理
- **数据平面转发** — 兼容 OpenAI/Claude 风格接口（`/v1/chat/completions`、`/v1/responses`、`/v1/messages` 等），多种协议自动转换
- **实时配置同步** — master 与 agent 通过 WebSocket 增量同步，轻量级分布式部署，零外部依赖
- **多地域路由** — 支持将a地域的请求路由到b地域的 agent，实现跨地域负载均衡、突破限制
- **配额与计费** — 基于 usage log 的扣费与配额检查
- **模型路由** — 将多个上游模型聚合为一个对外名称，按优先级/权重负载均衡，错误重试
- **BYOK (自带密钥)** — 终端用户可自助上传自己的 provider API key(AES-GCM 加密落盘);private channel 自动合并到候选池并优先于 admin 共享 channel,可选 service-fee 计费模式
- **单二进制部署** — 前端静态资源 embed 进二进制，无需独立前端服务

## 界面截图

<table>
  <tr>
    <td colspan="2"><a href="docs/images/zh/dashboard.png"><img src="docs/images/zh/dashboard.png" alt="仪表盘"/></a></td>
  </tr>
  <tr>
    <td width="50%"><a href="docs/images/zh/channels.png"><img src="docs/images/zh/channels.png" alt="渠道管理"/></a><br/><sub><b>渠道管理</b> — 上游渠道配置</sub></td>
    <td width="50%"><a href="docs/images/zh/models.png"><img src="docs/images/zh/models.png" alt="模型配置"/></a><br/><sub><b>模型配置</b> — 模型级定价</sub></td>
  </tr>
  <tr>
    <td><a href="docs/images/zh/model-routings.png"><img src="docs/images/zh/model-routings.png" alt="模型路由"/></a><br/><sub><b>模型路由</b> — 优先级/权重聚合</sub></td>
    <td><a href="docs/images/zh/logs.png"><img src="docs/images/zh/logs.png" alt="用量日志"/></a><br/><sub><b>用量日志</b> — 按请求审计</sub></td>
  </tr>
  <tr>
    <td><a href="docs/images/zh/billing.png"><img src="docs/images/zh/billing.png" alt="计费"/></a><br/><sub><b>计费</b> — 按令牌与渠道的日度汇总</sub></td>
    <td><a href="docs/images/zh/playground.png"><img src="docs/images/zh/playground.png" alt="对话测试"/></a><br/><sub><b>对话测试</b> — 浏览器内对话测试</sub></td>
  </tr>
</table>

[查看全部 20 张截图 →](docs/screenshots.zh.md)

## 架构

```
┌─────────────────────────────────────────────────────┐
│                   master（控制平面）                  │
│  ┌──────────┐  ┌──────────┐  ┌───────────────────┐ │
│  │管理 API  │  │  Web UI  │  │ Agent 同步 Hub    │ │
│  │& 认证    │  │ (embed)  │  │ (WebSocket)       │ │
│  └──────────┘  └──────────┘  └───────────────────┘ │
│  ┌──────────────────────────────────────────────┐   │
│  │            计费与配额结算                      │   │
│  └──────────────────────────────────────────────┘   │
└─────────────────────────────────────────────────────┘
          │ WebSocket 同步
          ▼
┌─────────────────────────────────────────────────────┐
│                   agent（数据平面）                   │
│  ┌──────────────┐  ┌────────────┐  ┌────────────┐  │
│  │ /v1/* 转发   │  │ Token/渠道 │  │  用量      │  │
│  │ 端点         │  │ 缓存       │  │  上报      │  │
│  └──────────────┘  └────────────┘  └────────────┘  │
└─────────────────────────────────────────────────────┘
```

### 部署拓扑

| 拓扑                          | 优点                         | 缺点                              | 适用场景              |
| ----------------------------- | ---------------------------- | --------------------------------- | --------------------- |
| 单节点（master + 内置 agent） | 部署最简；单容器即可运行     | 控制面与数据面共享资源；单点故障  | PoC、测试、小规模生产 |
| 多节点（master + 外置 agent） | 数据面可水平扩展；故障域隔离 | 运维复杂度更高；需要注册/凭据管理 | 中大规模生产、多地域  |

## 快速开始

```bash
# 1. 准备配置
mkdir -p deploy data
cp config.example.yaml deploy/config.yaml
# 编辑 deploy/config.yaml — 设置 jwt_secret 和 admin_password

# 2. Docker Compose 启动
export AI_GATEWAY_IMAGE=vaalacat/ai-gateway:latest
docker compose up -d

# 3. 访问
# Web UI: http://localhost:8140
# 健康检查: http://localhost:8140/ping
```

## 配置

配置文件支持以下顶层键：

- `log_level` — 日志级别（debug, info, warn, error）
- `master` — 控制平面配置（监听地址、数据库、JWT、管理员凭据）
- `agent` — 数据平面配置（监听地址、master URL、注册）
- `runtime` — 可选高级调优（超时、心跳、重试）

完整模板参见 [`config.example.yaml`](config.example.yaml)。

## 部署

### 单节点（Docker Compose）

参见上方 [快速开始](#快速开始) 章节，完整配置见 [`docker-compose.yml`](docker-compose.yml)。

### 多节点（外置 Agent）

1. 在 master 侧生成 enrollment token
2. 配置 agent 的 `master_url` 和 `enrollment_token`
3. 启动：`docker compose -f docker-compose.yml -f docker-compose.agent.yml up -d`

模板参见 [`docker-compose.agent.yml`](docker-compose.agent.yml)。

### Kubernetes

参见 [`docs/k8s-deployment.md`](docs/k8s-deployment.md)。

## 开发

```bash
# 前置要求: Go 1.25+, Node.js 20+, pnpm

# 构建（前端 + 后端）
CGO_ENABLED=0 bash ./build.sh

# 运行测试
CGO_ENABLED=0 go test ./... -count=1 -timeout=120s

# 前端开发服务器（端口 8141，代理到 :8140）
cd web && pnpm install && pnpm dev
```

## 发布

推送 `v*` 格式的 git tag 即触发发布。GitHub Actions 会构建多架构镜像
（linux/amd64 + linux/arm64）并推送到
[Dockerhub](https://hub.docker.com/r/vaalacat/ai-gateway)。

```bash
# 正式版发布 — 同时更新 :latest
git tag v1.2.3
git push origin v1.2.3

# 预发布版本 — 仅推送 :v1.2.3-rc1，不更新 :latest
git tag v1.2.3-rc1
git push origin v1.2.3-rc1
```

git tag 会作为 `internal/version.Version` 注入到二进制中。

## 贡献

参见 [CONTRIBUTING.md](CONTRIBUTING.md) 了解开发环境搭建、代码规范和 PR 流程。

## 致谢

本项目支持native（纯自研，支持chat、response、messages协议），其他协议由 new-api 渠道提供支持，

站在以下工作的肩膀上：

- **[new-api](https://github.com/QuantumNous/new-api)**（作者 [@QuantumNous](https://github.com/QuantumNous)）— Legacy适配器、50+ 上游 provider 常量、模型拉取协议以及 token 计数工具均通过 `github.com/QuantumNous/new-api` 复用而来。没有这份前期工作，"开箱即用支持 50+ provider" 是不可能的。向 new-api 的维护者与所有贡献者致以诚挚的感谢。

## 许可证

[MIT](LICENSE)
